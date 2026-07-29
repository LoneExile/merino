/**
 * Standalone check for frontend/src/uiOpen.ts.
 *
 * This repo's frontend has no test runner (see package.json — vite + tsc
 * only), so this runs the parser directly via Node's TypeScript
 * type-stripping:
 *
 *   node --experimental-strip-types frontend/src/__checks__/uiOpen.check.ts
 *
 * Why this file exists: the tray menu is the one surface a headless session
 * cannot click. Its routing therefore has to be provable somewhere that is
 * not the tray, and uiOpen.ts is that seam — the Go side emits these exact
 * strings (see the tray menu in main.go).
 */
import { parseUiOpen, isSettingsTabId, SETTINGS_TAB_IDS } from "../uiOpen.ts";

let failures = 0;

function check(name: string, cond: boolean, detail?: string): void {
  if (cond) return;
  failures++;
  console.error(`FAIL ${name}${detail ? ` — ${detail}` : ""}`);
}

// Every string the tray menu emits must resolve to its intended destination.
// These literals are the contract with main.go; changing one without changing
// the other is exactly the bug this check exists to catch.
const TRAY_MENU: Record<string, { target: string | null; tab: string | null }> = {
  agents: { target: "agents", tab: null },
  settings: { target: "settings", tab: null },
  pair: { target: "pair", tab: null },
  "settings:system": { target: "settings", tab: "system" },
};

for (const [payload, want] of Object.entries(TRAY_MENU)) {
  const got = parseUiOpen(payload);
  check(
    `tray "${payload}" routes to ${want.target}/${want.tab}`,
    got.target === want.target && got.tab === want.tab,
    `got ${got.target}/${got.tab}`,
  );
}

// Bare settings must NOT pin a tab — that is what preserves "the tab you were
// last in" for the plain Settings item.
check("bare settings leaves the tab alone", parseUiOpen("settings").tab === null);

// Every tab id is reachable by deep link, so a future menu item cannot name a
// tab the parser silently drops.
for (const id of SETTINGS_TAB_IDS) {
  check(`settings:${id} reaches ${id}`, parseUiOpen(`settings:${id}`).tab === id);
}

// Garbage from across the process boundary opens nothing rather than throwing.
for (const junk of ["", "nope", "settings:", "settings:nope", ":", "SETTINGS", null, 7, {}]) {
  const got = parseUiOpen(junk);
  const ok =
    got.target === null || (got.target === "settings" && got.tab === null);
  check(`junk ${JSON.stringify(junk)} degrades safely`, ok, `got ${got.target}/${got.tab}`);
}

check("isSettingsTabId accepts a real id", isSettingsTabId("about"));
check("isSettingsTabId rejects a near miss", !isSettingsTabId("abouts"));
check("isSettingsTabId rejects a non-string", !isSettingsTabId(3));

if (failures > 0) {
  console.error(`${failures} check(s) failed`);
  process.exit(1);
}
console.log(`uiOpen: ${Object.keys(TRAY_MENU).length} tray routes + guards ok`);
