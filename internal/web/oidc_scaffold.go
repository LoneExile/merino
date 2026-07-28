package web

import (
	"net/http"

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
}

// OIDCFromEnv reads optional OAuth settings. All empty ⇒ disabled.
func OIDCFromEnv() OIDCConfig {
	return OIDCConfig{
		ClientID:     app.Env("OIDC_CLIENT_ID"),
		ClientSecret: app.Env("OIDC_CLIENT_SECRET"),
		Issuer:       app.Env("OIDC_ISSUER"),
		RedirectURL:  app.Env("OIDC_REDIRECT_URL"),
	}
}

// Enabled is true when enough config exists to attempt a real flow later.
func (c OIDCConfig) Enabled() bool {
	return c.ClientID != "" && c.Issuer != "" && c.RedirectURL != ""
}

// OIDCProvider is a ship-scaffold: advertises the OAuth rung and mounts a
// clear "not configured / not implemented" surface so Settings can link to it
// without lying that Google login works today.
//
// Full authorization-code + PKCE lands in a follow-up; the ladder and device
// store do not depend on it.
type OIDCProvider struct {
	Cfg OIDCConfig
}

func (p *OIDCProvider) Name() string      { return "oidc" }
func (p *OIDCProvider) LoginPath() string { return "/login/oidc" }

// Mount registers placeholder routes. success is unused until the real flow lands.
func (p *OIDCProvider) Mount(mux *http.ServeMux, success func(http.ResponseWriter, *http.Request, Identity)) {
	_ = success
	mux.HandleFunc("GET /login/oidc", func(w http.ResponseWriter, r *http.Request) {
		if !p.Cfg.Enabled() {
			http.Error(w, "OAuth login is not configured. Set MERINO_OIDC_* (or legacy HERDR_TUNNEL_OIDC_*) and a public HTTPS URL.", http.StatusNotImplemented)
			return
		}
		// Real redirect to the IdP belongs here.
		http.Error(w, "OAuth login is configured but the authorization-code flow is not implemented yet.", http.StatusNotImplemented)
	})
	mux.HandleFunc("GET /login/oidc/callback", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "OAuth callback not implemented yet.", http.StatusNotImplemented)
	})
}
