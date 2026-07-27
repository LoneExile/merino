import { useCallback, useEffect, useLayoutEffect, useRef, useState } from "react";
import type { Agent } from "../bindings/github.com/LoneExile/herdr-tunnel/internal/app";
import type { Client } from "./client";
import { usePaneStream } from "./usePaneStream";
import { StatusDot } from "./StatusDot";

/** Mirrors app.MaxFreeTextLen so the limit is felt while typing, not as a 400. */
const MAX_TEXT = 1000;

/** How close to the bottom still counts as "at the bottom", in px. */
const PIN_SLACK = 48;

export interface PaneViewProps {
  client: Client;
  agent: Agent;
  onBack: () => void;
  onRename: (agent: Agent) => void;
}

/**
 * One agent's terminal, with a composer fixed to the bottom of the view.
 *
 * The layout contract from design.md: this component owns exactly one
 * scrolling region — the terminal. The header and the composer are fixed
 * siblings in a grid, so reaching the input never requires scrolling past the
 * buffer. On a phone that is the whole difference between usable and not.
 */
export function PaneView({ client, agent, onBack, onRename }: PaneViewProps) {
  const { text, loaded, live, error } = usePaneStream(client, agent.paneId);
  const scroller = useRef<HTMLDivElement>(null);
  const [pinned, setPinned] = useState(true);
  const [menu, setMenu] = useState(false);

  const toBottom = useCallback((behavior: ScrollBehavior = "auto") => {
    const el = scroller.current;
    if (!el) return;
    el.scrollTo({ top: el.scrollHeight, behavior });
  }, []);

  // Re-pin whenever the pane changes: opening a terminal must land on the
  // newest output, never halfway up yesterday's buffer.
  useEffect(() => {
    setPinned(true);
  }, [agent.paneId]);

  // Layout effect, not effect: scroll before paint so the jump to the bottom
  // is never visible as a flash of the top of the buffer.
  useLayoutEffect(() => {
    if (pinned) toBottom();
  }, [text, pinned, toBottom]);

  const onScroll = useCallback(() => {
    const el = scroller.current;
    if (!el) return;
    const distance = el.scrollHeight - el.scrollTop - el.clientHeight;
    setPinned(distance <= PIN_SLACK);
  }, []);

  return (
    <section className="pane" aria-label={`${agent.agent} terminal`}>
      <header className="pane__bar">
        <button className="btn btn--icon" onClick={onBack} aria-label="Back to agents">
          <svg viewBox="0 0 16 16" width="16" height="16" aria-hidden="true">
            <path
              d="M10 3 5 8l5 5"
              fill="none"
              stroke="currentColor"
              strokeWidth="1.75"
              strokeLinecap="round"
              strokeLinejoin="round"
            />
          </svg>
        </button>

        <div className="pane__id">
          <span className="pane__agent">
            <StatusDot status={agent.status} />
            {agent.agent || "pane"}
          </span>
          <span className="mono pane__meta">{agent.paneId}</span>
        </div>

        <div className="pane__bar-actions">
          {!live && loaded && (
            <span className="chip chip--warn mono" role="status">
              reconnecting
            </span>
          )}
          <div className="menu">
            <button
              className="btn btn--icon"
              aria-haspopup="menu"
              aria-expanded={menu}
              onClick={() => setMenu((v) => !v)}
              aria-label="Pane actions"
            >
              <svg viewBox="0 0 16 16" width="16" height="16" aria-hidden="true">
                <circle cx="8" cy="3" r="1.4" fill="currentColor" />
                <circle cx="8" cy="8" r="1.4" fill="currentColor" />
                <circle cx="8" cy="13" r="1.4" fill="currentColor" />
              </svg>
            </button>
            {menu && (
              <>
                <div className="menu__scrim" onClick={() => setMenu(false)} />
                <div className="menu__list" role="menu">
                  {client.focus && (
                    <button
                      role="menuitem"
                      onClick={() => {
                        setMenu(false);
                        void client.focus?.(agent.paneId);
                      }}
                    >
                      Focus in herdr
                    </button>
                  )}
                  {client.rename && (
                    <button
                      role="menuitem"
                      onClick={() => {
                        setMenu(false);
                        onRename(agent);
                      }}
                    >
                      Rename…
                    </button>
                  )}
                  {client.interrupt && (
                    <button
                      role="menuitem"
                      className="danger"
                      onClick={() => {
                        setMenu(false);
                        void client.interrupt?.(agent.paneId);
                      }}
                    >
                      Interrupt
                    </button>
                  )}
                </div>
              </>
            )}
          </div>
        </div>
      </header>

      <div className="pane__screen" ref={scroller} onScroll={onScroll} tabIndex={0}>
        {loaded ? (
          <pre className="term">{text || "(this pane has produced no output)"}</pre>
        ) : (
          <p className="term term--muted">{error ?? "Connecting to pane…"}</p>
        )}
      </div>

      {!pinned && (
        <button className="jump" onClick={() => setPinned(true)}>
          Jump to latest
        </button>
      )}

      <Composer client={client} agent={agent} onSent={() => setPinned(true)} />
    </section>
  );
}

