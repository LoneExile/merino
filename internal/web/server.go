package web

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/LoneExile/herdr-tunnel/internal/app"
)

// Source supplies the data the web UI renders. Implemented by app.AgentsService.
//
// Only read operations appear here. Adding a write method is the deliberate,
// visible act that opens the write path — it cannot happen by accident.
type Source interface {
	List() []app.Agent
	Counts() app.Counts
	Read(paneID string, lines int) (string, error)
}

// Config configures the HTTP server.
type Config struct {
	// Addr is the listen address. Defaults to 127.0.0.1:8730. Binding to a
	// non-loopback address is an explicit choice by the operator.
	Addr string
	// Provider authenticates users.
	Provider Provider
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

	// Writer enables the write endpoints. Nil means read-only, and read-only
	// means the routes do not exist rather than being refused at runtime.
	Writer Writer
	// Audit records every write. Required whenever Writer is set: an
	// internet-reachable path into a live terminal without a durable record of
	// who used it is not something worth shipping.
	Audit *app.Audit
}

// Server is the read-only HTTP dashboard.
type Server struct {
	cfg      Config
	src      Source
	sessions *Sessions
	log      *slog.Logger
	http     *http.Server

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

	mux.HandleFunc("POST /logout", func(w http.ResponseWriter, r *http.Request) {
		s.sessions.Clear(w)
		http.Redirect(w, r, s.cfg.Provider.LoginPath(), http.StatusSeeOther)
	})

	mux.Handle("GET /api/session", s.authed(s.handleSession))
	mux.Handle("GET /api/agents", s.authed(s.handleAgents))
	mux.Handle("GET /api/events", s.authed(s.handleEvents))
	mux.Handle("GET /api/panes/{id}/output", s.authed(s.handleOutput))

	if s.cfg.Writer != nil {
		s.mountWrites(mux)
	}

	mux.Handle("GET /", s.authed(s.handleStatic))

	return securityHeaders(mux)
}

// authed rejects unauthenticated requests: a redirect for navigations, 401 for
// API calls so the frontend can react without following HTML.
func (s *Server) authed(h func(http.ResponseWriter, *http.Request, Identity)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, ok := s.sessions.Read(r)
		if !ok {
			if strings.HasPrefix(r.URL.Path, "/api/") {
				writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthenticated"})
				return
			}
			http.Redirect(w, r, s.cfg.Provider.LoginPath(), http.StatusSeeOther)
			return
		}
		h(w, r, id)
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
				"base-uri 'none'; form-action 'self'; object-src 'none'")

		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), nonceKey{}, nonce)))
	})
}

func (s *Server) handleSession(w http.ResponseWriter, _ *http.Request, id Identity) {
	writeJSON(w, http.StatusOK, map[string]any{
		"user":     id.Name,
		"provider": id.Provider,
		// A UX hint so the browser can hide affordances it cannot use. The
		// enforcement is that the routes are absent, not this flag.
		"readOnly": s.cfg.Writer == nil,
	})
}

func (s *Server) handleAgents(w http.ResponseWriter, _ *http.Request, id Identity) {
	agents := filterViewable(s.cfg.Policy, id, s.src.List())
	writeJSON(w, http.StatusOK, agents)
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

	text, err := s.src.Read(paneID, 50)
	if err != nil {
		s.log.Warn("web read pane", "pane", paneID, "err", err)
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "could not read pane"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"paneId": paneID, "text": text})
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
	if b, err := json.Marshal(filterViewable(s.cfg.Policy, id, s.src.List())); err == nil {
		fmt.Fprintf(w, "event: agents\ndata: %s\n\n", b)
		flusher.Flush()
	}

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
			flusher.Flush()
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
