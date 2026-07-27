import { useEffect, useState } from "react";
import { Events } from "@wailsio/runtime";
import {
  AgentsService,
  type Agent,
  type Conn,
} from "../bindings/github.com/LoneExile/herdr-tunnel/internal/app";

const EVENT_AGENTS_CHANGED = "agents:changed";
const EVENT_CONN_CHANGED = "conn:changed";

/**
 * Wails emits with a variadic payload, so a single value may arrive either
 * bare or wrapped in a one-element array depending on the runtime path.
 * Narrow the envelope at runtime rather than asserting its shape.
 */
function payload<T>(e: unknown): T | undefined {
  if (e === null || typeof e !== "object" || !("data" in e)) return undefined;
  const data: unknown = Array.isArray(e.data) ? e.data[0] : e.data;
  if (data === undefined || data === null) return undefined;
  // Unchecked by design: the Go side owns this schema and the bindings are
  // regenerated from it, so a mismatch is a build-time problem, not a runtime one.
  const typed = data as T;
  return typed;
}

/**
 * useHerd mirrors the Go store into React.
 *
 * The backend is authoritative: it emits the whole agent list on every change
 * (already coalesced and sorted most-urgent-first), so this hook replaces
 * state wholesale and never merges deltas.
 */
export function useHerd() {
  const [agents, setAgents] = useState<Agent[]>([]);
  const [conn, setConn] = useState<Conn | null>(null);
  const [ready, setReady] = useState(false);

  useEffect(() => {
    let alive = true;

    // Seed from the current state; events only cover subsequent changes.
    void (async () => {
      try {
        const [list, c] = await Promise.all([
          AgentsService.List(),
          AgentsService.Connection(),
        ]);
        if (!alive) return;
        setAgents(list ?? []);
        setConn(c);
      } catch {
        // Backend not ready yet; the event stream will catch us up.
      } finally {
        if (alive) setReady(true);
      }
    })();

    const offAgents = Events.On(EVENT_AGENTS_CHANGED, (e: unknown) => {
      const next = payload<Agent[]>(e);
      if (next) setAgents(next);
    });
    const offConn = Events.On(EVENT_CONN_CHANGED, (e: unknown) => {
      const next = payload<Conn>(e);
      if (next) setConn(next);
    });

    return () => {
      alive = false;
      offAgents?.();
      offConn?.();
    };
  }, []);

  return { agents, conn, ready };
}
