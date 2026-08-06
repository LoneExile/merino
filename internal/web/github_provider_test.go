package web

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// fakeGitHub serves the token + user + org/team endpoints the GitHub provider
// calls. apiHandler routes on path: /user, /orgs/<o>/members/<u>,
// /orgs/<o>/teams/<t>/memberships/<u>.
func fakeGitHub(t *testing.T, tokenHandler, apiHandler http.HandlerFunc) (*httptest.Server, *GitHubProvider) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/login/oauth/access_token" {
			tokenHandler(w, r)
			return
		}
		apiHandler(w, r)
	}))
	t.Cleanup(srv.Close)

	return srv, &GitHubProvider{
		Cfg: GitHubConfig{
			ClientID:     "cid",
			ClientSecret: "secret",
			RedirectURL:  srv.URL + "/login/github/callback",
			Allow:        []string{"lex"},
			Org:          "acme",
			Team:         "platform",
		},
		Log:        testLogger(),
		HTTP:       srv.Client(),
		authURL:    srv.URL + "/login/oauth/authorize",
		tokenURL:   srv.URL + "/login/oauth/access_token",
		apiBaseURL: srv.URL,
	}
}

func testLogger() *slog.Logger { return slog.New(slog.DiscardHandler) }

// githubFlow runs authorize then callback, returning the final recorder and
// the identity the success callback received (zero Identity if denied).
func githubFlow(t *testing.T, prov *GitHubProvider, code string) (*httptest.ResponseRecorder, Identity) {
	t.Helper()
	sess, err := NewSessions(false)
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	var got Identity
	prov.Mount(mux, sess, func(w http.ResponseWriter, r *http.Request, id Identity) {
		got = id
		http.Redirect(w, r, "/", http.StatusFound)
	})

	ar := httptest.NewRecorder()
	mux.ServeHTTP(ar, httptest.NewRequest(http.MethodGet, "/login/github", nil))
	if ar.Code != http.StatusFound {
		t.Fatalf("authorize: got %d, want 302", ar.Code)
	}
	cookies := ar.Result().Cookies()
	if len(cookies) == 0 {
		t.Fatal("authorize must set the state cookie")
	}
	loc := ar.Header().Get("Location")
	i := strings.Index(loc, "state=")
	if i < 0 {
		t.Fatalf("authorize redirect has no state: %s", loc)
	}
	state := loc[i+len("state="):]

	cb := httptest.NewRecorder()
	cbReq := httptest.NewRequest(http.MethodGet, "/login/github/callback?code="+code+"&state="+state, nil)
	cbReq.AddCookie(cookies[0])
	mux.ServeHTTP(cb, cbReq)
	return cb, got
}

func TestGitHubFlowAllowsListedLogin(t *testing.T) {
	_, prov := fakeGitHub(t,
		func(w http.ResponseWriter, r *http.Request) {
			if r.FormValue("client_secret") != "secret" {
				t.Error("token exchange must authenticate with the client secret")
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]string{"access_token": "tok-1", "token_type": "bearer"})
		},
		func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/user" {
				t.Fatalf("api handler got %s", r.URL.Path)
			}
			if got := r.Header.Get("Authorization"); got != "Bearer tok-1" {
				t.Errorf("user fetch missing bearer, got %q", got)
			}
			_ = json.NewEncoder(w).Encode(map[string]string{"login": "lex", "name": "Lex Luthor"})
		},
	)
	rr, id := githubFlow(t, prov, "code-1")
	if rr.Code != http.StatusFound {
		t.Fatalf("callback: got %d, want 302 (session issued)", rr.Code)
	}
	if id.Subject != "lex" || id.Name != "Lex Luthor" || id.Provider != "github" {
		t.Fatalf("identity = %+v, want github/lex", id)
	}
}

func TestGitHubFlowDeniesUnlistedLogin(t *testing.T) {
	_, prov := fakeGitHub(t,
		func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]string{"access_token": "tok-1", "token_type": "bearer"})
		},
		func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/user":
				_ = json.NewEncoder(w).Encode(map[string]string{"login": "mallory"})
			case "/orgs/acme/members/mallory":
				w.WriteHeader(http.StatusNotFound) // not a member
			default:
				t.Fatalf("unexpected api path %s", r.URL.Path)
			}
		},
	)
	rr, _ := githubFlow(t, prov, "code-1")
	if rr.Code != http.StatusForbidden {
		t.Fatalf("unlisted login with no org membership: got %d, want 403", rr.Code)
	}
}

func TestGitHubFlowAllowsOrgMember(t *testing.T) {
	_, prov := fakeGitHub(t,
		func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]string{"access_token": "tok-1", "token_type": "bearer"})
		},
		func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/user":
				_ = json.NewEncoder(w).Encode(map[string]string{"login": "ada"})
			case "/orgs/acme/members/ada":
				w.WriteHeader(http.StatusNoContent) // member
			case "/orgs/acme/teams/platform/memberships/ada":
				w.WriteHeader(http.StatusOK) // and in the team
			default:
				t.Fatalf("unexpected api path %s", r.URL.Path)
			}
		},
	)
	prov.Cfg.Allow = nil // org/team is the only admission rule
	rr, id := githubFlow(t, prov, "code-1")
	if rr.Code != http.StatusFound {
		t.Fatalf("org+team member: got %d, want 302", rr.Code)
	}
	if id.Subject != "ada" {
		t.Fatalf("identity = %+v, want ada", id)
	}
}

func TestGitHubFlowDeniesOrgMemberOutsideTeam(t *testing.T) {
	_, prov := fakeGitHub(t,
		func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]string{"access_token": "tok-1", "token_type": "bearer"})
		},
		func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/user":
				_ = json.NewEncoder(w).Encode(map[string]string{"login": "bob"})
			case "/orgs/acme/members/bob":
				w.WriteHeader(http.StatusNoContent) // org member
			case "/orgs/acme/teams/platform/memberships/bob":
				w.WriteHeader(http.StatusNotFound) // but not on the team
			default:
				t.Fatalf("unexpected api path %s", r.URL.Path)
			}
		},
	)
	prov.Cfg.Allow = nil
	rr, _ := githubFlow(t, prov, "code-1")
	if rr.Code != http.StatusForbidden {
		t.Fatalf("org member outside the team: got %d, want 403", rr.Code)
	}
}

// A transient GitHub API failure must fail CLOSED: the user is denied rather
// than admitted because the membership check could not run.
func TestGitHubFlowFailsClosedOnAPIFailure(t *testing.T) {
	_, prov := fakeGitHub(t,
		func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]string{"access_token": "tok-1", "token_type": "bearer"})
		},
		func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/user":
				_ = json.NewEncoder(w).Encode(map[string]string{"login": "mallory"})
			default:
				w.WriteHeader(http.StatusInternalServerError)
			}
		},
	)
	rr, _ := githubFlow(t, prov, "code-1")
	if rr.Code != http.StatusForbidden {
		t.Fatalf("membership-check failure must deny, got %d, want 403", rr.Code)
	}
}

func TestGitHubFlowRejectsTamperedState(t *testing.T) {
	sess, err := NewSessions(false)
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	prov := &GitHubProvider{Cfg: GitHubConfig{ClientID: "cid", ClientSecret: "secret", Allow: []string{"lex"}}}
	prov.Mount(mux, sess, func(w http.ResponseWriter, r *http.Request, id Identity) {
		t.Error("success callback must not run for a tampered state")
	})

	// Callback with NO state cookie at all.
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/login/github/callback?code=c&state=x", nil))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("callback without state cookie: got %d, want 400", rr.Code)
	}
}
