package web

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/LoneExile/merino/internal/app"
)

// Source supplies the data the web UI renders. Implemented by app.AgentsService.
//
// Only read operations appear here. Adding a write method is the deliberate,
// visible act that opens the write path — it cannot happen by accident.
type Source interface {
	List() []app.Agent
	Counts() app.Counts
	// Connection reports whether herdr itself is reachable. The agent list
	// going empty is ambiguous on its own — an idle herd and a dead socket
	// look identical — and a dashboard that cannot tell them apart is the
	// "lies about liveness" failure this product names as an anti-reference.
	Connection() app.Conn
	// ReadANSI returns a pane's visible screen with ANSI/SGR colour and
	// style preserved, so the dashboard can render it instead of dumping
	// plain text.
	ReadANSI(paneID string, lines int) (string, error)
	// StreamOutputANSI is ReadANSI's live-push counterpart: it calls onText
	// with the pane's ANSI-preserved text whenever it changes, until ctx is
	// cancelled.
	StreamOutputANSI(ctx context.Context, paneID string, lines int, onText func(string)) error
}

// Config configures the HTTP server.
type Config struct {
	// Addr is the listen address. Defaults to 127.0.0.1:8730. Binding to a
	// non-loopback address is an explicit choice by the operator.
	Addr string
	// Provider authenticates users.
	Provider Provider
	// OAuth lists the OAuth login rungs (GitHub, Keycloak) mounted alongside
	// the primary provider. Routes are always mounted; each provider gates
	// itself on its live config and only shows a button when enabled.
	OAuth []OAuthProvider
	// OAuthStore is the live, editable source of OAuth config that backs the
	// Settings UI. Nil disables OAuth management entirely.
	OAuthStore *OAuthStore
	// Policy authorises what an authenticated user may see.
	Policy Policy
	// BehindProxy declares that every request arrives through a trusted
	// TLS-terminating proxy such as a Cloudflare tunnel. It makes session
	// cookies Secure and makes Cloudflare's client-IP headers authoritative
	// for login throttling.
	//
	// Never enable it while the port is also reachable directly: the headers
	// are then attacker-supplied and the throttle becomes trivially bypassable.
	BehindProxy bool
	// Assets is the built frontend, served at /.
	Assets fs.FS
	Logger *slog.Logger

	// Writer enables write route registration when non-nil. Live acceptance is
	// gated by AllowWrites / SetAllowWrites (CLI or Mac Settings) so the operator
	// can open the gate without restarting. Nil Writer still means this build
	// cannot write at all.
	Writer Writer
	// Audit records every write. Required whenever Writer is set: an
	// internet-reachable path into a live terminal without a durable record of
	// who used it is not something worth shipping.
	Audit *app.Audit
	// Sessions lists the herdr sessions this server can see. Nil means the
	// route does not exist, the same absence convention Writer uses for the
	// write routes.
	Sessions SessionSource
	// Switcher repoints the server at a different herdr session when
	// session switch is enabled (CLI flag and/or Settings toggle).
	// Nil means the host cannot switch at all.
	Switcher SessionSwitcher
	// SessionSwitch starts enabled when true (CLI --allow-session-switch).
	// Desktop Settings can flip it at runtime when Switcher != nil.
	SessionSwitch bool
	// AllowWrites starts the write gate open when true (CLI --allow-writes).
	// Desktop Settings can flip it at runtime when Writer != nil.
	AllowWrites bool
	// PushDir is where the VAPID keypair and push-subscription store are
	// persisted. Empty disables Web Push entirely: no VAPID keys are ever
	// generated, the push routes do not exist, and /api/session reports
	// pushEnabled: false — the same absence convention Writer and Switcher
	// use. Wired from the same directory the audit log resolves to (see
	// app.DefaultAuditPath); left empty in tests so they never touch a real
	// VAPID keypair or subscription file on disk.
	PushDir string
	// Pairing enables short-lived QR/token phone login. Nil disables it.
	Pairing *Pairing
	// PublicBaseURL is the origin encoded into pairing QR links when set
	// (e.g. https://merino.example). Empty yields a path-only URL.
	PublicBaseURL string
	// Devices persists paired phones. Nil disables per-device identity (tests).
	Devices *DeviceStore
	// StateDir is where bootstrap/optional-password/first-run stamps live.
	// Empty falls back to the audit-log directory.
	StateDir string
}

