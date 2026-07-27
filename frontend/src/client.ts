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
//
// Capabilities are OPTIONAL METHODS, not booleans. A capability the host does
// not have is an absent method, so `if (client.streamPane)` is a real check the
// type system enforces — rather than a flag the UI can forget to honour.

import type { Agent } from "../bindings/github.com/LoneExile/herdr-tunnel/internal/app";

export interface Session {
  user: string;
  provider: string;
  /** True when the host exposes no write operations. */
  readOnly: boolean;
  /** True when the operator allowed the dashboard to change herdr session. */
  canSwitchSession?: boolean;
  /** True when rename operations are permitted. */
  canRename?: boolean;
  /** True when the server has a VAPID keypair wired and can accept push
   * subscriptions. Absence of this — not a false value the UI must branch
   * on — is what makes the push methods below absent from Client. */
  pushEnabled?: boolean;
}

export interface HerdrSession {
  id: string;
  name: string;
  panes: number;
  agents: number;
  current: boolean;
  reachable: boolean;
}

export interface SessionList {
  current: string;
  canSwitch: boolean;
  sessions: HerdrSession[];
}

export type RenameKind = "pane" | "tab" | "workspace";

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

  /**
   * Live terminal output for one pane. Returns an unsubscribe function.
   *
   * Absent on transports that cannot push, in which case the caller should
   * fall back to `read`. The server pushes the full visible screen on every
   * change plus one snapshot on connect, so a subscriber never has to poll and
   * never has to stitch fragments together.
   */
  streamPane?(
    paneId: string,
    onText: (text: string) => void,
    onError?: (err: unknown) => void,
  ): () => void;

  /** Write operations. Absent on read-only transports. */
  respond?(paneId: string, text: string): Promise<void>;
  /**
   * Send arbitrary text to a pane — replying to an agent rather than picking
   * a canned answer. Wider than respond: length-bounded, not allowlisted.
   */
  sendText?(paneId: string, text: string): Promise<void>;
  /**
   * Press allowlisted keys in a pane (Esc, arrows, Enter, Tab, Ctrl+c, …).
   * Required for TUI menus that do not read free text — the Ask chooser,
   * for example, wants ↑/↓ + Enter or Esc, not a typed reply.
   */
  sendKeys?(paneId: string, keys: string[]): Promise<void>;
  focus?(paneId: string): Promise<void>;
  interrupt?(paneId: string): Promise<void>;
  rename?(kind: RenameKind, id: string, name: string): Promise<void>;

  /** Session enumeration and switching. Absent unless the operator allowed it. */
  sessions?(): Promise<SessionList>;
  switchSession?(id: string): Promise<void>;

  /**
   * Web Push. Present only when session.pushEnabled is true, mirroring
   * every other server-decided capability in this interface.
   */
  pushKey?(): Promise<string>;
  pushSubscribe?(sub: PushSubscriptionJSON): Promise<void>;
  pushUnsubscribe?(endpoint: string): Promise<void>;
}

/**
 * True when the page was served by the Go web server rather than the webview.
 *
 * Read from a <meta> tag, not a global set by an inline <script>: the server's
 * own Content-Security-Policy blocks inline script, so a script-borne marker
 * silently never runs and the bundle takes the desktop path in a browser. That
 * failure looked identical to a bundle crash — hence a marker no policy can
 * refuse.
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

async function postJSON(url: string, body?: unknown): Promise<void> {
  const res = await fetch(url, {
    method: "POST",
    credentials: "same-origin",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body ?? {}),
  });
  if (res.status === 401) {
    window.location.href = "/login";
    throw new Error("unauthenticated");
  }
  if (!res.ok) {
    // The server's refusal text is the useful part — it names the rule that
    // refused, which is exactly what the person needs to see.
    const detail = (await res.text()).trim();
    throw new Error(detail || `${res.status} ${res.statusText}`);
  }
}

/**
 * HTTP + SSE client for the browser dashboard.
 *
 * Whether writes are available is decided by the SERVER and reported through
 * /api/session — the routes simply do not exist when it runs read-only. The
 * write methods are attached only when the server says so, which is what makes
 * `client.respond` a meaningful capability check in the UI rather than a
 * decoration.
 */
async function httpClient(): Promise<Client> {
  const session = await getJSON<Session>("/api/session");
  const pane = (id: string) => encodeURIComponent(id);

  const base: Client = {
    kind: "web",
    readOnly: session.readOnly,

    session: async () => session,
    list: () => getJSON<Agent[]>("/api/agents"),

    async read(paneId: string, lines: number) {
      const r = await getJSON<{ text: string }>(
        `/api/panes/${pane(paneId)}/output?lines=${lines}`,
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

    streamPane(paneId, onText, onError) {
      const es = new EventSource(`/api/panes/${pane(paneId)}/stream`, {
        withCredentials: true,
      });
      es.addEventListener("output", (ev) => {
        try {
          const parsed = JSON.parse((ev as MessageEvent<string>).data) as { text?: string };
          if (typeof parsed.text === "string") onText(parsed.text);
        } catch (err) {
          onError?.(err);
        }
      });
      es.onerror = (err) => onError?.(err);
      return () => es.close();
    },

    sessions: () => getJSON<SessionList>("/api/sessions"),
  };

  // Independent of read/write mode: a read-only dashboard should still be
  // able to subscribe to push notifications. Gated on session.pushEnabled
  // exactly like every other server-decided capability here — the routes
  // simply do not exist when the server has no VAPID keypair wired.
  const pushMethods = session.pushEnabled
    ? {
        pushKey: async () => (await getJSON<{ key: string }>("/api/push/key")).key,
        pushSubscribe: (sub: PushSubscriptionJSON) => {
          const { endpoint, keys } = sub;
          if (!endpoint || !keys?.p256dh || !keys?.auth) {
            return Promise.reject(new Error("incomplete push subscription"));
          }
          return postJSON("/api/push/subscribe", {
            endpoint,
            keys: { p256dh: keys.p256dh, auth: keys.auth },
          });
        },
        pushUnsubscribe: (endpoint: string) => postJSON("/api/push/unsubscribe", { endpoint }),
      }
    : {};

  if (session.readOnly) return { ...base, ...pushMethods };

  return {
    ...base,
    ...pushMethods,
    respond: (paneId, text) => postJSON(`/api/panes/${pane(paneId)}/respond`, { text }),
    sendText: (paneId, text) => postJSON(`/api/panes/${pane(paneId)}/text`, { text }),
    sendKeys: (paneId, keys) => postJSON(`/api/panes/${pane(paneId)}/keys`, { keys }),
    focus: (paneId) => postJSON(`/api/panes/${pane(paneId)}/focus`),
    interrupt: (paneId) => postJSON(`/api/panes/${pane(paneId)}/interrupt`),
    rename: (kind, id, name) =>
      postJSON(`/api/${kind === "workspace" ? "workspaces" : kind + "s"}/${pane(id)}/rename`, {
        name,
      }),
    ...(session.canSwitchSession
      ? { switchSession: (id: string) => postJSON("/api/sessions/switch", { id }) }
      : {}),
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
