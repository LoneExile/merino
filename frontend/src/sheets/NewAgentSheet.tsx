import { useEffect, useState } from "react";
import type { AgentKind, Client, NewPane, Workspace } from "../client";
import { Sheet } from "../Sheet";

export interface NewAgentSheetProps {
  client: Client;
  onClose: () => void;
  /** Called with the new pane so the caller can open it straight away. */
  onCreated: (pane: NewPane) => void;
}

/**
 * Start an agent in a new pane: pick a workspace, pick an agent, go.
 *
 * Two lists rather than a form. The workspace question is "where does this
 * land", the agent question is "what starts there", and both are short enough
 * to answer by looking. A dropdown would hide the workspace's pane count and
 * live status, which is exactly what tells you whether it is the busy one.
 *
 * Only agents actually installed on the host are offered; the host resolves
 * them through the user's login shell (see internal/app/agentkinds.go),
 * because the menu-bar app's own PATH sees almost nothing.
 */
export function NewAgentSheet({ client, onClose, onCreated }: NewAgentSheetProps) {
  const [spaces, setSpaces] = useState<Workspace[] | null>(null);
  const [kinds, setKinds] = useState<AgentKind[] | null>(null);
  const [workspaceId, setWorkspaceId] = useState<string>("");
  const [kind, setKind] = useState<string>("");
  const [label, setLabel] = useState("");
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);

  const canSpawn = Boolean(client.workspaces && client.agentKinds && client.startAgentPane);

  useEffect(() => {
    if (!canSpawn) {
      setErr("This build cannot create panes.");
      return;
    }
    let alive = true;
    void (async () => {
      try {
        const [ws, ks] = await Promise.all([client.workspaces!(), client.agentKinds!()]);
        if (!alive) return;
        setSpaces(ws);
        setKinds(ks);
        // Default to where the operator is looking and the first agent, so
        // the common case is one tap.
        setWorkspaceId((ws.find((w) => w.focused) ?? ws[0])?.workspaceId ?? "");
        setKind(ks[0]?.kind ?? "");
      } catch (e) {
        if (alive) setErr(e instanceof Error ? e.message : String(e));
      }
    })();
    return () => {
      alive = false;
    };
  }, [client, canSpawn]);

  const create = async () => {
    if (!kind || busy || !client.startAgentPane) return;
    setBusy(true);
    setErr(null);
    try {
      const pane = await client.startAgentPane(workspaceId, kind, label.trim());
      onCreated(pane);
    } catch (e) {
      // Loud failure: the host rolled the tab back, so the herd is unchanged
      // and the reason is the only thing worth showing.
      setErr(e instanceof Error ? e.message : String(e));
      setBusy(false);
    }
  };

  const loading = !err && (spaces === null || kinds === null);

  return (
    <Sheet title="New agent" onClose={onClose}>
      {err && (
        <p className="composer__err" role="alert">
          {err}
        </p>
      )}
      {loading && <p className="hint">Looking for workspaces and agents…</p>}

      {spaces !== null && kinds !== null && (
        <>
          <section className="spawn-block" aria-labelledby="spawn-where">
            <h3 id="spawn-where" className="spawn-block__title">
              Workspace
            </h3>
            {spaces.length === 0 ? (
              <p className="hint">No workspaces reported.</p>
            ) : (
              <ul className="list list--plain">
                {spaces.map((w) => (
                  <li key={w.workspaceId}>
                    <button
                      type="button"
                      className={`row row--session${w.workspaceId === workspaceId ? " is-on" : ""}`}
                      disabled={busy}
                      aria-pressed={w.workspaceId === workspaceId}
                      onClick={() => setWorkspaceId(w.workspaceId)}
                    >
                      <span className="row__main">
                        <span className="row__title">{w.label || w.workspaceId}</span>
                        <span className="mono row__sub">
                          {w.paneCount} panes · {w.tabCount} tabs
                        </span>
                      </span>
                      {w.focused && <span className="chip mono">focused</span>}
                    </button>
                  </li>
                ))}
              </ul>
            )}
          </section>

          <section className="spawn-block" aria-labelledby="spawn-what">
            <h3 id="spawn-what" className="spawn-block__title">
              Agent
            </h3>
            {kinds.length === 0 ? (
              <p className="hint">
                No supported agents found on this machine. Install one (omp, claude, codex…) and
                reopen this sheet.
              </p>
            ) : (
              <div className="spawn-kinds" role="radiogroup" aria-label="Agent">
                {kinds.map((k) => (
                  <button
                    key={k.kind}
                    type="button"
                    role="radio"
                    aria-checked={k.kind === kind}
                    className={`spawn-kind${k.kind === kind ? " is-on" : ""}`}
                    disabled={busy}
                    title={k.path}
                    onClick={() => setKind(k.kind)}
                  >
                    <span className="spawn-kind__name">{k.label}</span>
                    <span className="spawn-kind__cmd mono">{k.kind}</span>
                  </button>
                ))}
              </div>
            )}
          </section>

          <section className="spawn-block">
            <label className="field__label" htmlFor="spawn-label">
              Name (optional)
            </label>
            <input
              id="spawn-label"
              className="field__input"
              value={label}
              placeholder={kind || "agent"}
              disabled={busy}
              autoCapitalize="off"
              autoCorrect="off"
              spellCheck={false}
              onChange={(e) => setLabel(e.target.value)}
            />
          </section>

          <div className="sheet__actions">
            <button type="button" className="btn" disabled={busy} onClick={onClose}>
              Cancel
            </button>
            <button
              type="button"
              className="btn btn--primary"
              disabled={busy || !kind || kinds.length === 0}
              onClick={() => void create()}
            >
              {busy ? "Starting…" : "Start agent"}
            </button>
          </div>
          {busy && (
            <p className="hint">
              Waiting for the agent to reach its prompt. This can take a while on a cold start.
            </p>
          )}
        </>
      )}
    </Sheet>
  );
}