// Server is the read-only HTTP dashboard.
type Server struct {
	cfg      Config
	src      Source
	sessions *Sessions
	log      *slog.Logger
	http     *http.Server
	switchMu sync.Mutex
	// switchOn gates POST /api/sessions/switch when cfg.Switcher != nil.
	switchOn bool
	// writeOn gates pane write handlers when cfg.Writer != nil.
	// Routes stay registered so Settings can open the gate without restart.
	writeOn bool
	// push is nil whenever Web Push was never configured (PushDir == "") or
	// failed to initialise — see New. Every push-aware code path checks this
	// rather than assuming a non-empty Config.PushDir implies success.
	push *pushManager
	// pairing is nil when Config.Pairing was not set.
	pairing *Pairing

	mu      sync.Mutex
	clients map[chan []byte]struct{}
}

// clientIP resolves the caller's address, honouring proxy headers only when
// the operator has declared the server sits behind one.
func (s *Server) clientIP(r *http.Request) string {
	if s.cfg.BehindProxy {
		return ProxiedIP(r)
	}
	return DirectIP(r)
}

// New builds the server. It does not listen until Start is called.
func New(src Source, cfg Config) (*Server, error) {
	if cfg.Addr == "" {
		cfg.Addr = "127.0.0.1:8730"
	}
	if cfg.Provider == nil {
		return nil, errors.New("web: a Provider is required; refusing to serve unauthenticated")
	}
	if cfg.Policy == nil {
		return nil, errors.New("web: a Policy is required")
	}
	if cfg.Writer != nil && cfg.Audit == nil {
		return nil, errors.New("web: writes require an Audit; refusing to accept unlogged writes")
	}
	if cfg.Switcher != nil && cfg.Sessions == nil {
		return nil, errors.New("web: session switching requires a Sessions source")
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	sessions, err := NewSessions(cfg.BehindProxy)
	if err != nil {
		return nil, err
	}
	s := &Server{
		cfg:      cfg,
		src:      src,
		sessions: sessions,
		log:      cfg.Logger,
		clients:  make(map[chan []byte]struct{}),
		pairing:  cfg.Pairing,
		switchOn: cfg.SessionSwitch && cfg.Switcher != nil,
		writeOn:  cfg.AllowWrites && cfg.Writer != nil,
	}
	if s.pairing != nil && cfg.PublicBaseURL != "" {
		s.pairing.SetBaseURL(cfg.PublicBaseURL)
	}
	// The password provider owns the /login form; hand it a LIVE getter for
	// the OAuth buttons so a Settings edit shows/hides them without a restart.
	if pp, ok := cfg.Provider.(*PasswordProvider); ok && cfg.OAuthStore != nil {
		pp.SetOAuthButtons(cfg.OAuthStore.Buttons)
	}

	// Push failing to initialise must never take the dashboard down over a
	// notifications feature — see newPushManager's doc comment. Disabled
	// (PushDir == "") gets no log line; a real failure does, so an operator
	// who expects push and doesn't get it has somewhere to look.
	if cfg.PushDir != "" {
		pm, err := newPushManager(cfg.PushDir, cfg.Logger)
		if err != nil {
			cfg.Logger.Warn("push notifications disabled", "dir", cfg.PushDir, "err", err)
		} else {
			s.push = pm
		}
	}
	s.http = &http.Server{
		Addr:              cfg.Addr,
		Handler:           s.routes(),
		ReadHeaderTimeout: 10 * time.Second,
		// No WriteTimeout: SSE responses are intentionally long-lived.
		IdleTimeout: 120 * time.Second,
	}
	return s, nil
}

// SessionSwitchAllowed is true when the host can switch and the operator
// left the gate open (CLI flag or Mac Settings toggle).
func (s *Server) SessionSwitchAllowed() bool {
	if s == nil || s.cfg.Switcher == nil {
		return false
	}
	s.switchMu.Lock()
	defer s.switchMu.Unlock()
	return s.switchOn
}

// SetSessionSwitch turns the phone/web session-switch gate on or off.
// No-op error if the host has no Switcher.
func (s *Server) SetSessionSwitch(on bool) error {
	if s == nil {
		return errors.New("server unavailable")
	}
	if s.cfg.Switcher == nil {
		return errors.New("this build cannot switch herdr sessions")
	}
	s.switchMu.Lock()
	s.switchOn = on
	s.switchMu.Unlock()
	if s.log != nil {
		s.log.Info("session switch gate", "enabled", on)
	}
	return nil
}

// WritesAllowed is true when a Writer is wired and the operator left the
// write gate open (CLI --allow-writes or Mac Settings toggle).
func (s *Server) WritesAllowed() bool {
	if s == nil || s.cfg.Writer == nil {
		return false
	}
	s.switchMu.Lock()
	defer s.switchMu.Unlock()
	return s.writeOn
}

// SetAllowWrites turns the phone/web write gate on or off.
func (s *Server) SetAllowWrites(on bool) error {
	if s == nil {
		return errors.New("server unavailable")
	}
	if s.cfg.Writer == nil {
		return errors.New("this build cannot accept pane writes")
	}
	if on && s.cfg.Audit == nil {
		return errors.New("writes require an audit log")
	}
	s.switchMu.Lock()
	s.writeOn = on
	s.switchMu.Unlock()
	if s.log != nil {
		s.log.Info("write gate", "enabled", on)
	}
	return nil
}

func (s *Server) routes() http.Handler {
	mux := http.NewServeMux()

	s.cfg.Provider.Mount(mux, func(w http.ResponseWriter, r *http.Request, id Identity) {
		s.sessions.Issue(w, id)
		s.log.Info("web login", "user", id.Name, "provider", id.Provider, "ip", s.clientIP(r))
		s.log.Debug("login transport",
			"xForwardedProto", r.Header.Get("X-Forwarded-Proto"),
			"cfVisitor", r.Header.Get("CF-Visitor"),
			"cfConnectingIP", r.Header.Get("CF-Connecting-IP"))
		http.Redirect(w, r, "/", http.StatusSeeOther)
	})
	for _, o := range s.cfg.OAuth {
		o.Mount(mux, s.sessions, func(w http.ResponseWriter, r *http.Request, id Identity) {
			s.sessions.Issue(w, id)
			s.log.Info("web login", "user", id.Name, "provider", id.Provider, "ip", s.clientIP(r))
			http.Redirect(w, r, "/", http.StatusSeeOther)
		})
	}
	s.mountPairing(mux)
	s.mountDevices(mux)
	s.mountOAuthAdmin(mux)

	// POST only — Settings uses a form method=post. GET would allow CSRF
	// logout via <img src="/logout"> / link prefetch.
	mux.HandleFunc("POST /logout", func(w http.ResponseWriter, r *http.Request) {
		s.sessions.Clear(w)
		w.Header().Set("Cache-Control", "no-store")
		http.Redirect(w, r, s.cfg.Provider.LoginPath(), http.StatusSeeOther)
	})

	mux.Handle("GET /api/session", s.authed(s.handleSession))
	mux.Handle("GET /api/agents", s.authed(s.handleAgents))
	mux.Handle("GET /api/slash", s.authed(s.handleSlash))
	if s.cfg.Sessions != nil {
		mux.Handle("GET /api/sessions", s.authed(s.handleSessions))
	}
	if s.cfg.Switcher != nil {
		mux.Handle("POST /api/sessions/switch", s.authed(s.handleSessionSwitch))
	}
	mux.Handle("GET /api/events", s.authed(s.handleEvents))
	mux.Handle("GET /api/panes/{id}/output", s.authed(s.handleOutput))
	mux.Handle("GET /api/panes/{id}/stream", s.authed(s.handleStream))

	if s.cfg.Writer != nil {
		s.mountWrites(mux)
	}
	// Image GETs are display-only; available even when writes are gated off.
	s.mountPaste(mux)

	if s.push != nil {
		s.mountPush(mux)
	}

	// PWA assets are served WITHOUT authentication, unlike everything else.
	//
	// The browser fetches <link rel="manifest">, the icons it names, and
	// service-worker updates on its own, outside any page, and it does not
	// send our session cookie. Behind s.authed those requests fell through to
	// the SPA handler and got the login page back with a 200, so Chrome
	// reported `Manifest: Line: 1, column: 1, Syntax error`, the icons decoded
	// as HTML, and the app was not installable. The 200 is what makes it
	// nasty: the browser cannot even tell it was redirected.
	//
	// None of these carry user data — an app name, two colours, and a picture
	// of a sheep. The list is explicit rather than a prefix so that widening
	// it is a deliberate act.
	//
	// Served with an explicit content type (see serveAsset) rather than
	// falling through to handleStatic's generic, extension-sniffed file
	// serving — a service worker or manifest served with the wrong type, or
	// silently swallowed by the SPA fallback, fails in ways a browser gives
	// almost no diagnostic for.
	mux.Handle("GET /sw.js", s.public(s.handleServiceWorker))
	// /healthz is likewise unauthenticated: a probe (Kubernetes liveness, or
	// a human checking merinod) needs a cheap machine-readable target that
	// does not run the /login page's insecure-transport check or render
	// HTML. It carries no user data — see handleHealthz for what it omits
	// and why.
	mux.Handle("GET /healthz", s.public(s.handleHealthz))
	mux.Handle("GET /manifest.webmanifest", s.public(s.handleManifest))
	for _, icon := range pwaIcons {
		mux.Handle("GET /"+icon, s.publicAsset(icon, "image/png"))
	}

	mux.Handle("GET /", s.authed(s.handleStatic))

	return securityHeaders(s.redirectAuthedFromLogin(mux))
}

// redirectAuthedFromLogin sends session holders home instead of letting them
// see the login form. Sign-in leaves /login in the browser's back stack, so a
// PWA swipe-back gesture (or any history navigation) lands on /login with a
// perfectly good session — and re-rendering the form there reads as a forced
// re-login. A valid session gets a 303 to /; only genuinely unauthenticated
// requests reach the provider's form.
//
// Token-bearing GETs (/login?token=…) are deliberately exempt: that is the QR
// redemption path, and a session holder may still need it — a device revoked
// in Settings keeps a valid-looking cookie, and re-pairing scans a fresh QR
// precisely because the old session no longer counts. Bouncing that GET would
// silently drop the one-shot token (2-minute TTL) and force a re-scan.
func (s *Server) redirectAuthedFromLogin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == s.cfg.Provider.LoginPath() && r.URL.Query().Get("token") == "" {
			if _, ok := s.sessions.ReadSession(r); ok {
				http.Redirect(w, r, "/", http.StatusSeeOther)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// authed rejects unauthenticated requests: a redirect for navigations, 401 for
// API calls so the frontend can react without following HTML.
func (s *Server) authed(h func(http.ResponseWriter, *http.Request, Identity)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sess, ok := s.sessions.ReadSession(r)
		if !ok {
			if strings.HasPrefix(r.URL.Path, "/api/") {
				writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthenticated"})
				return
			}
			http.Redirect(w, r, s.cfg.Provider.LoginPath(), http.StatusSeeOther)
			return
		}
		id := sess.Identity
		// Per-device grants can be revoked while a cookie is still valid.
		if s.cfg.Devices != nil && IsDeviceSubject(id.Subject) && !s.cfg.Devices.Active(id.Subject) {
			s.sessions.Clear(w)
			if strings.HasPrefix(r.URL.Path, "/api/") {
				writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "device revoked"})
				return
			}
			http.Redirect(w, r, s.cfg.Provider.LoginPath(), http.StatusSeeOther)
			return
		}
		// Renew before the handler runs: it sets a header, and handlers here
		// stream (SSE) or write bodies immediately, after which a Set-Cookie
		// would be silently dropped. Deliberately after the revocation check,
		// so a revoked device is never handed a fresh cookie.
		s.sessions.Renew(w, sess)
		h(w, r, id)
	})
}

