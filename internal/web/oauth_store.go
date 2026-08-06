package web

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/LoneExile/merino/internal/app"
)

// errEnvLocked is returned when the UI tries to change a provider that is
// pinned by environment variables. A headless/k8s deployment configures OAuth
// through MERINO_*; a persisted file must never silently shadow that.
var errEnvLocked = errors.New("this provider is configured by environment variables and cannot be edited here")

// OAuthStore is the live, editable source of OAuth provider config.
//
// It backs the Settings UI: the providers and the login page read the current
// config through it on every request, so a change takes effect without a
// restart. Config comes from one of two places per provider:
//
//   - environment (MERINO_GITHUB_* / MERINO_OIDC_*) — wins, and is read-only
//     in the UI, so a file cannot shadow a deliberate deployment;
//   - oauth.json in the state dir — the UI-managed layer (0600, same trust
//     boundary as bootstrap-creds.json and optional-password.json).
//
// The redirect URL is not stored: it is derived from the public base URL, so
// there is one less field to get wrong and it always matches where the server
// actually is.
type OAuthStore struct {
	mu      sync.RWMutex
	path    string
	baseURL string

	fileGH   GitHubConfig
	fileOIDC OIDCConfig

	envGH    GitHubConfig
	envGHOn  bool
	envOIDC  OIDCConfig
	envOIDON bool
}

// oauthFile is the on-disk shape of oauth.json.
type oauthFile struct {
	GitHub GitHubConfig `json:"github"`
	OIDC   OIDCConfig   `json:"oidc"`
}

// NewOAuthStore loads oauth.json (if present) and pins any provider that has
// environment variables set. baseURL is the public origin used to derive
// redirect URLs; empty means OAuth cannot be enabled (no valid redirect).
func NewOAuthStore(dir, baseURL string) *OAuthStore {
	s := &OAuthStore{
		path:    filepath.Join(dir, "oauth.json"),
		baseURL: baseURL,
	}
	if envAnyGitHub() {
		s.envGH = GitHubFromEnv()
		s.envGHOn = true
	}
	if envAnyOIDC() {
		s.envOIDC = OIDCFromEnv()
		s.envOIDON = true
	}
	s.load()
	return s
}

func envAnyGitHub() bool {
	for _, k := range []string{"GITHUB_CLIENT_ID", "GITHUB_CLIENT_SECRET", "GITHUB_ALLOW", "GITHUB_ORG", "GITHUB_TEAM"} {
		if app.Env(k) != "" {
			return true
		}
	}
	return false
}

func envAnyOIDC() bool {
	for _, k := range []string{"OIDC_CLIENT_ID", "OIDC_CLIENT_SECRET", "OIDC_ISSUER", "OIDC_ALLOW_ROLE"} {
		if app.Env(k) != "" {
			return true
		}
	}
	return false
}

func (s *OAuthStore) load() {
	raw, err := os.ReadFile(s.path)
	if err != nil {
		return // missing file ⇒ nothing configured, which is fine
	}
	var f oauthFile
	if json.Unmarshal(raw, &f) != nil {
		return // unparseable ⇒ treat as empty rather than crash the dashboard
	}
	s.fileGH = f.GitHub
	s.fileOIDC = f.OIDC
}

func (s *OAuthStore) persistLocked() error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(oauthFile{GitHub: s.fileGH, OIDC: s.fileOIDC}, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.path, raw, 0o600)
}

func (s *OAuthStore) deriveRedirect(provider string) string {
	if s.baseURL == "" {
		return ""
	}
	return strings.TrimRight(s.baseURL, "/") + "/login/" + provider + "/callback"
}

// GitHub returns the effective GitHub config (env or file), with the redirect
// URL derived. It is a method value assigned to GitHubProvider.Config, so the
// provider reads live config on every request.
func (s *OAuthStore) GitHub() GitHubConfig {
	s.mu.RLock()
	defer s.mu.RUnlock()
	c := s.fileGH
	if s.envGHOn {
		c = s.envGH
	}
	if c.RedirectURL == "" {
		c.RedirectURL = s.deriveRedirect("github")
	}
	return c
}

// OIDC returns the effective OIDC config (env or file), redirect derived.
func (s *OAuthStore) OIDC() OIDCConfig {
	s.mu.RLock()
	defer s.mu.RUnlock()
	c := s.fileOIDC
	if s.envOIDON {
		c = s.envOIDC
	}
	if c.RedirectURL == "" {
		c.RedirectURL = s.deriveRedirect("oidc")
	}
	return c
}

// GitHubSettings is the editable, secret-in write shape from the UI.
type GitHubSettings struct {
	ClientID     string   `json:"clientID"`
	ClientSecret string   `json:"clientSecret"` // empty ⇒ keep the stored secret
	Allow        []string `json:"allow"`
	Org          string   `json:"org"`
	Team         string   `json:"team"`
	Label        string   `json:"label"`
}

// OIDCSettings is the editable, secret-in write shape from the UI.
type OIDCSettings struct {
	ClientID     string `json:"clientID"`
	ClientSecret string `json:"clientSecret"` // empty ⇒ keep the stored secret
	Issuer       string `json:"issuer"`
	AllowRole    string `json:"allowRole"`
	Label        string `json:"label"`
}

