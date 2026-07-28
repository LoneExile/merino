import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from "react";
import type { Agent } from "../bindings/github.com/LoneExile/merino/internal/app";
import type { Client, SlashCommand } from "./client";
import { usePaneStream } from "./usePaneStream";
import { StatusDot } from "./StatusDot";
import { parseAnsi } from "./ansi";
import { pasteImageURL, splitPasteImages } from "./pasteImages";
import type { useTermFontPref } from "./termFontPref";
import { findMatches, stripAnsi } from "./termSearch";

/** Mirrors app.MaxFreeTextLen so the limit is felt while typing, not as a 400. */
const MAX_TEXT = 1000;

/** How close to the bottom still counts as "at the bottom", in px. */
const PIN_SLACK = 48;

export interface PaneViewProps {
  client: Client;
  agent: Agent;
  /** From /api/session — live write gate (Mac Settings). */
  readOnly?: boolean;
  wrap: boolean;
  termFont: ReturnType<typeof useTermFontPref>;
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
/** Full-screen image preview — chips and terminal thumbs open this. */
function ImageLightbox({
  src,
  alt,
  onClose,
}: {
  src: string;
  alt: string;
  onClose: () => void;
}) {
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") onClose();
    };
    window.addEventListener("keydown", onKey);
    const prev = document.body.style.overflow;
    document.body.style.overflow = "hidden";
    return () => {
      window.removeEventListener("keydown", onKey);
      document.body.style.overflow = prev;
    };
  }, [onClose]);

  return (
    <div
      className="lightbox"
      role="dialog"
      aria-modal="true"
      aria-label="Image preview"
      onClick={onClose}
    >
      <button type="button" className="lightbox__close btn btn--icon" aria-label="Close preview" onClick={onClose}>
        <svg viewBox="0 0 16 16" width="18" height="18" aria-hidden="true">
          <path
            d="m4 4 8 8M12 4l-8 8"
            fill="none"
            stroke="currentColor"
            strokeWidth="1.75"
            strokeLinecap="round"
          />
        </svg>
      </button>
      <img
        className="lightbox__img"
        src={src}
        alt={alt}
        onClick={(e) => e.stopPropagation()}
      />
      {alt ? <p className="lightbox__cap mono">{alt}</p> : null}
    </div>
  );
}


/** Render pane text with optional search highlights + inline paste images. */
function renderTermBody(
  text: string,
  matches: { start: number; end: number }[],
  activeIdx: number,
  onImg: (src: string, alt: string) => void,
) {
  const pieces = splitPasteImages(text);
  // Map match ranges from full plain text; pieces of kind text need local offsets.
  let plainCursor = 0;
  const nodes: React.ReactNode[] = [];

  pieces.forEach((piece, pi) => {
    if (piece.kind === "img") {
      // Path characters count in plain text too.
      plainCursor += stripAnsi(piece.path).length;
      nodes.push(
        <button
          key={`img-${pi}`}
          type="button"
          className="term__img-wrap"
          onClick={() => onImg(pasteImageURL(piece.name), piece.path)}
        >
          <img
            className="term__img"
            src={pasteImageURL(piece.name)}
            alt={piece.path}
            title="Tap to enlarge"
            loading="lazy"
          />
        </button>,
      );
      return;
    }

    const segs = parseAnsi(piece.text);
    let localPlain = 0;
    const piecePlainLen = stripAnsi(piece.text).length;
    const piecePlainStart = plainCursor;

    segs.forEach((seg, si) => {
      let rest = seg.text;
      let restPlainAt = piecePlainStart + localPlain;
      // localPlain advances by visible chars only (seg.text has no escapes).
      while (rest.length > 0) {
        const abs = restPlainAt;
        // Next match boundary strictly after abs, or end of rest.
        let cutPlain = abs + rest.length;
        let mark: "none" | "hit" | "active" = "none";
        for (let mi = 0; mi < matches.length; mi++) {
          const m = matches[mi]!;
          if (abs >= m.end || abs + rest.length <= m.start) continue;
          if (abs >= m.start && abs < m.end) {
            mark = mi === activeIdx ? "active" : "hit";
            cutPlain = Math.min(cutPlain, m.end);
            break;
          }
          if (m.start > abs) {
            cutPlain = Math.min(cutPlain, m.start);
            break;
          }
        }
        const take = Math.max(1, cutPlain - abs);
        const chunk = rest.slice(0, take);
        rest = rest.slice(take);
        restPlainAt += take;
        localPlain += take;
        const cls = [
          seg.style.backgroundColor ? "term__bg" : "",
          mark === "hit" ? "term__hit" : "",
          mark === "active" ? "term__hit term__hit--active" : "",
        ]
          .filter(Boolean)
          .join(" ");
        nodes.push(
          <span
            key={`${pi}-${si}-${abs}`}
            className={cls || undefined}
            style={seg.style}
            data-match-active={mark === "active" ? "true" : undefined}
          >
            {chunk}
          </span>,
        );
      }
    });
    plainCursor += piecePlainLen;
  });

  return nodes;
}

