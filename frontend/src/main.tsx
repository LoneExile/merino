import React from "react";
import ReactDOM from "react-dom/client";
import App from "./App";
import { ErrorBoundary } from "./ErrorBoundary";
import { applyStoredTheme } from "./theme";
import "./app.css";

// Paint the stored theme before the first frame. Deferring this to a React
// effect gives a dark-mode user a white flash on every load — brief on a
// desktop, genuinely unpleasant on a phone at night.
applyStoredTheme();

// Desktop menubar panel gets rounded-corner chrome (see app.css html.desktop).
// Web mode keeps full-viewport square chrome.
const isWebMode =
  document.querySelector('meta[name="herdr-mode"]')?.getAttribute("content") === "web";
if (!isWebMode) {
  document.documentElement.classList.add("desktop");
}

// Mobile soft keyboards do not shrink 100dvh / layout viewport on most
// browsers — the composer sits under the keys. visualViewport.height is the
// actually-visible area; keep --app-height in sync so .app fits above it.
// Desktop Wails webview already has a fixed panel size — do not touch it.
if (isWebMode) {
  const root = document.documentElement;
  const KEYBOARD_GAP_PX = 120; // layout vs visual — soft keyboard heuristic

  const syncViewportHeight = () => {
    const vv = window.visualViewport;
    // Prefer visualViewport; fall back to innerHeight. Never exceed innerHeight
    // (some WebViews briefly report vv taller than the layout viewport).
    const layoutH = window.innerHeight;
    const vvH = vv?.height ?? layoutH;
    const height = Math.min(Math.round(vvH), layoutH);
    const offsetTop = Math.max(0, Math.round(vv?.offsetTop ?? 0));
    const keyboardOpen = layoutH - height > KEYBOARD_GAP_PX;

    root.style.setProperty("--app-height", `${height}px`);
    root.style.setProperty("--app-offset-top", `${offsetTop}px`);
    // While the keyboard is up the home-indicator safe-area is already outside
    // the visual viewport — padding it again shoves Send under the keys.
    root.style.setProperty(
      "--composer-pad-bottom",
      keyboardOpen ? "8px" : "max(8px, env(safe-area-inset-bottom, 0px))",
    );
    root.classList.toggle("keyboard-open", keyboardOpen);
  };

  syncViewportHeight();
  window.visualViewport?.addEventListener("resize", syncViewportHeight);
  window.visualViewport?.addEventListener("scroll", syncViewportHeight);
  window.addEventListener("orientationchange", () => {
    // iOS fires orientationchange before the new vv settles.
    window.setTimeout(syncViewportHeight, 50);
    window.setTimeout(syncViewportHeight, 250);
  });
  window.addEventListener("focusin", syncViewportHeight);
  window.addEventListener("focusout", () => {
    // After blur, iOS often animates the keyboard closed over ~200ms.
    window.setTimeout(syncViewportHeight, 50);
    window.setTimeout(syncViewportHeight, 300);
  });
}
ReactDOM.createRoot(document.getElementById("root") as HTMLElement).render(
  <React.StrictMode>
    <ErrorBoundary>
      <App />
    </ErrorBoundary>
  </React.StrictMode>,
);

// Register the service worker only in the browser dashboard, never inside
// the Wails desktop webview — gated on the same `herdr-mode` marker
// client.ts reads to pick its transport (see server.go's webModeMarker). A
// service worker inside the Wails webview would be meaningless: there is no
// network boundary to insulate against, and the webview's WKWebView /
// WebView2 host may not implement navigator.serviceWorker the same way a
// real browser does.
//
// Registration must never block or fail boot: a browser without SW support,
// a build without /sw.js, or a transient install error should all just
// leave the app running without offline/installable support.
// isWebMode is defined above (desktop class + viewport height).

if ("serviceWorker" in navigator && isWebMode) {
  window.addEventListener("load", () => {
    navigator.serviceWorker.register("/sw.js").catch(() => {
      // Silent by design — see comment above.
    });
  });
}
