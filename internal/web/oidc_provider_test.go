package web

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v4"
)

// fakeIssuer is a minimal OIDC identity provider: discovery document, JWKS,
// and a token endpoint that mints signed RS256 ID tokens on demand.
type fakeIssuer struct {
	srv  *httptest.Server
	priv *rsa.PrivateKey

	// Set per callback to control what the minted token carries.
	nonce        string
	realmRoles   []string
	lastVerifier string
	lastCode     string
}

func newFakeIssuer(t *testing.T) *fakeIssuer {
	t.Helper()
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeIssuer{priv: priv}
	f.srv = httptest.NewServer(http.HandlerFunc(f.handle))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeIssuer) handle(w http.ResponseWriter, r *http.Request) {
	writeJSON := func(v any) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(v)
	}
	switch r.URL.Path {
	case "/.well-known/openid-configuration":
		writeJSON(map[string]any{
			"issuer":                                f.srv.URL,
			"authorization_endpoint":                f.srv.URL + "/authorize",
			"token_endpoint":                        f.srv.URL + "/token",
			"jwks_uri":                              f.srv.URL + "/jwks",
			"id_token_signing_alg_values_supported": []string{"RS256"},
			"response_types_supported":              []string{"code"},
			"subject_types_supported":               []string{"public"},
		})
	case "/jwks":
		jwk := jose.JSONWebKey{Key: &f.priv.PublicKey, KeyID: "test-key", Algorithm: "RS256", Use: "sig"}
		writeJSON(map[string]any{"keys": []jose.JSONWebKey{jwk}})
	case "/token":
		// The provider must send the PKCE verifier it minted at authorize.
		f.lastVerifier = r.FormValue("code_verifier")
		f.lastCode = r.FormValue("code")
		if r.FormValue("client_secret") != "secret" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		idToken := f.mintIDToken()
		writeJSON(map[string]any{
			"access_token": "at-1",
			"token_type":   "Bearer",
			"expires_in":   300,
			"id_token":     idToken,
		})
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func (f *fakeIssuer) mintIDToken() string {
	claims := map[string]any{
		"iss":   f.srv.URL,
		"sub":   "keycloak-user-1",
		"aud":   "merino-client",
		"exp":   time.Now().Add(5 * time.Minute).Unix(),
		"iat":   time.Now().Unix(),
		"name":  "Ada Lovelace",
		"email": "ada@example.com",
	}
	if f.nonce != "" {
		claims["nonce"] = f.nonce
	}
	if len(f.realmRoles) > 0 {
		claims["realm_access"] = map[string]any{"roles": f.realmRoles}
	}
	payload, err := json.Marshal(claims)
	if err != nil {
		panic(err)
	}
	signer, err := jose.NewSigner(
		jose.SigningKey{Algorithm: jose.RS256, Key: f.priv},
		(&jose.SignerOptions{}).WithType("JWT").WithHeader("kid", "test-key"),
	)
	if err != nil {
		panic(err)
	}
	jws, err := signer.Sign(payload)
	if err != nil {
		panic(err)
	}
	out, err := jws.CompactSerialize()
	if err != nil {
		panic(err)
	}
	return out
}

// oidcProviderFor builds a provider pointed at the fake issuer.
func oidcProviderFor(t *testing.T, iss *fakeIssuer) *OIDCProvider {
	t.Helper()
	return &OIDCProvider{
		Cfg: OIDCConfig{
			ClientID:     "merino-client",
			ClientSecret: "secret",
			Issuer:       iss.srv.URL,
			RedirectURL:  iss.srv.URL + "/login/oidc/callback",
			AllowRole:    "herd-admin",
		},
		Log:  testLogger(),
		HTTP: iss.srv.Client(),
	}
}

// oidcFlow runs authorize then callback. It reads the state cookie so the
// token it gets back is minted with the right nonce, and asserts the PKCE
// verifier the provider sent matches the one it minted.
func oidcFlow(t *testing.T, prov *OIDCProvider, iss *fakeIssuer) (*httptest.ResponseRecorder, Identity) {
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

	// Authorize: must redirect with state, nonce and a PKCE challenge.
	ar := httptest.NewRecorder()
	mux.ServeHTTP(ar, httptest.NewRequest(http.MethodGet, "/login/oidc", nil))
	if ar.Code != http.StatusFound {
		t.Fatalf("authorize: got %d, want 302", ar.Code)
	}
	loc := ar.Header().Get("Location")
	for _, want := range []string{"state=", "nonce=", "code_challenge=", "code_challenge_method=S256"} {
		if !strings.Contains(loc, want) {
			t.Fatalf("authorize URL missing %s: %s", want, loc)
		}
	}
	cookies := ar.Result().Cookies()
	if len(cookies) == 0 {
		t.Fatal("authorize must set the state cookie")
	}
	st := decodeOAuthStateCookie(t, cookies[0].Value)
	state := loc[strings.Index(loc, "state=")+len("state="):]
	if state != st.State {
		t.Fatalf("redirect state %q != cookie state %q — the callback must compare them", state, st.State)
	}

	// Mint the token for the nonce the provider generated.
	iss.nonce = st.Nonce

	cb := httptest.NewRecorder()
	cbReq := httptest.NewRequest(http.MethodGet, "/login/oidc/callback?code=code-1&state="+state, nil)
	cbReq.AddCookie(cookies[0])
	mux.ServeHTTP(cb, cbReq)

	if iss.lastVerifier == "" {
		t.Error("token endpoint never received a code_verifier — PKCE must be wired")
	} else if iss.lastVerifier != st.Verifier {
		t.Errorf("code_verifier = %q, want %q (the one minted at authorize)", iss.lastVerifier, st.Verifier)
	}
	return cb, got
}

// decodeOAuthStateCookie reads the state cookie without the signature (the
// signature check is covered by oauth_config_test).
func decodeOAuthStateCookie(t *testing.T, value string) oauthState {
	t.Helper()
	payload, _, ok := strings.Cut(value, "~")
	if !ok {
		t.Fatal("state cookie missing signature separator")
	}
	raw, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil {
		t.Fatal(err)
	}
	var st oauthState
	if err := json.Unmarshal(raw, &st); err != nil {
		t.Fatal(err)
	}
	return st
}

func TestOIDCFlowAllowsRole(t *testing.T) {
	iss := newFakeIssuer(t)
	prov := oidcProviderFor(t, iss)
	iss.realmRoles = []string{"herd-admin", "viewer"}

	rr, id := oidcFlow(t, prov, iss)
	if rr.Code != http.StatusFound {
		t.Fatalf("callback: got %d, want 302 (session issued)", rr.Code)
	}
	if id.Subject != "keycloak-user-1" || id.Name != "Ada Lovelace" || id.Provider != "oidc" {
		t.Fatalf("identity = %+v", id)
	}
	if !hasRole(id, "herd-admin") {
		t.Fatalf("roles = %v, want herd-admin carried", id.Roles)
	}
}

func TestOIDCFlowDeniesWithoutRole(t *testing.T) {
	iss := newFakeIssuer(t)
	prov := oidcProviderFor(t, iss)
	iss.realmRoles = nil

	rr, _ := oidcFlow(t, prov, iss)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("token without the allow role: got %d, want 403", rr.Code)
	}
}

