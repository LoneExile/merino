package web

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/LoneExile/merino/internal/app"
)

// errLocked is returned when the UI tries to change a provider that is pinned
// by a higher-precedence source (environment or config.yml). Those sources are
// how a headless/GitOps deployment configures OAuth; a UI-written file must
// never silently shadow them. The wrapped message names the source.
var errLocked = errors.New("this provider is configured elsewhere and cannot be edited here")

// OAuth config precedence, highest first:
//
//	environment (MERINO_*)  >  config.yml  >  oauth.json (the Settings UI)
//
// Each layer is anchored on its client ID: a source "owns" a provider when it
// supplies a client ID, and the two higher layers make the UI read-only so a
// file cannot shadow a deliberate deployment. The client secret for the
// config.yml layer never lives in config.yml — it is resolved (from a secret
// file or env) before it reaches this store.
const (
	sourceEnv      = "environment"
	sourceConfig   = "config.yml"
	sourceSettings = "settings"
)

// OAuthConfigLayer is the config.yml-derived layer, resolved by the boot path
// (which reads the secret file). Set is true when config.yml owns the provider.
type OAuthConfigLayer struct {
	GitHub    GitHubConfig
	GitHubSet bool
	OIDC      OIDCConfig
	OIDCSet   bool
}

// OAuthStore is the live, editable source of OAuth provider config. Providers
// and the login page read it on every request, so a change takes effect
// without a restart. The redirect URL is derived from the public base rather
// than stored, so it always matches where the server actually is.
type OAuthStore struct {
	mu      sync.RWMutex
	path    string
	baseURL string

	// oauth.json (UI-managed, editable)
	fileGH   GitHubConfig
	fileOIDC OIDCConfig

	// config.yml layer (resolved by boot)
	cfgGH    GitHubConfig
	cfgGHOn  bool
	cfgOIDC  OIDCConfig
	cfgOIDON bool

	// environment layer
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

// NewOAuthStore loads oauth.json and layers config.yml (via layer) and the
// environment on top. baseURL is the public origin used to derive redirect
// URLs; empty means OAuth cannot be enabled (no valid redirect).
func NewOAuthStore(dir, baseURL string, layer OAuthConfigLayer) *OAuthStore {
	s := &OAuthStore{
		path:     filepath.Join(dir, "oauth.json"),
		baseURL:  baseURL,
		cfgGH:    layer.GitHub,
		cfgGHOn:  layer.GitHubSet,
		cfgOIDC:  layer.OIDC,
		cfgOIDON: layer.OIDCSet,
	}
	// Env anchors on the client ID: a stray MERINO_GITHUB_ALLOW without an ID
	// is incomplete, not an intent to pin.
	if app.Env("GITHUB_CLIENT_ID") != "" {
		s.envGH = GitHubFromEnv()
		s.envGHOn = true
	}
	if app.Env("OIDC_CLIENT_ID") != "" {
		s.envOIDC = OIDCFromEnv()
		s.envOIDON = true
	}
	s.load()
	return s
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

// sourceGitHub / lockedGitHub report which layer owns GitHub. Caller holds the
// read lock.
func (s *OAuthStore) sourceGitHub() string {
	switch {
	case s.envGHOn:
		return sourceEnv
	case s.cfgGHOn:
		return sourceConfig
	default:
		return sourceSettings
	}
}

func (s *OAuthStore) sourceOIDC() string {
	switch {
	case s.envOIDON:
		return sourceEnv
	case s.cfgOIDON:
		return sourceConfig
	default:
		return sourceSettings
	}
}

// GitHub returns the effective GitHub config (env > config.yml > file), with
// the redirect URL derived. Assigned to GitHubProvider.Config as a method
// value, so the provider reads live config on every request.
func (s *OAuthStore) GitHub() GitHubConfig {
	s.mu.RLock()
	defer s.mu.RUnlock()
	c := s.fileGH
	switch {
	case s.envGHOn:
		c = s.envGH
	case s.cfgGHOn:
		c = s.cfgGH
	}
	if c.RedirectURL == "" {
		c.RedirectURL = s.deriveRedirect("github")
	}
	return c
}

// OIDC returns the effective OIDC config (env > config.yml > file).
func (s *OAuthStore) OIDC() OIDCConfig {
	s.mu.RLock()
	defer s.mu.RUnlock()
	c := s.fileOIDC
	switch {
	case s.envOIDON:
		c = s.envOIDC
	case s.cfgOIDON:
		c = s.cfgOIDC
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

func lockedErr(source string) error {
	return fmt.Errorf("%w (set by %s)", errLocked, source)
}

// SetGitHub persists a UI edit. An empty secret keeps the stored one, so the
// operator can change the allowlist without re-typing the secret.
func (s *OAuthStore) SetGitHub(in GitHubSettings) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.envGHOn || s.cfgGHOn {
		return lockedErr(s.sourceGitHub())
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
	if s.envOIDON || s.cfgOIDON {
		return lockedErr(s.sourceOIDC())
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
	if s.envGHOn || s.cfgGHOn {
		return lockedErr(s.sourceGitHub())
	}
	s.fileGH = GitHubConfig{}
	return s.persistLocked()
}

// ClearOIDC removes OIDC config from oauth.json.
func (s *OAuthStore) ClearOIDC() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.envOIDON || s.cfgOIDON {
		return lockedErr(s.sourceOIDC())
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
	Configured bool `json:"configured"` // live-enabled ⇒ a button shows
	// Locked ⇒ read-only in the UI (env or config.yml owns it).
	Locked bool `json:"locked"`
	// Source is "environment", "config.yml" or "settings".
	Source      string   `json:"source"`
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
			Locked:      s.envGHOn || s.cfgGHOn,
			Source:      s.sourceGitHub(),
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
			Locked:      s.envOIDON || s.cfgOIDON,
			Source:      s.sourceOIDC(),
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
// per provider that is currently enabled.
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
