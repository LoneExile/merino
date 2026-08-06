package web

import (
	"context"
	"log/slog"
	"net/http"

	oidc "github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

// OIDCProvider is the Keycloak (generic OIDC) rung of the identity ladder.
//
// Flow: /login/oidc mints state+PKCE+nonce (signed cookie), redirects to the
// IdP; the callback validates the state cookie, exchanges the code with the
// IdP, verifies the ID token (signature, issuer, audience, nonce) and lets
// the identity through only when it carries AllowRole. Roles land in
// Identity.Roles so a later multi-user swap to RequireRole is a one-liner.
type OIDCProvider struct {
	Cfg  OIDCConfig
	Log  *slog.Logger
	HTTP *http.Client // overridable for tests
}

func (p *OIDCProvider) Name() string      { return "oidc" }
func (p *OIDCProvider) LoginPath() string { return "/login/oidc" }
func (p *OIDCProvider) LoginLabel() string {
	return p.Cfg.LoginLabel()
}

// Mount registers the authorize and callback routes. sessions owns the
// signed state cookie that protects the exchange.
func (p *OIDCProvider) Mount(mux *http.ServeMux, sessions *Sessions, success func(http.ResponseWriter, *http.Request, Identity)) {
	mux.HandleFunc("GET /login/oidc", p.handleAuthorize(sessions, success))
	mux.HandleFunc("GET /login/oidc/callback", p.handleCallback(sessions, success))
}

// oidcEndpoints lazily resolves the IdP discovery document and builds the
// OAuth2 config. The provider is created per-request (not stored) so tests
// can point HTTP at a fake issuer.
func (p *OIDCProvider) oauth(ctx context.Context) (*oidc.Provider, oauth2.Config, error) {
	client := p.HTTP
	if client == nil {
		client = http.DefaultClient
	}
	ctx = oidc.ClientContext(ctx, client)
	prov, err := oidc.NewProvider(ctx, p.Cfg.Issuer)
	if err != nil {
		return nil, oauth2.Config{}, err
	}
	cfg := oauth2.Config{
		ClientID:     p.Cfg.ClientID,
		ClientSecret: p.Cfg.ClientSecret,
		Endpoint:     prov.Endpoint(),
		RedirectURL:  p.Cfg.RedirectURL,
		Scopes:       []string{oidc.ScopeOpenID, "profile", "email"},
	}
	return prov, cfg, nil
}

func (p *OIDCProvider) handleAuthorize(sessions *Sessions, success func(http.ResponseWriter, *http.Request, Identity)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !p.Cfg.Enabled() {
			http.Error(w, "OAuth login is not configured. Set MERINO_OIDC_* and a public HTTPS URL.", http.StatusNotImplemented)
			return
		}
		ctx := r.Context()
		prov, cfg, err := p.oauth(ctx)
		if err != nil {
			p.Log.Warn("oidc discovery failed", "issuer", p.Cfg.Issuer, "err", err)
			http.Error(w, "OIDC discovery failed. Check MERINO_OIDC_ISSUER.", http.StatusBadGateway)
			return
		}
		_ = prov

		st, challenge, err := newOAuthState(true, true) // PKCE + nonce
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		if err := setOAuthStateCookie(w, sessions, st); err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}

		authURL := cfg.AuthCodeURL(st.State,
			oauth2.SetAuthURLParam("code_challenge", challenge),
			oauth2.SetAuthURLParam("code_challenge_method", "S256"),
			oauth2.SetAuthURLParam("nonce", st.Nonce),
		)
		http.Redirect(w, r, authURL, http.StatusFound)
	}
}