// pwaIcons are the icon files the manifest names. The browser fetches these
// with no cookie while deciding whether the app is installable, so they are
// served publicly; keeping them in one list means the manifest and the router
// cannot drift apart silently.
var pwaIcons = []string{
	"icon-192.png",
	"icon-512.png",
	"icon-512-maskable.png",
	"apple-touch-icon.png",
	"favicon-32.png",
	"favicon-64.png",
}

// public serves a handler with no identity. Use it ONLY for assets that carry
// no user data; everything else goes through authed.
func (s *Server) public(h func(http.ResponseWriter, *http.Request, Identity)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h(w, r, Identity{})
	})
}

// publicAsset serves one named file from the embedded bundle, unauthenticated.
func (s *Server) publicAsset(name, contentType string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.serveAsset(w, r, name, contentType, false)
	})
}

// nonceKey carries the per-request CSP nonce to the HTML handler.
type nonceKey struct{}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// A fresh nonce per response. Inline <script> in index.html is stamped
		// with it by handleStatic; anything not stamped is refused by the
		// browser, which is the point.
		raw := make([]byte, 16)
		if _, err := rand.Read(raw); err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		nonce := base64.RawStdEncoding.EncodeToString(raw)

		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		// Camera is used only on /login for in-PWA QR scan (BarcodeDetector).
		h.Set("Permissions-Policy", "camera=(self), microphone=(), geolocation=()")
		// script-src must be explicit. Relying on the default-src fallback
		// blocks every inline script — including the ones this server injects,
		// which silently broke browser-mode detection until a real browser
		// surfaced it. 'unsafe-inline' is deliberately NOT used for scripts;
		// a nonce keeps the policy strict while allowing our own.
		h.Set("Content-Security-Policy",
			"default-src 'self'; "+
				"script-src 'self' 'nonce-"+nonce+"'; "+
				"style-src 'self' 'unsafe-inline'; "+
				"img-src 'self' data:; connect-src 'self'; "+
				"manifest-src 'self'; worker-src 'self'; "+
				"base-uri 'none'; form-action 'self'; object-src 'none'")

		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), nonceKey{}, nonce)))
	})
}

