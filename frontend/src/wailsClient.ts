// Desktop transport, backed by the Wails IPC bridge.
//
// This module is loaded ONLY through a dynamic import from client.ts, and only
// when the page is not being served over HTTP. Importing `@wailsio/runtime`
// initialises `window.webkit.messageHandlers` at module scope, which throws in
// a plain browser — hence the code split. Do not import this file statically
// from anything the browser can reach.

import { Events } from "@wailsio/runtime";
import {
  AgentsService,
  type Agent,
} from "../bindings/github.com/LoneExile/herdr-tunnel/internal/app";
import type { Client, Session } from "./client";

const EVENT_AGENTS_CHANGED = "agents:changed";

/**
 * Extract an event payload.
 *
 * Wails' EventManager.Emit sets `event.Data = data[0]` when exactly one value
 * is emitted, so `e.data` IS the emitted value — do not "unwrap" arrays here.
 * Treating an array payload as a variadic envelope turned the agent list into
 * a single agent, threw in `.filter`, and blanked the whole panel.
 */
function payload<T>(e: unknown): T | undefined {
  if (e === null || typeof e !== "object" || !("data" in e)) return undefined;
  const data: unknown = e.data;
  if (data === undefined || data === null) return undefined;
  return data as T;
}

export function wailsClient(): Client {
  return {
    kind: "desktop",
    readOnly: false,

    async session(): Promise<Session> {
      return { user: "local", provider: "desktop", readOnly: false };
    },

    // A nil Go slice serialises to null, so the generated binding is typed
    // Agent[] | null. Normalise here so callers never handle both.
    list: async () => (await AgentsService.List()) ?? [],
    read: (paneId: string, lines: number) => AgentsService.Read(paneId, lines),

    subscribe(onAgents) {
      const off = Events.On(EVENT_AGENTS_CHANGED, (e: unknown) => {
        const next = payload<Agent[]>(e);
        // Guard the invariant: a non-array would throw inside the component
        // tree and unmount everything.
        if (Array.isArray(next)) onAgents(next);
      });
      return () => off?.();
    },

    respond: (paneId: string, text: string) => AgentsService.Respond(paneId, text),
    focus: (paneId: string) => AgentsService.Focus(paneId),
    interrupt: (paneId: string) => AgentsService.Interrupt(paneId),
  };
}
