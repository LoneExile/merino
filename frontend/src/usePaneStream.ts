import { useCallback, useEffect, useRef, useState } from "react";
import type { Client } from "./client";

export interface PaneStream {
  text: string;
  /** True once a payload has arrived, so the UI can distinguish "connecting"
   *  from "this pane is genuinely empty". */
  loaded: boolean;
  /** True while a push transport is delivering. False means the content is
   *  still correct but arriving by poll, or not arriving at all. */
  live: boolean;
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
 * Reads use herdr's `recent` source (via the backend), which includes
 * scrollback beyond the current viewport. Live updates keep the same
 * history window so scrolling up stays stable while the agent is working.
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

  const active = useRef<string | null>(null);
  const historyRef = useRef(INITIAL_HISTORY_LINES);
  const textLenRef = useRef(0);
  // When true, the next stream payload must not shrink history (user scrolled up).
  const holdHistoryRef = useRef(false);

  useEffect(() => {
    if (pinned) holdHistoryRef.current = false;
  }, [pinned]);

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
    setLoadingMore(false);
    setCanLoadMore(true);
    if (!client || !paneId) return;

    let stopStream: (() => void) | undefined;
    let timer = 0;
    let polling = false;

    const accept = (t: string, fromStream: boolean) => {
      if (active.current !== paneId) return;
      // Stream payloads can briefly be shorter (viewport-only race). Never
      // clobber a larger history buffer the user already loaded.
      if (fromStream && holdHistoryRef.current && t.length < textLenRef.current) {
        return;
      }
      textLenRef.current = t.length;
      setText(t);
      setLoaded(true);
      setError(null);
    };

    const readOnce = async (lines: number) => {
      try {
        const t = await client.read(paneId, lines);
        accept(t, false);
        return t;
      } catch (err) {
        if (active.current !== paneId) return "";
        setError(err instanceof Error ? err.message : String(err));
        return "";
      }
    };

    const startPolling = () => {
      if (polling) return;
      polling = true;
      timer = window.setInterval(() => void readOnce(historyRef.current), POLL_MS);
    };

    void readOnce(historyRef.current);

    if (client.streamPane) {
      stopStream = client.streamPane(
        paneId,
        (t) => {
          accept(t, true);
          setLive(true);
          clearInterval(timer);
          polling = false;
        },
        () => {
          if (active.current !== paneId) return;
          setLive(false);
          startPolling();
        },
      );
    } else {
      startPolling();
    }

    return () => {
      active.current = null;
      clearInterval(timer);
      stopStream?.();
    };
  }, [client, paneId]);

  const loadMore = useCallback(() => {
    if (!client || !paneId || loadingMore || !canLoadMore) return;
    const next = Math.min(historyRef.current + HISTORY_STEP, MAX_HISTORY_LINES);
    if (next <= historyRef.current) {
      setCanLoadMore(false);
      return;
    }
    setLoadingMore(true);
    holdHistoryRef.current = true;
    const prevLen = textLenRef.current;
    void client
      .read(paneId, next)
      .then((t) => {
        if (active.current !== paneId) return;
        historyRef.current = next;
        setHistoryLines(next);
        // No growth → herdr has nothing older in this buffer.
        if (t.length <= prevLen) {
          setCanLoadMore(false);
        }
        if (next >= MAX_HISTORY_LINES) {
          setCanLoadMore(false);
        }
        textLenRef.current = t.length;
        setText(t);
        setLoaded(true);
        setError(null);
      })
      .catch((err: unknown) => {
        if (active.current !== paneId) return;
        setError(err instanceof Error ? err.message : String(err));
      })
      .finally(() => {
        if (active.current === paneId) setLoadingMore(false);
      });
  }, [client, paneId, loadingMore, canLoadMore]);

  return { text, loaded, live, error, historyLines, loadingMore, canLoadMore, loadMore };
}
