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
<title>Sign in · Merino</title>
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
  .oauth-btn {
    display: block;
    margin-top: 10px;
    padding: 12px;
    font: inherit;
    font-weight: 600;
    text-align: center;
    text-decoration: none;
    color: var(--text);
    background: transparent;
    border: 1px solid var(--border);
    border-radius: 10px;
    cursor: pointer;
  }
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

  /* Boot splash — sheep hop until the form is painted. */
  #splash {
    position: fixed;
    inset: 0;
    z-index: 50;
    display: grid;
    place-items: center;
    background: var(--bg);
    color: var(--dim);
    transition: opacity .28s ease, visibility .28s ease;
  }
  #splash.is-done {
    opacity: 0;
    visibility: hidden;
    pointer-events: none;
  }
  .splash__stage {
    display: grid;
    justify-items: center;
    gap: 16px;
    font: 12px/1.4 -apple-system, BlinkMacSystemFont, sans-serif;
    letter-spacing: .08em;
    text-transform: uppercase;
  }
  .splash__hop {
    position: relative;
    width: 88px;
    height: 100px;
    display: grid;
    place-items: end center;
  }
  .splash__sheep {
    width: 64px;
    height: 64px;
    display: block;
    transform-origin: 50% 100%;
    animation: splash-hop 700ms cubic-bezier(.33,.9,.4,1) infinite;
  }
  .splash__shadow {
    position: absolute;
    left: 50%;
    bottom: 4px;
    width: 40px;
    height: 9px;
    margin-left: -20px;
    border-radius: 50%;
    background: currentColor;
    opacity: .18;
    filter: blur(2px);
    animation: splash-shadow 700ms cubic-bezier(.33,.9,.4,1) infinite;
  }
  @keyframes splash-hop {
    0%, 100% { transform: translateY(0) scale(1,1); }
    18% { transform: translateY(0) scale(1.06,.9); }
    36% { transform: translateY(-26px) scale(.96,1.06); }
    52% { transform: translateY(-32px) scale(.98,1.02); }
    70% { transform: translateY(0) scale(1.08,.88); }
    82% { transform: translateY(-5px) scale(.98,1.04); }
  }
  @keyframes splash-shadow {
    0%, 100% { transform: scaleX(1); opacity: .2; }
    18% { transform: scaleX(1.1); opacity: .24; }
    36%, 52% { transform: scaleX(.55); opacity: .1; }
    70% { transform: scaleX(1.15); opacity: .26; }
    82% { transform: scaleX(.9); opacity: .16; }
  }
  @media (prefers-reduced-motion: reduce) {
    .splash__sheep, .splash__shadow { animation: none; }
  }
</style>
</head>
<body>
  <div id="splash" role="status" aria-live="polite" aria-busy="true">
    <div class="splash__stage">
      <div class="splash__hop" aria-hidden="true">
        <span class="splash__shadow"></span>
        <img class="splash__sheep" src="/icon-192.png" width="64" height="64" alt="" decoding="async">
      </div>
      <span>Loading</span>
    </div>
  </div>
  <div class="card">
    <form id="login-form" method="POST" action="/login">
      <h1>Merino</h1>
      <p class="sub">{{if .AllowPassword}}Sign in to view your agents.{{else}}Scan a QR from the Mac app to sign in.{{end}}</p>
      {{if .AllowPassword}}
      <label for="username">Username</label>
      <input id="username" name="username" autocomplete="username" autocapitalize="none" autocorrect="off">
      <label for="password">Password</label>
      <input id="password" name="password" type="password" autocomplete="current-password">
      <div class="or">or phone</div>
      {{else}}
      <p class="sub" style="margin-top:8px">Username/password sign-in is disabled on this server.</p>
      {{end}}
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
      {{if .OAuthButtons}}
      <div class="or">or single sign-on</div>
      {{range .OAuthButtons}}
      <a class="oauth-btn" href="{{.Path}}">Sign in with {{.Label}}</a>
      {{end}}
      {{end}}
      {{if .Error}}<div class="err">{{.Error}}</div>{{end}}
    </form>
  </div>
  <script nonce="{{.Nonce}}">
// Shared by the form submit and QR redeem paths. The server 303s to the app
// shell on success and re-renders this page with an error on failure.
function landAfterLogin(res) {
  if (new URL(res.url).pathname === "/") {
    // Replace the CURRENT history entry (the /login page) with the app, so
    // sign-in never stays in the back stack — a PWA swipe-back gesture from
    // the dashboard or a terminal cannot land back on the form.
    location.replace("/");
    return true;
  }
  return false;
}