func (s *Server) handleSession(w http.ResponseWriter, _ *http.Request, id Identity) {
	origins := LocalAccessOrigins(s.cfg.Addr)
	if s.cfg.PublicBaseURL != "" {
		origins = append(origins, AccessOrigin{
			Kind:  "public",
			Label: "Public tunnel",
			URL:   trimRightSlash(s.cfg.PublicBaseURL),
			Hint:  "Cloudflare / public HTTPS — off-home access",
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"user":     id.Name,
		"provider": id.Provider,
		"subject":  id.Subject,
		// Live write gate (Settings / CLI), not merely "Writer was constructed".
		"readOnly":         !s.WritesAllowed(),
		"canRename":        s.WritesAllowed(),
		"canSwitchSession": s.SessionSwitchAllowed(),
		// Spawning needs the write gate open AND spawn authority. Reported
		// separately from canRename because Policy answers them separately:
		// a role-scoped identity may be allowed to type into an existing
		// pane without being allowed to start new ones.
		"canSpawn":         s.cfg.Writer != nil && s.WritesAllowed() && s.cfg.Policy.CanSpawn(id),
		"pushEnabled":      s.push != nil,
		"devicesEnabled":   s.cfg.Devices != nil,
		"canManageDevices": !IsDeviceSubject(id.Subject) && s.cfg.Devices != nil,
		"firstRunPending":  FirstRunPending(s.stateDir()),
		"oauthEnabled":     s.cfg.OAuthStore != nil && len(s.cfg.OAuthStore.Buttons()) > 0,
		"accessOrigins":    origins,
		"defaultPairBase":  s.defaultPairBase(),
		"passwordLoginEnabled": func() bool {
			if pp, ok := s.cfg.Provider.(*PasswordProvider); ok {
				return pp.PasswordLogin()
			}
			return PasswordLoginEnabled(s.stateDir())
		}(),
	})
}

func (s *Server) handleAgents(w http.ResponseWriter, _ *http.Request, id Identity) {
	agents := filterViewable(s.cfg.Policy, id, s.src.List())
	writeJSON(w, http.StatusOK, agents)
}

// handleSlash serves composer typeahead for "/" commands. Query:
//
//	?pane=w1:pA&agent=omp&q=hel
//
// cwd is intentionally NOT accepted from the client — skills resolve from the
// pane's store CWD only (see AgentsService.SlashCommands). Always 200 + array.
func (s *Server) handleSlash(w http.ResponseWriter, r *http.Request, id Identity) {
	_ = id
	paneID := r.URL.Query().Get("pane")
	if paneID == "" {
		paneID = r.URL.Query().Get("paneId")
	}
	agent := r.URL.Query().Get("agent")
	q := r.URL.Query().Get("q")
	if q == "" {
		q = r.URL.Query().Get("query")
	}
	// Prefer the live AgentsService when available (resolves cwd from store).
	type slashper interface {
		SlashCommands(paneID, agent, query string) []app.SlashCommand
	}
	if sp, ok := s.src.(slashper); ok {
		writeJSON(w, http.StatusOK, sp.SlashCommands(paneID, agent, q))
		return
	}
	// Fallback: builtins only — no filesystem walk without a trusted cwd.
	writeJSON(w, http.StatusOK, app.FilterSlashCommands(agent, q, ""))
}

func (s *Server) handleOutput(w http.ResponseWriter, r *http.Request, id Identity) {
	paneID := r.PathValue("id")

	// Authorise against the pane as the store knows it. Never trust the client
	// to send a pane it is entitled to; look it up and ask the policy.
	var target *app.Agent
	for _, a := range s.src.List() {
		if a.PaneID == paneID {
			target = &a
			break
		}
	}
	if target == nil || !s.cfg.Policy.CanView(id, *target) {
		// Same response either way: do not disclose which panes exist.
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no such pane"})
		return
	}

	lines := paneStreamLines
	if raw := r.URL.Query().Get("lines"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			lines = n
		}
	}
	if lines > app.MaxPaneHistoryLines {
		lines = app.MaxPaneHistoryLines
	}
	text, err := s.src.ReadANSI(paneID, lines)
	if err != nil {
		s.log.Warn("web read pane", "pane", paneID, "err", err)
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "could not read pane"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"paneId": paneID, "text": text, "lines": lines})
}

