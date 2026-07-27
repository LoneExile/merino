package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
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
	// Secure marks cookies Secure; enable behind TLS.
	Secure bool
	// Assets is the built frontend, served at /.
	Assets fs.FS
	Logger *slog.Logger
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
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	sessions, err := NewSessions(cfg.Secure)
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
		s.log.Info("web login", "user", id.Name, "provider", id.Provider, "ip", clientIP(r))
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

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		// The bundle is self-contained: no external origins, no inline event
		// handlers. 'unsafe-inline' is needed for the styles Vite inlines.
		h.Set("Content-Security-Policy",
			"default-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; connect-src 'self'; base-uri 'none'; form-action 'self'")
		next.ServeHTTP(w, r)
	})
}

func (s *Server) handleSession(w http.ResponseWriter, _ *http.Request, id Identity) {
	writeJSON(w, http.StatusOK, map[string]any{
		"user":     id.Name,
		"provider": id.Provider,
		// Tells the frontend to hide every write affordance. The server has no
		// write endpoints at all, so this is a UX hint, not the enforcement.
		"readOnly": true,
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
// Injected server-side rather than sniffed client-side: capability detection
// for the Wails IPC bridge is platform-specific and racy, whereas whoever
// served the page knows the answer for certain.
const webModeMarker = `<script>window.__HERDR_WEB__=true</script>`

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
	s.log.Info("web dashboard listening",
		"addr", ln.Addr().String(),
		"auth", s.cfg.Provider.Name(),
		"mode", "read-only")

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
