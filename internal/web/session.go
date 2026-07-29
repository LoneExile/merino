// Package web serves the agent dashboard over HTTP for browsers on the local
// network, alongside the desktop panel.
//
// Everything here is read-only for now. The write path (approvals, keys) is
// deliberately absent rather than merely hidden: this surface is reachable by
// anything that can route to the host, and pane writes are unrestricted input
// to terminals running with the user's privileges.
package web

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Identity is an authenticated user. Kept deliberately small so an OIDC
// provider can populate it from claims without reshaping anything downstream.
type Identity struct {
	// Subject uniquely identifies the user within its provider. For Keycloak
	// this is the `sub` claim.
	Subject string
	// Name is a human-readable label for logs and the UI.
	Name string
	// Provider names the authenticator that vouched for this identity.
	Provider string
	// Roles carries provider-supplied authorisation claims. Empty for the
	// password provider; populated from Keycloak realm/client roles later.
	Roles []string
}

const (
	sessionCookie = "herdr_session"
	// idleTTL bounds how long a browser stays signed in without being used.
	// Every authenticated request past the halfway mark pushes it out again,
	// so an active phone is not logged out mid-use.
	idleTTL = 12 * time.Hour
	// absoluteTTL is the hard ceiling measured from first sign-in, which no
	// amount of activity extends. Idle expiry alone would let a stolen cookie
	// live forever simply by being polled; this is what bounds that.
	absoluteTTL = 7 * 24 * time.Hour
	// renewAfter is the share of the idle window that must elapse before a
	// request re-issues the cookie. Renewing on every request would rotate
	// Set-Cookie on each poll for no benefit.
	renewAfter = idleTTL / 2
	// sessionFields is the number of dot-separated parts in a cookie payload.
	// It changed from 5 to 6 when issuedAt was added; a mismatch fails closed
	// in Read, so cookies from before the change are rejected rather than
	// half-parsed into a session with no absolute cap.
	sessionFields = 6
)

// Sessions issues and validates signed session cookies.
//
// The signing key is generated at startup and never persisted, so every
// restart invalidates outstanding sessions. That is the desired trade for a
// tool that controls terminals: a stolen cookie cannot outlive the process.
type Sessions struct {
	key      []byte
	idle     time.Duration
	absolute time.Duration
	// now is swappable so tests can cross a boundary without sleeping.
	now    func() time.Time
	secure bool
}

// NewSessions returns a session issuer with a fresh random key. secure marks
// cookies Secure, which must be enabled whenever the server is behind TLS.
func NewSessions(secure bool) (*Sessions, error) {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("generate session key: %w", err)
	}
	return &Sessions{
		key:      key,
		idle:     idleTTL,
		absolute: absoluteTTL,
		now:      time.Now,
		secure:   secure,
	}, nil
}

func (s *Sessions) sign(payload string) string {
	mac := hmac.New(sha256.New, s.key)
	mac.Write([]byte(payload))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func b64(s string) string { return base64.RawURLEncoding.EncodeToString([]byte(s)) }

// Session is a validated cookie: who, and the two clocks that bound it.
type Session struct {
	Identity
	// IssuedAt is the original sign-in, carried across renewals. The absolute
	// cap is measured from here, so renewing cannot push it out.
	IssuedAt time.Time
	// ExpiresAt is the current idle deadline.
	ExpiresAt time.Time
}

// Issue sets a session cookie for a fresh sign-in, starting both clocks.
func (s *Sessions) Issue(w http.ResponseWriter, id Identity) {
	s.write(w, id, s.now())
}

// deadline is the idle expiry a cookie written now would carry, clamped to the
// absolute cap. Both write and Renew go through it so the clamp cannot drift
// between "what we are about to store" and "what we predicted we would store".
func (s *Sessions) deadline(issuedAt time.Time) time.Time {
	next := s.now().Add(s.idle)
	if hard := issuedAt.Add(s.absolute); next.After(hard) {
		return hard
	}
	return next
}

// write emits a cookie preserving issuedAt, so a renewal cannot extend the
// absolute cap.
func (s *Sessions) write(w http.ResponseWriter, id Identity, issuedAt time.Time) {
	expiry := s.deadline(issuedAt)
	payload := strings.Join([]string{
		b64(id.Subject),
		b64(id.Name),
		b64(id.Provider),
		b64(strings.Join(id.Roles, ",")),
		strconv.FormatInt(expiry.Unix(), 10),
		strconv.FormatInt(issuedAt.Unix(), 10),
	}, ".")

	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    payload + "~" + s.sign(payload),
		Path:     "/",
		Expires:  expiry,
		HttpOnly: true,
		Secure:   s.secure,
		SameSite: http.SameSiteLaxMode,
	})
}

