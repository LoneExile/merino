import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from "react";
import type { Agent } from "../bindings/github.com/LoneExile/herdr-tunnel/internal/app";
import type { Client, SlashCommand } from "./client";
import { usePaneStream } from "./usePaneStream";
import { StatusDot } from "./StatusDot";
import { parseAnsi } from "./ansi";

/** Mirrors app.MaxFreeTextLen so the limit is felt while typing, not as a 400. */
const MAX_TEXT = 1000;

/** How close to the bottom still counts as "at the bottom", in px. */
const PIN_SLACK = 48;

export interface PaneViewProps {
  client: Client;
  agent: Agent;
  wrap: boolean;
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
 *
 * The terminal can ALSO scroll horizontally, independent of that vertical
 * region: box-drawing TUI output runs far wider than a phone (see .term in
 * app.css), so by default it keeps its real geometry instead of wrapping
 * into nonsense, and pans sideways instead. The vertical auto-scroll-to-
 * bottom contract and the horizontal pan position must not fight each
 * other — see the layout effect below.
 */
export function PaneView({ client, agent, wrap, onBack, onRename }: PaneViewProps) {
  const { text, loaded, live, error } = usePaneStream(client, agent.paneId);
  const scroller = useRef<HTMLDivElement>(null);
  const [pinned, setPinned] = useState(true);
  const [menu, setMenu] = useState(false);

  // Last user-set horizontal scroll position. Tracked separately from
  // `pinned` (which is vertical-only) and restored on every text update so a
  // live poll never yanks someone panned right back to column 0.
  const scrollLeftRef = useRef(0);

  // Re-pin whenever the pane changes: opening a terminal must land on the
  // newest output, never halfway up yesterday's buffer. A different pane's
  // horizontal pan position isn't this one's business either.
  useEffect(() => {
    setPinned(true);
    scrollLeftRef.current = 0;
  }, [agent.paneId]);

  // Layout effect, not effect: scroll before paint so the jump to the bottom
  // is never visible as a flash of the top of the buffer.
  useLayoutEffect(() => {
    const el = scroller.current;
    if (!el) return;
    if (pinned) el.scrollTo({ top: el.scrollHeight, behavior: "auto" });
    // Reapply on every text update, pinned or not: some engines clamp
    // scrollLeft to the new (possibly smaller) scrollWidth when an update
    // briefly narrows the widest line on screen, and never restore it once
    // the content widens back out. Explicit beats hoping scrollTo({top})
    // left scrollLeft alone.
    el.scrollLeft = scrollLeftRef.current;
  }, [text, pinned]);

  const onScroll = useCallback(() => {
    const el = scroller.current;
    if (!el) return;
    scrollLeftRef.current = el.scrollLeft;
    const distance = el.scrollHeight - el.scrollTop - el.clientHeight;
    setPinned(distance <= PIN_SLACK);
  }, []);

  // Re-parsed only when the text actually changes, not on every render — a
  // 300-line ANSI-styled screen re-parsed on every poll tick regardless of
  // whether the poll saw new content would be wasted work almost every
  // tick, since usePaneStream/StreamPaneOutputANSI already suppress
  // unchanged screens before `text` ever updates.
  const segments = useMemo(() => parseAnsi(text), [text]);

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
          <pre className={`term${wrap ? " term--wrap" : ""}`}>
            {text
              ? segments.map((seg, i) => (
                  <span
                    key={i}
                    className={seg.style.backgroundColor ? "term__bg" : undefined}
                    style={seg.style}
                  >
                    {seg.text}
                  </span>
                ))
              : "(this pane has produced no output)"}
          </pre>
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

/**
 * Find a slash token at caret.
 * Token = "/" + non-whitespace, starting at a word boundary (start or after
 * whitespace / newline). Returns null when the caret is not inside one.
 */
function slashTokenAt(
  text: string,
  caret: number,
): { start: number; end: number; query: string } | null {
  const pos = Math.max(0, Math.min(caret, text.length));
  // Walk left to the start of the current non-space run.
  let i = pos;
  while (i > 0 && !/\s/.test(text[i - 1]!)) i--;
  if (text[i] !== "/") return null;
  // Token cannot contain whitespace; end is first whitespace or EOS.
  let j = i + 1;
  while (j < text.length && !/\s/.test(text[j]!)) j++;
  // Caret must sit inside [i, j] (still editing this token).
  if (pos < i || pos > j) return null;
  return { start: i, end: j, query: text.slice(i + 1, j) };
}

function Composer({ client, agent, onSent }: ComposerProps) {
  const [value, setValue] = useState("");
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);
  const box = useRef<HTMLTextAreaElement>(null);
  const [slashHits, setSlashHits] = useState<SlashCommand[]>([]);
  const [slashIdx, setSlashIdx] = useState(0);
  // Caret position drives mid-string slash detection (not only draft-start).
  const [caret, setCaret] = useState(0);
  // Range of the active "/token" inside value when the menu is open.
  const slashRange = useRef<{ start: number; end: number } | null>(null);
  const slashOpen = slashHits.length > 0;

  const canWrite = Boolean(client.sendText);
  const canKeys = Boolean(client.sendKeys);
  const canApprove = Boolean(client.respond) && agent.status === "blocked";

  // Grow with the content up to a cap, so a long reply is readable while the
  // terminal keeps most of the screen.
  useEffect(() => {
    const el = box.current;
    if (!el) return;
    el.style.height = "auto";
    el.style.height = `${Math.min(el.scrollHeight, 140)}px`;
  }, [value]);

  // Slash typeahead at the caret: any "/token" whose start is a word boundary
  // (start of draft or after whitespace). Mid-sentence "/help" works the same
  // as a draft that is only "/hel".
  useEffect(() => {
    if (!client?.slashCommands || !canWrite) {
      setSlashHits([]);
      slashRange.current = null;
      return;
    }
    const token = slashTokenAt(value, caret);
    if (!token) {
      setSlashHits([]);
      slashRange.current = null;
      return;
    }
    slashRange.current = { start: token.start, end: token.end };
    const q = token.query;
    let alive = true;
    const t = window.setTimeout(() => {
      void client.slashCommands?.(agent.agent || "", q, agent.cwd || undefined).then(
        (hits) => {
          if (!alive) return;
          setSlashHits(Array.isArray(hits) ? hits : []);
          setSlashIdx(0);
        },
        () => {
          if (alive) setSlashHits([]);
        },
      );
    }, 80);
    return () => {
      alive = false;
      window.clearTimeout(t);
    };
  }, [value, caret, client, agent.agent, agent.cwd, canWrite]);

  const applySlash = useCallback((cmd: SlashCommand) => {
    const range = slashRange.current ?? slashTokenAt(value, caret);
    const start = range?.start ?? 0;
    const end = range?.end ?? value.length;
    const next = value.slice(0, start) + cmd.value + value.slice(end);
    const newCaret = start + cmd.value.length;
    setValue(next);
    setCaret(newCaret);
    setSlashHits([]);
    setSlashIdx(0);
    slashRange.current = null;
    // Restore caret after React commits the new value.
    requestAnimationFrame(() => {
      const el = box.current;
      if (!el) return;
      el.focus();
      el.setSelectionRange(newCaret, newCaret);
    });
  }, [value, caret]);

  const send = useCallback(
    async (text: string, kind: "reply" | "approve") => {
      const body = text.trim();
      if (!body || busy) return;
      setBusy(true);
      setErr(null);
      const restore = value;
      if (kind === "reply") {
        setValue("");
        setSlashHits([]);
      }
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

  // Allowlisted keys only (server-side guard). Used for TUI menus that do not
  // read free text — the Ask chooser wants ↑/↓ + Enter or Esc, not a typed
  // reply in the chat box.
  const press = useCallback(
    async (keys: string[]) => {
      if (!canKeys || busy) return;
      setBusy(true);
      setErr(null);
      try {
        await client.sendKeys?.(agent.paneId, keys);
        onSent();
      } catch (e) {
        setErr(e instanceof Error ? e.message : String(e));
      } finally {
        setBusy(false);
      }
    },
    [agent.paneId, busy, canKeys, client, onSent],
  );

  if (!canWrite && !canApprove && !canKeys) {
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
        <div className="composer__quick" role="group" aria-label="Quick responses">
          {(
            [
              { label: "Yes", value: "yes", aria: "Approve: Yes" },
              { label: "Trust", value: "trust", aria: "Approve: Trust" },
              { label: "No", value: "no", aria: "Refuse: No" },
            ] as const
          ).map((b) => (
            <button
              key={b.label}
              type="button"
              className="btn btn--quick"
              disabled={busy}
              aria-label={b.aria}
              onClick={() => void send(b.value, "approve")}
            >
              {b.label}
            </button>
          ))}
        </div>
      )}

      {canKeys && (
        <div className="composer__keys" role="toolbar" aria-label="Terminal keys">
          <span className="composer__keys-hint mono">TUI</span>
          {(
            [
              { label: "Esc", keys: ["Escape"], title: "Cancel / close TUI menu" },
              { label: "↑", keys: ["Up"], title: "Move up" },
              { label: "↓", keys: ["Down"], title: "Move down" },
              { label: "Enter", keys: ["Enter"], title: "Select / confirm" },
              { label: "Tab", keys: ["Tab"], title: "Tab" },
            ] as const
          ).map((b) => (
            <button
              key={b.label}
              type="button"
              className="btn btn--key"
              disabled={busy}
              title={b.title}
              aria-label={b.title}
              onClick={() => void press([...b.keys])}
            >
              {b.label}
            </button>
          ))}
        </div>
      )}

      {canWrite && slashOpen && (
        <ul className="slash" role="listbox" aria-label="Slash commands">
          {slashHits.map((h, i) => (
            <li key={`${h.source ?? "x"}:${h.name}`}>
              <button
                type="button"
                role="option"
                aria-selected={i === slashIdx}
                className={`slash__row${i === slashIdx ? " is-on" : ""}`}
                onMouseDown={(e) => {
                  // prevent blur-before-click wiping the menu
                  e.preventDefault();
                  applySlash(h);
                }}
              >
                <span className="slash__name mono">{h.value.trim()}</span>
                {h.description && <span className="slash__desc">{h.description}</span>}
              </button>
            </li>
          ))}
        </ul>
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
            placeholder={
              agent.agent
                ? `Message ${agent.agent}, or / for commands…`
                : canKeys
                  ? `Reply, or Esc/↑/↓/Enter for menus…`
                  : `Reply to pane…`
            }
            aria-label={`Reply to ${agent.agent || "pane"}`}
            onChange={(e) => {
              setValue(e.target.value);
              setCaret(e.target.selectionStart ?? e.target.value.length);
            }}
            onSelect={(e) => {
              setCaret(e.currentTarget.selectionStart ?? 0);
            }}
            onClick={(e) => {
              setCaret(e.currentTarget.selectionStart ?? 0);
            }}
            onKeyUp={(e) => {
              setCaret(e.currentTarget.selectionStart ?? 0);
            }}
            onKeyDown={(e) => {
              if (e.nativeEvent.isComposing) return;

              // Read the live DOM value, not React state: a keystroke that
              // just typed the first character has not yet re-rendered, so
              // `value` can still look empty and an immediate Enter would
              // go to the pane instead of sending the reply.
              const live = e.currentTarget.value;

              // Slash typeahead navigation — steals arrows/enter/tab/esc while open.
              if (slashOpen) {
                if (e.key === "ArrowDown") {
                  e.preventDefault();
                  setSlashIdx((i) => (i + 1) % slashHits.length);
                  return;
                }
                if (e.key === "ArrowUp") {
                  e.preventDefault();
                  setSlashIdx((i) => (i - 1 + slashHits.length) % slashHits.length);
                  return;
                }
                if (e.key === "Enter" || e.key === "Tab") {
                  e.preventDefault();
                  const hit = slashHits[slashIdx] ?? slashHits[0];
                  if (hit) applySlash(hit);
                  return;
                }
                if (e.key === "Escape") {
                  e.preventDefault();
                  setSlashHits([]);
                  return;
                }
              }

              // Empty box → TUI navigation keys go to the pane, not the chat.
              // That is how you drive the Ask chooser (↑/↓, Enter, Esc) from a
              // phone keyboard or a laptop without hunting for the toolbar.
              if (canKeys && live.length === 0 && !e.metaKey && !e.ctrlKey && !e.altKey) {
                const map: Record<string, string> = {
                  Escape: "Escape",
                  ArrowUp: "Up",
                  ArrowDown: "Down",
                  ArrowLeft: "Left",
                  ArrowRight: "Right",
                  Tab: "Tab",
                  Enter: "Enter",
                };
                const k = map[e.key];
                if (k) {
                  e.preventDefault();
                  void press([k]);
                  return;
                }
              }

              // Non-empty: Esc clears the draft (do not fire Escape into the
              // pane while the user is mid-reply). Enter sends, Shift+Enter
              // inserts a newline — the chat convention.
              if (e.key === "Escape" && live.length > 0) {
                e.preventDefault();
                setValue("");
                return;
              }
              if (e.key === "Enter" && !e.shiftKey) {
                e.preventDefault();
                void send(live, "reply");
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
