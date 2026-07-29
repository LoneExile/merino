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
	// behindProxy mirrors the server setting; the provider needs it to know
	// whether a Secure cookie can survive this request.
	behindProxy bool
	// ip resolves the client address for throttling. Injected so the caller
	// decides whether proxy headers are trustworthy; getting that wrong either
	// lets an attacker rotate past the throttle or lumps every remote user
	// into one bucket.
	ip IPResolver
	// pairing, when set, accepts short-lived one-shot tokens (QR login).
	pairing *Pairing
	// devices, when set, mints a per-device identity on QR redeem instead of
	// impersonating the master password user.
	devices *DeviceStore
	// altUser/altPass are an optional user-set phone password (Settings).
	altUser string
	altPass [32]byte
	hasAlt  bool
	// allowPassword gates HTTP username/password (bootstrap + optional).
	// QR/token pairing is unaffected.
	//
	// Default FALSE. This is the weakest door the app has and the constructor
	// is not where it should be opened: main.go resolves the persisted
	// preference (PasswordLoginEnabled) and calls SetPasswordLogin. A
	// construction site that forgets is then closed, not open.
	allowPassword bool

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
func NewPasswordProvider(user, password string, ip IPResolver, behindProxy bool) *PasswordProvider {
	if ip == nil {
		ip = DirectIP
	}
	return &PasswordProvider{
		user:          user,
		passHash:      sha256.Sum256([]byte(password)),
		ip:            ip,
		behindProxy:   behindProxy,
		allowPassword: false,
		failures:      make(map[string]*attemptRecord),
	}
}

func (p *PasswordProvider) Name() string      { return "password" }
func (p *PasswordProvider) LoginPath() string { return "/login" }

// SetPairing attaches the short-lived QR/token store. Nil disables token login.
func (p *PasswordProvider) SetPairing(pair *Pairing) { p.pairing = pair }

// SetDevices attaches the paired-device store used on QR redeem.
func (p *PasswordProvider) SetDevices(d *DeviceStore) { p.devices = d }

// SetPasswordLogin enables or disables HTTP username/password sign-in.
// Pairing tokens (QR) always work. Desktop Settings uses Wails IPC, not this.
func (p *PasswordProvider) SetPasswordLogin(on bool) { p.allowPassword = on }

// PasswordLogin reports whether HTTP user/pass is currently accepted.
func (p *PasswordProvider) PasswordLogin() bool { return p.allowPassword }

// SetOptionalPassword enables a second username/password for phone login
// without QR. Empty pass clears it.
func (p *PasswordProvider) SetOptionalPassword(user, pass string) {
	if pass == "" {
		p.hasAlt = false
		p.altUser = ""
		p.altPass = [32]byte{}
		return
	}
	if user == "" {
		user = "phone"
	}
	p.altUser = user
	p.altPass = sha256.Sum256([]byte(pass))
	p.hasAlt = true
}

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

// verify checks credentials in constant time against the bootstrap/master
// account and, when set, the optional phone password.
func (p *PasswordProvider) verify(user, password string) bool {
	got := sha256.Sum256([]byte(password))
	// Compare both fields unconditionally so a wrong username and a wrong
	// password take the same time.
	userOK := subtle.ConstantTimeCompare([]byte(user), []byte(p.user))
	passOK := subtle.ConstantTimeCompare(got[:], p.passHash[:])
	if userOK&passOK == 1 {
		return true
	}
	if !p.hasAlt {
		// Still touch alt fields to keep timing flatter when unset.
		_ = subtle.ConstantTimeCompare([]byte(user), []byte(p.altUser))
		_ = subtle.ConstantTimeCompare(got[:], p.altPass[:])
		return false
	}
	altUserOK := subtle.ConstantTimeCompare([]byte(user), []byte(p.altUser))
	altPassOK := subtle.ConstantTimeCompare(got[:], p.altPass[:])
	return altUserOK&altPassOK == 1
}

// insecureTransport reports whether a Secure cookie set on this request would
// be discarded by the browser.
//
// Cloudflare sets X-Forwarded-Proto to the scheme the client actually used.
// When it says http, the login will "succeed", the browser will silently drop
// the Secure session cookie, and the next request will bounce back to the
// login form with no explanation whatsoever. Detect it and say so, rather
// than letting the user retype a correct password indefinitely.
func insecureTransport(r *http.Request, behindProxy bool) bool {
	if !behindProxy {
		return false // plain HTTP is expected on a LAN; cookies are not Secure
	}
	// Two signals, because which one a cloudflared deployment sets is not
	// guaranteed. Checking both avoids depending on an assumption that has
	// already bitten once with CF-Connecting-IP.
	if proto := r.Header.Get("X-Forwarded-Proto"); proto != "" {
		return !strings.EqualFold(proto, "https")
	}
	// CF-Visitor is a small JSON object, e.g. {"scheme":"https"}.
	if v := r.Header.Get("CF-Visitor"); v != "" {
		return !strings.Contains(v, `"https"`)
	}
	// Neither header present: cannot tell, so do not block a working login.
	return false
}

// insecureMsg explains a failure that is otherwise completely silent.
const insecureMsg = "This page was loaded over plain HTTP. " +
	"Sign-in needs HTTPS, because the session cookie is marked Secure and " +
	"your browser will discard it otherwise. Reload using https:// and try again."