// handleEvents streams agent updates as Server-Sent Events.
//
// SSE rather than WebSocket: the traffic is one-directional push, browsers
// reconnect automatically, and it needs no dependency or upgrade handshake.
func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request, id Identity) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "streaming unsupported"})
		return
	}
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-store")
	h.Set("Connection", "keep-alive")
	// Defeat proxy buffering, which otherwise silently breaks SSE.
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	ch := s.subscribe()
	defer s.unsubscribe(ch)

	// Send current state immediately so a reconnecting client is never stale.
	sendConn := func() {
		b, err := json.Marshal(s.src.Connection())
		if err != nil {
			return
		}
		fmt.Fprintf(w, "event: conn\ndata: %s\n\n", b)
	}
	if b, err := json.Marshal(filterViewable(s.cfg.Policy, id, s.src.List())); err == nil {
		fmt.Fprintf(w, "event: agents\ndata: %s\n\n", b)
	}
	sendConn()
	flusher.Flush()

	keepalive := time.NewTicker(25 * time.Second)
	defer keepalive.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-keepalive.C:
			// Comment frame: keeps intermediaries from reaping an idle stream.
			fmt.Fprint(w, ": ping\n\n")
			flusher.Flush()
		case <-ch:
			// Re-read and re-filter per subscriber rather than trusting the
			// broadcast payload, so policy is applied per identity.
			b, err := json.Marshal(filterViewable(s.cfg.Policy, id, s.src.List()))
			if err != nil {
				continue
			}
			fmt.Fprintf(w, "event: agents\ndata: %s\n\n", b)
			// Connectivity rides the same wake. A herd that just went
			// unreachable publishes an empty list, and without this frame
			// the browser would render "no agents" for a dead socket.
			sendConn()
			flusher.Flush()
		}
	}
}

