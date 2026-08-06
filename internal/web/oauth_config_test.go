package web

import (
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// --- Enabled() fail-closed tables -----------------------------------------

func TestOIDCConfigEnabledRequiresEveryPiece(t *testing.T) {
	base := OIDCConfig{
		ClientID:     "cid",
		ClientSecret: "secret",
		Issuer:       "https://idp.example",
		RedirectURL:  "https://merino.example/login/oidc/callback",
		AllowRole:    "herd-admin",
	}
	if !base.Enabled() {
		t.Fatal("fully configured OIDC config must be enabled")
	}
	// Each single missing piece must disable the provider: a door that opens
	// with a piece missing is a door that opens by accident.
	missing := []struct {
		name   string
		mutate func(*OIDCConfig)
	}{
		{"client id", func(c *OIDCConfig) { c.ClientID = "" }},
		{"client secret", func(c *OIDCConfig) { c.ClientSecret = "" }},
		{"issuer", func(c *OIDCConfig) { c.Issuer = "" }},
		{"redirect", func(c *OIDCConfig) { c.RedirectURL = "" }},
		{"allow role", func(c *OIDCConfig) { c.AllowRole = "" }},
	}
	for _, m := range missing {
		c := base
		m.mutate(&c)
		if c.Enabled() {
			t.Errorf("OIDC config missing %s must be disabled — otherwise the login page shows a door that admits everyone", m.name)
		}
	}
}

func TestGitHubConfigEnabledRequiresAllowlist(t *testing.T) {
	base := GitHubConfig{
		ClientID:     "cid",
		ClientSecret: "secret",
		RedirectURL:  "https://merino.example/login/github/callback",
	}
	// Credentials WITHOUT an admission rule must stay off: a GitHub button
	// that admits every GitHub account is the exact hole this feature exists
	// to avoid.
	if base.Enabled() {
		t.Fatal("GitHub config with no allow rule must be disabled")
	}
	base.Allow = []string{"lex"}
	if !base.Enabled() {
		t.Fatal("GitHub config with an explicit allowlist must be enabled")
	}
	base.Allow = nil
	base.Org = "acme"
	if !base.Enabled() {
		t.Fatal("GitHub config with an org admission rule must be enabled")
	}
	base.Org = ""
	if base.Enabled() {
		t.Fatal("GitHub config with no allowlist and no org must be disabled")
	}
}

func TestSplitEnvList(t *testing.T) {
	if got := splitEnvList("  lex,  ada  , bob "); strings.Join(got, "|") != "lex|ada|bob" {
		t.Fatalf("splitEnvList = %v", got)
	}
	if got := splitEnvList(""); len(got) != 0 {
		t.Fatalf("empty list must yield none, got %v", got)
	}
}

// --- OAuth state cookie ----------------------------------------------------

func TestOAuthStateCookieRoundTrip(t *testing.T) {
	sess, err := NewSessions(false)
	if err != nil {
		t.Fatal(err)
	}
	st, _, err := newOAuthState(true, true)
	if err != nil {
		t.Fatal(err)
	}

	rr := httptest.NewRecorder()
	if err := setOAuthStateCookie(rr, sess, st); err != nil {
		t.Fatal(err)
	}
	c := rr.Result().Cookies()
	if len(c) != 1 {
		t.Fatalf("want 1 cookie, got %d", len(c))
	}
	req := httptest.NewRequest(http.MethodGet, "/login/oidc/callback", nil)
	req.AddCookie(c[0])
	got := readOAuthState(req, sess)
	if got == nil {
		t.Fatal("a valid state cookie must read back")
	}
	if got.State != st.State || got.Verifier != st.Verifier || got.Nonce != st.Nonce {
		t.Fatalf("round trip = %+v, want %+v", got, st)
	}
}

func TestOAuthStateCookieRejectsTampering(t *testing.T) {
	sess, err := NewSessions(false)
	if err != nil {
		t.Fatal(err)
	}
	st, _, err := newOAuthState(false, false)
	if err != nil {
		t.Fatal(err)
	}
	rr := httptest.NewRecorder()
	if err := setOAuthStateCookie(rr, sess, st); err != nil {
		t.Fatal(err)
	}
	c := rr.Result().Cookies()[0]

	// Flip one character of the state payload; the signature must no longer
	// verify and the callback must reject the exchange.
	payload, sig, ok := strings.Cut(c.Value, "~")
	if !ok {
		t.Fatal("cookie must use payload~sig shape")
	}
	raw, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil {
		t.Fatal(err)
	}
	if raw[0] == 'A' {
		raw[0] = 'B'
	} else {
		raw[0] = 'A'
	}
	tampered := base64.RawURLEncoding.EncodeToString(raw) + "~" + sig

	req := httptest.NewRequest(http.MethodGet, "/login/oidc/callback", nil)
	req.AddCookie(&http.Cookie{Name: oauthStateCookie, Value: tampered})
	if got := readOAuthState(req, sess); got != nil {
		t.Fatal("a tampered state cookie must be rejected — otherwise CSRF state is forgeable")
	}
}

func TestOAuthStateCookieRejectsForgedSignature(t *testing.T) {
	sess, err := NewSessions(false)
	if err != nil {
		t.Fatal(err)
	}
	// A cookie signed by a DIFFERENT server process (fresh key) must be
	// rejected: sessions are per-process, so a stolen state cookie is dead
	// across restarts, exactly like a session cookie.
	other, err := NewSessions(false)
	if err != nil {
		t.Fatal(err)
	}
	st, _, err := newOAuthState(false, false)
	if err != nil {
		t.Fatal(err)
	}
	rr := httptest.NewRecorder()
	if err := setOAuthStateCookie(rr, other, st); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/login/oidc/callback", nil)
	req.AddCookie(rr.Result().Cookies()[0])
	if got := readOAuthState(req, sess); got != nil {
		t.Fatal("a cookie signed by another process must be rejected")
	}
}

func TestPKCEChallengeDerivation(t *testing.T) {
	v := "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"
	want := "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM"
	if got := pkceChallenge(v); got != want {
		t.Fatalf("RFC 7636 appendix B vector: got %q want %q", got, want)
	}
}
