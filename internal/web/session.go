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
	// sessionTTL bounds how long a browser stays signed in.
	sessionTTL = 12 * time.Hour
)

// Sessions issues and validates signed session cookies.
//
// The signing key is generated at startup and never persisted, so every
// restart invalidates outstanding sessions. That is the desired trade for a
// tool that controls terminals: a stolen cookie cannot outlive the process.
type Sessions struct {
	key    []byte
	ttl    time.Duration
	secure bool
}

// NewSessions returns a session issuer with a fresh random key. secure marks
// cookies Secure, which must be enabled whenever the server is behind TLS.
func NewSessions(secure bool) (*Sessions, error) {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("generate session key: %w", err)
	}
	return &Sessions{key: key, ttl: sessionTTL, secure: secure}, nil
}

func (s *Sessions) sign(payload string) string {
	mac := hmac.New(sha256.New, s.key)
	mac.Write([]byte(payload))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func b64(s string) string { return base64.RawURLEncoding.EncodeToString([]byte(s)) }

// Issue sets a session cookie for the given identity.
func (s *Sessions) Issue(w http.ResponseWriter, id Identity) {
	expiry := time.Now().Add(s.ttl)
	payload := strings.Join([]string{
		b64(id.Subject),
		b64(id.Name),
		b64(id.Provider),
		b64(strings.Join(id.Roles, ",")),
		strconv.FormatInt(expiry.Unix(), 10),
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
// correctly signed and unexpired.
func (s *Sessions) Read(r *http.Request) (Identity, bool) {
	c, err := r.Cookie(sessionCookie)
	if err != nil {
		return Identity{}, false
	}
	payload, sig, ok := strings.Cut(c.Value, "~")
	if !ok {
		return Identity{}, false
	}
	// Constant-time: a timing oracle here would leak the signature byte by byte.
	if subtle.ConstantTimeCompare([]byte(sig), []byte(s.sign(payload))) != 1 {
		return Identity{}, false
	}

	parts := strings.Split(payload, ".")
	if len(parts) != 5 {
		return Identity{}, false
	}
	exp, err := strconv.ParseInt(parts[4], 10, 64)
	if err != nil || time.Now().After(time.Unix(exp, 0)) {
		return Identity{}, false
	}
	sub, err1 := base64.RawURLEncoding.DecodeString(parts[0])
	name, err2 := base64.RawURLEncoding.DecodeString(parts[1])
	prov, err3 := base64.RawURLEncoding.DecodeString(parts[2])
	roles, err4 := base64.RawURLEncoding.DecodeString(parts[3])
	if err1 != nil || err2 != nil || err3 != nil || err4 != nil {
		return Identity{}, false
	}
	id := Identity{Subject: string(sub), Name: string(name), Provider: string(prov)}
	if len(roles) > 0 {
		id.Roles = strings.Split(string(roles), ",")
	}
	return id, true
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