func (p *OIDCProvider) handleCallback(sessions *Sessions, success func(http.ResponseWriter, *http.Request, Identity)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		s := sessions
		st := readOAuthState(r, s)
		clearOAuthStateCookie(w, s)
		if st == nil {
			http.Error(w, "missing or invalid OAuth state", http.StatusBadRequest)
			return
		}
		if got := r.URL.Query().Get("state"); got != st.State {
			http.Error(w, "state mismatch", http.StatusBadRequest)
			return
		}
		if r.URL.Query().Get("error") != "" {
			http.Error(w, "identity provider refused the login: "+r.URL.Query().Get("error"), http.StatusBadRequest)
			return
		}
		code := r.URL.Query().Get("code")
		if code == "" {
			http.Error(w, "missing authorization code", http.StatusBadRequest)
			return
		}

		ctx := r.Context()
		prov, cfg, err := p.oauth(ctx)
		if err != nil {
			p.Log.Warn("oidc discovery failed", "issuer", p.Cfg.Issuer, "err", err)
			http.Error(w, "OIDC discovery failed.", http.StatusBadGateway)
			return
		}
		tok, err := cfg.Exchange(ctx, code,
			oauth2.SetAuthURLParam("code_verifier", st.Verifier),
		)
		if err != nil {
			p.Log.Warn("oidc token exchange failed", "err", err)
			http.Error(w, "token exchange failed", http.StatusBadGateway)
			return
		}

		rawIDToken, ok := tok.Extra("id_token").(string)
		if !ok || rawIDToken == "" {
			http.Error(w, "no id_token in token response", http.StatusBadGateway)
			return
		}
		idTok, err := prov.Verifier(&oidc.Config{ClientID: p.Cfg.ClientID}).Verify(ctx, rawIDToken)
		if err != nil {
			p.Log.Warn("oidc id_token verification failed", "err", err)
			http.Error(w, "id_token verification failed", http.StatusBadGateway)
			return
		}
		// go-oidc verifies signature/issuer/audience/expiry but NOT the nonce:
		// binding the token to THIS authorize request is the caller's job.
		// Without it, a token minted for another login could be replayed here.
		if idTok.Nonce != st.Nonce {
			p.Log.Warn("oidc nonce mismatch", "subject", idTok.Subject)
			http.Error(w, "nonce mismatch", http.StatusBadRequest)
			return
		}
		var claims struct {
			Sub   string   `json:"sub"`
			Email string   `json:"email"`
			Name  string   `json:"name"`
			Roles []string `json:"roles"` // some providers put roles top-level
			Realm struct {
				Roles []string `json:"roles"`
			} `json:"realm_access"`
			Resource map[string]struct {
				Roles []string `json:"roles"`
			} `json:"resource_access"`
		}
		if err := idTok.Claims(&claims); err != nil {
			http.Error(w, "cannot decode claims", http.StatusBadGateway)
			return
		}
		// Realm roles and client roles both count: Keycloak may grant the
		// allow-role at either scope, and the operator's choice of realm vs
		// client role is a Keycloak-side decision, not one to re-litigate in
		// code. Client roles are collected from the client this app is
		// registered as.
		roles := append([]string(nil), claims.Roles...)
		roles = append(roles, claims.Realm.Roles...)
		if cr, ok := claims.Resource[p.Cfg.ClientID]; ok {
			roles = append(roles, cr.Roles...)
		}
		if !hasRole(Identity{Roles: roles}, p.Cfg.AllowRole) {
			p.Log.Warn("oidc login denied", "subject", claims.Sub, "want_role", p.Cfg.AllowRole)
			denied(w)
			return
		}
		name := claims.Name
		if name == "" {
			name = claims.Email
		}
		if name == "" {
			name = claims.Sub
		}
		success(w, r, Identity{
			Subject:  claims.Sub,
			Name:     name,
			Provider: p.Name(),
			Roles:    roles,
		})
	}
}

// denied renders the shared "not allowed" page.
func denied(w http.ResponseWriter) {
	http.Error(w, "You signed in successfully, but this account is not allowed to access this herd.", http.StatusForbidden)
}

// provider with the sessions context — see server.go routes().
