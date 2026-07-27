import { useEffect, useState } from "react";
import type { Agent } from "../bindings/github.com/LoneExile/herdr-tunnel/internal/app";
import { isAuthDead, makeClient, onAuthDead, type Client, type Session } from "./client";

/**
 * useHerd mirrors backend state into React.
 *
 * The backend is authoritative: it sends the whole agent list on every change,
 * already coalesced and sorted most-urgent-first, so this hook replaces state
 * wholesale and never merges deltas.
 *
 * live tracks the push transport (SSE / Wails events), not one-shot HTTP.
 * client.subscribe re-seeds on SSE reopen; this hook only flips the soft
 * reconnect banner and hard-retries boot when makeClient/session fails.
 */
export function useHerd() {
  const [client, setClient] = useState<Client | null>(null);
  const [session, setSession] = useState<Session | null>(null);
  const [agents, setAgents] = useState<Agent[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [ready, setReady] = useState(false);
  const [live, setLive] = useState(false);

  useEffect(() => {
    let alive = true;
    let unsubscribe: (() => void) | undefined;
    let retryTimer: ReturnType<typeof setTimeout> | undefined;
    let backoffMs = 1000;
    let c: Client | null = null;

    const clearRetry = () => {
      if (retryTimer !== undefined) {
        clearTimeout(retryTimer);
        retryTimer = undefined;
      }
    };

    const seed = async (cli: Client) => {
      const [list, sess] = await Promise.all([cli.list(), cli.session()]);
      if (!alive) return;
      setAgents(list ?? []);
      setSession(sess);
    };

    const attach = (cli: Client) => {
      unsubscribe?.();
      unsubscribe = cli.subscribe(
        (next) => {
          if (!alive) return;
          setAgents(next);
          setError(null);
          setLive(true);
          backoffMs = 1000;
        },
        () => {
          if (!alive) return;
          // Soft: SSE is auto-retrying. Do not re-seed here — subscribe()
          // pulls /api/agents on reopen so live flips only when push is back.
          setLive(false);
          setError("Reconnecting…");
        },
      );
    };

    const boot = async () => {
      clearRetry();
      try {
        if (!c) {
          c = await makeClient();
          if (!alive) return;
          setClient(c);
        }
        await seed(c);
        if (!alive) return;
        attach(c);
        setError(null);
        setLive(true);
        backoffMs = 1000;
      } catch (err) {
        if (!alive) return;
        setLive(false);
        setError(err instanceof Error ? err.message : String(err));
        if (isAuthDead()) return;
        retryTimer = setTimeout(() => {
          void boot();
        }, backoffMs);
        backoffMs = Math.min(backoffMs * 2, 30_000);
      } finally {
        if (alive) setReady(true);
      }
    };

    const stopAuth = onAuthDead(() => {
      alive = false;
      clearRetry();
      unsubscribe?.();
      setLive(false);
    });

    void boot();

    const onOnline = () => {
      if (isAuthDead()) return;
      backoffMs = 1000;
      void boot();
    };
    const onVisible = () => {
      if (document.visibilityState !== "visible" || !c) return;
      // Refresh snapshot after phone sleep; SSE will re-attach itself.
      void seed(c).catch(() => {
        void boot();
      });
    };

    window.addEventListener("online", onOnline);
    document.addEventListener("visibilitychange", onVisible);

    return () => {
      alive = false;
      clearRetry();
      unsubscribe?.();
      stopAuth();
      window.removeEventListener("online", onOnline);
      document.removeEventListener("visibilitychange", onVisible);
    };
  }, []);

  return { client, session, agents, ready, live, error, setError };
}