export function PaneView({ client, agent, readOnly = false, wrap, termFont, onBack, onRename }: PaneViewProps) {
  // pinned is declared below; the stream hook only needs it to release a
  // history hold when the user returns to the live tail — initialise true.
  const [pinned, setPinned] = useState(true);
  const [lightbox, setLightbox] = useState<{ src: string; alt: string } | null>(null);
  const [searchOpen, setSearchOpen] = useState(false);
  const [searchQ, setSearchQ] = useState("");
  const [matchIdx, setMatchIdx] = useState(0);
  const searchInputRef = useRef<HTMLInputElement>(null);
  const { text, loaded, live, error, loadingMore, canLoadMore, loadMore } = usePaneStream(
    client,
    agent.paneId,
    pinned,
  );
  const scroller = useRef<HTMLDivElement>(null);
  const [menu, setMenu] = useState(false);

  const plainText = useMemo(() => stripAnsi(text), [text]);
  const searchMatches = useMemo(
    () => (searchOpen ? findMatches(plainText, searchQ) : []),
    [searchOpen, plainText, searchQ],
  );
  const safeMatchIdx =
    searchMatches.length === 0 ? 0 : ((matchIdx % searchMatches.length) + searchMatches.length) % searchMatches.length;

  // Last user-set horizontal scroll position. Tracked separately from
  // `pinned` (which is vertical-only) and restored on every text update so a
  // live poll never yanks someone panned right back to column 0.
  const scrollLeftRef = useRef(0);
  // When history grows upward, pin the viewport to the same content the user
  // was reading instead of jumping to the new top.
  const stickTopRef = useRef<{ height: number; top: number } | null>(null);
  const prevTextLenRef = useRef(0);

  // Re-pin whenever the pane changes: opening a terminal must land on the
  // newest output, never halfway up yesterday's buffer. A different pane's
  // horizontal pan position isn't this one's business either.
  useEffect(() => {
    setPinned(true);
    scrollLeftRef.current = 0;
    stickTopRef.current = null;
    prevTextLenRef.current = 0;
  }, [agent.paneId]);

  // Layout effect, not effect: scroll before paint so the jump to the bottom
  // is never visible as a flash of the top of the buffer.
  useLayoutEffect(() => {
    const el = scroller.current;
    if (!el) return;

    const grew = text.length > prevTextLenRef.current;
    const shrunk = text.length < prevTextLenRef.current;
    prevTextLenRef.current = text.length;

    // History prepend: keep the same content under the viewport.
    if (stickTopRef.current) {
      if (grew) {
        const { height, top } = stickTopRef.current;
        const delta = el.scrollHeight - height;
        el.scrollTop = top + Math.max(0, delta);
        stickTopRef.current = null;
      }
      // While waiting for loadMore (or after a failed shrink), do NOT fall
      // through to pin-to-bottom — that was yanking the user after an error.
    } else if (pinned && !shrunk) {
      el.scrollTo({ top: el.scrollHeight, behavior: "auto" });
    }

    // Reapply on every text update, pinned or not: some engines clamp
    // scrollLeft to the new (possibly smaller) scrollWidth when an update
    // briefly narrows the widest line on screen, and never restore it once
    // the content widens back out. Explicit beats hoping scrollTo({top})
    // left scrollLeft alone.
    el.scrollLeft = scrollLeftRef.current;
  }, [text, pinned]);

  // Keep the active search hit visible in the scrollport.
  useLayoutEffect(() => {
    if (!searchOpen || searchMatches.length === 0) return;
    const el = scroller.current?.querySelector("[data-match-active=\"true\"]");
    el?.scrollIntoView({ block: "center", inline: "nearest", behavior: "smooth" });
  }, [searchOpen, safeMatchIdx, searchMatches.length, text]);

  // Focus the find field when opened.
  useEffect(() => {
    if (searchOpen) {
      const t = window.setTimeout(() => searchInputRef.current?.focus(), 10);
      return () => window.clearTimeout(t);
    }
  }, [searchOpen]);

  const onScroll = useCallback(() => {
    const el = scroller.current;
    if (!el) return;
    scrollLeftRef.current = el.scrollLeft;
    const distance = el.scrollHeight - el.scrollTop - el.clientHeight;
    setPinned(distance <= PIN_SLACK);
    // Near the top → ask for a larger recent buffer (herdr has no offset API).
    if (el.scrollTop <= 24 && canLoadMore && !loadingMore) {
      stickTopRef.current = { height: el.scrollHeight, top: el.scrollTop };
      setPinned(false);
      loadMore();
    }
  }, [canLoadMore, loadingMore, loadMore]);

  const openSearch = useCallback(() => {
    setSearchOpen(true);
    setMenu(false);
  }, []);

  const closeSearch = useCallback(() => {
    setSearchOpen(false);
    setSearchQ("");
    setMatchIdx(0);
  }, []);

  const goMatch = useCallback(
    (dir: -1 | 1) => {
      if (searchMatches.length === 0) return;
      setMatchIdx((i) => {
        const n = searchMatches.length;
        return (i + dir + n * 8) % n;
      });
    },
    [searchMatches.length],
  );

  // Expand loaded history when find has zero hits and more is available.
  useEffect(() => {
    if (!searchOpen || !searchQ.trim()) return;
    if (searchMatches.length > 0) return;
    if (!canLoadMore || loadingMore) return;
    const el = scroller.current;
    if (el) {
      stickTopRef.current = { height: el.scrollHeight, top: el.scrollTop };
    }
    setPinned(false);
    loadMore();
  }, [searchOpen, searchQ, searchMatches.length, canLoadMore, loadingMore, loadMore]);

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      const mod = e.metaKey || e.ctrlKey;
      if (mod && (e.key === "f" || e.key === "F")) {
        e.preventDefault();
        openSearch();
        return;
      }
      if (mod && (e.key === "=" || e.key === "+")) {
        e.preventDefault();
        termFont.zoomIn();
        return;
      }
      if (mod && e.key === "-") {
        e.preventDefault();
        termFont.zoomOut();
        return;
      }
      if (mod && e.key === "0") {
        e.preventDefault();
        termFont.setPx(12);
        return;
      }
      if (!searchOpen) return;
      if (e.key === "Escape") {
        e.preventDefault();
        closeSearch();
        return;
      }
      if (e.key === "Enter") {
        e.preventDefault();
        goMatch(e.shiftKey ? -1 : 1);
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [searchOpen, openSearch, closeSearch, goMatch, termFont]);

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
          <button
            type="button"
            className={`btn btn--icon${searchOpen ? " is-on" : ""}`}
            aria-label="Find in pane"
            title="Find (⌘F)"
            aria-pressed={searchOpen}
            onClick={() => (searchOpen ? closeSearch() : openSearch())}
          >
            <svg viewBox="0 0 16 16" width="16" height="16" aria-hidden="true">
              <circle cx="7" cy="7" r="4.25" fill="none" stroke="currentColor" strokeWidth="1.5" />
              <path d="M10.2 10.2 13.5 13.5" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" />
            </svg>
          </button>
          <div className="pane__zoom" role="group" aria-label="Terminal font size">
            <button
              type="button"
              className="btn btn--icon"
              aria-label="Decrease font size"
              title="Smaller (⌘-)"
              disabled={!termFont.canZoomOut}
              onClick={() => termFont.zoomOut()}
            >
              <span className="pane__zoom-label" aria-hidden="true">A−</span>
            </button>
            <button
              type="button"
              className="btn btn--icon"
              aria-label="Increase font size"
              title="Larger (⌘+)"
              disabled={!termFont.canZoomIn}
              onClick={() => termFont.zoomIn()}
            >
              <span className="pane__zoom-label pane__zoom-label--lg" aria-hidden="true">A+</span>
            </button>
          </div>
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
                  <button
                    type="button"
                    role="menuitem"
                    onClick={() => openSearch()}
                  >
                    Find in pane…
                  </button>
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

      <div className="pane__body">
        {searchOpen && (
          <div className="pane__find" role="search">
            <input
              ref={searchInputRef}
              className="pane__find-input"
              type="search"
              value={searchQ}
              placeholder="Find in loaded output…"
              aria-label="Find in loaded output"
              autoCapitalize="off"
              autoCorrect="off"
              spellCheck={false}
              onChange={(e) => {
                setSearchQ(e.target.value);
                setMatchIdx(0);
              }}
            />
            <span className="pane__find-count mono" aria-live="polite">
              {searchQ.trim()
                ? searchMatches.length
                  ? `${safeMatchIdx + 1}/${searchMatches.length}`
                  : canLoadMore
                    ? "0 — loading older…"
                    : "0"
                : "—"}
            </span>
            <button
              type="button"
              className="btn btn--icon"
              aria-label="Previous match"
              disabled={searchMatches.length === 0}
              onClick={() => goMatch(-1)}
            >
              <svg viewBox="0 0 16 16" width="16" height="16" aria-hidden="true">
                <path d="M4 10 8 6l4 4" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round" />
              </svg>
            </button>
            <button
              type="button"
              className="btn btn--icon"
              aria-label="Next match"
              disabled={searchMatches.length === 0}
              onClick={() => goMatch(1)}
            >
              <svg viewBox="0 0 16 16" width="16" height="16" aria-hidden="true">
                <path d="M4 6 8 10l4-4" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round" />
              </svg>
            </button>
            <button type="button" className="btn btn--icon" aria-label="Close find" onClick={closeSearch}>
              <svg viewBox="0 0 16 16" width="16" height="16" aria-hidden="true">
                <path d="m4 4 8 8M12 4l-8 8" fill="none" stroke="currentColor" strokeWidth="1.75" strokeLinecap="round" />
              </svg>
            </button>
          </div>
        )}
        <div className="pane__screen" ref={scroller} onScroll={onScroll} tabIndex={0}>
          {loadingMore && (
            <div className="pane__history mono" role="status">
              Loading earlier output…
            </div>
          )}
          {!loadingMore && error && loaded && (
            <div className="pane__history pane__history--err mono" role="status">
              Couldn’t load more history — scroll stays here. {error}
            </div>
          )}
          {!loadingMore && !canLoadMore && text.length > 0 && !error && (
            <div className="pane__history pane__history--end mono" role="status">
              Beginning of available history
            </div>
          )}

          {loaded ? (
            <pre className={`term${wrap ? " term--wrap" : ""}`}>
              {text
                ? renderTermBody(text, searchMatches, safeMatchIdx, (src, alt) =>
                    setLightbox({ src, alt }),
                  )
                : "(this pane has produced no output)"}
            </pre>
          ) : (
            <p className="term term--muted">{error ?? "Connecting to pane…"}</p>
          )}
        </div>

        {/* Sits in the terminal column, above the composer — never covers keys. */}
        {!pinned && (
          <div className="jump-dock">
            <button
              type="button"
              className="jump"
              onClick={() => {
                stickTopRef.current = null;
                setPinned(true);
                const el = scroller.current;
                if (el) el.scrollTop = el.scrollHeight;
              }}
            >
              ↓ Latest
            </button>
          </div>
        )}
      </div>

      <Composer client={client} agent={agent} readOnly={readOnly} onSent={() => setPinned(true)} />

      {lightbox && (
        <ImageLightbox src={lightbox.src} alt={lightbox.alt} onClose={() => setLightbox(null)} />
      )}
    </section>
  );
}

