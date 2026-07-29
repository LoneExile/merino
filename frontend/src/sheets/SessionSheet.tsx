import { useEffect, useState } from "react";
import type { Client, HerdrSession, SessionList } from "../client";
import { Sheet } from "../Sheet";

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
    if (s.current || busy !== null) return;
    if (!client.switchSession) {
      setErr("This build cannot switch herdr sessions.");
      return;
    }
    setBusy(s.id);
    setErr(null);
    try {
      await client.switchSession(s.id);
      // Full reload: agent list + streams belong to the old session.
      // Keep path only — never re-apply ?pair=1 from a first-run cold start.
      const next = `${window.location.pathname}${window.location.hash}`;
      window.location.replace(next || "/");
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
              type="button"
              className={`row row--session${s.current ? " is-on" : ""}`}
              disabled={s.current || !s.reachable || busy !== null}
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
          Switching is off. Enable it under Settings on the Mac menu bar.
        </p>
      )}
    </Sheet>
  );
}
