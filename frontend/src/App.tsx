import { useCallback, useMemo, useState } from "react";
import type { Agent } from "../bindings/github.com/LoneExile/herdr-tunnel/internal/app";
import { AgentStatus } from "../bindings/github.com/LoneExile/herdr-tunnel/internal/herdr";
import type { Client } from "./client";
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
  client,
  onOutput,
  onError,
}: {
  agent: Agent;
  client: Client;
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

  // Write affordances only exist where the transport provides them. The web
  // server has no write endpoints at all, so this is presentation matching
  // reality rather than a permission check.
  const canWrite = !client.readOnly && client.respond && client.focus && client.interrupt;

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

      {canWrite && agent.needsAttention && (
        <div className="row-approvals">
          {APPROVALS.map((a) => (
            <button
              key={a.value}
              className={`btn btn-${a.kind}`}
              disabled={busy}
              onClick={() => run(() => client.respond!(agent.paneId, a.value))}
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
              const text = await client.read(agent.paneId, 50);
              onOutput(agent, text);
            })
          }
        >
          Output
        </button>
        {canWrite && (
          <>
            <button className="btn" disabled={busy} onClick={() => run(() => client.focus!(agent.paneId))}>
              Focus
            </button>
            <button
              className="btn btn-no"
              disabled={busy}
              onClick={() => run(() => client.interrupt!(agent.paneId))}
              title="Send Ctrl+c"
            >
              Stop
            </button>
          </>
        )}
      </div>
    </div>
  );
}

export default function App() {
  const { client, session, agents, ready, error, setError } = useHerd();
  const [output, setOutput] = useState<{ agent: Agent; text: string } | null>(null);

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
          <span className="dot" data-on={error ? "0" : "1"} />
          Herdr Tunnel
          {client?.readOnly && <span className="badge">read-only</span>}
        </div>
        <div className="header-meta">
          {agents.length} agents · {working} working
          {session && session.provider !== "desktop" && ` · ${session.user}`}
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

        {client && blocked.length > 0 && (
          <section>
            <h2 className="section-title section-title-alert">Needs you ({blocked.length})</h2>
            {blocked.map((a) => (
              <AgentRow
                key={a.paneId}
                agent={a}
                client={client}
                onOutput={(agent, text) => setOutput({ agent, text })}
                onError={setError}
              />
            ))}
          </section>
        )}

        {client &&
          byWorkspace.map(([ws, list]) => (
            <section key={ws}>
              <h2 className="section-title">
                {ws} <span className="count">{list.length}</span>
              </h2>
              {list.map((a) => (
                <AgentRow
                  key={a.paneId}
                  agent={a}
                  client={client}
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
