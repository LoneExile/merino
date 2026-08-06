package web

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

func TestOAuthStorePersistRoundTrip(t *testing.T) {
	clearOAuthEnv(t)
	dir := t.TempDir()
	store := NewOAuthStore(dir, "https://merino.example")

	if err := store.SetGitHub(GitHubSettings{ClientID: "cid", ClientSecret: "shh", Allow: []string{"lex", " ada "}, Label: "Acme GH"}); err != nil {
		t.Fatal(err)
	}
	// A reloaded store sees the same config (persisted to oauth.json).
	reloaded := NewOAuthStore(dir, "https://merino.example")
	gh := reloaded.GitHub()
	if gh.ClientID != "cid" || gh.ClientSecret != "shh" {
		t.Fatalf("reloaded github = %+v", gh)
	}
	if strings.Join(gh.Allow, ",") != "lex,ada" {
		t.Fatalf("allowlist not trimmed/persisted: %v", gh.Allow)
	}
	if gh.RedirectURL != "https://merino.example/login/github/callback" {
		t.Fatalf("redirect not derived: %q", gh.RedirectURL)
	}
	if !gh.Enabled() {
		t.Fatal("configured github must be enabled")
	}
}

// Editing the allowlist without re-supplying the secret must keep the secret.
func TestOAuthStoreKeepsSecretOnEmpty(t *testing.T) {
	clearOAuthEnv(t)
	store := NewOAuthStore(t.TempDir(), "https://merino.example")
	if err := store.SetGitHub(GitHubSettings{ClientID: "cid", ClientSecret: "shh", Allow: []string{"lex"}}); err != nil {
		t.Fatal(err)
	}
	if err := store.SetGitHub(GitHubSettings{ClientID: "cid", ClientSecret: "", Allow: []string{"lex", "bob"}}); err != nil {
		t.Fatal(err)
	}
	if got := store.GitHub().ClientSecret; got != "shh" {
		t.Fatalf("empty secret must keep the stored one, got %q", got)
	}
}

// Env-configured providers are read-only: a UI write must be refused so a file
// cannot shadow a deployment's environment.
func TestOAuthStoreEnvLocked(t *testing.T) {
	clearOAuthEnv(t)
	t.Setenv("MERINO_GITHUB_CLIENT_ID", "env-cid")
	t.Setenv("MERINO_GITHUB_CLIENT_SECRET", "env-secret")
	t.Setenv("MERINO_GITHUB_ALLOW", "envuser")
	store := NewOAuthStore(t.TempDir(), "https://merino.example")

	if !store.Status().GitHub.EnvLocked {
		t.Fatal("github configured via env must report EnvLocked")
	}
	if err := store.SetGitHub(GitHubSettings{ClientID: "x", ClientSecret: "y", Allow: []string{"z"}}); err == nil {
		t.Fatal("editing an env-locked provider must be refused")
	}
	if store.GitHub().ClientID != "env-cid" {
		t.Fatal("env config must be untouched after a refused edit")
	}
}

// Status is the client-facing view and must NEVER carry the client secret.
func TestOAuthStatusNeverLeaksSecret(t *testing.T) {
	clearOAuthEnv(t)
	store := NewOAuthStore(t.TempDir(), "https://merino.example")
	_ = store.SetOIDC(OIDCSettings{ClientID: "cid", ClientSecret: "TOP-SECRET", Issuer: "https://idp", AllowRole: "herd-admin"})

	raw, err := json.Marshal(store.Status())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "TOP-SECRET") {
		t.Fatalf("status serialized the client secret: %s", raw)
	}
	if !store.Status().OIDC.HasSecret {
		t.Fatal("status must report that a secret is set (without revealing it)")
	}
}

// --- endpoint gate ---------------------------------------------------------

func oauthAdminServer(t *testing.T, store *OAuthStore) *Server {
	t.Helper()
	s, err := New(&fakeSource{}, Config{
		Provider:   testProvider("alice", "correct-horse"),
		OAuth:      []OAuthProvider{&GitHubProvider{Config: store.GitHub}, &OIDCProvider{Config: store.OIDC}},
		OAuthStore: store,
		Policy:     SingleOperator{},
		Assets:     fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("<head></head>")}},
		Logger:     slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// A paired phone (device:*) is not an operator and must be refused, even
// though it holds a valid session — reconfiguring who can log in is not a
// phone's job.
func TestOAuthAdminRejectsPairedDevice(t *testing.T) {
	clearOAuthEnv(t)
	store := NewOAuthStore(t.TempDir(), "https://merino.example")
	srv := oauthAdminServer(t, store)
	routes := srv.routes()

	rec := httptest.NewRecorder()
	srv.sessions.Issue(rec, Identity{Subject: "device:phone1", Name: "iPhone", Provider: "pairing"})
	cookie := rec.Result().Cookies()[0]

	body := strings.NewReader(`{"clientID":"x","clientSecret":"y","allow":["z"]}`)
	req := httptest.NewRequest(http.MethodPost, "/api/auth/oauth/github", body)
	req.AddCookie(cookie)
	rr := httptest.NewRecorder()
	routes.ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("paired device: got %d, want 403", rr.Code)
	}
	if store.GitHub().ClientID != "" {
		t.Fatal("a refused write must not touch the config")
	}
}

func TestOAuthAdminOperatorCanConfigure(t *testing.T) {
	clearOAuthEnv(t)
	store := NewOAuthStore(t.TempDir(), "https://merino.example")
	srv := oauthAdminServer(t, store)
	routes := srv.routes()

	rec := httptest.NewRecorder()
	srv.sessions.Issue(rec, Identity{Subject: "alice", Name: "alice", Provider: "password"})
	cookie := rec.Result().Cookies()[0]

	body := strings.NewReader(`{"clientID":"cid","clientSecret":"shh","allow":["lex"]}`)
	req := httptest.NewRequest(http.MethodPost, "/api/auth/oauth/github", body)
	req.AddCookie(cookie)
	rr := httptest.NewRecorder()
	routes.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("operator configure: got %d, want 200: %s", rr.Code, rr.Body.String())
	}
	if !store.GitHub().Enabled() {
		t.Fatal("github must be enabled after operator configured it")
	}
	// The 200 body is a status view and must not echo the secret.
	if strings.Contains(rr.Body.String(), "shh") {
		t.Fatalf("response leaked the secret: %s", rr.Body.String())
	}
}
