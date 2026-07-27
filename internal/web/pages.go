package web

import (
	"html/template"
	"net/http"
)

// The login page is server-rendered rather than part of the React bundle so
// that authentication never depends on the app bundle loading, and so an OIDC
// provider can replace this with a redirect without touching the frontend.
//
// Inline <script> must carry the CSP nonce from securityHeaders (see
// writeLoginPage). Without it the browser refuses the scanner code and the
// page falls back to password-only.
var loginTmpl = template.Must(template.New("login").Parse(`<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1, viewport-fit=cover">
<meta name="color-scheme" content="dark light">
<meta name="apple-mobile-web-app-capable" content="yes">
<meta name="mobile-web-app-capable" content="yes">
<title>Sign in · Herdr Tunnel</title>
<style>
  :root {
    --bg: #0c0e14;
    --elev: #141822;
    --border: #232a39;
    --text: #e6e9ef;
    --dim: #8b93a7;
    --alert: #f87171;
    --accent: #6aa2ff;
    --ok: #4ade80;
  }
  * { box-sizing: border-box; }
  body {
    margin: 0;
    min-height: 100dvh;
    display: grid;
    place-items: center;
    background: var(--bg);
    color: var(--text);
    font: 14px/1.5 -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif;
    padding: max(24px, env(safe-area-inset-top)) 24px max(24px, env(safe-area-inset-bottom));
  }
  .card {
    background: var(--elev);
    border: 1px solid var(--border);
    border-radius: 14px;
    padding: 24px;
    width: 100%;
    max-width: 340px;
  }
  h1 { font-size: 17px; margin: 0 0 2px; letter-spacing: -0.02em; }
  p.sub { color: var(--dim); font-size: 12px; margin: 0 0 18px; }
  label {
    display: block;
    font-size: 11px;
    text-transform: uppercase;
    letter-spacing: .06em;
    color: var(--dim);
    margin: 12px 0 4px;
  }
  input {
    width: 100%;
    padding: 11px 12px;
    font: inherit;
    color: var(--text);
    background: var(--bg);
    border: 1px solid var(--border);
    border-radius: 10px;
  }
  input:focus { outline: none; border-color: var(--accent); }
  button {
    width: 100%;
    margin-top: 14px;
    padding: 12px;
    font: inherit;
    font-weight: 600;
    color: var(--bg);
    background: var(--accent);
    border: 0;
    border-radius: 10px;
    cursor: pointer;
  }
  button.secondary {
    margin-top: 10px;
    color: var(--text);
    background: transparent;
    border: 1px solid var(--border);
  }
  button:disabled { opacity: .55; cursor: not-allowed; }
  .err {
    margin-top: 14px;
    padding: 8px 10px;
    border-radius: 8px;
    font-size: 12px;
    color: var(--alert);
    background: color-mix(in oklab, var(--alert) 16%, transparent);
  }
  .or {
    display: flex;
    align-items: center;
    gap: 10px;
    margin: 18px 0 4px;
    color: var(--dim);
    font-size: 11px;
    text-transform: uppercase;
    letter-spacing: .06em;
  }
  .or::before, .or::after { content: ""; flex: 1; height: 1px; background: var(--border); }
  .scan {
    display: none;
    margin-top: 12px;
  }
  .scan.is-on { display: block; }
  .scan video {
    width: 100%;
    aspect-ratio: 1;
    object-fit: cover;
    border-radius: 12px;
    background: #000;
    border: 1px solid var(--border);
  }
  .scan .hint {
    margin: 8px 0 0;
    font-size: 12px;
    color: var(--dim);
    text-align: center;
  }
  .scan .hint.ok { color: var(--ok); }
  .scan .hint.bad { color: var(--alert); }
  #scan-btn { display: none; }
  #scan-btn.is-ready { display: block; }
</style>
</head>
<body>
  <div class="card">
    <form id="login-form" method="POST" action="/login">
      <h1>Herdr Tunnel</h1>
      <p class="sub">Sign in to view your agents.</p>
      <label for="username">Username</label>
      <input id="username" name="username" autocomplete="username" autocapitalize="none" autocorrect="off">
      <label for="password">Password</label>
      <input id="password" name="password" type="password" autocomplete="current-password">
      <div class="or">or phone</div>
      <button type="button" id="scan-btn" class="secondary">Scan desktop QR</button>
      <div id="scan" class="scan" hidden>
        <video id="scan-video" playsinline muted></video>
        <p id="scan-hint" class="hint">Point at the QR from Settings → Phone sign-in</p>
        <button type="button" id="scan-stop" class="secondary">Cancel scan</button>
      </div>
      <label for="token">Phone code</label>
      <input id="token" name="token" autocomplete="one-time-code" autocapitalize="none" autocorrect="off"
             spellcheck="false" placeholder="Or paste code from desktop">
      <button type="submit">Sign in</button>
      {{if .Error}}<div class="err">{{.Error}}</div>{{end}}
    </form>
  </div>
  <script nonce="{{.Nonce}}">
(function () {
  var btn = document.getElementById("scan-btn");
  var panel = document.getElementById("scan");
  var video = document.getElementById("scan-video");
  var hint = document.getElementById("scan-hint");
  var stopBtn = document.getElementById("scan-stop");
  var stream = null;
  var timer = null;
  var Detector = window.BarcodeDetector;

  // Feature-detect: BarcodeDetector + camera. Hide the button when either is
  // missing so iOS Safari (no detector yet) keeps the paste field primary.
  var canScan = !!(Detector && navigator.mediaDevices && navigator.mediaDevices.getUserMedia);
  if (!canScan) return;
  btn.classList.add("is-ready");

  function stop() {
    if (timer) { clearInterval(timer); timer = null; }
    if (stream) {
      stream.getTracks().forEach(function (t) { t.stop(); });
      stream = null;
    }
    video.srcObject = null;
    panel.classList.remove("is-on");
    panel.hidden = true;
    hint.textContent = "Point at the QR from Settings \u2192 Phone sign-in";
    hint.className = "hint";
  }

  function tokenFromText(text) {
    if (!text) return "";
    try {
      var u = new URL(text, location.origin);
      var t = u.searchParams.get("token");
      if (t) return t;
    } catch (_) {}
    // Bare code from Settings sheet (not a full URL).
    var m = String(text).trim().match(/^[A-Za-z0-9_-]{8,}$/);
    return m ? m[0] : "";
  }

  function redeem(token) {
    // Same-origin navigation keeps the installed PWA in its own context —
    // unlike the OS camera app, which hands the URL to the default browser.
    stop();
    hint.textContent = "Signing in\u2026";
    hint.className = "hint ok";
    location.href = "/login?token=" + encodeURIComponent(token);
  }

  async function start() {
    hint.textContent = "Starting camera\u2026";
    hint.className = "hint";
    panel.hidden = false;
    panel.classList.add("is-on");
    try {
      stream = await navigator.mediaDevices.getUserMedia({
        audio: false,
        video: { facingMode: { ideal: "environment" }, width: { ideal: 1280 }, height: { ideal: 720 } }
      });
      video.srcObject = stream;
      await video.play();
    } catch (err) {
      hint.textContent = "Camera blocked. Paste the code instead.";
      hint.className = "hint bad";
      return;
    }

    var detector;
    try {
      detector = new Detector({ formats: ["qr_code"] });
    } catch (_) {
      hint.textContent = "QR scan not supported here. Paste the code.";
      hint.className = "hint bad";
      stop();
      return;
    }

    hint.textContent = "Point at the QR from Settings \u2192 Phone sign-in";
    timer = setInterval(async function () {
      if (!video.videoWidth) return;
      try {
        var codes = await detector.detect(video);
        for (var i = 0; i < codes.length; i++) {
          var tok = tokenFromText(codes[i].rawValue);
          if (tok) {
            redeem(tok);
            return;
          }
        }
      } catch (_) {
        // transient decode errors while the frame is empty
      }
    }, 350);
  }

  btn.addEventListener("click", function () {
    if (stream) stop();
    else start();
  });
  stopBtn.addEventListener("click", stop);
  window.addEventListener("pagehide", stop);
})();
  </script>
</body>
</html>`))

func writeLoginPage(w http.ResponseWriter, r *http.Request, errMsg string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	// Never cache a page that reflects auth state.
	w.Header().Set("Cache-Control", "no-store")
	nonce, _ := r.Context().Value(nonceKey{}).(string)
	_ = loginTmpl.Execute(w, struct {
		Error string
		Nonce string
	}{Error: errMsg, Nonce: nonce})
}
