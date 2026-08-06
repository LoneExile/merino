package web

import (
	"context"
	"log/slog"
	"net/http"
	"net/url"
	"strings"

	"golang.org/x/oauth2"
)

// GitHubProvider is the GitHub rung of the identity ladder.
//
// GitHub is OAuth2, not OIDC: there is no discovery document and no ID token,
// so the identity is fetched from the /user endpoint after the code exchange.
// Authorization is decided here, at the door: the login must be listed in
// Allow, OR be a member of Org (optionally of Team within Org). GitHub's
// /user/orgs endpoint only reports PUBLIC memberships, so team checks use the
// org membership endpoint with the Bearer token instead.
type GitHubProvider struct {
	// Config returns the live config on every request (backed by OAuthStore),
	// so a Settings edit takes effect without a restart. Cfg is the static
	// fallback used by tests.
	Config func() GitHubConfig
	Cfg    GitHubConfig
	Log    *slog.Logger
	HTTP   *http.Client // overridable for tests

	// authURL/tokenURL/apiBase override the GitHub endpoints. Production
	// defaults to github.com; tests point them at an httptest server.
	authURL    string
	tokenURL   string
	apiBaseURL string
}

func (p *GitHubProvider) Name() string      { return "github" }
func (p *GitHubProvider) LoginPath() string { return "/login/github" }
func (p *GitHubProvider) LoginLabel() string {
	return p.cfg().LoginLabel()
}

// cfg returns the live config, falling back to the static Cfg for tests.
func (p *GitHubProvider) cfg() GitHubConfig {
	if p.Config != nil {
		return p.Config()
	}
	return p.Cfg
}

// Enabled reports whether a login button should show for this provider.
func (p *GitHubProvider) Enabled() bool { return p.cfg().Enabled() }

func (p *GitHubProvider) oauth() oauth2.Config {
	authURL, tokenURL := p.authURL, p.tokenURL
	if authURL == "" {
		authURL = "https://github.com/login/oauth/authorize"
	}
	if tokenURL == "" {
		tokenURL = "https://github.com/login/oauth/access_token"
	}
	return oauth2.Config{
		ClientID:     p.cfg().ClientID,
		ClientSecret: p.cfg().ClientSecret,
		Endpoint: oauth2.Endpoint{
			AuthURL:  authURL,
			TokenURL: tokenURL,
			// GitHub's token endpoint accepts credentials in the POST body,
			// not Basic auth. Without this, x/oauth2's auto-detect sends no
			// client_secret at all and every exchange fails.
			AuthStyle: oauth2.AuthStyleInParams,
		},
		RedirectURL: p.cfg().RedirectURL,
		// read:user proves identity; read:org is required for the team
		// membership check (org membership for a single named org can also be
		// checked with read:org). A login without org/team configured needs
		// only the explicit allowlist and never asks for org scopes.
		Scopes: []string{"read:user", "read:org"},
	}
}

// apiBase returns the GitHub REST base, overridable in tests.
func (p *GitHubProvider) apiBase() string {
	if p.apiBaseURL != "" {
		return p.apiBaseURL
	}
	return "https://api.github.com"
}

func (p *GitHubProvider) Mount(mux *http.ServeMux, sessions *Sessions, success func(http.ResponseWriter, *http.Request, Identity)) {
	mux.HandleFunc("GET /login/github", p.handleAuthorize(sessions, success))
	mux.HandleFunc("GET /login/github/callback", p.handleCallback(sessions, success))
}

func (p *GitHubProvider) handleAuthorize(sessions *Sessions, success func(http.ResponseWriter, *http.Request, Identity)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !p.cfg().Enabled() {
			http.Error(w, "GitHub login is not configured. Set MERINO_GITHUB_* and a public HTTPS URL.", http.StatusNotImplemented)
			return
		}
		cfg := p.oauth()
		st, _, err := newOAuthState(false, false) // confidential client, no PKCE/nonce
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		s := sessions
		if err := setOAuthStateCookie(w, s, st); err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		http.Redirect(w, r, cfg.AuthCodeURL(st.State), http.StatusFound)
	}
}