// Read returns the identity carried by the request, if the cookie is present,
// correctly signed, within its idle window and within the absolute cap.
func (s *Sessions) Read(r *http.Request) (Identity, bool) {
	sess, ok := s.ReadSession(r)
	return sess.Identity, ok
}

// ReadSession is Read plus the timing a caller needs to decide on renewal.
func (s *Sessions) ReadSession(r *http.Request) (Session, bool) {
	c, err := r.Cookie(sessionCookie)
	if err != nil {
		return Session{}, false
	}
	payload, sig, ok := strings.Cut(c.Value, "~")
	if !ok {
		return Session{}, false
	}
	// Constant-time: a timing oracle here would leak the signature byte by byte.
	if subtle.ConstantTimeCompare([]byte(sig), []byte(s.sign(payload))) != 1 {
		return Session{}, false
	}

	parts := strings.Split(payload, ".")
	// Fails closed on the pre-issuedAt 5-field format: an old cookie is
	// rejected outright rather than parsed into a session with no cap.
	if len(parts) != sessionFields {
		return Session{}, false
	}
	exp, err := strconv.ParseInt(parts[4], 10, 64)
	if err != nil {
		return Session{}, false
	}
	iat, err := strconv.ParseInt(parts[5], 10, 64)
	if err != nil {
		return Session{}, false
	}
	now := s.now()
	expiresAt, issuedAt := time.Unix(exp, 0), time.Unix(iat, 0)
	// Both clocks, every read. Checking only the idle deadline would let a
	// cookie renewed just under the ceiling outlive the cap by a full window.
	if now.After(expiresAt) || now.After(issuedAt.Add(s.absolute)) {
		return Session{}, false
	}
	sub, err1 := base64.RawURLEncoding.DecodeString(parts[0])
	name, err2 := base64.RawURLEncoding.DecodeString(parts[1])
	prov, err3 := base64.RawURLEncoding.DecodeString(parts[2])
	roles, err4 := base64.RawURLEncoding.DecodeString(parts[3])
	if err1 != nil || err2 != nil || err3 != nil || err4 != nil {
		return Session{}, false
	}
	id := Identity{Subject: string(sub), Name: string(name), Provider: string(prov)}
	if len(roles) > 0 {
		id.Roles = strings.Split(string(roles), ",")
	}
	return Session{Identity: id, IssuedAt: issuedAt, ExpiresAt: expiresAt}, true
}

// Renew pushes the idle deadline out when a request arrives late enough in the
// window for that to be worth a Set-Cookie.
//
// Deliberately not derived from ExpiresAt minus idle: once the clamp bites,
// that no longer reconstructs the last write and the arithmetic drifts.
// IssuedAt is carried on the session, so use it.
//
// Callers must invoke it before writing a response body: it sets a header.
func (s *Sessions) Renew(w http.ResponseWriter, sess Session) bool {
	next := s.deadline(sess.IssuedAt)
	gain := next.Sub(sess.ExpiresAt)
	if gain <= 0 {
		// Already sitting on this deadline: the clamp has pinned the cookie to
		// the cap and rewriting it would change nothing. This is what stops a
		// polling client rotating Set-Cookie for the last stretch.
		return false
	}
	// The final top-up is always taken, however small. Skipping it because the
	// gain looked trivial would strand the cookie short of the cap and log an
	// actively used session out early — up to renewAfter before its ceiling,
	// which is a bug a "meaningful gain only" rule quietly introduces.
	if gain < renewAfter && !next.Equal(sess.IssuedAt.Add(s.absolute)) {
		return false
	}
	s.write(w, sess.Identity, sess.IssuedAt)
	return true
}

// Clear removes the session cookie.
func (s *Sessions) Clear(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   s.secure,
		SameSite: http.SameSiteLaxMode,
	})
}
