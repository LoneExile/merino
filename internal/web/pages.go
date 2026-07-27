package web

import (
	"html/template"
	"net/http"
)

// The login page is server-rendered rather than part of the React bundle so
// that authentication never depends on the app bundle loading, and so an OIDC
// provider can replace this with a redirect without touching the frontend.
var loginTmpl = template.Must(template.New("login").Parse(`<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1, viewport-fit=cover">
<meta name="color-scheme" content="dark light">
<title>Sign in · Herdr Tunnel</title>
<style>
  :root { --bg:#0c0e14; --elev:#141822; --border:#232a39; --text:#e6e9ef; --dim:#8b93a7; --alert:#f87171; --accent:#6aa2ff; }
  * { box-sizing:border-box; }
  body { margin:0; min-height:100vh; display:grid; place-items:center; background:var(--bg); color:var(--text);
         font:14px/1.5 -apple-system,BlinkMacSystemFont,"Segoe UI",sans-serif; padding:24px; }
  form { background:var(--elev); border:1px solid var(--border); border-radius:12px; padding:24px; width:100%; max-width:320px; }
  h1 { font-size:16px; margin:0 0 2px; }
  p.sub { color:var(--dim); font-size:12px; margin:0 0 18px; }
  label { display:block; font-size:11px; text-transform:uppercase; letter-spacing:.06em; color:var(--dim); margin:12px 0 4px; }
  input { width:100%; padding:9px 10px; font:inherit; color:var(--text); background:var(--bg);
          border:1px solid var(--border); border-radius:8px; }
  input:focus { outline:none; border-color:var(--accent); }
  button { width:100%; margin-top:18px; padding:10px; font:inherit; font-weight:600; color:var(--bg);
           background:var(--accent); border:0; border-radius:8px; cursor:pointer; }
  .err { margin-top:14px; padding:8px 10px; border-radius:8px; font-size:12px;
         color:var(--alert); background:color-mix(in oklab, var(--alert) 16%, transparent); }
</style>
</head>
<body>
  <form method="POST" action="/login">
    <h1>Herdr Tunnel</h1>
    <p class="sub">Sign in to view your agents.</p>
    <label for="username">Username</label>
    <input id="username" name="username" autocomplete="username" autocapitalize="none" autocorrect="off" required autofocus>
    <label for="password">Password</label>
    <input id="password" name="password" type="password" autocomplete="current-password" required>
    <button type="submit">Sign in</button>
    {{if .Error}}<div class="err">{{.Error}}</div>{{end}}
  </form>
</body>
</html>`))

func writeLoginPage(w http.ResponseWriter, errMsg string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	// Never cache a page that reflects auth state.
	w.Header().Set("Cache-Control", "no-store")
	_ = loginTmpl.Execute(w, struct{ Error string }{Error: errMsg})
}
