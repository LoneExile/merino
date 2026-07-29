import { useCallback, useEffect, useRef, useState } from "react";
import type { Client } from "./client";
import { isAuthDead } from "./client";

export interface PaneStream {
  text: string;
  /** True once a payload has arrived, so the UI can distinguish "connecting"
   *  from "this pane is genuinely empty". */
  loaded: boolean;
  /** True while a push transport is delivering. False means the content is
   *  still correct but arriving by poll, or not arriving at all. */
  live: boolean;
  /**
   * True only when a transport that HAS a push stream is not delivering, so
   * the UI is on the slower poll fallback. False on a transport with no
   * stream at all: the desktop panel polls by design (Wails cannot marshal a
   * Go func across IPC, so StreamOutputANSI is unreachable from JS), and
   * flagging its normal operating mode as "reconnecting" is a lie that never
   * clears.
   */
  degraded: boolean;
  error: string | null;
  /** How many lines the current buffer was requested with. */
  historyLines: number;
  /** True while a larger history fetch is in flight. */
  loadingMore: boolean;
  /** False once a larger fetch returned no additional content. */
  canLoadMore: boolean;
  /** Request more scrollback (capped server-side). No-op if already maxed. */
  loadMore: () => void;
}

const POLL_MS = 1500;
/** Initial recent-buffer size. Bigger than a phone viewport so opening a
 *  pane already includes content that scrolled off the live TUI screen. */
export const INITIAL_HISTORY_LINES = 400;
/** Step when the user scrolls to the top and asks for more. */
export const HISTORY_STEP = 400;
/** Hard cap — matches app.MaxPaneHistoryLines. */
export const MAX_HISTORY_LINES = 2000;

/**
 * Live terminal output for one pane.
 *
 * Pane switches reset the buffer. loadMore expands the recent-window and
 * reconnects the stream at that size WITHOUT clearing text or resetting the
 * line budget (that bug yanked users to the bottom with a loading error).
 */