interface ComposerProps {
  client: Client;
  agent: Agent;
  readOnly?: boolean;
  onSent: () => void;
}

interface PendingAttach {
  id: string;
  path: string;
  mime: string;
  preview: string;
  name: string;
}

const MAX_ATTACH = 4;

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

function Composer({ client, agent, readOnly = false, onSent }: ComposerProps) {
  const [value, setValue] = useState("");
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);
  const box = useRef<HTMLTextAreaElement>(null);
  const [slashHits, setSlashHits] = useState<SlashCommand[]>([]);
  const [slashIdx, setSlashIdx] = useState(0);
  // Caret position drives mid-string slash detection (not only draft-start).
  const [caret, setCaret] = useState(0);
  const [pending, setPending] = useState<PendingAttach[]>([]);
  const [attachBusy, setAttachBusy] = useState(false);
  const [chipPreview, setChipPreview] = useState<PendingAttach | null>(null);
  const fileRef = useRef<HTMLInputElement>(null);
  // Range of the active "/token" inside value when the menu is open.
  const slashRange = useRef<{ start: number; end: number } | null>(null);
  const slashOpen = slashHits.length > 0;

  const canWrite = Boolean(client.sendText) && !readOnly;
  const canKeys = Boolean(client.sendKeys) && !readOnly;
  const canApprove = Boolean(client.respond) && agent.status === "blocked" && !readOnly;
  const canAttach = Boolean(client.attachImage) && canWrite;

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
      void client.slashCommands?.(agent.paneId, agent.agent || "", q).then(
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


  const stageBlob = useCallback(
    async (blob: Blob, nameHint?: string) => {
      if (!canAttach || !client.attachImage) return;
      if (pending.length >= MAX_ATTACH) {
        setErr(`At most ${MAX_ATTACH} images per message`);
        return;
      }
      const nameGuess = nameHint || (blob instanceof File ? blob.name : "") || "image.jpg";
      if (/\.(heic|heif)$/i.test(nameGuess) || /image\/hei[cf]/i.test(blob.type || "")) {
        setErr("HEIC photos aren’t supported — choose JPEG or PNG (or disable “High Efficiency” in Camera settings).");
        return;
      }
      const looksImage =
        (blob.type && blob.type.startsWith("image/")) ||
        /\.(png|jpe?g|gif|webp)$/i.test(nameGuess);
      if (!looksImage) {
        setErr("Only PNG, JPEG, GIF, or WebP images can be attached");
        return;
      }
      // Build a preview URL first (FileReader is more reliable than object URLs
      // for some mobile camera/gallery picks).
      const preview = await new Promise<string>((resolve, reject) => {
        const fr = new FileReader();
        fr.onload = () => resolve(String(fr.result || ""));
        fr.onerror = () => reject(fr.error ?? new Error("preview failed"));
        fr.readAsDataURL(blob);
      }).catch(() => URL.createObjectURL(blob));

      setAttachBusy(true);
      setErr(null);
      try {
        // Ensure the server gets a usable MIME when the OS leaves type empty.
        let upload = blob;
        if (!blob.type || blob.type === "application/octet-stream") {
          const ext = nameGuess.split(".").pop()?.toLowerCase();
          const mime =
            ext === "png"
              ? "image/png"
              : ext === "gif"
                ? "image/gif"
                : ext === "webp"
                  ? "image/webp"
                  : "image/jpeg";
          upload = new File([blob], nameGuess, { type: mime });
        }
        const { path, mime } = await client.attachImage(agent.paneId, upload);
        const name = nameGuess.split("/").pop() || path.split("/").pop() || "image";
        setPending((prev) => {
          if (prev.length >= MAX_ATTACH) return prev;
          return [
            ...prev,
            {
              id: `${Date.now()}-${Math.random().toString(36).slice(2, 7)}`,
              path,
              mime: mime || upload.type,
              preview,
              name,
            },
          ];
        });
      } catch (e) {
        setErr(e instanceof Error ? e.message : String(e));
      } finally {
        setAttachBusy(false);
      }
    },
    [agent.paneId, canAttach, client, pending.length],
  );

  // Revoke object URLs on unmount / clear.
  useEffect(() => {
    return () => {
      for (const p of pending) {
        if (p.preview.startsWith("blob:")) URL.revokeObjectURL(p.preview);
      }
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps -- only on unmount
  }, []);

  const send = useCallback(
    async (text: string, kind: "reply" | "approve") => {
      const caption = text.trim();
      const paths = kind === "reply" ? pending.map((p) => p.path) : [];
      const body =
        paths.length === 0
          ? caption
          : caption
            ? `${paths.join("\n")}\n${caption}`
            : paths.join("\n");
      if (!body || busy || attachBusy) return;
      setBusy(true);
      setErr(null);
      const restore = value;
      const restorePending = pending;
      if (kind === "reply") {
        setValue("");
        setSlashHits([]);
        setPending([]);
      }
      try {
        // No trailing newline: the backend presses Enter as a key. A bare "\n"
        // is ignored by a TUI reading raw input and the text would sit
        // unsubmitted in the prompt.
        if (kind === "approve") await client.respond?.(agent.paneId, body);
        else await client.sendText?.(agent.paneId, body);
        if (kind === "reply") {
          for (const p of restorePending) URL.revokeObjectURL(p.preview);
        }
        onSent();
      } catch (e) {
        setErr(e instanceof Error ? e.message : String(e));
        if (kind === "reply") {
          setValue(restore);
          setPending(restorePending);
        }
      } finally {
        setBusy(false);
      }
    },
    [agent.paneId, attachBusy, busy, client, onSent, pending, value],
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
        <span className="mono">
          read-only · enable &quot;Allow phone writes&quot; in Mac Settings, then reload
        </span>
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
          <button
            type="button"
            className="btn btn--key"
            disabled={busy}
            title="Cancel / close menu"
            aria-label="Escape"
            onClick={() => void press(["Escape"])}
          >
            Esc
          </button>

          <div className="keypad" role="group" aria-label="Arrow keys">
            {(
              [
                { label: "←", keys: ["Left"], title: "Left" },
                { label: "↑", keys: ["Up"], title: "Up" },
                { label: "↓", keys: ["Down"], title: "Down" },
                { label: "→", keys: ["Right"], title: "Right" },
              ] as const
            ).map((b) => (
              <button
                key={b.label}
                type="button"
                className="btn btn--key btn--keypad"
                disabled={busy}
                title={b.title}
                aria-label={b.title}
                onClick={() => void press([...b.keys])}
              >
                {b.label}
              </button>
            ))}
          </div>

          <button
            type="button"
            className="btn btn--key btn--key-primary"
            disabled={busy}
            title="Select / confirm"
            aria-label="Enter"
            onClick={() => void press(["Enter"])}
          >
            Enter
          </button>
          <button
            type="button"
            className="btn btn--key"
            disabled={busy}
            title="Tab"
            aria-label="Tab"
            onClick={() => void press(["Tab"])}
          >
            Tab
          </button>
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

      {canWrite && canAttach && pending.length > 0 && (
        <ul className="composer__attach" aria-label="Attached images">
          {pending.map((p) => (
            <li key={p.id} className="composer__chip">
              <button
                type="button"
                className="composer__chip-hit"
                onClick={() => setChipPreview(p)}
                aria-label={`Preview ${p.name}`}
              >
                <img
                  src={p.preview}
                  alt=""
                  className="composer__chip-thumb"
                  onError={(e) => {
                    (e.currentTarget as HTMLImageElement).classList.add("is-broken");
                  }}
                />
                <span className="composer__chip-name mono" title={p.path}>
                  {p.name}
                </span>
              </button>
              <button
                type="button"
                className="composer__chip-x"
                aria-label={`Remove ${p.name}`}
                onClick={() => {
                  if (p.preview.startsWith("blob:")) URL.revokeObjectURL(p.preview);
                  setPending((prev) => prev.filter((x) => x.id !== p.id));
                  setChipPreview((cur) => (cur?.id === p.id ? null : cur));
                }}
              >
                ×
              </button>
            </li>
          ))}
        </ul>
      )}

      {canWrite && (
        <div className="composer__row">
          {canAttach && (
            <>
              <input
                ref={fileRef}
                type="file"
                accept="image/png,image/jpeg,image/jpg,image/gif,image/webp,image/*"
                multiple
                hidden
                onChange={(e) => {
                  const files = e.target.files;
                  if (!files) return;
                  for (const f of Array.from(files)) void stageBlob(f, f.name);
                  e.target.value = "";
                }}
              />
              <button
                type="button"
                className="btn btn--icon"
                disabled={busy || attachBusy || pending.length >= MAX_ATTACH}
                title="Attach image"
                aria-label="Attach image"
                onClick={() => fileRef.current?.click()}
              >
                <svg viewBox="0 0 16 16" width="16" height="16" aria-hidden="true">
                  <path
                    d="M4.5 9.5 8 6l3.5 3.5M8 6v7"
                    fill="none"
                    stroke="currentColor"
                    strokeWidth="1.5"
                    strokeLinecap="round"
                    strokeLinejoin="round"
                  />
                  <path
                    d="M3 3.5h10a1 1 0 0 1 1 1v7a1 1 0 0 1-1 1H3a1 1 0 0 1-1-1v-7a1 1 0 0 1 1-1z"
                    fill="none"
                    stroke="currentColor"
                    strokeWidth="1.5"
                  />
                </svg>
              </button>
            </>
          )}
          <textarea
            ref={box}
            className="composer__input"
            rows={1}
            value={value}
            maxLength={MAX_TEXT}
            disabled={busy}
            placeholder={
              agent.agent
                ? canAttach
                  ? `Message ${agent.agent} · paste image or /…`
                  : `Message ${agent.agent}, or / for commands…`
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
            onPaste={(e) => {
              if (!canAttach) return;
              const items = e.clipboardData?.items;
              if (!items) return;
              const images: File[] = [];
              for (let i = 0; i < items.length; i++) {
                const it = items[i]!;
                if (it.kind === "file" && it.type.startsWith("image/")) {
                  const f = it.getAsFile();
                  if (f) images.push(f);
                }
              }
              if (images.length === 0) return;
              e.preventDefault();
              for (const f of images) void stageBlob(f, f.name || "paste.png");
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
            disabled={busy || attachBusy || (!value.trim() && pending.length === 0)}
            onClick={() => void send(value, "reply")}
          >
            Send
          </button>
        </div>
      )}

      {chipPreview && (
        <ImageLightbox
          src={chipPreview.preview}
          alt={chipPreview.name}
          onClose={() => setChipPreview(null)}
        />
      )}

      {value.length > MAX_TEXT - 100 && (
        <p className="composer__count mono">
          {MAX_TEXT - value.length} characters left
        </p>
      )}
    </footer>
  );
}
