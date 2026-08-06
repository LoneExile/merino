package web

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

// clearOAuthEnv removes any ambient MERINO_/HERDR_TUNNEL_ OAuth vars so a
// developer's real environment cannot env-lock the store mid-test.
func clearOAuthEnv(t *testing.T) {
	t.Helper()
	keys := []string{
		"GITHUB_CLIENT_ID", "GITHUB_CLIENT_SECRET", "GITHUB_ALLOW", "GITHUB_ORG", "GITHUB_TEAM",
		"OIDC_CLIENT_ID", "OIDC_CLIENT_SECRET", "OIDC_ISSUER", "OIDC_ALLOW_ROLE",
	}
	for _, k := range keys {
		t.Setenv("MERINO_"+k, "")
		t.Setenv("HERDR_TUNNEL_"+k, "")
	}
}

// The login page renders one button per LIVE-enabled provider, sourced from
// the OAuthStore. This proves the wiring end to end AND that an edit takes
// effect without a new server: the same running server shows no buttons, then
// both, after the store is edited.
func TestLoginPageRendersConfiguredOAuthButtons(t *testing.T) {
	clearOAuthEnv(t)
	store := NewOAuthStore(t.TempDir(), "https://merino.example", OAuthConfigLayer{})

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
	routes := s.routes()

	// Nothing configured yet → no SSO divider, no buttons.
	if body := getLogin(t, routes); strings.Contains(body, "single sign-on") {
		t.Fatal("no provider configured, but the login page shows the SSO section")
	}

	// Configure both — the SAME running server must now render both buttons.
	if err := store.SetGitHub(GitHubSettings{ClientID: "cid", ClientSecret: "s", Allow: []string{"lex"}}); err != nil {
		t.Fatal(err)
	}
	if err := store.SetOIDC(OIDCSettings{ClientID: "cid", ClientSecret: "s", Issuer: "https://idp", AllowRole: "herd-admin"}); err != nil {
		t.Fatal(err)
	}
	body := getLogin(t, routes)
	for _, want := range []string{`href="/login/github"`, `href="/login/oidc"`, "Sign in with GitHub", "Sign in with Keycloak"} {
		if !strings.Contains(body, want) {
			t.Errorf("after enabling both providers, login page missing %q", want)
		}
	}
}

// Without a public URL, no redirect can be derived, so no provider can enable
// even when fully credentialed — the buttons stay hidden.
func TestLoginPageHidesButtonsWithoutPublicURL(t *testing.T) {
	clearOAuthEnv(t)
	store := NewOAuthStore(t.TempDir(), "", OAuthConfigLayer{}) // no public origin
	if err := store.SetGitHub(GitHubSettings{ClientID: "cid", ClientSecret: "s", Allow: []string{"lex"}}); err != nil {
		t.Fatal(err)
	}
	s, err := New(&fakeSource{}, Config{
		Provider:   testProvider("alice", "correct-horse"),
		OAuth:      []OAuthProvider{&GitHubProvider{Config: store.GitHub}},
		OAuthStore: store,
		Policy:     SingleOperator{},
		Assets:     fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("<head></head>")}},
		Logger:     slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatal(err)
	}
	if body := getLogin(t, s.routes()); strings.Contains(body, "/login/github") {
		t.Fatal("provider credentialed but no public URL — button must stay hidden")
	}
}

func getLogin(t *testing.T, h http.Handler) string {
	t.Helper()
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/login", nil))
	return rr.Body.String()
}
