package web

import (
	"encoding/json"
	"errors"
	"net/http"
)

// OAuthStatus returns the secret-free Settings view. Empty when no store.
func (s *Server) OAuthStatus() OAuthStatus {
	if s.cfg.OAuthStore == nil {
		return OAuthStatus{}
	}
	return s.cfg.OAuthStore.Status()
}

// SetOAuthGitHub applies and persists a GitHub config edit (live, no restart).
func (s *Server) SetOAuthGitHub(in GitHubSettings) error {
	if s.cfg.OAuthStore == nil {
		return errors.New("OAuth is not available on this server")
	}
	return s.cfg.OAuthStore.SetGitHub(in)
}

// SetOAuthOIDC applies and persists a Keycloak/OIDC config edit.
func (s *Server) SetOAuthOIDC(in OIDCSettings) error {
	if s.cfg.OAuthStore == nil {
		return errors.New("OAuth is not available on this server")
	}
	return s.cfg.OAuthStore.SetOIDC(in)
}

// ClearOAuth removes a provider's stored config.
func (s *Server) ClearOAuth(provider string) error {
	if s.cfg.OAuthStore == nil {
		return errors.New("OAuth is not available on this server")
	}
	switch provider {
	case "github":
		return s.cfg.OAuthStore.ClearGitHub()
	case "oidc":
		return s.cfg.OAuthStore.ClearOIDC()
	default:
		return errors.New("unknown provider")
	}
}

// mountOAuthAdmin registers the operator-gated config endpoints. Paired phones
// (device:*) are refused by isOperator, matching the password-login toggle.
func (s *Server) mountOAuthAdmin(mux *http.ServeMux) {
	if s.cfg.OAuthStore == nil {
		return
	}
	mux.Handle("GET /api/auth/oauth", s.authed(s.handleOAuthStatus))
	mux.Handle("POST /api/auth/oauth/github", s.authed(s.handleSetOAuthGitHub))
	mux.Handle("POST /api/auth/oauth/oidc", s.authed(s.handleSetOAuthOIDC))
	mux.Handle("POST /api/auth/oauth/github/clear", s.authed(s.handleClearOAuthGitHub))
	mux.Handle("POST /api/auth/oauth/oidc/clear", s.authed(s.handleClearOAuthOIDC))
}

func (s *Server) handleOAuthStatus(w http.ResponseWriter, r *http.Request, id Identity) {
	if !isOperator(id) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "only an operator can view sign-in settings"})
		return
	}
	writeJSON(w, http.StatusOK, s.OAuthStatus())
}

func (s *Server) handleSetOAuthGitHub(w http.ResponseWriter, r *http.Request, id Identity) {
	if !isOperator(id) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "only an operator can change sign-in settings"})
		return
	}
	var in GitHubSettings
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&in); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "malformed request"})
		return
	}
	if err := s.SetOAuthGitHub(in); err != nil {
		s.oauthWriteErr(w, r, id, "github", err)
		return
	}
	s.audit(r, id, "oauth_config_set", "github", "", true, "")
	writeJSON(w, http.StatusOK, s.OAuthStatus())
}

func (s *Server) handleSetOAuthOIDC(w http.ResponseWriter, r *http.Request, id Identity) {
	if !isOperator(id) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "only an operator can change sign-in settings"})
		return
	}
	var in OIDCSettings
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&in); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "malformed request"})
		return
	}
	if err := s.SetOAuthOIDC(in); err != nil {
		s.oauthWriteErr(w, r, id, "oidc", err)
		return
	}
	s.audit(r, id, "oauth_config_set", "oidc", "", true, "")
	writeJSON(w, http.StatusOK, s.OAuthStatus())
}

func (s *Server) handleClearOAuthGitHub(w http.ResponseWriter, r *http.Request, id Identity) {
	s.clearOAuth(w, r, id, "github")
}

func (s *Server) handleClearOAuthOIDC(w http.ResponseWriter, r *http.Request, id Identity) {
	s.clearOAuth(w, r, id, "oidc")
}

func (s *Server) clearOAuth(w http.ResponseWriter, r *http.Request, id Identity, provider string) {
	if !isOperator(id) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "only an operator can change sign-in settings"})
		return
	}
	if err := s.ClearOAuth(provider); err != nil {
		s.oauthWriteErr(w, r, id, provider, err)
		return
	}
	s.audit(r, id, "oauth_config_clear", provider, "", true, "")
	writeJSON(w, http.StatusOK, s.OAuthStatus())
}

// oauthWriteErr maps an env-locked write to 409 (a deliberate refusal the UI
// explains) and anything else to 500, auditing the refusal either way.
func (s *Server) oauthWriteErr(w http.ResponseWriter, r *http.Request, id Identity, provider string, err error) {
	code := http.StatusInternalServerError
	if errors.Is(err, errLocked) {
		code = http.StatusConflict
	}
	s.audit(r, id, "oauth_config_set", provider, "", false, err.Error())
	writeJSON(w, code, map[string]string{"error": err.Error()})
}
