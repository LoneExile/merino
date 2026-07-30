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
 * so the kind is legible on every surface either way and never printed twice.
 *
 * `where` is the trailing detail: the project for a list row, the pane id in
 * the pane header, which is the one surface where the raw id is worth the
 * space. Parameterised rather than duplicated so a named pane demotes its
 * kind by exactly one rule everywhere.
 */
export function agentSubtitle(
  a: Pick<Agent, "label" | "agent" | "project" | "cwd" | "paneId">,
  where: string = a.project || a.cwd || a.paneId,
): string {
  if (!a.label?.trim()) return where;
  return `${a.agent || "pane"} · ${where}`;
}
