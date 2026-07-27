import { useCallback, useState } from "react";

const KEY = "herdr.wrapLines";

function stored(): boolean {
  try {
    return localStorage.getItem(KEY) === "1";
  } catch {
    // Private browsing and some embedded webviews throw on localStorage rather
    // than returning null. A display preference is never worth breaking boot for.
    return false;
  }
}

/**
 * "Wrap long lines" preference for the terminal pane. Mirrors theme.ts's
 * storage pattern (same try/catch-around-localStorage shape).
 *
 * Default OFF: this app's live panes run far wider than a phone (288 columns
 * is typical). TUI output is column-aligned by the agent that drew it, so
 * wrapping scrambles box-drawing borders into unrelated mid-screen fragments
 * rather than merely looking cramped. OFF keeps the real geometry and scrolls
 * the terminal horizontally instead, the way mobile terminal apps do it.
 */
export function useWrapPref() {
  const [wrap, setWrapState] = useState<boolean>(stored);

  const setWrap = useCallback((next: boolean) => {
    setWrapState(next);
    try {
      localStorage.setItem(KEY, next ? "1" : "0");
    } catch {
      // Non-fatal: the session keeps the choice in memory.
    }
  }, []);

  return { wrap, setWrap };
}
