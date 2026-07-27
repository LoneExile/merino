import { useCallback, useMemo, useState } from "react";
import {
  AgentsService,
  type Agent,
} from "../bindings/github.com/LoneExile/herdr-tunnel/internal/app";
import { AgentStatus } from "../bindings/github.com/LoneExile/herdr-tunnel/internal/herdr";
import { useHerd } from "./useHerd";
import "./app.css";

/** Canned replies the backend allowlist accepts (internal/app/guard.go). */
const APPROVALS = [
  { label: "Yes", value: "yes, single permission", kind: "ok" },
  { label: "Trust", value: "trust, always allow", kind: "ok" },
  { label: "No", value: "no (tab to edit)", kind: "no" },
] as const;

function StatusPill({ status }: { status: AgentStatus }) {
  return <span className={`pill pill-${status || "unknown"}`}>{status || "unknown"}</span>;
}

function AgentRow({
  agent,
  onOutput,
  onError,
}: {
  agent: Agent;
  onOutput: (a: Agent, text: string) => void;
  onError: (msg: string) => void;
}) {
  const [busy, setBusy] = useState(false);

  const run = useCallback(
    async (fn: () => Promise<unknown>) => {
      setBusy(true);
      try {
        await fn();
      } catch (err) {
        onError(err instanceof Error ? err.message : String(err));
      } finally {
        setBusy(false);
      }
    },
    [onError],
  );

  return (
    <div className={`row ${agent.needsAttention ? "row-blocked" : ""}`}>
      <div className="row-main">
        <StatusPill status={agent.status} />
        <div className="row-text">
          <div className="row-title">
            {agent.agent || "agent"}
            <span className="row-project">{agent.project}</span>
          </div>
          <div className="row-sub" title={agent.cwd}>
            {agent.paneId} · {agent.cwd}
          </div>
        </div>
      </div>

      {agent.needsAttention && (
        <div className="row-approvals">
          {APPROVALS.map((a) => (
            <button
              key={a.value}
              className={`btn btn-${a.kind}`}
              disabled={busy}
              onClick={() => run(() => AgentsService.Respond(agent.paneId, a.value))}
            >
              {a.label}
            </button>
          ))}
        </div>
      )}

      <div className="row-actions">
        <button
          className="btn"
          disabled={busy}
          onClick={() =>
            run(async () => {
              const text = await AgentsService.Read(agent.paneId, 50);
              onOutput(agent, text);
            })
          }
        >
          Output
        </button>
        <button className="btn" disabled={busy} onClick={() => run(() => AgentsService.Focus(agent.paneId))}>
          Focus
        </button>
        <button
          className="btn btn-no"
          disabled={busy}
          onClick={() => run(() => AgentsService.Interrupt(agent.paneId))}
          title="Send Ctrl+c"
        >
          Stop
        </button>
      </div>
    </div>
  );
}

export default function App() {
  const { agents, conn, ready } = useHerd();
  const [output, setOutput] = useState<{ agent: Agent; text: string } | null>(null);
  const [error, setError] = useState<string | null>(null);

  const blocked = useMemo(() => agents.filter((a) => a.needsAttention), [agents]);
  const rest = useMemo(() => agents.filter((a) => !a.needsAttention), [agents]);

  const byWorkspace = useMemo(() => {
    const groups: Record<string, Agent[]> = {};
    for (const a of rest) {
      (groups[a.workspaceId] ??= []).push(a);
    }
    return Object.entries(groups).sort(([a], [b]) => a.localeCompare(b));
  }, [rest]);

  const working = agents.filter((a) => a.status === AgentStatus.StatusWorking).length;

  return (
    <div className="app">
      <header className="header">
        <div className="header-title">
          <span className="dot" data-on={conn?.connected ? "1" : "0"} />
          Herdr Tunnel
        </div>
        <div className="header-meta">
          {conn?.connected
            ? `herdr ${conn.version} · ${agents.length} agents · ${working} working`
            : (conn?.error ?? "connecting…")}
        </div>
      </header>

      {error && (
        <div className="banner banner-error" onClick={() => setError(null)}>
          {error}
        </div>
      )}

      <main className="list">
        {!ready && <div className="empty">Loading…</div>}

        {ready && agents.length === 0 && (
          <div className="empty">
            No agents running.
            <div className="empty-sub">Start one in herdr and it will appear here.</div>
          </div>
        )}

        {blocked.length > 0 && (
          <section>
            <h2 className="section-title section-title-alert">Needs you ({blocked.length})</h2>
            {blocked.map((a) => (
              <AgentRow
                key={a.paneId}
                agent={a}
                onOutput={(agent, text) => setOutput({ agent, text })}
                onError={setError}
              />
            ))}
          </section>
        )}

        {byWorkspace.map(([ws, list]) => (
          <section key={ws}>
            <h2 className="section-title">
              {ws} <span className="count">{list.length}</span>
            </h2>
            {list.map((a) => (
              <AgentRow
                key={a.paneId}
                agent={a}
                onOutput={(agent, text) => setOutput({ agent, text })}
                onError={setError}
              />
            ))}
          </section>
        ))}
      </main>

      {output && (
        <div className="drawer">
          <div className="drawer-head">
            <span>
              {output.agent.agent} · {output.agent.paneId}
            </span>
            <button className="btn" onClick={() => setOutput(null)}>
              Close
            </button>
          </div>
          <pre className="drawer-body">{output.text || "(no recent output)"}</pre>
        </div>
      )}
    </div>
  );
}