// Mount registers the login form and its submission handler.
func (p *PasswordProvider) Mount(mux *http.ServeMux, success func(http.ResponseWriter, *http.Request, Identity)) {
	mux.HandleFunc("GET /login", func(w http.ResponseWriter, r *http.Request) {
		if insecureTransport(r, p.behindProxy) {
			writeLoginPage(w, r, insecureMsg, p.allowPassword)
			return
		}
		// Phone scanned a QR: /login?token=… redeems in one GET so the
		// camera-app open does not require a second tap on Submit.
		// Same IP throttle as POST so a sprayed token list cannot bypass lockout.
		if tok := r.URL.Query().Get("token"); tok != "" {
			client := p.ip(r)
			if p.throttled(client) {
				w.WriteHeader(http.StatusTooManyRequests)
				writeLoginPage(w, r, "Too many attempts. Wait a minute and try again.", p.allowPassword)
				return
			}
			if p.redeemToken(w, r, tok, success) {
				p.clearFailures(client)
				return
			}
			p.recordFailure(client)
			writeLoginPage(w, r, "That sign-in link expired or was already used. Ask the desktop app for a new QR.", p.allowPassword)
			return
		}
		writeLoginPage(w, r, "", p.allowPassword)
	})

	mux.HandleFunc("POST /login", func(w http.ResponseWriter, r *http.Request) {
		if insecureTransport(r, p.behindProxy) {
			// Refuse rather than issue a cookie the browser will throw away.
			w.WriteHeader(http.StatusBadRequest)
			writeLoginPage(w, r, insecureMsg, p.allowPassword)
			return
		}
		client := p.ip(r)
		if p.throttled(client) {
			w.WriteHeader(http.StatusTooManyRequests)
			writeLoginPage(w, r, "Too many attempts. Wait a minute and try again.", p.allowPassword)
			return
		}
		if err := r.ParseForm(); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			writeLoginPage(w, r, "Malformed request.", p.allowPassword)
			return
		}
		// Token form field (manual paste fallback from the QR sheet).
		if tok := r.PostFormValue("token"); tok != "" {
			if p.redeemToken(w, r, tok, success) {
				return
			}
			p.recordFailure(client)
			w.WriteHeader(http.StatusUnauthorized)
			writeLoginPage(w, r, "That sign-in code expired or was already used.", p.allowPassword)
			return
		}
		user := r.PostFormValue("username")
		pass := r.PostFormValue("password")
		// Empty password fields with no token → user submitted the password form.
		if (user != "" || pass != "") && !p.allowPassword {
			w.WriteHeader(http.StatusForbidden)
			writeLoginPage(w, r, "Username and password sign-in is turned off. Scan a QR from the Mac app.", p.allowPassword)
			return
		}
		if !p.allowPassword {
			w.WriteHeader(http.StatusUnauthorized)
			writeLoginPage(w, r, "Username and password sign-in is turned off. Scan a QR from the Mac app.", p.allowPassword)
			return
		}

		if !p.verify(user, pass) {
			p.recordFailure(client)
			// Deliberately vague: do not reveal which field was wrong.
			w.WriteHeader(http.StatusUnauthorized)
			writeLoginPage(w, r, "Incorrect username or password.", p.allowPassword)
			return
		}
		p.clearFailures(client)
		// Prefer the name the user typed when it matched the optional phone
		// account so the UI does not always show the bootstrap "local" user.
		name := p.user
		subject := p.user
		if p.hasAlt && subtle.ConstantTimeCompare([]byte(user), []byte(p.altUser)) == 1 {
			name = p.altUser
			subject = "password:" + p.altUser
		}
		success(w, r, Identity{Subject: subject, Name: name, Provider: p.Name(), Roles: []string{"view", "control"}})
	})
}

// redeemToken burns a one-shot pairing token and, on success, issues a session.
// Prefer a per-device grant when a DeviceStore is wired; fall back to the
// master user only when devices are unavailable (tests / legacy).
func (p *PasswordProvider) redeemToken(w http.ResponseWriter, r *http.Request, tok string, success func(http.ResponseWriter, *http.Request, Identity)) bool {
	if p.pairing == nil {
		return false
	}
	if !p.pairing.Consume(tok) {
		return false
	}
	if p.devices != nil {
		label := friendlyDeviceName(r.UserAgent())
		_, id, err := p.devices.Mint(label, "pairing", nil)
		if err != nil {
			// Do not leave the user with a burned token and no session.
			writeLoginPage(w, r, "Paired, but saving this device failed. Try minting a new QR.", p.allowPassword)
			return true
		}
		success(w, r, id)
		return true
	}
	success(w, r, Identity{Subject: p.user, Name: p.user, Provider: "pairing", Roles: []string{"view", "control"}})
	return true
}

// friendlyDeviceName turns a User-Agent into a short Settings label.
func friendlyDeviceName(ua string) string {
	l := strings.ToLower(ua)
	switch {
	case strings.Contains(l, "iphone"):
		return "iPhone"
	case strings.Contains(l, "ipad"):
		return "iPad"
	case strings.Contains(l, "android"):
		// Most Android browsers omit "Mobile" in compact UAs (e.g. "Android 10; K").
		if strings.Contains(l, "tablet") {
			return "Android tablet"
		}
		return "Android phone"
	case strings.Contains(l, "macintosh") || strings.Contains(l, "mac os"):
		return "Mac browser"
	case strings.Contains(l, "windows"):
		return "Windows browser"
	case strings.Contains(l, "crios") || strings.Contains(l, "fxios"):
		return "iPhone"
	case ua == "":
		return "Phone"
	default:
		return "Phone"
	}
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
