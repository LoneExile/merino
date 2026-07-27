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
if (document.querySelector('meta[name="herdr-mode"]')?.getAttribute("content") !== "web") {
  document.documentElement.classList.add("desktop");
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
const isWebMode = document.querySelector('meta[name="herdr-mode"]')?.getAttribute("content") === "web";

if ("serviceWorker" in navigator && isWebMode) {
  window.addEventListener("load", () => {
    navigator.serviceWorker.register("/sw.js").catch(() => {
      // Silent by design — see comment above.
    });
  });
}
