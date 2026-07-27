import { useCallback, useState } from "react";

const KEY = "herdr.termFontPx";

/** Discrete steps — monospaced terminal sizes that stay readable on phones. */
export const TERM_FONT_STEPS = [11, 12, 13, 14, 15, 16, 18, 20] as const;
export type TermFontPx = (typeof TERM_FONT_STEPS)[number];

const DEFAULT_PX: TermFontPx = 12;

function clampStep(n: number): TermFontPx {
  let best: TermFontPx = DEFAULT_PX;
  let bestDist = Infinity;
  for (const s of TERM_FONT_STEPS) {
    const d = Math.abs(s - n);
    if (d < bestDist) {
      best = s;
      bestDist = d;
    }
  }
  return best;
}

function stored(): TermFontPx {
  try {
    const raw = localStorage.getItem(KEY);
    if (!raw) return DEFAULT_PX;
    const n = Number(raw);
    if (!Number.isFinite(n)) return DEFAULT_PX;
    return clampStep(n);
  } catch {
    return DEFAULT_PX;
  }
}

function applyCss(px: TermFontPx) {
  try {
    document.documentElement.style.setProperty("--term-font-size", `${px}px`);
  } catch {
    /* ignore */
  }
}

// Apply once at module load so the first pane paint isn't wrong-sized.
if (typeof document !== "undefined") {
  applyCss(stored());
}

/**
 * Terminal font size preference. Stored in localStorage; applied as
 * `--term-font-size` on :root so every pane shares one size.
 */
export function useTermFontPref() {
  const [px, setPxState] = useState<TermFontPx>(stored);

  const setPx = useCallback((next: number) => {
    const step = clampStep(next);
    setPxState(step);
    applyCss(step);
    try {
      localStorage.setItem(KEY, String(step));
    } catch {
      /* session keeps memory value */
    }
  }, []);

  const bump = useCallback(
    (dir: -1 | 1) => {
      const i = TERM_FONT_STEPS.indexOf(px);
      const j = Math.max(0, Math.min(TERM_FONT_STEPS.length - 1, (i < 0 ? 1 : i) + dir));
      setPx(TERM_FONT_STEPS[j]!);
    },
    [px, setPx],
  );

  return {
    px,
    setPx,
    zoomIn: () => bump(1),
    zoomOut: () => bump(-1),
    canZoomIn: px < TERM_FONT_STEPS[TERM_FONT_STEPS.length - 1]!,
    canZoomOut: px > TERM_FONT_STEPS[0]!,
  };
}