interface ComposerProps {
  client: Client;
  agent: Agent;
  onSent: () => void;
}

/**
 * The reply box, fixed to the bottom of the pane view.
 *
 * Optimistic clear with restore on failure: the text goes back in the box with
 * the error, because making someone retype a message the server refused is the
 * rudest possible way to report a refusal.
 */
function Composer({ client, agent, onSent }: ComposerProps) {
  const [value, setValue] = useState("");
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);
  const box = useRef<HTMLTextAreaElement>(null);

  const canWrite = Boolean(client.sendText);
  const canApprove = Boolean(client.respond) && agent.status === "blocked";

  // Grow with the content up to a cap, so a long reply is readable while the
  // terminal keeps most of the screen.
  useEffect(() => {
    const el = box.current;
    if (!el) return;
    el.style.height = "auto";
    el.style.height = `${Math.min(el.scrollHeight, 140)}px`;
  }, [value]);

  const send = useCallback(
    async (text: string, kind: "reply" | "approve") => {
      const body = text.trim();
      if (!body || busy) return;
      setBusy(true);
      setErr(null);
      const restore = value;
      if (kind === "reply") setValue("");
      try {
        // No trailing newline: the backend presses Enter as a key. A bare "\n"
        // is ignored by a TUI reading raw input and the text would sit
        // unsubmitted in the prompt.
        if (kind === "approve") await client.respond?.(agent.paneId, body);
        else await client.sendText?.(agent.paneId, body);
        onSent();
      } catch (e) {
        setErr(e instanceof Error ? e.message : String(e));
        if (kind === "reply") setValue(restore);
      } finally {
        setBusy(false);
      }
    },
    [agent.paneId, busy, client, onSent, value],
  );

  if (!canWrite && !canApprove) {
    return (
      <footer className="composer composer--ro">
        <span className="mono">read-only · writes are disabled on this server</span>
      </footer>
    );
  }

  return (
    <footer className="composer">
      {err && (
        <p className="composer__err" role="alert">
          {err}
        </p>
      )}

      {canApprove && (
        <div className="composer__quick">
          {["Yes", "Trust", "No"].map((label) => (
            <button
              key={label}
              className="btn btn--quick"
              disabled={busy}
              onClick={() => void send(label.toLowerCase(), "approve")}
            >
              {label}
            </button>
          ))}
        </div>
      )}

      {canWrite && (
        <div className="composer__row">
          <textarea
            ref={box}
            className="composer__input"
            rows={1}
            value={value}
            maxLength={MAX_TEXT}
            disabled={busy}
            placeholder={`Reply to ${agent.agent || "pane"}…`}
            aria-label={`Reply to ${agent.agent || "pane"}`}
            onChange={(e) => setValue(e.target.value)}
            onKeyDown={(e) => {
              // Enter sends, Shift+Enter makes a newline — the convention every
              // chat surface uses. IME composition must never be interrupted.
              if (e.key === "Enter" && !e.shiftKey && !e.nativeEvent.isComposing) {
                e.preventDefault();
                void send(value, "reply");
              }
            }}
          />
          <button
            className="btn btn--primary"
            disabled={busy || !value.trim()}
            onClick={() => void send(value, "reply")}
          >
            Send
          </button>
        </div>
      )}

      {value.length > MAX_TEXT - 100 && (
        <p className="composer__count mono">
          {MAX_TEXT - value.length} characters left
        </p>
      )}
    </footer>
  );
}
