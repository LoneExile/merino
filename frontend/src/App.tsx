import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import type { Agent } from "../bindings/github.com/LoneExile/merino/internal/app";
import { useHerd } from "./useHerd";
import { useTheme } from "./theme";
import { useTermFontPref } from "./termFontPref";
import { useWrapPref } from "./wrapPref";
import { PaneView } from "./PaneView";
import { StatusDot, statusLabel } from "./StatusDot";
import { Palette, type Command } from "./Palette";
import { PairPhoneSheet, RenameSheet, SessionSheet, SettingsSheet } from "./Sheets";

type Overlay = "settings" | "sessions" | "palette" | "pair" | null;

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
  const termFont = useTermFontPref();

  // Full-page sheep splash (index.html #splash). Hold at least ~500ms so the
  // hop reads as intentional, then fade once the herd stream is ready.
  const splashAt = useRef(typeof performance !== "undefined" ? performance.now() : 0);
  useEffect(() => {
    if (!ready) return;
    const splash = document.getElementById("splash");
    if (!splash || splash.classList.contains("is-done")) return;
    const MIN_MS = 520;
    const elapsed = (typeof performance !== "undefined" ? performance.now() : 0) - splashAt.current;
    const wait = Math.max(0, MIN_MS - elapsed);
    const t = window.setTimeout(() => {
      splash.classList.add("is-done");
      splash.setAttribute("aria-busy", "false");
      window.setTimeout(() => splash.remove(), 360);
    }, wait);
    return () => window.clearTimeout(t);
  }, [ready]);

  const [openPane, setOpenPane] = useState<string | null>(null);
  const [overlay, setOverlay] = useState<Overlay>(null);
  const [renaming, setRenaming] = useState<Agent | null>(null);
  // First-run pairing: open Pair phone once. Drop ?pair=1 from the URL so a
  // later full reload (e.g. session switch) does not re-open this sheet.
  useEffect(() => {
    if (typeof window === "undefined") return;
    if (!window.location.search.includes("pair=1")) return;
    setOverlay("pair");
    try {
      const url = new URL(window.location.href);
      url.searchParams.delete("pair");
      const qs = url.searchParams.toString();
      window.history.replaceState(
        null,
        "",
        url.pathname + (qs ? `?${qs}` : "") + url.hash,
      );
    } catch {
      /* ignore */
    }
  }, []);

  // Once the first-run pair sheet has been shown, stamp it so the next cold
  // start does not force /?pair=1 again (session switch reloads the panel).
  useEffect(() => {
    if (overlay !== "pair") return;
    void client?.markFirstRunDone?.();
  }, [overlay, client]);

  // Tray context menu → open Settings / Pair phone (desktop only).
  useEffect(() => {
    if (typeof document === "undefined") return;
    const isWeb =
      document.querySelector('meta[name="herdr-mode"]')?.getAttribute("content") === "web";
    if (isWeb) return;
    let off: (() => void) | undefined;
    let cancelled = false;
    void import("@wailsio/runtime")
      .then(({ Events }) => {
        if (cancelled) return;
        off = Events.On("ui:open", (e: unknown) => {
          const data =
            e && typeof e === "object" && "data" in e
              ? (e as { data: unknown }).data
              : undefined;
          const which = typeof data === "string" ? data : "";
          if (which === "settings") {
            setOverlay("settings");
          } else if (which === "pair") {
            setOverlay("pair");
          } else if (which === "agents") {
            setOverlay(null);
            setOpenPane(null);
          }
        });
      })
      .catch(() => {
        /* browser path — no tray menu */
      });
    return () => {
      cancelled = true;
      off?.();
    };
  }, []);

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
            <img className="rail__mark" src="/favicon-64.png" alt="" width="22" height="22" />
            <span className="rail__name">Merino</span>
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
          termFont={termFont}
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
                      <div className="row">
                        <button
                          type="button"
                          className="row__open"
                          onClick={() => setOpenPane(a.paneId)}
                        >
                          <StatusDot status={a.status} />
                          <span className="row__main">
                            <span className="row__title">{a.agent || "pane"}</span>
                            <span className="row__sub mono">{a.project || a.cwd || a.paneId}</span>
                          </span>
                        </button>
                        {client?.focus && (
                          <button
                            type="button"
                            className="row__icon"
                            aria-label={`Focus ${a.agent || "pane"} in herdr`}
                            title="Focus in herdr"
                            onClick={() => void client.focus?.(a.paneId)}
                          >
                            {/* Crosshair / target — bring this pane to the front in herdr. */}
                            <svg viewBox="0 0 16 16" width="16" height="16" aria-hidden="true">
                              <circle
                                cx="8"
                                cy="8"
                                r="3.1"
                                fill="none"
                                stroke="currentColor"
                                strokeWidth="1.5"
                              />
                              <path
                                d="M8 1.75v2.4M8 11.85v2.4M1.75 8h2.4M11.85 8h2.4"
                                fill="none"
                                stroke="currentColor"
                                strokeWidth="1.5"
                                strokeLinecap="round"
                              />
                            </svg>
                          </button>
                        )}
                        <button
                          type="button"
                          className="row__icon row__icon--go"
                          aria-label={`Open ${a.agent || "pane"}`}
                          title="Open"
                          onClick={() => setOpenPane(a.paneId)}
                        >
                          <svg viewBox="0 0 16 16" width="16" height="16" aria-hidden="true">
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
                      </div>
                    </li>
                  ))}
                </ul>
              </section>
            );
          })}
        </main>
      )}

      {overlay === "palette" && <Palette commands={commands} onClose={closeOverlay} />}
      {overlay === "pair" && client && (
        <PairPhoneSheet
          client={client}
          onClose={closeOverlay}
          onOpenSettings={() => setOverlay("settings")}
        />
      )}
      {overlay === "settings" && (
        <SettingsSheet
          session={session}
          client={client}
          onOpenPair={() => setOverlay("pair")}
          onOpenSessions={() => setOverlay("sessions")}
          pref={pref}
          actual={actual}
          wrap={wrap}
          onWrap={setWrap}
          onPref={setPref}
          termFont={termFont}
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
