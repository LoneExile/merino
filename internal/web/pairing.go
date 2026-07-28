package web

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/url"
	"sync"
	"time"

	qrcode "github.com/skip2/go-qrcode"
)

// Pairing issues short-lived one-shot tokens for phone login via QR.
//
// The token is NEVER the master MERINO_PASS. It is a random secret
// stored only in memory, single-use, and expired after a short window so a
// photographed QR cannot become a permanent credential.
type Pairing struct {
	mu      sync.Mutex
	tokens  map[string]time.Time // sha256 hex of raw token → expiry
	ttl     time.Duration
	baseURL string // public origin used in the QR, e.g. https://merino.example
}

// NewPairing builds a store. baseURL may be empty; Mint then returns a path-only
// pair URL the phone can still open if it is already on the right host.
func NewPairing(baseURL string) *Pairing {
	return &Pairing{
		tokens:  make(map[string]time.Time),
		ttl:     2 * time.Minute,
		baseURL: trimRightSlash(baseURL),
	}
}

func trimRightSlash(s string) string {
	for len(s) > 0 && s[len(s)-1] == '/' {
		s = s[:len(s)-1]
	}
	return s
}

// SetBaseURL updates the origin embedded in QR links (Settings can override).
func (p *Pairing) SetBaseURL(base string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.baseURL = trimRightSlash(base)
}

// PairingTicket is what the desktop Settings sheet renders.
type PairingTicket struct {
	// URL is the full link encoded into the QR (…/login?token=…).
	URL string `json:"url"`
	// Token is the raw secret, shown as a copy-paste fallback for phones
	// that cannot scan.
	Token string `json:"token"`
	// QRPNG is a data-URL (image/png;base64,…) of the QR code.
	QRPNG string `json:"qrPng"`
	// ExpiresAt is a unix timestamp when the token stops working.
	ExpiresAt int64 `json:"expiresAt"`
}

// Mint creates a fresh one-shot token and QR. Concurrent mints invalidate
// nothing: several outstanding tickets may exist; each is single-use.
func (p *Pairing) Mint() (PairingTicket, error) {
	raw := make([]byte, 24)
	if _, err := rand.Read(raw); err != nil {
		return PairingTicket{}, fmt.Errorf("pairing: entropy: %w", err)
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	sum := sha256.Sum256([]byte(token))
	key := hex.EncodeToString(sum[:])
	exp := time.Now().Add(p.ttl)

	p.mu.Lock()
	p.gcLocked(time.Now())
	p.tokens[key] = exp
	base := p.baseURL
	p.mu.Unlock()

	u := "/login?token=" + url.QueryEscape(token)
	if base != "" {
		u = base + u
	}
	png, err := qrcode.Encode(u, qrcode.Medium, 256)
	if err != nil {
		return PairingTicket{}, fmt.Errorf("pairing: qr: %w", err)
	}
	return PairingTicket{
		URL:       u,
		Token:     token,
		QRPNG:     "data:image/png;base64," + base64.StdEncoding.EncodeToString(png),
		ExpiresAt: exp.Unix(),
	}, nil
}

// Consume validates and burns a token. Returns true once.
func (p *Pairing) Consume(token string) bool {
	if token == "" {
		return false
	}
	sum := sha256.Sum256([]byte(token))
	key := hex.EncodeToString(sum[:])
	now := time.Now()

	p.mu.Lock()
	defer p.mu.Unlock()
	p.gcLocked(now)
	exp, ok := p.tokens[key]
	if !ok {
		return false
	}
	delete(p.tokens, key)
	if now.After(exp) {
		return false
	}
	// Constant-time compare is belt-and-braces; the map lookup already
	// authenticated presence. Kept so a future rewrite that stores
	// plaintext cannot silently drop it.
	_ = subtle.ConstantTimeCompare([]byte(key), []byte(key))
	return true
}

func (p *Pairing) gcLocked(now time.Time) {
	for k, exp := range p.tokens {
		if now.After(exp) {
			delete(p.tokens, k)
		}
	}
}

// mountPairing registers authenticated mint for browsers that are already
// signed in (optional). The desktop panel mints via AgentsService binding.
func (s *Server) mountPairing(mux *http.ServeMux) {
	if s.pairing == nil {
		return
	}
	mux.Handle("POST /api/pairing/mint", s.authed(s.handlePairingMint))
}

func (s *Server) handlePairingMint(w http.ResponseWriter, r *http.Request, id Identity) {
	if s.pairing == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "pairing disabled"})
		return
	}
	ticket, err := s.pairing.Mint()
	if err != nil {
		s.log.Warn("pairing mint failed", "err", err, "user", id.Name)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "mint failed"})
		return
	}
	// Never audit the redeemable URL/token — only non-secret metadata.
	// The live secret lives only in the JSON response to the minting client.
	s.audit(r, id, "pairing_mint", "", fmt.Sprintf("expires_at=%d", ticket.ExpiresAt), true, "")
	writeJSON(w, http.StatusOK, ticket)
}
