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
import { readFileSync } from "node:fs";

import {
  parseUiOpen,
  isSettingsTabId,
  nextTabRequest,
  SETTINGS_TAB_IDS,
  type TabRequest,
} from "../uiOpen.ts";

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
  spawn: { target: "spawn", tab: null },
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

// The other half of the contract, read from the Go source rather than
// restated here. The table above is hand-copied, so on its own it proves only
// that uiOpen.ts agrees with itself: renaming openUI("spawn") to openUI("new")
// in main.go would leave every assertion above green while the tray item
// silently opened nothing.
const mainGo = readFileSync(new URL("../../../main.go", import.meta.url), "utf8");
const emitted = [...mainGo.matchAll(/openUI\("([^"]+)"\)/g)].map((m) => m[1]);

check("main.go emits deep links at all", emitted.length > 0, `found ${emitted.length}`);
for (const payload of emitted) {
  check(`main.go "${payload}" resolves`, parseUiOpen(payload).target !== null);
  check(`main.go "${payload}" is covered above`, payload in TRAY_MENU);
}
// And the reverse, so a deleted tray item leaves a stale expectation behind.
for (const payload of Object.keys(TRAY_MENU)) {
  check(`TRAY_MENU "${payload}" is still emitted by main.go`, emitted.includes(payload));
}

// Bare settings must NOT pin a tab — that is what preserves "the tab you were
// last in" for the plain Settings item.
check("bare settings leaves the tab alone", parseUiOpen("settings").tab === null);

// Every tab id is reachable by deep link, so a future menu item cannot name a
// tab the parser silently drops.
for (const id of SETTINGS_TAB_IDS) {
  check(`settings:${id} reaches ${id}`, parseUiOpen(`settings:${id}`).tab === id);
}

// Garbage from across the process boundary opens nothing. Asserted as two
// groups with different right answers: a single disjunctive predicate would
// accept an unknown tray string silently opening Settings, which is the
// regression most worth catching here.
for (const junk of ["", "nope", ":", "SETTINGS", "settings2", null, 7, {}]) {
  const got = parseUiOpen(junk);
  check(`junk ${JSON.stringify(junk)} opens nothing`, got.target === null, `got ${got.target}`);
}
// A real target with a missing or unknown argument keeps the target and drops
// the argument — that is how "settings" restores the user's last tab.
for (const near of ["settings:", "settings:nope"]) {
  const got = parseUiOpen(near);
  check(
    `${near} keeps settings and drops the tab`,
    got.target === "settings" && got.tab === null,
    `got ${got.target}/${got.tab}`,
  );
}

check("isSettingsTabId accepts a real id", isSettingsTabId("about"));
check("isSettingsTabId rejects a near miss", !isSettingsTabId("abouts"));
check("isSettingsTabId rejects a non-string", !isSettingsTabId(3));

// Regression: the deep link is a command, not a value.
//
// Reported sequence — "Check for Updates…" lands on System, switch to
// Pairing, dismiss, then "Check for Updates…" again and it shows Pairing.
// Dismissing by clicking away hides the window without unmounting the app,
// so the request survives; asking for "system" a second time has to produce
// a DISTINCT request or nothing downstream re-runs.
const first = nextTabRequest(null, "system");
check("first request names the tab", first?.tab === "system");

const repeat = nextTabRequest(first, "system");
check("repeat names the same tab", repeat?.tab === "system");
check(
  "repeat is a distinct request",
  repeat !== first && repeat?.seq !== first?.seq,
  `seq ${first?.seq} -> ${repeat?.seq}`,
);

// Ten presses of one menu item are ten distinct requests, never a plateau.
let cur: TabRequest | null = null;
const seqs = new Set<number>();
for (let i = 0; i < 10; i++) {
  cur = nextTabRequest(cur, "system");
  seqs.add(cur!.seq);
}
check("ten repeats are ten distinct requests", seqs.size === 10, `${seqs.size} distinct`);

// Bare "settings" clears the request so the plain menu item still restores
// the tab the user was last in.
check("no tab clears the request", nextTabRequest(first, null) === null);

// Switching target tabs keeps advancing rather than restarting.
const moved = nextTabRequest(repeat, "pairing");
check(
  "a different tab still advances",
  moved?.tab === "pairing" && moved!.seq > repeat!.seq,
);

if (failures > 0) {
  console.error(`${failures} check(s) failed`);
  process.exit(1);
}
console.log(`uiOpen: ${Object.keys(TRAY_MENU).length} tray routes + request/guard checks ok`);
