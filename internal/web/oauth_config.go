package web

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/LoneExile/merino/internal/app"
)

// OIDCConfig enables the optional OAuth rung of the identity ladder.
//
// Empty ClientID keeps the provider inert (no routes that surprise operators).
// Wired only when a public HTTPS origin exists — OAuth redirect URIs on plain
// LAN HTTP are a footgun most IdPs refuse anyway.
type OIDCConfig struct {
	ClientID     string
	ClientSecret string
	Issuer       string // e.g. https://accounts.google.com
	// RedirectURL must match the IdP app registration exactly.
	RedirectURL string
	// AllowRole is the realm or client role a token must carry to be let in.
	// Empty denies everyone: OAuth proves who someone is, it does not decide
	// who may drive this herd. Every provider ends at the same SingleOperator
	// policy, so the allowlist IS the authorization — fail closed.
	AllowRole string
	// Label names the IdP on the login page, e.g. "Sign in with Acme SSO".
	Label string
}

// OIDCFromEnv reads optional OAuth settings. All empty ⇒ disabled.
func OIDCFromEnv() OIDCConfig {
	return OIDCConfig{
		ClientID:     app.Env("OIDC_CLIENT_ID"),
		ClientSecret: app.Env("OIDC_CLIENT_SECRET"),
		Issuer:       app.Env("OIDC_ISSUER"),
		RedirectURL:  app.Env("OIDC_REDIRECT_URL"),
		AllowRole:    app.Env("OIDC_ALLOW_ROLE"),
		Label:        app.Env("OIDC_LABEL"),
	}
}

// Enabled is true when enough config exists to attempt a real flow.
//
// Secret and AllowRole are deliberately required: a confidential client
// without its secret cannot exchange codes, and a provider without an
// allow-role would authenticate anyone who can reach the IdP.
func (c OIDCConfig) Enabled() bool {
	return c.ClientID != "" && c.ClientSecret != "" && c.Issuer != "" &&
		c.RedirectURL != "" && c.AllowRole != ""
}

// LoginLabel returns the IdP name shown on the login page.
func (c OIDCConfig) LoginLabel() string {
	if c.Label != "" {
		return c.Label
	}
	return "Keycloak"
}

// GitHubConfig enables GitHub OAuth login.
//
// GitHub is OAuth2, not OIDC: there is no issuer/discovery and no ID token.
// Authorization is decided here, at the door — Allow is an explicit login
// allowlist, Org/Team admits members of a GitHub org (optionally one team).
// Empty Allow AND empty Org means nobody is admitted; the provider stays
// inert rather than opening the herd to every GitHub account.
type GitHubConfig struct {
	ClientID     string
	ClientSecret string
	// RedirectURL must match the GitHub OAuth app registration exactly.
	RedirectURL string
	// Allow lists GitHub logins that may sign in.
	Allow []string
	// Org, when set, admits members of this organization. GitHub's
	// /user/orgs endpoint only reports PUBLIC memberships, so the member
	// check uses the org membership endpoint instead.
	Org string
	// Team, when set with Org, restricts admission to members of that team.
	Team string
	// Label names the provider on the login page.
	Label string
}

// GitHubFromEnv reads GitHub OAuth settings from the environment.
func GitHubFromEnv() GitHubConfig {
	allow := splitEnvList(app.Env("GITHUB_ALLOW"))
	return GitHubConfig{
		ClientID:     app.Env("GITHUB_CLIENT_ID"),
		ClientSecret: app.Env("GITHUB_CLIENT_SECRET"),
		RedirectURL:  app.Env("GITHUB_REDIRECT_URL"),
		Allow:        allow,
		Org:          app.Env("GITHUB_ORG"),
		Team:         app.Env("GITHUB_TEAM"),
		Label:        app.Env("GITHUB_LABEL"),
	}
}

// splitEnvList parses a comma/space-separated env value into a set.
func splitEnvList(s string) []string {
	fields := strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ' ' })
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		if f = strings.TrimSpace(f); f != "" {
			out = append(out, f)
		}
	}
	return out
}

// Enabled is true when the provider is fully configured AND at least one
// admission rule is set. Credentials without an allowlist would be a door
// that admits everyone — the provider stays off instead.
func (c GitHubConfig) Enabled() bool {
	return c.ClientID != "" && c.ClientSecret != "" && c.RedirectURL != "" &&
		(len(c.Allow) > 0 || c.Org != "")
}

// LoginLabel returns the provider name shown on the login page.
func (c GitHubConfig) LoginLabel() string {
	if c.Label != "" {
		return c.Label
	}
	return "GitHub"
}

// oauthState protects an authorization-code exchange against CSRF and replay.
//
// The value is a signed cookie holding everything the callback needs:
//   - state:     random nonce, echoed back by the IdP on callback
//   - verifier:  PKCE code_verifier (OIDC only; GitHub confidential clients
//     authenticate with their secret instead)
//   - nonce:     OIDC nonce bound into the ID token
//
// The whole blob is signed with the per-process session key, so a forged
// cookie is rejected before any IdP round-trip. Same expiry discipline as
// sessions: an exchange that did not finish in ten minutes is abandoned.
type oauthState struct {
	State    string `json:"s"`
	Verifier string `json:"v,omitempty"`
	Nonce    string `json:"n,omitempty"`
}

