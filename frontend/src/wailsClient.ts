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
import * as DesktopSettings from "../bindings/github.com/LoneExile/herdr-tunnel/internal/desktop/settings";
import type { Client, Session, SessionList, SlashCommand } from "./client";

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
      return {
        user: "local",
        provider: "desktop",
        readOnly: false,
        // The desktop panel talks to the Go service directly, so it can do
        // both. It reports them so the UI's capability checks mean the same
        // thing on both transports.
        //
        // Note the asymmetry, which is deliberate and predates this: the
        // service layer's Guard (pane existence, response allowlist, free-text
        // and rename bounds) applies to BOTH transports, but Policy and the
        // audit log are HTTP-only. Both answer "which of several remote
        // identities did this", and the desktop panel has exactly one identity
        // sitting at the machine. Respond and SendText have always worked this
        // way; renames and session switching follow suit.
        canSwitchSession: true,
        canRename: true,
      };
    },

    // A nil Go slice serialises to null, so the generated binding is typed
    // Agent[] | null. Normalise here so callers never handle both.
    list: async () => (await AgentsService.List()) ?? [],
    slashCommands: async (paneId: string, agent: string, query: string) => {
      const list = (await AgentsService.SlashCommands(paneId, agent, query)) ?? [];
      return list as SlashCommand[];
    },
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
    sendText: (paneId: string, text: string) => AgentsService.SendText(paneId, text),
    attachImage: async (paneId: string, blob: Blob) => {
      const buf = await blob.arrayBuffer();
      const bytes = new Uint8Array(buf);
      let binary = "";
      const chunk = 0x8000;
      for (let i = 0; i < bytes.length; i += chunk) {
        binary += String.fromCharCode(...bytes.subarray(i, i + chunk));
      }
      const data = btoa(binary);
      const path = await AgentsService.AttachImageB64(
        paneId,
        blob.type || "application/octet-stream",
        data,
      );
      return { path: path ?? "", mime: blob.type || "application/octet-stream" };
    },
    sendKeys: (paneId: string, keys: string[]) => AgentsService.SendKeys(paneId, keys),
    focus: (paneId: string) => AgentsService.Focus(paneId),
    interrupt: (paneId: string) => AgentsService.Interrupt(paneId),

    rename: (kind, id, name) =>
      kind === "pane"
        ? AgentsService.RenamePane(id, name)
        : kind === "tab"
          ? AgentsService.RenameTab(id, name)
          : AgentsService.RenameWorkspace(id, name),

    // These were bound in Go from the start but never wired here, so the
    // session picker opened on the desktop and sat on "Looking for
    // sessions…" forever — the sheet awaited a method that did not exist.
    async sessions(): Promise<SessionList> {
      const list = (await AgentsService.Sessions()) ?? [];
      return {
        current: list.find((s) => s.current)?.id ?? "",
        canSwitch: true,
        sessions: list.map((s) => ({
          id: s.id,
          name: s.name,
          panes: s.panes,
          agents: s.agents,
          current: s.current,
          reachable: s.reachable,
        })),
      };
    },
    switchSession: (id: string) => AgentsService.SwitchSession(id),

    launchAtLogin: () => DesktopSettings.LaunchAtLogin(),
    setLaunchAtLogin: (on: boolean) => DesktopSettings.SetLaunchAtLogin(on),
    checkUpdate: async () => {
      const u = await DesktopSettings.CheckUpdate();
      return {
        current: u.current,
        latest: u.latest,
        newer: u.newer,
        releaseUrl: u.releaseUrl,
        body: u.body,
        published: u.published,
        checkedAt: u.checkedAt,
      };
    },
    mintPairing: async () => {
      const t = await DesktopSettings.MintPairing();
      return {
        url: t.url,
        token: t.token,
        qrPng: t.qrPng,
        expiresAt: t.expiresAt,
      };
    },
    setPairingBaseURL: (base: string) => DesktopSettings.SetPairingBaseURL(base),
    listDevices: async () => {
      const devices = await DesktopSettings.ListDevices();
      return {
        devices: (devices || []).map((d) => ({
          id: d.id,
          name: d.name,
          provider: d.provider,
          roles: d.roles,
          createdAt: String(d.createdAt),
          lastSeen: String(d.lastSeen),
          revokedAt: d.revokedAt ? String(d.revokedAt) : null,
        })),
        activeCount: (devices || []).filter((d) => !d.revokedAt).length,
        firstRunPending: await DesktopSettings.FirstRunPending(),
      };
    },
    revokeDevice: (id: string) => DesktopSettings.RevokeDevice(id),
    revokeAllDevices: () => DesktopSettings.RevokeAllDevices(),
    setOptionalPassword: (user: string, pass: string) => DesktopSettings.SetOptionalPassword(user, pass),
    markFirstRunDone: () => DesktopSettings.MarkFirstRunDone(),
    optionalPasswordEnabled: () => DesktopSettings.OptionalPasswordEnabled(),
  };
}
