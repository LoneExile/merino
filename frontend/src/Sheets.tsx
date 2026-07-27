import { useEffect, useState } from "react";
import type { Agent } from "../bindings/github.com/LoneExile/herdr-tunnel/internal/app";
import type {
  Client,
  HerdrSession,
  PairingTicket,
  RenameKind,
  Session,
  SessionList,
  UpdateInfo,
} from "./client";
import { Sheet } from "./Sheet";
import type { ThemePref } from "./theme";

export interface SettingsSheetProps {
  session: Session | null;
  client: Client | null;
  pref: ThemePref;
  actual: "light" | "dark";
  onPref: (p: ThemePref) => void;
  wrap: boolean;
  onWrap: (w: boolean) => void;
  termFont: {
    px: number;
    zoomIn: () => void;
    zoomOut: () => void;
    canZoomIn: boolean;
    canZoomOut: boolean;
  };
  onClose: () => void;
}

const THEMES: { id: ThemePref; label: string }[] = [
  { id: "light", label: "Light" },
  { id: "dark", label: "Dark" },
  { id: "system", label: "System" },
];

const WRAP_OPTS: { value: boolean; label: string }[] = [
  { value: false, label: "Off" },
  { value: true, label: "On" },
];

type PushStatus = "checking" | "unsupported" | "denied" | "off" | "subscribed";

// Feature-detects rather than trusting client.pushSubscribe alone: a server
// with push enabled says nothing about whether THIS browser can act on it.
async function checkPushStatus(client: Client | null): Promise<PushStatus> {
  if (!client?.pushSubscribe || !("serviceWorker" in navigator) || !("PushManager" in window)) {
    return "unsupported";
  }
  if (Notification.permission === "denied") return "denied";
  try {
    const registration = await navigator.serviceWorker.ready;
    const subscription = await registration.pushManager.getSubscription();
    return subscription ? "subscribed" : "off";
  } catch {
    return "unsupported";
  }
}

// PushManager.subscribe wants the VAPID public key as a raw byte array, not
// the base64url string the server hands back — the standard conversion
// every Web Push integration needs.
function urlBase64ToUint8Array(base64Url: string): Uint8Array<ArrayBuffer> {
  const padding = "=".repeat((4 - (base64Url.length % 4)) % 4);
  const base64 = (base64Url + padding).replace(/-/g, "+").replace(/_/g, "/");
  const raw = atob(base64);
  const bytes = new Uint8Array(raw.length);
  for (let i = 0; i < raw.length; i++) bytes[i] = raw.charCodeAt(i);
  return bytes;
}