const (
	oauthStateCookie = "herdr_oauth"
	// oauthStateTTL bounds the authorize→callback window. Long enough for a
	// human to finish at the IdP, short enough that a leaked cookie cannot
	// be replayed for long. Ten minutes matches the IdP authorization codes.
	oauthStateTTL = 10 * time.Minute
)

// newOAuthState mints random state (+ optional PKCE verifier and OIDC nonce).
func newOAuthState(withPKCE, withNonce bool) (oauthState, string, error) {
	st := oauthState{}
	raw, err := randomString(32)
	if err != nil {
		return st, "", err
	}
	st.State = raw
	if withPKCE {
		// RFC 7636: 43-128 chars of unreserved characters. 64 bytes of
		// base64url is a valid verifier and a comfortable margin below the
		// 128-char ceiling.
		v, err := randomString(64)
		if err != nil {
			return st, "", err
		}
		st.Verifier = v
	}
	if withNonce {
		n, err := randomString(24)
		if err != nil {
			return st, "", err
		}
		st.Nonce = n
	}
	return st, pkceChallenge(st.Verifier), nil
}

// pkceChallenge derives the S256 code_challenge for a verifier.
func pkceChallenge(verifier string) string {
	if verifier == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// randomString returns n random bytes as base64url, for state/nonce/verifier.
func randomString(n int) (string, error) {
	raw := make([]byte, n)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

// setOAuthStateCookie writes the signed state cookie for the authorize flow.
func setOAuthStateCookie(w http.ResponseWriter, s *Sessions, st oauthState) error {
	payload, err := json.Marshal(st)
	if err != nil {
		return err
	}
	s.writeOAuthCookie(w, payload)
	return nil
}

// readOAuthState validates and returns the state cookie, or nil.
func readOAuthState(r *http.Request, s *Sessions) *oauthState {
	c, err := r.Cookie(oauthStateCookie)
	if err != nil {
		return nil
	}
	payload, ok := s.readOAuthCookie(c.Value)
	if !ok {
		return nil
	}
	var st oauthState
	if json.Unmarshal(payload, &st) != nil || st.State == "" {
		return nil
	}
	return &st
}

// clearOAuthStateCookie deletes the state cookie after a callback.
func clearOAuthStateCookie(w http.ResponseWriter, s *Sessions) {
	http.SetCookie(w, &http.Cookie{
		Name:     oauthStateCookie,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   s.secure,
		SameSite: http.SameSiteLaxMode,
	})
}

// writeOAuthCookie / readOAuthCookie piggyback the session signing key. The
// OAuth state is not a session; it is a short-lived bearer that must not be
// forgeable, and Sessions already owns the per-process HMAC key. The value
// uses the same `payload~sig` shape as the session cookie, so both share one
// constant-time verification convention.
func (s *Sessions) writeOAuthCookie(w http.ResponseWriter, payload []byte) {
	http.SetCookie(w, &http.Cookie{
		Name:     oauthStateCookie,
		Value:    b64(string(payload)) + "~" + s.sign(string(payload)),
		Path:     "/",
		MaxAge:   int(oauthStateTTL.Seconds()),
		HttpOnly: true,
		Secure:   s.secure,
		SameSite: http.SameSiteLaxMode,
	})
}

func (s *Sessions) readOAuthCookie(value string) ([]byte, bool) {
	payload, sig, ok := strings.Cut(value, "~")
	if !ok {
		return nil, false
	}
	raw, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil {
		return nil, false
	}
	// Constant-time, same discipline as ReadSession: a timing oracle here
	// would leak the HMAC byte by byte.
	if subtle.ConstantTimeCompare([]byte(sig), []byte(s.sign(string(raw)))) != 1 {
		return nil, false
	}
	return raw, true
}

// fetchJSON does an authenticated GET and decodes the JSON body, with a
// bounded response size so a hostile endpoint cannot balloon memory.
func fetchJSON(ctx context.Context, client *http.Client, url, token string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("%s: %s: %s", url, resp.Status, strings.TrimSpace(string(body)))
	}
	dec := json.NewDecoder(io.LimitReader(resp.Body, 1<<20))
	if err := dec.Decode(out); err != nil {
		return err
	}
	return nil
}

// errDenied is returned when an authenticated identity is not on the
// allowlist. It maps to a friendly login-page message rather than a bare 500.
var errDenied = errors.New("sign-in succeeded but this account is not allowed to access this herd")

// OAuthProvider is a login rung mounted alongside the primary provider.
// Unlike Provider, Mount receives the session store directly: OAuth flows
// protect their authorize→callback exchange with a signed state cookie that
// only Sessions can sign.
type OAuthProvider interface {
	// Name identifies the provider in logs and session cookies.
	Name() string
	// LoginPath is where the login page's button points.
	LoginPath() string
	// LoginLabel is the human name on the button.
	LoginLabel() string
	// Mount registers the authorize + callback routes.
	Mount(mux *http.ServeMux, sessions *Sessions, success func(http.ResponseWriter, *http.Request, Identity))
}

// OAuthButton is one "Sign in with …" entry rendered on the login page.
type OAuthButton struct {
	Label string
	Path  string
}
