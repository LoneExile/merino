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

/** Once a 401 is seen, stop SSE reconnect storms and boot retries. */
let authDead = false;
const authDeadListeners = new Set<() => void>();

export function isAuthDead(): boolean {
  return authDead;
}

export function onAuthDead(fn: () => void): () => void {
  authDeadListeners.add(fn);
  if (authDead) fn();
  return () => {
    authDeadListeners.delete(fn);
  };
}

function markAuthDead(): void {
  if (authDead) return;
  authDead = true;
  for (const fn of authDeadListeners) {
    try {
      fn();
    } catch {
      /* ignore listener errors */
    }
  }
  if (typeof window !== "undefined" && !window.location.pathname.startsWith("/login")) {
    // Full navigation so a stuck React tree / open SSE cannot keep the PWA frozen.
    window.location.replace("/login");
  }
}


export interface AccessOrigin {
  kind: "local" | "lan" | "public" | string;
  label: string;
  url: string;
  hint?: string;
}

export interface Session {
  user: string;
  provider: string;
  subject?: string;
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
  devicesEnabled?: boolean;
  canManageDevices?: boolean;
  firstRunPending?: boolean;
  oidcEnabled?: boolean;
  accessOrigins?: AccessOrigin[];
  defaultPairBase?: string;
  passwordLoginEnabled?: boolean;
}

export interface PairedDevice {
  id: string;
  name: string;
  provider: string;
  roles?: string[];
  createdAt: string;
  lastSeen: string;
  revokedAt?: string | null;
}

export interface DeviceList {
  devices: PairedDevice[];
  activeCount: number;
  firstRunPending: boolean;
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
    lines?: number,
  ): () => void;

  /** Write operations. Absent on read-only transports. */
  respond?(paneId: string, text: string): Promise<void>;
  /**
   * Send arbitrary text to a pane — replying to an agent rather than picking
   * a canned answer. Wider than respond: length-bounded, not allowlisted.
   */
  sendText?(paneId: string, text: string): Promise<void>;
  /**
   * Stage an image on the host and return its absolute path. Agents open the
   * file the same way a terminal clipboard-image paste does.
   */
  attachImage?(paneId: string, blob: Blob): Promise<{ path: string; mime: string }>;
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

  /**
   * Slash-command typeahead for the composer. agent is the herdr label
   * (omp/pi/claude/grok). query may include the leading '/'.
   */
  slashCommands?(paneId: string, agent: string, query: string): Promise<SlashCommand[]>;

  /** Desktop-only. Absent on the browser transport. */
  launchAtLogin?(): Promise<boolean>;
  setLaunchAtLogin?(on: boolean): Promise<void>;
  checkUpdate?(): Promise<UpdateInfo>;
  mintPairing?(): Promise<PairingTicket>;
  setPairingBaseURL?(base: string): Promise<void>;
  listDevices?(): Promise<DeviceList>;
  revokeDevice?(id: string): Promise<void>;
  revokeAllDevices?(): Promise<number>;
  setOptionalPassword?(user: string, pass: string): Promise<void>;
  markFirstRunDone?(): Promise<void>;
  optionalPasswordEnabled?(): Promise<boolean>;
  accessOrigins?(): Promise<AccessOrigin[]>;
  defaultPairBase?(): Promise<string>;
  passwordLoginEnabled?(): Promise<boolean>;
  setPasswordLoginEnabled?(on: boolean): Promise<void>;
  sessionSwitchEnabled?(): Promise<boolean>;
  setSessionSwitchEnabled?(on: boolean): Promise<void>;
}

export interface UpdateInfo {
  current: string;
  latest: string;
  newer: boolean;
  releaseUrl: string;
  body: string;
  published: string;
  checkedAt: number;
}

export interface PairingTicket {
  url: string;
  token: string;
  qrPng: string;
  expiresAt: number;
}

export interface SlashCommand {
  name: string;
  value: string;
  description?: string;
  source?: string;
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
    markAuthDead();
    throw new Error("unauthenticated");
  }
  if (!res.ok) throw new Error(`${url}: ${res.status} ${res.statusText}`);
  return (await res.json()) as T;
}