// Failure: the server re-rendered this page with an error message. Lift just
// the .err block into the live form — a whole-document swap would inherit
// THIS page's CSP nonce and silently block the failure page's inline scripts
// (the splash would never dismiss, the QR scanner would stay dead).
function renderServerError(res) {
  return res.text().then(function (html) {
    var doc = new DOMParser().parseFromString(html, "text/html");
    var err = doc.querySelector(".err");
    var form = document.getElementById("login-form");
    var btn = form && form.querySelector('button[type="submit"]');
    if (btn) btn.disabled = false;
    if (!err || !form) return;
    var slot = form.querySelector(".err");
    if (slot) slot.replaceWith(err);
    else form.appendChild(err);
  });
}

(function () {
  var el = document.getElementById("splash");
  if (!el) return;
  var done = function () {
    el.classList.add("is-done");
    el.setAttribute("aria-busy", "false");
    setTimeout(function () { el.remove(); }, 320);
  };
  if (window.matchMedia && window.matchMedia("(prefers-reduced-motion: reduce)").matches) done();
  else setTimeout(done, 480);
})();
(function () {
  var btn = document.getElementById("scan-btn");
  var panel = document.getElementById("scan");
  var video = document.getElementById("scan-video");
  var hint = document.getElementById("scan-hint");
  var stopBtn = document.getElementById("scan-stop");
  var stream = null;
  var timer = null;
  var Detector = window.BarcodeDetector;

  // Feature-detect: BarcodeDetector + camera. Camera APIs require a secure
  // context (HTTPS or http://localhost). Plain http://LAN and http://Tailscale
  // are NOT secure — Chrome/Safari hide getUserMedia, so we explain and keep
  // the paste field primary (same as iOS without BarcodeDetector).
  var secure = window.isSecureContext === true;
  var canScan = !!(secure && Detector && navigator.mediaDevices && navigator.mediaDevices.getUserMedia);
  if (!canScan) {
    btn.style.display = "block";
    btn.disabled = true;
    btn.textContent = secure
      ? "Camera scan unavailable in this browser"
      : "Camera scan needs HTTPS (not plain HTTP)";
    btn.title = secure
      ? "This browser has no QR detector. Paste the code from the Mac QR sheet."
      : "Browsers only allow the camera on https:// or localhost. Use a public HTTPS tunnel, or paste the one-shot code under the QR on the Mac.";
    return;
  }
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
    // Same-origin fetch keeps the installed PWA in its own context — unlike
    // the OS camera app, which hands the URL to the default browser. On
    // success landAfterLogin REPLACES the current /login entry with /, so
    // this page never stays in the back stack: a later PWA swipe-back
    // gesture cannot land back on a form the user already left.
    stop();
    hint.textContent = "Signing in\u2026";
    hint.className = "hint ok";
    fetch("/login?token=" + encodeURIComponent(token), {
      credentials: "same-origin",
      redirect: "follow"
    }).then(function (res) {
      if (!landAfterLogin(res)) renderServerError(res);
    }).catch(function () {
      hint.textContent = "Sign-in failed. Paste the code instead.";
      hint.className = "hint bad";
    });
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

// Sign-in without leaving /login in the back stack.
//
// The native form POST works, but the browser keeps the /login entry in
// history — so a PWA swipe-back gesture from the dashboard or a terminal
// lands back on the form and the user signs in again for no reason. Both
// sign-in paths below fetch instead and, on success, REPLACE the current
// entry with / via landAfterLogin. The form keeps its native POST as the
// no-JS / fetch-failure fallback.
(function () {
  var form = document.getElementById("login-form");
  if (!form) return;
  form.addEventListener("submit", function (e) {
    e.preventDefault();
    var btn = form.querySelector('button[type="submit"]');
    if (btn) btn.disabled = true;
    // URLSearchParams, not FormData: the server reads r.PostFormValue,
    // which only parses application/x-www-form-urlencoded bodies. A
    // multipart FormData POST would arrive with every field empty.
    fetch("/login", {
      method: "POST",
      body: new URLSearchParams(new FormData(form)),
      credentials: "same-origin",
      redirect: "follow"
    }).then(function (res) {
      if (!landAfterLogin(res)) renderServerError(res);
    }).catch(function () {
      if (btn) btn.disabled = false;
      form.submit();
    });
  });
})();
  </script>
</body>
</html>`))

func writeLoginPage(w http.ResponseWriter, r *http.Request, errMsg string, allowPassword bool, oauthBtns []OAuthButton) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	// Never cache a page that reflects auth state.
	w.Header().Set("Cache-Control", "no-store")
	nonce, _ := r.Context().Value(nonceKey{}).(string)
	_ = loginTmpl.Execute(w, struct {
		Error         string
		Nonce         string
		AllowPassword bool
		OAuthButtons  []OAuthButton
	}{Error: errMsg, Nonce: nonce, AllowPassword: allowPassword, OAuthButtons: oauthBtns})
}