export function SettingsSheet({
  session,
  client,
  pref,
  actual,
  onPref,
  wrap,
  onWrap,
  termFont,
  onClose,
}: SettingsSheetProps) {
  const [pushStatus, setPushStatus] = useState<PushStatus>("checking");
  const [pushErr, setPushErr] = useState<string | null>(null);
  const [pushBusy, setPushBusy] = useState(false);

  const isDesktop = client?.kind === "desktop";
  const [loginLaunch, setLoginLaunch] = useState<boolean | null>(null);
  const [loginLaunchErr, setLoginLaunchErr] = useState<string | null>(null);
  const [loginLaunchBusy, setLoginLaunchBusy] = useState(false);

  const [update, setUpdate] = useState<UpdateInfo | null>(null);
  const [updateErr, setUpdateErr] = useState<string | null>(null);
  const [updateBusy, setUpdateBusy] = useState(false);

  const [pair, setPair] = useState<PairingTicket | null>(null);
  const [pairErr, setPairErr] = useState<string | null>(null);
  const [pairBusy, setPairBusy] = useState(false);
  const [pairBase, setPairBase] = useState(
    () => localStorage.getItem("herdr.pairBase") ?? "https://herdr-tunnel.0dl.me",
  );

  useEffect(() => {
    let alive = true;
    void checkPushStatus(client).then((status) => {
      if (alive) setPushStatus(status);
    });
    return () => {
      alive = false;
    };
  }, [client]);

  useEffect(() => {
    if (!client?.launchAtLogin) return;
    let alive = true;
    void client.launchAtLogin().then(
      (on) => {
        if (alive) setLoginLaunch(on);
      },
      (e: unknown) => {
        if (alive) setLoginLaunchErr(e instanceof Error ? e.message : String(e));
      },
    );
    return () => {
      alive = false;
    };
  }, [client]);

  const enableNotifications = async () => {
    const pushKey = client?.pushKey;
    const pushSubscribe = client?.pushSubscribe;
    if (!pushKey || !pushSubscribe || pushBusy) return;
    setPushBusy(true);
    setPushErr(null);
    try {
      // MUST run inside this click handler: iOS silently refuses
      // Notification.requestPermission() called any other way (e.g. from an
      // effect on mount), leaving permission stuck at "default" forever.
      const permission = await Notification.requestPermission();
      if (permission !== "granted") {
        setPushStatus(await checkPushStatus(client));
        return;
      }
      const registration = await navigator.serviceWorker.ready;
      const key = await pushKey();
      const subscription = await registration.pushManager.subscribe({
        userVisibleOnly: true,
        applicationServerKey: urlBase64ToUint8Array(key),
      });
      await pushSubscribe(subscription.toJSON());
      setPushStatus("subscribed");
    } catch (e) {
      setPushErr(e instanceof Error ? e.message : String(e));
      setPushStatus(await checkPushStatus(client));
    } finally {
      setPushBusy(false);
    }
  };

  const disableNotifications = async () => {
    const pushUnsubscribe = client?.pushUnsubscribe;
    if (!pushUnsubscribe || pushBusy) return;
    setPushBusy(true);
    setPushErr(null);
    try {
      const registration = await navigator.serviceWorker.ready;
      const subscription = await registration.pushManager.getSubscription();
      if (subscription) {
        await pushUnsubscribe(subscription.endpoint);
        await subscription.unsubscribe();
      }
      setPushStatus("off");
    } catch (e) {
      setPushErr(e instanceof Error ? e.message : String(e));
    } finally {
      setPushBusy(false);
    }
  };

  return (
    <Sheet
      title="Settings"
      subtitle={
        isDesktop
          ? `Desktop · ${session?.user ?? "local"}`
          : `Browser · ${session?.user ?? "—"}`
      }
      panelClass="sheet--settings"
      onClose={onClose}
    >
      <section className="settings-block" aria-labelledby="set-appear">
        <header className="settings-block__head">
          <h3 id="set-appear">Appearance</h3>
        </header>
        <div className="settings-row">
          <div className="settings-row__meta">
            <span className="settings-row__label">Theme</span>
            <span className="settings-row__hint">
              {pref === "system" ? `Follows device · ${actual}` : `Locked · ${pref}`}
            </span>
          </div>
          <div className="seg seg--compact" role="radiogroup" aria-label="Theme">
            {THEMES.map((t) => (
              <button
                key={t.id}
                type="button"
                role="radio"
                aria-checked={pref === t.id}
                className={`seg__opt${pref === t.id ? " is-on" : ""}`}
                onClick={() => onPref(t.id)}
              >
                {t.label}
              </button>
            ))}
          </div>
        </div>
        <div className="settings-row">
          <div className="settings-row__meta">
            <span className="settings-row__label">Line wrap</span>
            <span className="settings-row__hint">
              {wrap ? "Long lines fold to the pane width" : "Scroll sideways for long lines"}
            </span>
          </div>
          <div className="seg seg--compact" role="radiogroup" aria-label="Wrap long lines">
            {WRAP_OPTS.map((o) => (
              <button
                key={String(o.value)}
                type="button"
                role="radio"
                aria-checked={wrap === o.value}
                className={`seg__opt${wrap === o.value ? " is-on" : ""}`}
                onClick={() => onWrap(o.value)}
              >
                {o.label}
              </button>
            ))}
          </div>
        </div>
        <div className="settings-row">
          <div className="settings-row__meta">
            <span className="settings-row__label">Terminal size</span>
            <span className="settings-row__hint">{termFont.px}px monospaced</span>
          </div>
          <div className="seg seg--compact" role="group" aria-label="Terminal font size">
            <button
              type="button"
              className="seg__opt"
              disabled={!termFont.canZoomOut}
              onClick={() => termFont.zoomOut()}
              aria-label="Decrease font size"
            >
              A−
            </button>
            <button
              type="button"
              className="seg__opt"
              disabled={!termFont.canZoomIn}
              onClick={() => termFont.zoomIn()}
              aria-label="Increase font size"
            >
              A+
            </button>
          </div>
        </div>
      </section>

      {client?.pushSubscribe && (
        <section className="settings-block" aria-labelledby="set-push">
          <header className="settings-block__head">
            <h3 id="set-push">Alerts</h3>
            <span
              className={`settings-pill${
                pushStatus === "subscribed"
                  ? " settings-pill--ok"
                  : pushStatus === "denied"
                    ? " settings-pill--warn"
                    : ""
              }`}
            >
              {pushStatus === "checking"
                ? "…"
                : pushStatus === "subscribed"
                  ? "On"
                  : pushStatus === "denied"
                    ? "Blocked"
                    : pushStatus === "unsupported"
                      ? "N/A"
                      : "Off"}
            </span>
          </header>
          {pushStatus === "unsupported" && (
            <p className="settings-copy">Push is not available in this browser.</p>
          )}
          {pushStatus === "denied" && (
            <p className="settings-copy settings-copy--warn">
              Notifications are blocked. Allow them for this site in system settings, then reopen.
            </p>
          )}
          {(pushStatus === "off" || pushStatus === "checking") && (
            <button
              type="button"
              className="btn btn--solid"
              disabled={pushBusy || pushStatus === "checking"}
              onClick={() => void enableNotifications()}
            >
              {pushBusy ? "Enabling…" : "Enable notifications"}
            </button>
          )}
          {pushStatus === "subscribed" && (
            <>
              <p className="settings-copy">
                Notified the moment an agent needs you — even with the app closed.
              </p>
              <button
                type="button"
                className="btn"
                disabled={pushBusy}
                onClick={() => void disableNotifications()}
              >
                Turn off
              </button>
            </>
          )}
          {pushErr && (
            <p className="composer__err" role="alert">
              {pushErr}
            </p>
          )}
          <p className="settings-copy settings-copy--quiet">
            iPhone: Home Screen install required (Share → Add to Home Screen). Safari tabs do not
            receive push.
          </p>
        </section>
      )}

      {isDesktop && (client?.setLaunchAtLogin || client?.checkUpdate) && (
        <section className="settings-block" aria-labelledby="set-machine">
          <header className="settings-block__head">
            <h3 id="set-machine">This Mac</h3>
          </header>
          {client?.setLaunchAtLogin && (
            <div className="settings-row settings-row--toggle">
              <div className="settings-row__meta">
                <span className="settings-row__label">Launch at login</span>
                <span className="settings-row__hint">Open with your user session</span>
              </div>
              <label className="switch">
                <input
                  type="checkbox"
                  checked={loginLaunch === true}
                  disabled={loginLaunchBusy || loginLaunch === null}
                  onChange={(e) => {
                    const on = e.target.checked;
                    setLoginLaunchBusy(true);
                    setLoginLaunchErr(null);
                    void client
                      .setLaunchAtLogin?.(on)
                      .then(
                        () => setLoginLaunch(on),
                        (err: unknown) =>
                          setLoginLaunchErr(err instanceof Error ? err.message : String(err)),
                      )
                      .finally(() => setLoginLaunchBusy(false));
                  }}
                />
                <span className="switch__ui" aria-hidden="true" />
                <span className="sr-only">Launch at login</span>
              </label>
            </div>
          )}
          {loginLaunchErr && (
            <p className="composer__err" role="alert">
              {loginLaunchErr}
            </p>
          )}
          {client?.checkUpdate && (
            <div className="settings-stack">
              <button
                type="button"
                className="btn"
                disabled={updateBusy}
                onClick={() => {
                  setUpdateBusy(true);
                  setUpdateErr(null);
                  void client
                    .checkUpdate?.()
                    .then(
                      (info) => setUpdate(info),
                      (err: unknown) =>
                        setUpdateErr(err instanceof Error ? err.message : String(err)),
                    )
                    .finally(() => setUpdateBusy(false));
                }}
              >
                {updateBusy ? "Checking…" : "Check for updates"}
              </button>
              {update && (
                <dl className="facts facts--dense">
                  <div>
                    <dt>Installed</dt>
                    <dd className="mono">{update.current || "—"}</dd>
                  </div>
                  <div>
                    <dt>Latest</dt>
                    <dd className="mono">{update.latest || "—"}</dd>
                  </div>
                </dl>
              )}
              {update?.newer && (
                <p className="settings-copy">
                  <a href={update.releaseUrl} target="_blank" rel="noreferrer">
                    Open release {update.latest} ↗
                  </a>
                </p>
              )}
              {update && !update.newer && update.latest && (
                <p className="settings-copy settings-copy--quiet">Up to date.</p>
              )}
              {updateErr && (
                <p className="composer__err" role="alert">
                  {updateErr}
                </p>
              )}
            </div>
          )}
        </section>
      )}

      {isDesktop && client?.mintPairing && (
        <section className="settings-block" aria-labelledby="set-pair">
          <header className="settings-block__head">
            <h3 id="set-pair">Phone sign-in</h3>
          </header>
          <p className="settings-copy">
            One-shot QR. Expires in two minutes. No password typing on the phone.
          </p>
          <label className="field__label" htmlFor="pair-base">
            Public URL
          </label>
          <input
            id="pair-base"
            className="field__input"
            value={pairBase}
            onChange={(e) => setPairBase(e.target.value)}
            placeholder="https://herdr-tunnel.0dl.me"
            spellCheck={false}
            autoCapitalize="off"
            autoCorrect="off"
          />
          <button
            type="button"
            className="btn btn--solid"
            disabled={pairBusy}
            onClick={() => {
              setPairBusy(true);
              setPairErr(null);
              const base = pairBase.trim();
              localStorage.setItem("herdr.pairBase", base);
              void (async () => {
                try {
                  if (client.setPairingBaseURL) await client.setPairingBaseURL(base);
                  const ticket = await client.mintPairing?.();
                  if (ticket) setPair(ticket);
                } catch (err) {
                  setPairErr(err instanceof Error ? err.message : String(err));
                } finally {
                  setPairBusy(false);
                }
              })();
            }}
          >
            {pairBusy ? "Minting…" : pair ? "Refresh QR" : "Show QR code"}
          </button>
          {pair && (
            <div className="pair">
              <img
                className="pair__qr"
                src={pair.qrPng}
                alt="Sign-in QR code"
                width={192}
                height={192}
              />
              <p className="hint mono pair__token">{pair.token}</p>
              <p className="settings-copy settings-copy--quiet">
                Expires {new Date(pair.expiresAt * 1000).toLocaleTimeString()}. Paste the code on
                the login page if you cannot scan.
              </p>
            </div>
          )}
          {pairErr && (
            <p className="composer__err" role="alert">
              {pairErr}
            </p>
          )}
        </section>
      )}

      <section className="settings-block" aria-labelledby="set-conn">
        <header className="settings-block__head">
          <h3 id="set-conn">Connection</h3>
          {!session?.readOnly && <span className="settings-pill settings-pill--warn">Writes on</span>}
          {session?.readOnly && <span className="settings-pill">Read-only</span>}
        </header>
        <dl className="facts facts--dense">
          <div>
            <dt>User</dt>
            <dd className="mono">{session?.user ?? "—"}</dd>
          </div>
          <div>
            <dt>Auth</dt>
            <dd className="mono">{session?.provider ?? "—"}</dd>
          </div>
          <div>
            <dt>Transport</dt>
            <dd className="mono">{client?.kind ?? "—"}</dd>
          </div>
          <div>
            <dt>Live output</dt>
            <dd className="mono">{client?.streamPane ? "stream" : "poll"}</dd>
          </div>
        </dl>
        {!session?.readOnly && (
          <p className="settings-copy settings-copy--warn">
            This dashboard can type into terminals. Writes are recorded in the host audit log.
          </p>
        )}
      </section>

      <section className="settings-block" aria-labelledby="set-keys">
        <header className="settings-block__head">
          <h3 id="set-keys">Shortcuts</h3>
        </header>
        <ul className="settings-keys">
          <li>
            <span className="settings-keys__combo">
              <kbd>⌘</kbd>
              <kbd>K</kbd>
            </span>
            <span className="settings-keys__label">Jump to agent</span>
          </li>
          <li>
            <span className="settings-keys__combo">
              <kbd>Enter</kbd>
            </span>
            <span className="settings-keys__label">Send reply</span>
          </li>
          <li>
            <span className="settings-keys__combo">
              <kbd>⇧</kbd>
              <kbd>Enter</kbd>
            </span>
            <span className="settings-keys__label">Newline</span>
          </li>
          <li>
            <span className="settings-keys__combo">
              <kbd>Esc</kbd>
            </span>
            <span className="settings-keys__label">Close / back</span>
          </li>
          <li>
            <span className="settings-keys__combo">
              <kbd>⌘</kbd>
              <kbd>F</kbd>
            </span>
            <span className="settings-keys__label">Find in pane</span>
          </li>
          <li>
            <span className="settings-keys__combo">
              <kbd>⌘</kbd>
              <kbd>+</kbd>
              <span className="settings-keys__sep">/</span>
              <kbd>−</kbd>
            </span>
            <span className="settings-keys__label">Terminal font size</span>
          </li>
        </ul>
      </section>

      {!isDesktop && (
        <p className="sheet__foot">
          <a className="btn btn--ghost-danger" href="/logout">
            Sign out
          </a>
        </p>
      )}
    </Sheet>
  );
}

