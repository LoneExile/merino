import { useEffect, useState } from "react";
import type { Agent } from "../bindings/github.com/LoneExile/herdr-tunnel/internal/app";
import { makeClient, type Client, type Session } from "./client";

/**
 * useHerd mirrors backend state into React.
 *
 * The backend is authoritative: it sends the whole agent list on every change,
 * already coalesced and sorted most-urgent-first, so this hook replaces state
 * wholesale and never merges deltas.
 *
 * The transport — Wails IPC on the desktop, HTTP + SSE in a browser — is
 * resolved once at mount and is otherwise invisible to components.
 */
export function useHerd() {
  const [client, setClient] = useState<Client | null>(null);
  const [session, setSession] = useState<Session | null>(null);
  const [agents, setAgents] = useState<Agent[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [ready, setReady] = useState(false);

  useEffect(() => {
    let alive = true;
    let unsubscribe: (() => void) | undefined;

    void (async () => {
      try {
        const c = await makeClient();
        if (!alive) return;
        setClient(c);

        // Seed before subscribing: events only describe subsequent changes.
        const [list, sess] = await Promise.all([c.list(), c.session()]);
        if (!alive) return;
        setAgents(list ?? []);
        setSession(sess);

        unsubscribe = c.subscribe(
          (next) => setAgents(next),
          (err) => setError(err instanceof Error ? err.message : "connection lost"),
        );
      } catch (err) {
        if (alive) setError(err instanceof Error ? err.message : String(err));
      } finally {
        if (alive) setReady(true);
      }
    })();

    return () => {
      alive = false;
      unsubscribe?.();
    };
  }, []);

  return { client, session, agents, ready, error, setError };
}
