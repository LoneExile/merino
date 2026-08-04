/**
 * Full-viewport "reconnecting" splash for the browser/PWA dashboard.
 *
 * The boot splash (index.html #splash) owns the very first paint; this one
 * owns every reconnect after it: the SSE transport dropped (phone slept,
 * network blip, server restart) and the agent list on screen is stale. It
 * reuses the boot splash's sheep-hop markup and animation classes so the
 * brand moment reads the same at boot and on reconnect — one visual
 * language, two triggers. The container styling lives in app.css under
 * `.reconnect`; the `.splash__*` inner classes and keyframes are the
 * document-global ones index.html defines for the boot splash.
 *
 * Mounted only while `ready && !live` on the web transport — a desktop
 * panel's Wails events never drop, and its boot-failure path must keep the
 * raw error banner visible.
 */
export function ReconnectSplash({ done = false }: { done?: boolean }) {
  return (
    <div
      className={`reconnect${done ? " is-done" : ""}`}
      role="status"
      aria-live="polite"
      aria-busy={done ? "false" : "true"}
    >
      <div className="splash__stage">
        <div className="splash__hop" aria-hidden="true">
          <span className="splash__shadow" />
          <img
            className="splash__sheep"
            src="/icon-192.png"
            width="72"
            height="72"
            alt=""
            decoding="async"
          />
        </div>
        <p className="splash__label">Reconnecting</p>
      </div>
    </div>
  );
}