// streamCoalesceWindow bounds how often a pane's live output can flush to
// the browser. herdr's output events fire per terminal repaint — hundreds a
// second for something like `seq 1 100000` — so forwarding every one would
// turn a firehose into the browser's problem instead of solving it here.
const streamCoalesceWindow = 100 * time.Millisecond

// paneStreamLines is how much visible screen both the initial snapshot and
// every live push carry. Kept equal so a client never sees the view jump
// size the moment the first live event arrives.
const paneStreamLines = 800

// handleStream streams one pane's live output as Server-Sent Events.
//
// The SSE plumbing — headers, flush discipline, keepalive — is the same
// pattern as handleEvents, copied rather than shared because the two differ
// in everything else: this is scoped to a single pane, and its payload comes
// from a per-pane herdr subscription that must be torn down with the request
// instead of living for the server's lifetime.
func (s *Server) handleStream(w http.ResponseWriter, r *http.Request, id Identity) {
	paneID := r.PathValue("id")

	// Authorise against the pane as the store knows it, exactly like
	// handleOutput: never trust the client to send a pane it is entitled to.
	var target *app.Agent
	for _, a := range s.src.List() {
		if a.PaneID == paneID {
			target = &a
			break
		}
	}
	if target == nil || !s.cfg.Policy.CanView(id, *target) {
		// Same response either way: do not disclose which panes exist.
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no such pane"})
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "streaming unsupported"})
		return
	}
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-store")
	h.Set("Connection", "keep-alive")
	// Defeat proxy buffering, which otherwise silently breaks SSE.
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	sendOutput := func(text string) {
		b, err := json.Marshal(map[string]string{"text": text})
		if err != nil {
			return
		}
		fmt.Fprintf(w, "event: output\ndata: %s\n\n", b)
		flusher.Flush()
	}

	lines := paneStreamLines
	if raw := r.URL.Query().Get("lines"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			lines = n
		}
	}
	if lines > app.MaxPaneHistoryLines {
		lines = app.MaxPaneHistoryLines
	}

	// Snapshot immediately so a client that just connected paints without
	// waiting on live output, which may not arrive for a long time on a
	// settled pane.
	if text, err := s.src.ReadANSI(paneID, lines); err != nil {
		s.log.Warn("web stream initial read", "pane", paneID, "err", err)
	} else {
		sendOutput(text)
	}

	ctx := r.Context()

	// mailbox holds the latest text pushed by the herdr subscription. The
	// coalescing ticker below drains it at a fixed rate instead of flushing
	// per event, so a burst of output collapses to its latest state rather
	// than melting a phone's connection.
	var (
		mu      sync.Mutex
		latest  string
		pending bool
	)
	push := func(text string) {
		mu.Lock()
		latest, pending = text, true
		mu.Unlock()
	}
	drain := func() (string, bool) {
		mu.Lock()
		defer mu.Unlock()
		if !pending {
			return "", false
		}
		pending = false
		return latest, true
	}

	// The subscription must not outlive this request: it is torn down the
	// moment ctx ends, and subDone lets the handler wait for that teardown
	// to finish before returning, so no goroutine survives the response.
	subDone := make(chan struct{})
	go func() {
		defer close(subDone)
		if err := s.src.StreamOutputANSI(ctx, paneID, lines, push); err != nil && ctx.Err() == nil {
			s.log.Warn("web pane stream ended", "pane", paneID, "err", err)
		}
	}()

	coalesce := time.NewTicker(streamCoalesceWindow)
	defer coalesce.Stop()
	keepalive := time.NewTicker(25 * time.Second)
	defer keepalive.Stop()

	for {
		select {
		case <-ctx.Done():
			<-subDone
			return
		case <-keepalive.C:
			// Comment frame: keeps intermediaries from reaping an idle stream.
			fmt.Fprint(w, ": ping\n\n")
			flusher.Flush()
		case <-coalesce.C:
			if text, ok := drain(); ok {
				sendOutput(text)
			}
		}
	}
}

