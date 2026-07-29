/**
 * The `ui:open` deep-link vocabulary shared by the tray menu (Go) and the app.
 *
 * Kept React-free and in its own module for two reasons: Sheets.tsx and
 * App.tsx both need the tab ids, and a module with no DOM or React import can
 * be exercised directly by __checks__/uiOpen.check.ts. The tray is the one
 * surface no headless session can click, so its routing has to be provable
 * somewhere that is not the tray.
 *
 * Grammar: `<target>` or `<target>:<arg>`
 *
 *   agents             close overlays, show the herd
 *   pair               Pair phone sheet
 *   settings           Settings, on whichever tab the user was last in
 *   settings:system    Settings, forced to the System tab
 */

export type SettingsTabId = "pairing" | "access" | "display" | "system" | "about";

export const SETTINGS_TAB_IDS: SettingsTabId[] = [
  "pairing",
  "access",
  "display",
  "system",
  "about",
];

export function isSettingsTabId(v: unknown): v is SettingsTabId {
  return typeof v === "string" && (SETTINGS_TAB_IDS as string[]).includes(v);
}

export type UiOpenTarget = "agents" | "pair" | "settings";

export interface UiOpen {
  target: UiOpenTarget | null;
  /** Only ever set for `settings`. Null means "leave the tab alone". */
  tab: SettingsTabId | null;
}

/**
 * Parses an `ui:open` payload. Unknown targets and unknown tab names resolve
 * to null rather than throwing: this crosses a process boundary, and a menu
 * item that opens nothing beats one that white-screens the panel.
 */
export function parseUiOpen(payload: unknown): UiOpen {
  const raw = typeof payload === "string" ? payload : "";
  const [target, arg] = raw.split(":");

  if (target === "agents" || target === "pair") return { target, tab: null };
  if (target === "settings") {
    return { target: "settings", tab: isSettingsTabId(arg) ? arg : null };
  }
  return { target: null, tab: null };
}