export function usePaneStream(
  client: Client | null,
  paneId: string | null,
  /** When true (scrolled to bottom), stream may replace a shorter buffer. */
  pinned = true,
): PaneStream {
  const [text, setText] = useState("");
  const [loaded, setLoaded] = useState(false);
  const [live, setLive] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [historyLines, setHistoryLines] = useState(INITIAL_HISTORY_LINES);
  const [loadingMore, setLoadingMore] = useState(false);
  const [canLoadMore, setCanLoadMore] = useState(true);
  // Bumped when loadMore expands the window so the stream effect reconnects.
  const [streamGen, setStreamGen] = useState(0);

  const active = useRef<string | null>(null);
  const historyRef = useRef(INITIAL_HISTORY_LINES);
  const textLenRef = useRef(0);
  // When true, reject stream payloads from a smaller window than historyRef.
  const holdHistoryRef = useRef(false);
  // loadMore in flight — stream snapshots must not clobber the larger buffer.
  const loadingMoreRef = useRef(false);

  useEffect(() => {
    if (pinned) holdHistoryRef.current = false;
  }, [pinned]);

  // Pane identity change only — full reset.
  useEffect(() => {
    active.current = paneId;
    setText("");
    setLoaded(false);
    setError(null);
    setLive(false);
    setHistoryLines(INITIAL_HISTORY_LINES);
    historyRef.current = INITIAL_HISTORY_LINES;
    textLenRef.current = 0;
    holdHistoryRef.current = false;
    loadingMoreRef.current = false;
    setLoadingMore(false);
    setCanLoadMore(true);
    setStreamGen(0);
  }, [paneId]);

  // Transport: initial read + stream/poll. Re-runs on streamGen to adopt a
  // larger window after loadMore — must NOT wipe text or historyRef.
  useEffect(() => {
    if (!client || !paneId) return;
    const thisPane = paneId;
    active.current = thisPane;

    let stopStream: (() => void) | undefined;
    let timer = 0;
    let polling = false;
    const lines = historyRef.current;

    const accept = (t: string, fromStream: boolean, streamLines?: number) => {
      if (active.current !== thisPane) return;

      if (fromStream || loadingMoreRef.current) {
        const win = streamLines ?? 0;
        // Never shrink an expanded buffer via a lagging stream snapshot.
        if (holdHistoryRef.current || loadingMoreRef.current) {
          if (win > 0 && win < historyRef.current) return;
          if (t.length < textLenRef.current && (win === 0 || win <= historyRef.current)) {
            return;
          }
        }
        if (win > historyRef.current) {
          historyRef.current = win;
          setHistoryLines(win);
        }
      }

      textLenRef.current = t.length;
      setText(t);
      setLoaded(true);
      // Don't clear a loadMore error on unrelated stream ticks if we want —
      // but a successful paint means the pane is healthy.
      setError(null);
    };

    const readOnce = async (n: number) => {
      if (isAuthDead()) return "";
      try {
        const t = await client.read(thisPane, n);
        // While holding history, ignore a shorter one-shot (stale race).
        if (
          (holdHistoryRef.current || loadingMoreRef.current) &&
          t.length < textLenRef.current
        ) {
          return t;
        }
        accept(t, false, n);
        return t;
      } catch (err) {
        if (active.current !== thisPane) return "";
        if (isAuthDead()) {
          setLive(false);
          return "";
        }
        // Keep existing text on transient read failures so scrollback isn't
        // replaced by an empty "error" screen.
        if (textLenRef.current === 0) {
          setError(err instanceof Error ? err.message : String(err));
        }
        return "";
      }
    };

    const startPolling = () => {
      if (polling || isAuthDead()) return;
      polling = true;
      timer = window.setInterval(() => void readOnce(historyRef.current), POLL_MS);
    };

    // Fresh pane (no text yet) or reconnect after loadMore: fill/refresh.
    void readOnce(lines);

    if (client.streamPane) {
      stopStream = client.streamPane(
        thisPane,
        (t) => {
          accept(t, true, historyRef.current);
          setLive(true);
          clearInterval(timer);
          polling = false;
        },
        () => {
          if (active.current !== thisPane || isAuthDead()) return;
          setLive(false);
          startPolling();
        },
        lines,
      );
    } else {
      startPolling();
    }

    return () => {
      clearInterval(timer);
      stopStream?.();
    };
  }, [client, paneId, streamGen]);

  const loadMore = useCallback(() => {
    if (!client || !paneId || loadingMoreRef.current || !canLoadMore || isAuthDead()) {
      return;
    }
    const next = Math.min(historyRef.current + HISTORY_STEP, MAX_HISTORY_LINES);
    if (next <= historyRef.current) {
      setCanLoadMore(false);
      return;
    }

    loadingMoreRef.current = true;
    setLoadingMore(true);
    holdHistoryRef.current = true;
    const prevLen = textLenRef.current;
    const prevLines = historyRef.current;

    void client
      .read(paneId, next)
      .then((t) => {
        if (active.current !== paneId) return;
        // No growth → herdr has nothing older in this buffer.
        if (t.length <= prevLen) {
          setCanLoadMore(false);
          // Keep current text; do not reconnect stream.
          return;
        }
        historyRef.current = next;
        setHistoryLines(next);
        if (next >= MAX_HISTORY_LINES) {
          setCanLoadMore(false);
        }
        textLenRef.current = t.length;
        setText(t);
        setLoaded(true);
        setError(null);
        // Reconnect stream at the expanded window only — effect must not reset.
        setStreamGen((g) => g + 1);
      })
      .catch((err: unknown) => {
        if (active.current !== paneId) return;
        // Restore line budget; keep existing scrollback on screen.
        historyRef.current = prevLines;
        if (!isAuthDead()) {
          // Soft error — don't blank the terminal.
          setError(err instanceof Error ? err.message : String(err));
        }
      })
      .finally(() => {
        if (active.current === paneId) {
          loadingMoreRef.current = false;
          setLoadingMore(false);
        }
      });
  }, [client, paneId, canLoadMore]);

  return {
    text,
    loaded,
    live,
    // Only a transport that offers a stream can be degraded by losing it.
    degraded: Boolean(client?.streamPane) && !live,
    error,
    historyLines,
    loadingMore,
    canLoadMore,
    loadMore,
  };
}
