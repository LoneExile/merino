import { useCallback, useEffect, useMemo, useState } from "react";
import type { Agent } from "../bindings/github.com/LoneExile/herdr-tunnel/internal/app";
import { useHerd } from "./useHerd";
import { useTheme } from "./theme";
import { useWrapPref } from "./wrapPref";
import { PaneView } from "./PaneView";
import { StatusDot, statusLabel } from "./StatusDot";
import { Palette, type Command } from "./Palette";
import { RenameSheet, SessionSheet, SettingsSheet } from "./Sheets";

type Overlay = "settings" | "sessions" | "palette" | null;

/** Blocked first: the whole point of the dashboard is answering what is stuck. */
const GROUPS: { key: string; label: string }[] = [
  { key: "blocked", label: "Needs you" },
  { key: "working", label: "Working" },
  { key: "idle", label: "Idle" },
];

export default function App() {
  const { client, session, agents, ready, live, error } = useHerd();
  const { pref, actual, setPref } = useTheme();
  const { wrap, setWrap } = useWrapPref();
  const [openPane, setOpenPane] = useState<string | null>(null);
  const [overlay, setOverlay] = useState<Overlay>(null);
  const [renaming, setRenaming] = useState<Agent | null>(null);

  const current = useMemo(
    () => agents.find((a) => a.paneId === openPane) ?? null,
    [agents, openPane],
  );

  // The pane the user opened can vanish — the agent exits, or the session is
  // switched underneath them. Fall back to the list rather than showing a
  // terminal for something that no longer exists.
  useEffect(() => {
    if (openPane && ready && !current) setOpenPane(null);
  }, [openPane, current, ready]);

  const grouped = useMemo(() => {
    const by = new Map<string, Agent[]>();
    for (const a of agents) {
      const k = GROUPS.some((g) => g.key === a.status) ? a.status : "idle";
      const list = by.get(k);
      if (list) list.push(a);
      else by.set(k, [a]);
    }
    return by;
  }, [agents]);

  const commands = useMemo<Command[]>(() => {
    const cmds: Command[] = agents.map((a) => ({
      id: `pane:${a.paneId}`,
      label: `${a.agent || "pane"} · ${a.project || a.cwd || a.paneId}`,
      hint: `${statusLabel(a.status)} · ${a.paneId}`,
      run: () => setOpenPane(a.paneId),
    }));
    cmds.push({
      id: "cmd:settings",
      label: "Open settings",
      hint: "theme, server",
      run: () => setOverlay("settings"),
    });
    // Only when the transport can actually enumerate sessions. Offering it
    // unconditionally is how the desktop panel ended up opening a picker that
    // sat on "Looking for sessions…" forever.
    if (client?.sessions) {
      cmds.push({
        id: "cmd:sessions",
        label: "Switch session",
        hint: "herdr session",
        run: () => setOverlay("sessions"),
      });
    }
    cmds.push(
      {
        id: "cmd:theme",
        label: `Theme: ${pref}`,
        hint: "cycle light / dark / system",
        run: () => setPref(pref === "light" ? "dark" : pref === "dark" ? "system" : "light"),
      },
    );
    return cmds;
  }, [agents, client, pref, setPref]);

  // Global keys. Escape is deliberately NOT handled here: each overlay owns its
  // own Escape so the innermost surface closes first.
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === "k") {
        e.preventDefault();
        setOverlay((v) => (v === "palette" ? null : "palette"));
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, []);

  const closeOverlay = useCallback(() => setOverlay(null), []);

  const counts = useMemo(() => {
    const blocked = agents.filter((a) => a.status === "blocked").length;
    const working = agents.filter((a) => a.status === "working").length;
    return { blocked, working, total: agents.length };
  }, [agents]);

  return (
    <div className={`app${current ? " app--pane" : ""}`}>
      {/* Global chrome stays on the board. Inside a pane the pane header is
          the only top bar — two stacked rails ate half the phone keyboard. */}
      {!current && (
        <header className="rail">
          <div className="rail__brand">
            <img className="rail__mark" src="/favicon-64.png" alt="" width="16" height="16" />
            <span className="rail__name">Herdr</span>
            <span
              className={`rail__pulse${live ? " is-live" : ""}${!ready ? " is-boot" : ""}`}
              title={live ? "Live" : ready ? "Reconnecting" : "Connecting"}
              aria-label={live ? "Live" : ready ? "Reconnecting" : "Connecting"}
            />
          </div>

          <p className="rail__summary mono" aria-live="polite">
            {!ready
              ? "…"
              : !live
                ? "reconnecting"
                : counts.blocked > 0
                  ? `${counts.blocked} need${counts.blocked === 1 ? "s" : ""} you`
                  : `${counts.working}/${counts.total}`}
          </p>

          <div className="rail__actions">
            <button
              className="btn btn--icon"
              onClick={() => setOverlay("palette")}
              aria-label="Command palette"
              title="Jump to an agent (⌘K)"
            >
              <span className="mono kbd-hint">⌘K</span>
            </button>
            {client?.sessions && (
              <button
                className="btn btn--icon"
                onClick={() => setOverlay("sessions")}
                aria-label="Switch session"
                title="Session"
              >
                <svg viewBox="0 0 16 16" width="16" height="16" aria-hidden="true">
                  <rect
                    x="2"
                    y="3"
                    width="12"
                    height="4"
                    rx="1.2"
                    fill="none"
                    stroke="currentColor"
                    strokeWidth="1.4"
                  />
                  <rect
                    x="2"
                    y="9"
                    width="12"
                    height="4"
                    rx="1.2"
                    fill="none"
                    stroke="currentColor"
                    strokeWidth="1.4"
                  />
                </svg>
              </button>
            )}
            <button
              className="btn btn--icon"
              onClick={() => setOverlay("settings")}
              aria-label="Settings"
              title="Settings"
            >
              <svg viewBox="0 0 16 16" width="16" height="16" aria-hidden="true">
                <circle cx="8" cy="8" r="2.4" fill="none" stroke="currentColor" strokeWidth="1.4" />
                <path
                  d="M8 1.6v1.6M8 12.8v1.6M14.4 8h-1.6M3.2 8H1.6M12.5 3.5l-1.1 1.1M4.6 11.4l-1.1 1.1M12.5 12.5l-1.1-1.1M4.6 4.6 3.5 3.5"
                  stroke="currentColor"
                  strokeWidth="1.4"
                  strokeLinecap="round"
                />
              </svg>
            </button>
          </div>
        </header>
      )}

      {error && (
        <p className={`banner${live ? "" : " banner--soft"}`} role="status">
          {error}
        </p>
      )}

      {current && client ? (
        <PaneView
          key={current.paneId}
          client={client}
          agent={current}
          wrap={wrap}
          onBack={() => setOpenPane(null)}
          onRename={(a) => setRenaming(a)}
        />
      ) : (
        <main className="board">
          {!ready && <p className="empty mono">Connecting to herdr…</p>}
          {ready && agents.length === 0 && (
            <p className="empty">
              No agents are running.
              <span className="mono">Start one in herdr and it appears here.</span>
            </p>
          )}

          {GROUPS.map(({ key, label }) => {
            const list = grouped.get(key);
            if (!list || list.length === 0) return null;
            return (
              <section className="group" key={key}>
                <h2 className={`group__head mono group__head--${key}`}>
                  {label}
                  <span className="group__n">{list.length}</span>
                </h2>
                <ul className="list">
                  {list.map((a) => (
                    <li key={a.paneId}>
                      <button className="row" onClick={() => setOpenPane(a.paneId)}>
                        <StatusDot status={a.status} />
                        <span className="row__main">
                          <span className="row__title">{a.agent || "pane"}</span>
                          <span className="row__sub mono">{a.project || a.cwd || a.paneId}</span>
                        </span>
                        <svg className="row__go" viewBox="0 0 16 16" width="16" height="16" aria-hidden="true">
                          <path
                            d="m6 3 5 5-5 5"
                            fill="none"
                            stroke="currentColor"
                            strokeWidth="1.6"
                            strokeLinecap="round"
                            strokeLinejoin="round"
                          />
                        </svg>
                      </button>
                    </li>
                  ))}
                </ul>
              </section>
            );
          })}
        </main>
      )}

      {overlay === "palette" && <Palette commands={commands} onClose={closeOverlay} />}
      {overlay === "settings" && (
        <SettingsSheet
          session={session}
          client={client}
          pref={pref}
          actual={actual}
          wrap={wrap}
          onWrap={setWrap}
          onPref={setPref}
          onClose={closeOverlay}
        />
      )}
      {overlay === "sessions" && client && (
        <SessionSheet client={client} onClose={closeOverlay} />
      )}
      {renaming && client && (
        <RenameSheet client={client} agent={renaming} onClose={() => setRenaming(null)} />
      )}
    </div>
  );
}
