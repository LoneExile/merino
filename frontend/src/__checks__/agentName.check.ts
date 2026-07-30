/**
 * Standalone check for frontend/src/agentName.ts.
 *
 * This repo's frontend has no test runner (see the justfile — vite + tsc
 * only), so this runs the helpers directly via Node's TypeScript
 * type-stripping:
 *
 *   node --experimental-strip-types frontend/src/__checks__/agentName.check.ts
 *
 * Why this file exists: a rename that herdr accepted still read as a no-op in
 * the UI for as long as `label` was dropped between herdr.PaneInfo and
 * app.Agent. The naming rules are the surface that made the write visible, so
 * "an unnamed pane is unchanged" and "a named pane leads with its name" are
 * both worth pinning.
 */
import { agentSubtitle, agentTitle } from "../agentName.ts";

let failures = 0;

function check(name: string, got: string, want: string): void {
  if (got === want) return;
  failures++;
  console.error(`FAIL ${name} — got ${JSON.stringify(got)}, want ${JSON.stringify(want)}`);
}

type Row = Parameters<typeof agentSubtitle>[0];

const unnamed: Row = {
  label: "",
  agent: "omp",
  project: "cozystack",
  cwd: "/Users/lex/dev/cozystack",
  paneId: "w2:pA7",
};

// An unnamed pane must render exactly as it did before labels existed. This
// is the regression that would silently reshape every row in the herd.
check("unnamed title", agentTitle(unnamed), "omp");
check("unnamed subtitle", agentSubtitle(unnamed), "cozystack");

// A named pane leads with the name; the kind survives in the subtitle so the
// row still says what tool it is.
const named: Row = { ...unnamed, label: "merino" };
check("named title", agentTitle(named), "merino");
check("named subtitle", agentSubtitle(named), "omp · cozystack");

// Whitespace is not a name. herdr accepts a label of spaces, and treating it
// as one would print a blank primary line with no way to tell which row it is.
const blank: Row = { ...unnamed, label: "   " };
check("blank label falls back to kind", agentTitle(blank), "omp");
check("blank label leaves subtitle alone", agentSubtitle(blank), "cozystack");

// The kind is never printed twice: a pane named after its own kind still
// reads once in the title and once in the subtitle, not twice in the title.
const selfNamed: Row = { ...unnamed, label: "omp" };
check("self-named title", agentTitle(selfNamed), "omp");
check("self-named subtitle", agentSubtitle(selfNamed), "omp · cozystack");

// Fallback chain when herdr reports no cwd or project for the pane.
const bare: Row = { label: "", agent: "", project: "", cwd: "", paneId: "w2:p1" };
check("bare title", agentTitle(bare), "pane");
check("bare subtitle", agentSubtitle(bare), "w2:p1");
check("bare named subtitle", agentSubtitle({ ...bare, label: "scratch" }), "pane · w2:p1");

// cwd carries the row when herdr sends a path but no project basename.
const cwdOnly: Row = { ...unnamed, project: "", cwd: "/srv/app" };
check("cwd fallback", agentSubtitle(cwdOnly), "/srv/app");

if (failures > 0) {
  console.error(`\n${failures} check(s) failed`);
  process.exit(1);
}
console.log("agentName: all checks passed");