func (s *Server) subscribe() chan []byte {
	ch := make(chan []byte, 1)
	s.mu.Lock()
	s.clients[ch] = struct{}{}
	s.mu.Unlock()
	return ch
}

func (s *Server) unsubscribe(ch chan []byte) {
	s.mu.Lock()
	delete(s.clients, ch)
	s.mu.Unlock()
}

// Notify wakes every connected browser. Called when the store changes.
func (s *Server) Notify() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for ch := range s.clients {
		select {
		case ch <- nil:
		default: // a wake is already pending for this client
		}
	}
}

// Clients reports how many browsers are connected.
func (s *Server) Clients() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.clients)
}

// webModeMarker tells the bundle it is running in a browser rather than the
// desktop webview, so it uses the HTTP transport instead of Wails bindings.
//
// A <meta> tag, deliberately not a <script>. The first attempt injected an
// inline script, which this server's own Content-Security-Policy then blocked:
// the flag was never set, the bundle fell back to the Wails IPC bridge, and
// every call died on POST /wails/runtime 405. A meta tag carries no script and
// cannot be refused by any script policy.
const webModeMarker = `<meta name="herdr-mode" content="web">`

// inlineScriptOpen matches an inline <script> (one with no src attribute) so a
// nonce can be stamped onto it.
var inlineScriptOpen = regexp.MustCompile(`<script(?:\s[^>]*)?>`)

func (s *Server) handleStatic(w http.ResponseWriter, r *http.Request, _ Identity) {
	if s.cfg.Assets == nil {
		http.Error(w, "no assets", http.StatusNotFound)
		return
	}
	// Serve index.html for unknown paths so client-side routing works.
	p := strings.TrimPrefix(r.URL.Path, "/")
	if p == "" {
		p = "index.html"
	}
	if _, err := fs.Stat(s.cfg.Assets, p); err != nil {
		p = "index.html"
	}

	if p == "index.html" {
		raw, err := fs.ReadFile(s.cfg.Assets, p)
		if err != nil {
			http.Error(w, "missing index", http.StatusInternalServerError)
			return
		}
		html := strings.Replace(string(raw), "<head>", "<head>"+webModeMarker, 1)

		// Stamp the CSP nonce onto inline scripts the build emitted (the boot
		// fallback). Scripts with src are already allowed by 'self'.
		if nonce, ok := r.Context().Value(nonceKey{}).(string); ok {
			html = inlineScriptOpen.ReplaceAllStringFunc(html, func(tag string) string {
				if strings.Contains(tag, "src=") || strings.Contains(tag, "nonce=") {
					return tag
				}
				return strings.Replace(tag, "<script", `<script nonce="`+nonce+`"`, 1)
			})
		}

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write([]byte(html))
		return
	}

	r2 := r.Clone(r.Context())
	r2.URL.Path = "/" + p
	http.FileServer(http.FS(s.cfg.Assets)).ServeHTTP(w, r2)
}

