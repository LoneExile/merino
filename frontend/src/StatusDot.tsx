import type { AgentStatus } from "../bindings/github.com/LoneExile/merino/internal/herdr";

const LABEL: Record<string, string> = {
  blocked: "Needs you",
  working: "Working",
  idle: "Idle",
  unknown: "Unknown",
};

export function statusLabel(status: AgentStatus | string): string {
  return LABEL[status] ?? String(status);
}

/**
 * Status indicator.
 *
 * Carries a text label to assistive tech rather than relying on hue: colour
 * alone is not a status, and this is the one signal in the app someone might
 * act on without reading anything else.
 */
export function StatusDot({ status }: { status: AgentStatus | string }) {
  return (
    <span className={`dot dot--${status}`} role="img" aria-label={statusLabel(status)}>
      <span className="dot__core" />
    </span>
  );
}
