package web

import (
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// ErrBadCredentials is returned when a login attempt fails.
var ErrBadCredentials = errors.New("invalid credentials")

// Provider authenticates users and mounts whatever routes it needs to do so.
//
// The seam exists so Keycloak can be added without touching sessions, the API
// or the frontend. A password provider serves a form; an OIDC provider will
// redirect to the identity provider and handle a callback. Both end by calling
// the same `success` callback with an Identity, and everything downstream is
// unchanged.
type Provider interface {
	// Name identifies the provider in logs and on the login page.
	Name() string

	// LoginPath is where unauthenticated users are sent.
	LoginPath() string

	// Mount registers the provider's routes. success must be invoked with the
	// authenticated identity; the caller issues the session and redirects.
	Mount(mux *http.ServeMux, success func(http.ResponseWriter, *http.Request, Identity))
}

// PasswordProvider is a single-user username/password login.
//
// Interim by design: it exists so the LAN surface is not open to anyone who
// can route to the host, and so the session/policy plumbing is exercised
// before Keycloak arrives. It supports exactly one account, has no
// registration, no reset and no user store.
type PasswordProvider struct {
	user     string
	passHash [32]byte
	// ip resolves the client address for throttling. Injected so the caller
	// decides whether proxy headers are trustworthy; getting that wrong either
	// lets an attacker rotate past the throttle or lumps every remote user
	// into one bucket.
	ip IPResolver

	mu       sync.Mutex
	failures map[string]*attemptRecord
}

type attemptRecord struct {
	count int
	last  time.Time
}

const (
	// lockoutAfter failed attempts triggers a delay window.
	lockoutAfter = 5
	// lockoutWindow is how long the throttle applies.
	lockoutWindow = 1 * time.Minute
)

// NewPasswordProvider builds a provider for one account. The password is
// hashed immediately so the plaintext does not linger in memory longer than
// necessary.
//
// Note this is a plain SHA-256, not a password-stretching KDF. That is
// acceptable only because the hash is never persisted or transmitted — it
// exists to compare against a value supplied through the environment at
// startup. Do not reuse this pattern for a stored credential.
func NewPasswordProvider(user, password string, ip IPResolver) *PasswordProvider {
	if ip == nil {
		ip = DirectIP
	}
	return &PasswordProvider{
		user:     user,
		passHash: sha256.Sum256([]byte(password)),
		ip:       ip,
		failures: make(map[string]*attemptRecord),
	}
}

func (p *PasswordProvider) Name() string      { return "password" }
func (p *PasswordProvider) LoginPath() string { return "/login" }

// throttled reports whether this client should be refused outright, and
// records the attempt.
func (p *PasswordProvider) throttled(client string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	rec, ok := p.failures[client]
	if !ok {
		return false
	}
	if time.Since(rec.last) > lockoutWindow {
		delete(p.failures, client)
		return false
	}
	return rec.count >= lockoutAfter
}

func (p *PasswordProvider) recordFailure(client string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	rec, ok := p.failures[client]
	if !ok || time.Since(rec.last) > lockoutWindow {
		p.failures[client] = &attemptRecord{count: 1, last: time.Now()}
		return
	}
	rec.count++
	rec.last = time.Now()
}

func (p *PasswordProvider) clearFailures(client string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.failures, client)
}

// verify checks credentials in constant time.
func (p *PasswordProvider) verify(user, password string) bool {
	got := sha256.Sum256([]byte(password))
	// Compare both fields unconditionally so a wrong username and a wrong
	// password take the same time.
	userOK := subtle.ConstantTimeCompare([]byte(user), []byte(p.user))
	passOK := subtle.ConstantTimeCompare(got[:], p.passHash[:])
	return userOK&passOK == 1
}

// Mount registers the login form and its submission handler.
func (p *PasswordProvider) Mount(mux *http.ServeMux, success func(http.ResponseWriter, *http.Request, Identity)) {
	mux.HandleFunc("GET /login", func(w http.ResponseWriter, r *http.Request) {
		writeLoginPage(w, "")
	})

	mux.HandleFunc("POST /login", func(w http.ResponseWriter, r *http.Request) {
		client := p.ip(r)
		if p.throttled(client) {
			w.WriteHeader(http.StatusTooManyRequests)
			writeLoginPage(w, "Too many attempts. Wait a minute and try again.")
			return
		}
		if err := r.ParseForm(); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			writeLoginPage(w, "Malformed request.")
			return
		}
		user := r.PostFormValue("username")
		pass := r.PostFormValue("password")

		if !p.verify(user, pass) {
			p.recordFailure(client)
			// Deliberately vague: do not reveal which field was wrong.
			w.WriteHeader(http.StatusUnauthorized)
			writeLoginPage(w, "Incorrect username or password.")
			return
		}
		p.clearFailures(client)
		success(w, r, Identity{Subject: p.user, Name: p.user, Provider: p.Name()})
	})
}

// IPResolver extracts the client address used for throttling and logging.
type IPResolver func(*http.Request) string

// DirectIP reads the peer address. Correct when the server is reached
// directly, as on a LAN.
func DirectIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// ProxiedIP reads the client address from Cloudflare's headers.
//
// Only safe when every request provably arrives through the tunnel. Reached
// directly, these headers are attacker-supplied: a caller could send a fresh
// CF-Connecting-IP per attempt and never trip the login throttle.
//
// CF-Connecting-IP is preferred over X-Forwarded-For because Cloudflare
// overwrites it, whereas XFF is a client-appendable list whose leftmost entry
// is whatever the caller put there.
func ProxiedIP(r *http.Request) string {
	if ip := r.Header.Get("CF-Connecting-IP"); ip != "" {
		return ip
	}
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		if first, _, ok := strings.Cut(xff, ","); ok {
			return strings.TrimSpace(first)
		}
		return strings.TrimSpace(xff)
	}
	return DirectIP(r)
}
