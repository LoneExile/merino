// Transport abstraction between the desktop panel and the browser dashboard.
//
// The same bundle runs in two very different hosts:
//
//   - Inside the Wails webview, where Go methods are callable through the
//     runtime's IPC bridge and state arrives via Wails events.
//   - In an ordinary browser over the LAN, where there is no bridge at all and
//     everything goes over HTTP + Server-Sent Events.
//
// `@wailsio/runtime` must never be imported at module scope: it touches
// `window.webkit.messageHandlers` while initialising, which throws in a plain
// browser before any of our code runs. The Wails implementation therefore
// lives in a separate module loaded through a dynamic import, so the browser
// never evaluates — or even downloads — that chunk.

import type { Agent } from "../bindings/github.com/LoneExile/herdr-tunnel/internal/app";

export interface Session {
  user: string;
  provider: string;
  /** True when the host exposes no write operations. */
  readOnly: boolean;
}

export interface Client {
  /** Human-facing description of the transport, for diagnostics. */
  readonly kind: "desktop" | "web";
  /** Whether write operations are available at all. */
  readonly readOnly: boolean;

  session(): Promise<Session>;
  list(): Promise<Agent[]>;
  read(paneId: string, lines: number): Promise<string>;

  /** Subscribe to agent updates. Returns an unsubscribe function. */
  subscribe(onAgents: (agents: Agent[]) => void, onError?: (err: unknown) => void): () => void;

  /** Write operations. Absent on read-only transports. */
  respond?(paneId: string, text: string): Promise<void>;
  focus?(paneId: string): Promise<void>;
  interrupt?(paneId: string): Promise<void>;
}

/**
 * True when the page was served by the Go web server rather than the webview.
 *
 * Read from a <meta> tag, not a global set by an inline script: the server's
 * Content-Security-Policy blocks inline scripts, so a script-based flag was
 * silently dropped and the bundle wrongly took the desktop path.
 */
const webMode = (): boolean =>
  typeof document !== "undefined" &&
  document.querySelector('meta[name="herdr-mode"]')?.getAttribute("content") === "web";

async function getJSON<T>(url: string): Promise<T> {
  const res = await fetch(url, { credentials: "same-origin" });
  if (res.status === 401) {
    // The session expired or was never established. Hand the browser to the
    // login page rather than rendering an empty dashboard.
    window.location.href = "/login";
    throw new Error("unauthenticated");
  }
  if (!res.ok) throw new Error(`${url}: ${res.status} ${res.statusText}`);
  return (await res.json()) as T;
}

/** HTTP + SSE client for the browser dashboard. Read-only by construction. */
function httpClient(): Client {
  return {
    kind: "web",
    readOnly: true,

    session: () => getJSON<Session>("/api/session"),
    list: () => getJSON<Agent[]>("/api/agents"),

    async read(paneId: string, lines: number) {
      const r = await getJSON<{ text: string }>(
        `/api/panes/${encodeURIComponent(paneId)}/output?lines=${lines}`,
      );
      return r.text;
    },

    subscribe(onAgents, onError) {
      // EventSource reconnects on its own, which is most of why this is SSE
      // rather than a WebSocket.
      const es = new EventSource("/api/events", { withCredentials: true });
      es.addEventListener("agents", (ev) => {
        try {
          const parsed: unknown = JSON.parse((ev as MessageEvent<string>).data);
          if (Array.isArray(parsed)) onAgents(parsed as Agent[]);
        } catch (err) {
          onError?.(err);
        }
      });
      es.onerror = (err) => onError?.(err);
      return () => es.close();
    },
  };
}

/**
 * Build the client appropriate to the current host.
 *
 * Async because the desktop implementation must be code-split: a static import
 * of `@wailsio/runtime` initialises `window.webkit.messageHandlers` at module
 * scope and throws in a plain browser, so the browser must never evaluate that
 * module at all. This is the "platform-specific module" case — a static import
 * genuinely cannot work here.
 */
export async function makeClient(): Promise<Client> {
  if (webMode()) return httpClient();
  const mod = await import("./wailsClient");
  return mod.wailsClient();
}
