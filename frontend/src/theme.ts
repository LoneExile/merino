import { useCallback, useEffect, useState } from "react";

export type ThemePref = "light" | "dark" | "system";

const KEY = "herdr.theme";
const isPref = (v: unknown): v is ThemePref =>
  v === "light" || v === "dark" || v === "system";

function stored(): ThemePref {
  try {
    const v = localStorage.getItem(KEY);
    return isPref(v) ? v : "system";
  } catch {
    // Private browsing and some embedded webviews throw on localStorage rather
    // than returning null. A theme preference is never worth breaking boot for.
    return "system";
  }
}

const media = () =>
  typeof window !== "undefined" && typeof window.matchMedia === "function"
    ? window.matchMedia("(prefers-color-scheme: dark)")
    : null;

/** Resolve a preference to the theme actually painted. */
export function resolve(pref: ThemePref): "light" | "dark" {
  if (pref !== "system") return pref;
  return media()?.matches ? "dark" : "light";
}

function paint(theme: "light" | "dark") {
  const root = document.documentElement;
  root.dataset.theme = theme;
  // Keep the browser's own surfaces (form controls, scrollbars, the URL bar on
  // iOS) in step with the app. Without this the phone paints a white status bar
  // above a graphite app.
  root.style.colorScheme = theme;
}

/**
 * Theme preference with a live "system" mode.
 *
 * "system" is not a one-shot read: it subscribes to the media query, so the app
 * follows the OS when it flips at sunset without a reload.
 */
export function useTheme() {
  const [pref, setPrefState] = useState<ThemePref>(stored);
  const [actual, setActual] = useState<"light" | "dark">(() => resolve(stored()));

  useEffect(() => {
    setActual(resolve(pref));
    if (pref !== "system") return;
    const mq = media();
    if (!mq) return;
    const onChange = () => setActual(resolve("system"));
    mq.addEventListener("change", onChange);
    return () => mq.removeEventListener("change", onChange);
  }, [pref]);

  useEffect(() => paint(actual), [actual]);

  const setPref = useCallback((next: ThemePref) => {
    setPrefState(next);
    try {
      localStorage.setItem(KEY, next);
    } catch {
      // Non-fatal: the session keeps the choice in memory.
    }
  }, []);

  return { pref, actual, setPref };
}

/**
 * Paint the stored theme before React mounts.
 *
 * Called from the entry module so the first frame is already correct. Without
 * it a dark-mode user gets a white flash on every load — small on desktop,
 * genuinely unpleasant on a phone at night.
 */
export function applyStoredTheme() {
  paint(resolve(stored()));
}
