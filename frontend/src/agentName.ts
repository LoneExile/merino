import type { Agent } from "../bindings/github.com/LoneExile/merino/internal/app";

/**
 * How an agent pane is named in the list, the palette, and sheet headings.
 *
 * A herd of one project looks like this in the list:
 *
 *   omp          omp          omp
 *   cozystack    cozystack    cozystack
 *
 * Three rows that are the same row. `pane.rename` is the operator's answer to
 * that, so a named pane leads with its name and demotes the kind to the
 * subtitle; an unnamed pane is untouched and still leads with its kind.
 */

/** Primary line: the operator's name if there is one, else the agent kind. */
export function agentTitle(a: Pick<Agent, "label" | "agent">): string {
  return a.label?.trim() || a.agent || "pane";
}

/**
 * Secondary line. Carries the agent kind only when the title has given it up,
 * so the kind is legible on every row either way and never printed twice.
 */
export function agentSubtitle(
  a: Pick<Agent, "label" | "agent" | "project" | "cwd" | "paneId">,
): string {
  const where = a.project || a.cwd || a.paneId;
  if (!a.label?.trim()) return where;
  return `${a.agent || "pane"} · ${where}`;
}