export interface SessionSheetProps {
  client: Client;
  onClose: () => void;
}

/**
 * herdr session picker.
 *
 * Switching is a privileged act — it repoints the dashboard at a different set
 * of live terminals — so the control only exists when the operator started the
 * server with it enabled. When it is off this sheet still lists what is running,
 * because knowing which session you are driving matters even when you cannot
 * change it.
 */
export function SessionSheet({ client, onClose }: SessionSheetProps) {
  const [data, setData] = useState<SessionList | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const [busy, setBusy] = useState<string | null>(null);

  useEffect(() => {
    let alive = true;
    if (!client.sessions) {
      // Defensive: the palette no longer offers this without the capability,
      // but a sheet that waits on a method the transport does not implement
      // shows "Looking for sessions…" forever, which is what the desktop
      // panel did.
      setErr("This build cannot list herdr sessions.");
      return;
    }
    void (async () => {
      try {
        const d = await client.sessions?.();
        if (alive && d) setData(d);
      } catch (e) {
        if (alive) setErr(e instanceof Error ? e.message : String(e));
      }
    })();
    return () => {
      alive = false;
    };
  }, [client]);

  const pick = async (s: HerdrSession) => {
    if (!client.switchSession || s.current) return;
    setBusy(s.id);
    setErr(null);
    try {
      await client.switchSession(s.id);
      // The agent list, every open stream and the pane view all belong to the
      // old session. Reload rather than trying to reconcile them — a stale
      // pane id pointed at a new session is worse than a blink.
      window.location.reload();
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
      setBusy(null);
    }
  };

  return (
    <Sheet title="Session" onClose={onClose}>
      {err && (
        <p className="composer__err" role="alert">
          {err}
        </p>
      )}
      {!data && !err && <p className="hint">Looking for sessions…</p>}

      <ul className="list list--plain">
        {data?.sessions.map((s) => (
          <li key={s.id}>
            <button
              className={`row row--session${s.current ? " is-on" : ""}`}
              disabled={!data.canSwitch || s.current || busy !== null}
              onClick={() => void pick(s)}
            >
              <span className="row__main">
                <span className="row__title">{s.name}</span>
                <span className="mono row__sub">
                  {s.reachable ? `${s.panes} panes · ${s.agents} agents` : "not responding"}
                </span>
              </span>
              {s.current && <span className="chip mono">current</span>}
              {busy === s.id && <span className="chip mono">switching…</span>}
            </button>
          </li>
        ))}
      </ul>

      {data && !data.canSwitch && (
        <p className="hint">
          Switching is disabled. Start the server with <code className="mono">
            --allow-session-switch
          </code>{" "}
          to change session from here.
        </p>
      )}
    </Sheet>
  );
}

