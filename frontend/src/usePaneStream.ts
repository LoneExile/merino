import { useEffect, useRef, useState } from "react";
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
}

const POLL_MS = 1500;

/**
 * Live terminal output for one pane.
 *
 * Two independent mechanisms, deliberately:
 *
 *   1. One `read` immediately, so the screen paints from the first round trip.
 *   2. A push subscription for everything after that.
 *
 * The read is not redundant with the stream's own connect-time snapshot. It
 * means a server that has no stream endpoint, or a stream that fails behind a
 * proxy, still shows a correct terminal instead of an eternal "Connecting…" —
 * the failure degrades to stale-but-right rather than blank. If the stream
 * never establishes, polling takes over.
 */
export function usePaneStream(
  client: Client | null,
  paneId: string | null,
  lines = 300,
): PaneStream {
  const [text, setText] = useState("");
  const [loaded, setLoaded] = useState(false);
  const [live, setLive] = useState(false);
  const [error, setError] = useState<string | null>(null);

  // Survives re-renders so a late payload from a pane the user already left
  // cannot overwrite the pane they are now looking at.
  const active = useRef<string | null>(null);

  useEffect(() => {
    active.current = paneId;
    setText("");
    setLoaded(false);
    setError(null);
    setLive(false);
    if (!client || !paneId) return;

    let stopStream: (() => void) | undefined;
    let timer = 0;
    let polling = false;

    const accept = (t: string) => {
      if (active.current !== paneId) return;
      setText(t);
      setLoaded(true);
      setError(null);
    };

    const readOnce = async () => {
      try {
        accept(await client.read(paneId, lines));
      } catch (err) {
        if (active.current !== paneId) return;
        setError(err instanceof Error ? err.message : String(err));
      }
    };

    const startPolling = () => {
      if (polling) return;
      polling = true;
      timer = window.setInterval(() => void readOnce(), POLL_MS);
    };

    void readOnce();

    if (client.streamPane) {
      stopStream = client.streamPane(
        paneId,
        (t) => {
          accept(t);
          setLive(true);
          // The push path won: stop the backup poll so a phone is not doing
          // both.
          clearInterval(timer);
          polling = false;
        },
        () => {
          if (active.current !== paneId) return;
          // EventSource retries on its own, so this is a gap, not an end. Poll
          // meanwhile: a stale terminal is worse than a slow one.
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
  }, [client, paneId, lines]);

  return { text, loaded, live, error };
}
