package web

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

// The login page is rendered by the password provider, but the buttons come
// from Config.OAuth via New(). A change that decouples those two (e.g. New
// stops calling SetOAuthButtons, or the template drops the range) must break
// this test — otherwise the wiring is only ever tested by the unit tests of
// the two halves separately.
func TestLoginPageRendersConfiguredOAuthButtons(t *testing.T) {
	gh := &GitHubProvider{Cfg: GitHubConfig{ClientID: "cid", ClientSecret: "s", RedirectURL: "https://x/cb", Allow: []string{"lex"}}}
	oidc := &OIDCProvider{Cfg: OIDCConfig{ClientID: "cid", ClientSecret: "s", Issuer: "https://idp", RedirectURL: "https://x/cb", AllowRole: "r"}}

	s, err := New(&fakeSource{}, Config{
		Provider: testProvider("alice", "correct-horse"),
		OAuth:    []OAuthProvider{gh, oidc},
		Policy:   SingleOperator{},
		Assets:   fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("<head></head>")}},
		Logger:   slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatal(err)
	}
	pp := s.cfg.Provider.(*PasswordProvider)
	if len(pp.OAuthButtons()) != 2 {
		t.Fatalf("password provider holds %d buttons, want 2", len(pp.OAuthButtons()))
	}

	req := httptest.NewRequest(http.MethodGet, "/login", nil)
	rr := httptest.NewRecorder()
	s.routes().ServeHTTP(rr, req)
	body := rr.Body.String()
	for _, want := range []string{`href="/login/github"`, `href="/login/oidc"`, "Sign in with GitHub", "Sign in with Keycloak"} {
		if !strings.Contains(body, want) {
			t.Errorf("login page missing %q", want)
		}
	}
	// And no provider → no buttons at all, not even an empty divider.
	s2, err := New(&fakeSource{}, Config{
		Provider: testProvider("alice", "correct-horse"),
		Policy:   SingleOperator{},
		Assets:   fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("<head></head>")}},
		Logger:   slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatal(err)
	}
	rr2 := httptest.NewRecorder()
	s2.routes().ServeHTTP(rr2, httptest.NewRequest(http.MethodGet, "/login", nil))
	if strings.Contains(rr2.Body.String(), "single sign-on") {
		t.Error("login page must not show the SSO divider when no OAuth provider is configured")
	}
}