func (p *GitHubProvider) handleCallback(sessions *Sessions, success func(http.ResponseWriter, *http.Request, Identity)) http.HandlerFunc {
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
			http.Error(w, "GitHub refused the login: "+r.URL.Query().Get("error"), http.StatusBadRequest)
			return
		}
		code := r.URL.Query().Get("code")
		if code == "" {
			http.Error(w, "missing authorization code", http.StatusBadRequest)
			return
		}

		ctx := r.Context()
		httpc := p.HTTP
		if httpc == nil {
			httpc = http.DefaultClient
		}
		cfg := p.oauth()
		tok, err := cfg.Exchange(ctx, code)
		if err != nil {
			p.Log.Warn("github token exchange failed", "err", err)
			http.Error(w, "token exchange failed", http.StatusBadGateway)
			return
		}

		var user struct {
			Login string `json:"login"`
			Name  string `json:"name"`
			Email string `json:"email"`
		}
		if err := fetchJSON(ctx, httpc, p.apiBase()+"/user", tok.AccessToken, &user); err != nil {
			p.Log.Warn("github /user failed", "err", err)
			http.Error(w, "cannot fetch GitHub identity", http.StatusBadGateway)
			return
		}
		if user.Login == "" {
			http.Error(w, "GitHub returned an empty login", http.StatusBadGateway)
			return
		}

		allowed := false
		for _, a := range p.cfg().Allow {
			if strings.EqualFold(a, user.Login) {
				allowed = true
				break
			}
		}
		if !allowed && p.cfg().Org != "" {
			member, err := p.orgAllows(ctx, httpc, tok.AccessToken, user.Login)
			if err != nil {
				p.Log.Warn("github org check failed", "login", user.Login, "org", p.cfg().Org, "err", err)
				// Fail closed on a membership-check error: a transient API
				// failure must not open the door wider than configured.
				denied(w)
				return
			}
			allowed = member
		}
		if !allowed {
			p.Log.Warn("github login denied", "login", user.Login)
			denied(w)
			return
		}

		name := user.Name
		if name == "" {
			name = user.Login
		}
		success(w, r, Identity{
			Subject:  user.Login,
			Name:     name,
			Provider: p.Name(),
		})
	}
}

// orgAllows reports whether login is a member of the configured org, and of
// the configured team when one is set. Uses the org membership endpoint
// (https://api.github.com/orgs/{org}/members/{user}) which honors private
// memberships, rather than /user/orgs which lists only public ones.
func (p *GitHubProvider) orgAllows(ctx context.Context, httpc *http.Client, token, login string) (bool, error) {
	base := p.apiBase()
	memberURL := base + "/orgs/" + url.PathEscape(p.cfg().Org) + "/members/" + url.PathEscape(login)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, memberURL, nil)
	if err != nil {
		return false, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	// Explicitly ask for the JSON API rather than the default text/vnd diff.
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	resp, err := httpc.Do(req)
	if err != nil {
		return false, err
	}
	resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusNoContent:
		// member
	case http.StatusNotFound, http.StatusForbidden:
		// not a member (or membership is private and not visible)
		return false, nil
	default:
		return false, &githubAPIError{status: resp.Status}
	}
	if p.cfg().Team == "" {
		return true, nil
	}
	teamURL := base + "/orgs/" + url.PathEscape(p.cfg().Org) + "/teams/" + url.PathEscape(p.cfg().Team) + "/memberships/" + url.PathEscape(login)
	req2, err := http.NewRequestWithContext(ctx, http.MethodGet, teamURL, nil)
	if err != nil {
		return false, err
	}
	req2.Header.Set("Authorization", "Bearer "+token)
	req2.Header.Set("Accept", "application/vnd.github+json")
	req2.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	resp2, err := httpc.Do(req2)
	if err != nil {
		return false, err
	}
	resp2.Body.Close()
	switch resp2.StatusCode {
	case http.StatusOK:
		return true, nil
	case http.StatusNotFound, http.StatusForbidden:
		return false, nil
	default:
		return false, &githubAPIError{status: resp2.Status}
	}
}

type githubAPIError struct{ status string }

func (e *githubAPIError) Error() string { return "github api: " + e.status }