async function postJSON<T = void>(url: string, body?: unknown): Promise<T> {
  const res = await fetch(url, {
    method: "POST",
    credentials: "same-origin",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body ?? {}),
  });
  if (res.status === 401) {
    markAuthDead();
    throw new Error("unauthenticated");
  }
  if (!res.ok) {
    const detail = (await res.text()).trim();
    throw new Error(detail || `${res.status} ${res.statusText}`);
  }
  const text = await res.text();
  if (!text) return undefined as T;
  try {
    return JSON.parse(text) as T;
  } catch {
    return undefined as T;
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

    slashCommands: (paneId: string, agent: string, query: string) => {
      const q = new URLSearchParams({ q: query });
      if (paneId) q.set("pane", paneId);
      if (agent) q.set("agent", agent);
      return getJSON<SlashCommand[]>(`/api/slash?${q}`);
    },

    async read(paneId: string, lines: number) {
      const r = await getJSON<{ text: string }>(
        `/api/panes/${pane(paneId)}/output?lines=${lines}`,
      );
      return r.text;
    },

    subscribe(onAgents, onError) {
      // EventSource reconnects on its own, which is most of why this is SSE
      // rather than a WebSocket. On each (re)open we re-seed the agent list so
      // a phone that slept through a burst of events still catches up.
      const es = new EventSource("/api/events", { withCredentials: true });
      let sawError = false;
      const pull = () => {
        void getJSON<Agent[]>("/api/agents")
          .then((list) => {
            if (Array.isArray(list)) onAgents(list);
          })
          .catch((err) => onError?.(err));
      };
      es.addEventListener("agents", (ev) => {
        try {
          const parsed: unknown = JSON.parse((ev as MessageEvent<string>).data);
          if (Array.isArray(parsed)) onAgents(parsed as Agent[]);
          sawError = false;
        } catch (err) {
          onError?.(err);
        }
      });
      es.onopen = () => {
        if (sawError) pull();
        sawError = false;
      };
      es.onerror = (err) => {
        sawError = true;
        onError?.(err);
        // EventSource hides HTTP status; probe session so a revoked device
        // (401) immediately jumps to /login instead of freezing on reconnect.
        if (!isAuthDead()) {
          void getJSON("/api/session").catch(() => {
            /* markAuthDead already ran on 401 */
          });
        } else {
          es.close();
        }
      };
      // First paint: stream may be quiet until something changes.
      pull();
      return () => es.close();
    },

    streamPane(paneId, onText, onError, lines) {
      const q = typeof lines === "number" && lines > 0 ? `?lines=${lines}` : "";
      const es = new EventSource(`/api/panes/${pane(paneId)}/stream${q}`, {
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
      es.onerror = (err) => {
        if (isAuthDead()) {
          es.close();
          return;
        }
        onError?.(err);
        void getJSON("/api/session").catch(() => {
          es.close();
        });
      };
      return () => es.close();
    },

    sessions: () => getJSON<SessionList>("/api/sessions"),

    ...(session.canManageDevices
      ? {
          listDevices: () => getJSON<DeviceList>("/api/devices"),
          revokeDevice: (id: string) => postJSON("/api/devices/revoke", { id }),
          revokeAllDevices: async () => {
            const r = await postJSON<{ revoked: number }>("/api/devices/revoke-all", {});
            return r.revoked;
          },
          setOptionalPassword: (user: string, pass: string) =>
            postJSON("/api/auth/password", { user, pass }),
          passwordLoginEnabled: async () => {
            const r = await getJSON<{ enabled: boolean }>("/api/auth/password-login");
            return !!r.enabled;
          },
          setPasswordLoginEnabled: (on: boolean) =>
            postJSON("/api/auth/password-login", { enabled: on }),
          markFirstRunDone: () => postJSON("/api/first-run/done", {}),
        }
      : {}),
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
    attachImage: async (paneId, blob) => {
      // Multipart — more reliable on mobile than multi-MB base64 JSON.
      const fd = new FormData();
      const name =
        blob instanceof File && blob.name
          ? blob.name
          : `paste.${(blob.type || "image/png").split("/")[1] || "png"}`;
      fd.append("file", blob, name);
      if (blob.type) fd.append("mime", blob.type);
      const res = await fetch(`/api/panes/${pane(paneId)}/attach`, {
        method: "POST",
        credentials: "same-origin",
        body: fd,
      });
      if (res.status === 401) {
        markAuthDead();
        throw new Error("unauthenticated");
      }
      if (!res.ok) {
        let detail = (await res.text()).trim();
        try {
          const j = JSON.parse(detail) as { error?: string };
          if (j.error) detail = j.error;
        } catch {
          /* keep raw */
        }
        throw new Error(detail || `${res.status} ${res.statusText}`);
      }
      return (await res.json()) as { path: string; mime: string };
    },
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
