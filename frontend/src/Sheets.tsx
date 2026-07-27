import { useEffect, useState } from "react";
import type { Agent } from "../bindings/github.com/LoneExile/herdr-tunnel/internal/app";
import type { Client, HerdrSession, RenameKind, Session, SessionList } from "./client";
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
  onClose,
}: SettingsSheetProps) {
  const [pushStatus, setPushStatus] = useState<PushStatus>("checking");
  const [pushErr, setPushErr] = useState<string | null>(null);
  const [pushBusy, setPushBusy] = useState(false);

  useEffect(() => {
    let alive = true;
    void checkPushStatus(client).then((status) => {
      if (alive) setPushStatus(status);
    });
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
    <Sheet title="Settings" onClose={onClose}>
      <fieldset className="field">
        <legend>Appearance</legend>
        <div className="seg" role="radiogroup" aria-label="Theme">
          {THEMES.map((t) => (
            <button
              key={t.id}
              role="radio"
              aria-checked={pref === t.id}
              className={`seg__opt${pref === t.id ? " is-on" : ""}`}
              onClick={() => onPref(t.id)}
            >
              {t.label}
            </button>
          ))}
        </div>
        <p className="hint">
          {pref === "system"
            ? `Following your device — currently ${actual}.`
            : `Always ${pref}.`}
        </p>
      </fieldset>

      <fieldset className="field">
        <legend>Terminal</legend>
        <div className="seg" role="radiogroup" aria-label="Wrap long lines">
          {WRAP_OPTS.map((o) => (
            <button
              key={String(o.value)}
              role="radio"
              aria-checked={wrap === o.value}
              className={`seg__opt${wrap === o.value ? " is-on" : ""}`}
              onClick={() => onWrap(o.value)}
            >
              {o.label}
            </button>
          ))}
        </div>
        <p className="hint">
          {wrap
            ? "Wrap long lines: long output wraps to fit the screen."
            : "Wrap long lines: off. Long output keeps its width — scroll the terminal sideways to read it."}
        </p>
      </fieldset>

      {client?.pushSubscribe && (
        <fieldset className="field">
          <legend>Notifications</legend>
          {pushStatus === "unsupported" && <p className="hint">Not supported in this browser.</p>}
          {pushStatus === "denied" && (
            <p className="hint hint--warn">
              Blocked by the browser. Allow notifications for this site in your browser or system
              settings, then reopen Settings.
            </p>
          )}
          {(pushStatus === "off" || pushStatus === "checking") && (
            <button
              type="button"
              className="btn"
              disabled={pushBusy || pushStatus === "checking"}
              onClick={() => void enableNotifications()}
            >
              Enable notifications
            </button>
          )}
          {pushStatus === "subscribed" && (
            <>
              <p className="hint">
                You will be notified here the moment an agent needs you, even with the app closed.
              </p>
              <button
                type="button"
                className="btn"
                disabled={pushBusy}
                onClick={() => void disableNotifications()}
              >
                Turn off notifications
              </button>
            </>
          )}
          {pushErr && (
            <p className="composer__err" role="alert">
              {pushErr}
            </p>
          )}
          <p className="hint">
            On iPhone this only works after adding Herdr Tunnel to your Home Screen (Share → Add
            to Home Screen) and opening it from there — Safari does not deliver push to a page
            open in a regular tab.
          </p>
        </fieldset>
      )}

      <fieldset className="field">
        <legend>This server</legend>
        <dl className="facts">
          <dt>Signed in</dt>
          <dd className="mono">{session?.user ?? "—"}</dd>
          <dt>Auth</dt>
          <dd className="mono">{session?.provider ?? "—"}</dd>
          <dt>Transport</dt>
          <dd className="mono">{client?.kind ?? "—"}</dd>
          <dt>Writes</dt>
          <dd className="mono">{session?.readOnly ? "disabled" : "enabled"}</dd>
          <dt>Live output</dt>
          <dd className="mono">{client?.streamPane ? "streaming" : "polling"}</dd>
        </dl>
        {!session?.readOnly && (
          <p className="hint hint--warn">
            This dashboard can type into your terminals. Every write is recorded
            in the audit log on the host.
          </p>
        )}
      </fieldset>

      <fieldset className="field">
        <legend>Keyboard</legend>
        <dl className="facts">
          <dt>
            <kbd>⌘</kbd> <kbd>K</kbd>
          </dt>
          <dd>Jump to an agent</dd>
          <dt>
            <kbd>Enter</kbd>
          </dt>
          <dd>Send reply</dd>
          <dt>
            <kbd>Shift</kbd> <kbd>Enter</kbd>
          </dt>
          <dd>Newline</dd>
          <dt>
            <kbd>Esc</kbd>
          </dt>
          <dd>Close / back</dd>
        </dl>
      </fieldset>

      <p className="sheet__foot mono">
        <a href="/logout">Sign out</a>
      </p>
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