export interface RenameTarget {
  kind: RenameKind;
  id: string;
  current: string;
}

export function renameTargets(agent: Agent): RenameTarget[] {
  return [
    { kind: "pane", id: agent.paneId, current: agent.agent || agent.paneId },
    { kind: "tab", id: agent.tabId, current: agent.tabId },
    { kind: "workspace", id: agent.workspaceId, current: agent.workspaceId },
  ];
}

export interface RenameSheetProps {
  client: Client;
  agent: Agent;
  onClose: () => void;
}

export function RenameSheet({ client, agent, onClose }: RenameSheetProps) {
  const targets = renameTargets(agent);
  const [kind, setKind] = useState<RenameKind>("pane");
  const [name, setName] = useState("");
  const [err, setErr] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  const target = targets.find((t) => t.kind === kind)!;

  const submit = async () => {
    const next = name.trim();
    if (!next || busy) return;
    setBusy(true);
    setErr(null);
    try {
      await client.rename?.(kind, target.id, next);
      onClose();
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
      setBusy(false);
    }
  };

  return (
    <Sheet title="Rename" onClose={onClose}>
      <div className="seg" role="radiogroup" aria-label="What to rename">
        {targets.map((t) => (
          <button
            key={t.kind}
            role="radio"
            aria-checked={kind === t.kind}
            className={`seg__opt${kind === t.kind ? " is-on" : ""}`}
            onClick={() => setKind(t.kind)}
          >
            {t.kind}
          </button>
        ))}
      </div>

      <p className="hint mono">{target.id}</p>

      <label className="field">
        <span className="field__label">New name</span>
        <input
          className="input"
          value={name}
          maxLength={120}
          autoFocus
          placeholder={target.current}
          onChange={(e) => setName(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === "Enter") {
              e.preventDefault();
              void submit();
            }
          }}
        />
      </label>

      {err && (
        <p className="composer__err" role="alert">
          {err}
        </p>
      )}

      <div className="sheet__actions">
        <button className="btn" onClick={onClose}>
          Cancel
        </button>
        <button className="btn btn--primary" disabled={busy || !name.trim()} onClick={() => void submit()}>
          Rename {kind}
        </button>
      </div>
    </Sheet>
  );
}