func TestOIDCFlowDeniesWrongRole(t *testing.T) {
	iss := newFakeIssuer(t)
	prov := oidcProviderFor(t, iss)
	iss.realmRoles = []string{"viewer"}

	rr, _ := oidcFlow(t, prov, iss)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("wrong role: got %d, want 403", rr.Code)
	}
}

// The nonce binds the ID token to THIS authorize request. A token minted for
// a different login (nonce mismatch) must be rejected even when it carries
// the right role.
func TestOIDCFlowRejectsNonceMismatch(t *testing.T) {
	iss := newFakeIssuer(t)
	prov := oidcProviderFor(t, iss)
	iss.realmRoles = []string{"herd-admin"}

	// Drive the flow but force the minted token to carry the WRONG nonce.
	sess, _ := NewSessions(false)
	mux := http.NewServeMux()
	prov.Mount(mux, sess, func(w http.ResponseWriter, r *http.Request, id Identity) {
		t.Error("success callback must not run on a nonce mismatch")
	})
	ar := httptest.NewRecorder()
	mux.ServeHTTP(ar, httptest.NewRequest(http.MethodGet, "/login/oidc", nil))
	cookies := ar.Result().Cookies()
	iss.nonce = "attacker-minted-nonce"

	cb := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/login/oidc/callback?code=c&state=x", nil)
	req.AddCookie(cookies[0])
	mux.ServeHTTP(cb, req)
	if cb.Code != http.StatusBadRequest {
		t.Fatalf("nonce mismatch: got %d, want 400", cb.Code)
	}
}

func TestOIDCFlowRejectsCallbackWithoutState(t *testing.T) {
	iss := newFakeIssuer(t)
	prov := oidcProviderFor(t, iss)
	sess, _ := NewSessions(false)
	mux := http.NewServeMux()
	prov.Mount(mux, sess, func(w http.ResponseWriter, r *http.Request, id Identity) {
		t.Error("success callback must not run without a state cookie")
	})
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/login/oidc/callback?code=c&state=x", nil))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("callback without state cookie: got %d, want 400", rr.Code)
	}
}