// SetGitHub persists a UI edit. An empty secret keeps the stored one, so the
// operator can change the allowlist without re-typing the secret.
func (s *OAuthStore) SetGitHub(in GitHubSettings) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.envGHOn {
		return errEnvLocked
	}
	secret := in.ClientSecret
	if secret == "" {
		secret = s.fileGH.ClientSecret
	}
	s.fileGH = GitHubConfig{
		ClientID:     strings.TrimSpace(in.ClientID),
		ClientSecret: secret,
		Allow:        cleanList(in.Allow),
		Org:          strings.TrimSpace(in.Org),
		Team:         strings.TrimSpace(in.Team),
		Label:        strings.TrimSpace(in.Label),
	}
	return s.persistLocked()
}

// SetOIDC persists a UI edit. Empty secret keeps the stored one.
func (s *OAuthStore) SetOIDC(in OIDCSettings) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.envOIDON {
		return errEnvLocked
	}
	secret := in.ClientSecret
	if secret == "" {
		secret = s.fileOIDC.ClientSecret
	}
	s.fileOIDC = OIDCConfig{
		ClientID:     strings.TrimSpace(in.ClientID),
		ClientSecret: secret,
		Issuer:       strings.TrimSpace(in.Issuer),
		AllowRole:    strings.TrimSpace(in.AllowRole),
		Label:        strings.TrimSpace(in.Label),
	}
	return s.persistLocked()
}

// ClearGitHub removes GitHub config from oauth.json.
func (s *OAuthStore) ClearGitHub() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.envGHOn {
		return errEnvLocked
	}
	s.fileGH = GitHubConfig{}
	return s.persistLocked()
}

// ClearOIDC removes OIDC config from oauth.json.
func (s *OAuthStore) ClearOIDC() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.envOIDON {
		return errEnvLocked
	}
	s.fileOIDC = OIDCConfig{}
	return s.persistLocked()
}

func cleanList(in []string) []string {
	out := make([]string, 0, len(in))
	for _, v := range in {
		if v = strings.TrimSpace(v); v != "" {
			out = append(out, v)
		}
	}
	return out
}

// OAuthProviderStatus is the non-secret view of one provider for the UI. The
// client secret is NEVER included — only whether one is stored.
type OAuthProviderStatus struct {
	Configured  bool     `json:"configured"` // live-enabled ⇒ a button shows
	EnvLocked   bool     `json:"envLocked"`
	ClientID    string   `json:"clientID"`
	HasSecret   bool     `json:"hasSecret"`
	Allow       []string `json:"allow,omitempty"`
	Org         string   `json:"org,omitempty"`
	Team        string   `json:"team,omitempty"`
	Issuer      string   `json:"issuer,omitempty"`
	AllowRole   string   `json:"allowRole,omitempty"`
	Label       string   `json:"label"`
	RedirectURL string   `json:"redirectURL"`
}

// OAuthStatus is the whole Settings view: both providers plus whether a public
// URL exists at all (without one, no provider can be enabled).
type OAuthStatus struct {
	PublicURLSet bool                `json:"publicUrlSet"`
	GitHub       OAuthProviderStatus `json:"github"`
	OIDC         OAuthProviderStatus `json:"oidc"`
}

// Status returns the secret-free view for the Settings UI.
func (s *OAuthStore) Status() OAuthStatus {
	gh := s.GitHub()
	oi := s.OIDC()
	s.mu.RLock()
	defer s.mu.RUnlock()
	return OAuthStatus{
		PublicURLSet: s.baseURL != "",
		GitHub: OAuthProviderStatus{
			Configured:  gh.Enabled(),
			EnvLocked:   s.envGHOn,
			ClientID:    gh.ClientID,
			HasSecret:   gh.ClientSecret != "",
			Allow:       gh.Allow,
			Org:         gh.Org,
			Team:        gh.Team,
			Label:       gh.LoginLabel(),
			RedirectURL: gh.RedirectURL,
		},
		OIDC: OAuthProviderStatus{
			Configured:  oi.Enabled(),
			EnvLocked:   s.envOIDON,
			ClientID:    oi.ClientID,
			HasSecret:   oi.ClientSecret != "",
			Issuer:      oi.Issuer,
			AllowRole:   oi.AllowRole,
			Label:       oi.LoginLabel(),
			RedirectURL: oi.RedirectURL,
		},
	}
}

// Buttons returns the live "Sign in with …" entries for the login page — one
// per provider that is currently enabled. Read on every render, so an edit in
// Settings shows or hides a button without a restart.
func (s *OAuthStore) Buttons() []OAuthButton {
	var b []OAuthButton
	if gh := s.GitHub(); gh.Enabled() {
		b = append(b, OAuthButton{Label: gh.LoginLabel(), Path: "/login/github"})
	}
	if oi := s.OIDC(); oi.Enabled() {
		b = append(b, OAuthButton{Label: oi.LoginLabel(), Path: "/login/oidc"})
	}
	return b
}
