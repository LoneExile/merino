/**
 * Standalone check for frontend/src/composerKeys.ts.
 *
 * This repo's frontend has no test runner (see the justfile — vite + tsc
 * only), so this runs the real function via Node's TypeScript type-stripping:
 *
 *   node --experimental-strip-types frontend/src/__checks__/composerKeys.check.ts
 *
 * Why this file exists: Escape in the composer now has four possible
 * consumers (the slash typeahead, fullscreen compose, the terminal's TUI key
 * routing, and the draft itself). Getting the order wrong does not throw — it
 * silently steals a keypress from one of them, and two of those four
 * behaviours predate fullscreen compose and must not regress.
 */
import { composerEscapeAction, type ComposerEscapeState } from "../composerKeys.ts";

let failures = 0;

function check(name: string, state: ComposerEscapeState, want: string): void {
  const got = composerEscapeAction(state);
  if (got === want) return;
  failures++;
  console.error(`FAIL ${name} — got ${JSON.stringify(got)}, want ${JSON.stringify(want)}`);
}

const base: ComposerEscapeState = {
  slashOpen: false,
  expanded: false,
  empty: true,
  canKeys: true,
};

// 1. The typeahead outranks everything. Escape while picking a command must
//    close the menu, never collapse the surface or reach the terminal.
check("slash menu takes Escape", { ...base, slashOpen: true }, "slash");
check(
  "slash menu takes Escape even while expanded",
  { ...base, slashOpen: true, expanded: true },
  "slash",
);
check(
  "slash menu takes Escape over a non-empty draft",
  { ...base, slashOpen: true, empty: false },
  "slash",
);

// 2. Expanded collapses, and the draft survives. Losing a long reply to the
//    same key that closes the surface it was written in would be the worst
//    possible outcome of this feature.
check("expanded collapses", { ...base, expanded: true }, "collapse");
check("expanded collapses with a draft", { ...base, expanded: true, empty: false }, "collapse");
check(
  "expanded collapses even without key routing",
  { ...base, expanded: true, canKeys: false },
  "collapse",
);

// 3. Pre-existing behaviour, unchanged: an empty composer is a TUI keypad.
check("collapsed + empty + canKeys routes to the pane", base, "pane");

// 4. Pre-existing behaviour, unchanged: a draft is cleared, not forwarded.
check("collapsed + draft clears the draft", { ...base, empty: false }, "clear");
check(
  "a draft is cleared even when key routing is on",
  { ...base, empty: false, canKeys: true },
  "clear",
);

// 5. Nothing to do. Notably NOT "pane": without key routing the terminal must
//    not receive anything, which is the whole point of the read-only gate.
check("collapsed + empty + no key routing does nothing", { ...base, canKeys: false }, "none");

if (failures > 0) {
  console.error(`\n${failures} check(s) failed`);
  process.exit(1);
}
console.log("composerKeys: all checks passed");