// handleServiceWorker serves the PWA service worker from the site root so
// its scope covers the whole app; a worker registered from /assets/sw.js
// could only ever control paths under /assets/.
func (s *Server) handleServiceWorker(w http.ResponseWriter, r *http.Request, _ Identity) {
	s.serveAsset(w, r, "sw.js", "text/javascript; charset=utf-8", true)
}

// handleManifest serves the Web App Manifest. Go's mime package does not
// know the .webmanifest extension, and neither the browser's mime-sniffer
// nor an operator's /etc/mime.types can be relied on to guess
// "application/manifest+json" — so, like handleServiceWorker, this bypasses
// handleStatic's generic (extension-sniffed) file serving entirely.
func (s *Server) handleManifest(w http.ResponseWriter, r *http.Request, _ Identity) {
	s.serveAsset(w, r, "manifest.webmanifest", "application/manifest+json", false)
}

// serveAsset serves one file straight out of the embedded assets with an
// explicit, hardcoded content type — never sniffed, never dependent on the
// host OS's mime database — so these two PWA entry points can never be
// served with a type the browser refuses to treat as a service worker or
// manifest, and never silently swallowed by handleStatic's SPA fallback.
//
// Caching:
//
//   - noCache=true (service worker): Cache-Control: no-cache with no
//     validator. A stale SW is worse than a slow one — every fetch must hit
//     the origin.
//   - noCache=false (icons, manifest): strong ETag from the bytes +
//     Cache-Control: no-cache. Browsers and Cloudflare still store the
//     response, but every use revalidates; an unchanged icon returns 304
//     (no body). Without a validator the previous no-header default left
//     heuristic caches holding a favicon for days, so a bare-sheep rebuild
//     never reached the tab strip.
func (s *Server) serveAsset(w http.ResponseWriter, r *http.Request, name, contentType string, noCache bool) {
	if s.cfg.Assets == nil {
		http.Error(w, "no assets", http.StatusNotFound)
		return
	}
	raw, err := fs.ReadFile(s.cfg.Assets, name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", contentType)
	if noCache {
		// A stale cached service worker is worse than a slow one: an
		// intermediate cache (or a phone's HTTP cache over a flaky tunnel
		// connection) must never be allowed to serve last week's sw.js and
		// silently stop an update from ever installing.
		w.Header().Set("Cache-Control", "no-cache")
		_, _ = w.Write(raw)
		return
	}
	sum := sha256.Sum256(raw)
	etag := `"` + hex.EncodeToString(sum[:16]) + `"`
	w.Header().Set("ETag", etag)
	w.Header().Set("Cache-Control", "no-cache")
	if match := r.Header.Get("If-None-Match"); match != "" {
		for _, candidate := range strings.Split(match, ",") {
			if strings.TrimSpace(candidate) == etag {
				w.WriteHeader(http.StatusNotModified)
				return
			}
		}
	}
	_, _ = w.Write(raw)
}

// Start begins listening. It returns once the listener is open so callers can
// report the real address, then serves in the background.
func (s *Server) Start() error {
	ln, err := net.Listen("tcp", s.cfg.Addr)
	if err != nil {
		return fmt.Errorf("web: listen %s: %w", s.cfg.Addr, err)
	}
	mode := "read-only"
	if s.cfg.Writer != nil {
		mode = "read-write"
	}
	s.log.Info("web dashboard listening",
		"addr", ln.Addr().String(),
		"auth", s.cfg.Provider.Name(),
		"mode", mode)

	go func() {
		if err := s.http.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			s.log.Error("web server stopped", "err", err)
		}
	}()
	return nil
}

// Stop shuts the server down gracefully.
func (s *Server) Stop(ctx context.Context) error {
	return s.http.Shutdown(ctx)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
