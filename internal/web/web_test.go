package web

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/LoneExile/herdr-tunnel/internal/app"
	"github.com/LoneExile/herdr-tunnel/internal/herdr"
)

type fakeSource struct {
	agents []app.Agent
	text   string

	// streamEvents are delivered to onText, in order, as soon as
	// StreamOutput is called.
	streamEvents []string
	// started, if set, is closed the moment StreamOutput is invoked.
	started chan struct{}
	// stopped, if set, is closed when StreamOutput returns — proof the
	// background subscription actually exits rather than leaking.
	stopped chan struct{}
}

func (f *fakeSource) List() []app.Agent  { return f.agents }
func (f *fakeSource) Counts() app.Counts { return app.Counts{Total: len(f.agents)} }
func (f *fakeSource) ReadANSI(string, int) (string, error) {
	return f.text, nil
}

func (f *fakeSource) StreamOutputANSI(ctx context.Context, _ string, _ int, onText func(string)) error {
	if f.started != nil {
		close(f.started)
	}
	if f.stopped != nil {
		defer close(f.stopped)
	}
	for _, text := range f.streamEvents {
		onText(text)
	}
	<-ctx.Done()
	return nil
}

func testServer(t *testing.T, src Source, policy Policy) *Server {
	t.Helper()
	if policy == nil {
		policy = SingleOperator{}
	}
	s, err := New(src, Config{
		Provider: NewPasswordProvider("alice", "correct-horse", DirectIP, false),
		Policy:   policy,
		Assets:   fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("<head></head>")}},
		Logger:   slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	return s
}

func agent(id string) app.Agent {
	return app.Agent{PaneID: id, Agent: "omp", Status: herdr.StatusIdle, WorkspaceID: "w1"}
}

// A server must refuse to exist without an authenticator. Defaulting to open
// would mean a misconfiguration silently exposes every pane.
func TestNewRequiresProviderAndPolicy(t *testing.T) {
	if _, err := New(&fakeSource{}, Config{Policy: SingleOperator{}}); err == nil {
		t.Error("server without a Provider should be refused")
	}
	if _, err := New(&fakeSource{}, Config{Provider: NewPasswordProvider("a", "b", DirectIP, false)}); err == nil {
		t.Error("server without a Policy should be refused")
	}
}

// API calls get 401 JSON; navigations get redirected. A browser following an
// HTML redirect for a fetch() would silently render the login page as data.
func TestUnauthenticatedAccess(t *testing.T) {
	s := testServer(t, &fakeSource{agents: []app.Agent{agent("p1")}}, nil)

	for _, path := range []string{"/api/agents", "/api/session", "/api/events", "/api/panes/p1/output", "/api/panes/p1/stream"} {
		rr := httptest.NewRecorder()
		s.routes().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, path, nil))
		if rr.Code != http.StatusUnauthorized {
			t.Errorf("GET %s = %d, want 401", path, rr.Code)
		}
		if body := rr.Body.String(); strings.Contains(body, "p1") {
			t.Errorf("GET %s leaked pane data while unauthenticated: %s", path, body)
		}
	}

	rr := httptest.NewRecorder()
	s.routes().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/", nil))
	if rr.Code != http.StatusSeeOther {
		t.Errorf("GET / = %d, want 303 redirect to login", rr.Code)
	}
}

func login(t *testing.T, s *Server, user, pass string) *http.Cookie {
	t.Helper()
	form := url.Values{"username": {user}, "password": {pass}}
	req := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()
	s.routes().ServeHTTP(rr, req)

	for _, c := range rr.Result().Cookies() {
		if c.Name == sessionCookie && c.Value != "" {
			return c
		}
	}
	return nil
}

func TestLoginRejectsBadCredentials(t *testing.T) {
	s := testServer(t, &fakeSource{}, nil)

	for _, tc := range []struct{ user, pass string }{
		{"alice", "wrong"},
		{"mallory", "correct-horse"},
		{"", ""},
	} {
		if c := login(t, s, tc.user, tc.pass); c != nil {
			t.Errorf("login(%q,%q) issued a session cookie", tc.user, tc.pass)
		}
	}
	if c := login(t, s, "alice", "correct-horse"); c == nil {
		t.Error("correct credentials did not issue a session cookie")
	}
}

// The login form must not reveal whether the username exists.
func TestLoginDoesNotEnumerateUsers(t *testing.T) {
	s := testServer(t, &fakeSource{}, nil)

	body := func(user, pass string) string {
		form := url.Values{"username": {user}, "password": {pass}}
		req := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		rr := httptest.NewRecorder()
		s.routes().ServeHTTP(rr, req)
		return rr.Body.String()
	}
	if body("alice", "wrong") != body("nobody", "wrong") {
		t.Error("responses differ for existing vs unknown user; that enumerates accounts")
	}
}

func TestAuthenticatedAccess(t *testing.T) {
	src := &fakeSource{agents: []app.Agent{agent("p1"), agent("p2")}, text: "hello"}
	s := testServer(t, src, nil)

	c := login(t, s, "alice", "correct-horse")
	if c == nil {
		t.Fatal("login failed")
	}

	req := httptest.NewRequest(http.MethodGet, "/api/agents", nil)
	req.AddCookie(c)
	rr := httptest.NewRecorder()
	s.routes().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET /api/agents = %d, want 200", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "p1") {
		t.Errorf("agents response missing pane: %s", rr.Body.String())
	}
}

// A forged or altered cookie must not authenticate.
func TestTamperedSessionRejected(t *testing.T) {
	s := testServer(t, &fakeSource{agents: []app.Agent{agent("p1")}}, nil)
	c := login(t, s, "alice", "correct-horse")
	if c == nil {
		t.Fatal("login failed")
	}

	for name, value := range map[string]string{
		"flipped signature": c.Value[:len(c.Value)-2] + "xy",
		"payload swapped":   "YWRtaW4.YWRtaW4.cGFzc3dvcmQ.0.99999999999~" + strings.Split(c.Value, "~")[1],
		"no signature":      strings.Split(c.Value, "~")[0],
		"empty":             "",
	} {
		req := httptest.NewRequest(http.MethodGet, "/api/agents", nil)
		req.AddCookie(&http.Cookie{Name: sessionCookie, Value: value})
		rr := httptest.NewRecorder()
		s.routes().ServeHTTP(rr, req)
		if rr.Code != http.StatusUnauthorized {
			t.Errorf("%s: got %d, want 401", name, rr.Code)
		}
	}
}

// Reading a pane must consult the policy, and a refusal must not disclose that
// the pane exists.
func TestOutputRespectsPolicy(t *testing.T) {
	src := &fakeSource{agents: []app.Agent{agent("p1")}, text: "secret output"}
	s := testServer(t, src, denyAll{})

	c := login(t, s, "alice", "correct-horse")
	req := httptest.NewRequest(http.MethodGet, "/api/panes/p1/output", nil)
	req.AddCookie(c)
	rr := httptest.NewRecorder()
	s.routes().ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Errorf("denied pane = %d, want 404", rr.Code)
	}
	if strings.Contains(rr.Body.String(), "secret output") {
		t.Error("policy-denied pane leaked its output")
	}
}

func TestAgentsFilteredByPolicy(t *testing.T) {
	src := &fakeSource{agents: []app.Agent{agent("p1"), agent("p2")}}
	s := testServer(t, src, denyAll{})

	c := login(t, s, "alice", "correct-horse")
	req := httptest.NewRequest(http.MethodGet, "/api/agents", nil)
	req.AddCookie(c)
	rr := httptest.NewRecorder()
	s.routes().ServeHTTP(rr, req)

	body := rr.Body.String()
	if strings.Contains(body, "p1") || strings.Contains(body, "p2") {
		t.Errorf("policy-denied agents were listed: %s", body)
	}
}

type denyAll struct{}

func (denyAll) CanView(Identity, app.Agent) bool    { return false }
func (denyAll) CanControl(Identity, app.Agent) bool { return false }

// The server must have no write endpoints while it is read-only.
func TestNoWriteEndpoints(t *testing.T) {
	s := testServer(t, &fakeSource{agents: []app.Agent{agent("p1")}}, nil)
	c := login(t, s, "alice", "correct-horse")

	for _, path := range []string{
		"/api/panes/p1/respond", "/api/panes/p1/keys", "/api/panes/p1/interrupt", "/api/respond",
	} {
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader("{}"))
		req.AddCookie(c)
		rr := httptest.NewRecorder()
		s.routes().ServeHTTP(rr, req)
		// Anything but a success proves the route is not wired for writes.
		if rr.Code >= 200 && rr.Code < 300 {
			t.Errorf("POST %s returned %d — a write endpoint exists in read-only mode", path, rr.Code)
		}
	}
}

// The bundle must be told it is running in a browser, or it will try to use
// the Wails IPC bridge and fail.
//
// The marker must also survive the server's own Content-Security-Policy. An
// earlier version injected it as an inline <script>, which the CSP then
// blocked: the flag never got set, the bundle took the desktop path and every
// call died on POST /wails/runtime 405. Assert the marker is not script-borne.
func TestIndexCarriesWebMarker(t *testing.T) {
	srv := testServer(t, &fakeSource{}, nil)
	c := login(t, srv, "alice", "correct-horse")

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(c)
	rr := httptest.NewRecorder()
	srv.routes().ServeHTTP(rr, req)

	body, _ := io.ReadAll(rr.Body)
	html := string(body)

	if !strings.Contains(html, `name="herdr-mode"`) || !strings.Contains(html, `content="web"`) {
		t.Errorf("index.html served without the web-mode meta marker: %s", html)
	}
	if strings.Contains(html, "__HERDR_WEB__") {
		t.Error("marker is script-borne; the CSP will block it")
	}
}

// Every inline script the server emits must carry the CSP nonce, or the
// browser silently drops it.
func TestInlineScriptsAreNonced(t *testing.T) {
	assets := fstest.MapFS{"index.html": &fstest.MapFile{
		Data: []byte(`<head></head><body><script>window.boot=1</script><script src="/a.js"></script></body>`),
	}}
	srv, err := New(&fakeSource{}, Config{
		Provider: NewPasswordProvider("alice", "correct-horse", DirectIP, false),
		Policy:   SingleOperator{},
		Assets:   assets,
		Logger:   slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatal(err)
	}
	c := login(t, srv, "alice", "correct-horse")

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(c)
	rr := httptest.NewRecorder()
	srv.routes().ServeHTTP(rr, req)

	html := rr.Body.String()
	csp := rr.Header().Get("Content-Security-Policy")

	if !strings.Contains(csp, "script-src") {
		t.Error("CSP has no explicit script-src; the default-src fallback blocks all inline scripts")
	}
	if !strings.Contains(html, `<script nonce="`) {
		t.Errorf("inline script was not nonced: %s", html)
	}
	// The nonce in the document must be the one the header authorises.
	i := strings.Index(html, `<script nonce="`)
	nonce := html[i+len(`<script nonce="`):]
	nonce = nonce[:strings.Index(nonce, `"`)]
	if !strings.Contains(csp, "'nonce-"+nonce+"'") {
		t.Errorf("document nonce %q is not present in CSP %q", nonce, csp)
	}
}

func TestSessionRoundTrip(t *testing.T) {
	s, err := NewSessions(false)
	if err != nil {
		t.Fatal(err)
	}
	want := Identity{Subject: "sub-1", Name: "Alice", Provider: "oidc", Roles: []string{"herd-admin", "viewer"}}

	rr := httptest.NewRecorder()
	s.Issue(rr, want)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	for _, c := range rr.Result().Cookies() {
		req.AddCookie(c)
	}
	got, ok := s.Read(req)
	if !ok {
		t.Fatal("freshly issued session did not validate")
	}
	if got.Subject != want.Subject || got.Name != want.Name || got.Provider != want.Provider {
		t.Errorf("round trip = %+v, want %+v", got, want)
	}
	if strings.Join(got.Roles, ",") != strings.Join(want.Roles, ",") {
		t.Errorf("roles = %v, want %v", got.Roles, want.Roles)
	}
}

// A session signed by one key must not validate against another, or restarting
// the process would not invalidate outstanding cookies.
func TestSessionKeysAreIndependent(t *testing.T) {
	a, _ := NewSessions(false)
	b, _ := NewSessions(false)

	rr := httptest.NewRecorder()
	a.Issue(rr, Identity{Subject: "x", Name: "x", Provider: "password"})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	for _, c := range rr.Result().Cookies() {
		req.AddCookie(c)
	}
	if _, ok := b.Read(req); ok {
		t.Error("a session issued by one server validated against another")
	}
}

func TestRequireRolePolicy(t *testing.T) {
	p := RequireRole{View: "viewer", Control: "herd-admin"}
	viewer := Identity{Roles: []string{"viewer"}}
	admin := Identity{Roles: []string{"viewer", "herd-admin"}}
	nobody := Identity{}

	if !p.CanView(viewer, app.Agent{}) {
		t.Error("viewer should be able to view")
	}
	if p.CanControl(viewer, app.Agent{}) {
		t.Error("viewer must not be able to control")
	}
	if !p.CanControl(admin, app.Agent{}) {
		t.Error("admin should be able to control")
	}
	if p.CanView(nobody, app.Agent{}) || p.CanControl(nobody, app.Agent{}) {
		t.Error("roleless identity must be denied")
	}
}

// Proxy headers must only be believed when the operator has declared a proxy.
// Trusting them on a directly-reachable port lets a caller rotate the throttle
// key on every attempt and brute-force the password unimpeded.
func TestIPResolution(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/login", nil)
	req.RemoteAddr = "192.0.2.10:54321"
	req.Header.Set("CF-Connecting-IP", "203.0.113.7")
	req.Header.Set("X-Forwarded-For", "198.51.100.4, 203.0.113.7")

	if got := DirectIP(req); got != "192.0.2.10" {
		t.Errorf("DirectIP = %q, want the peer address 192.0.2.10 — headers must be ignored", got)
	}
	if got := ProxiedIP(req); got != "203.0.113.7" {
		t.Errorf("ProxiedIP = %q, want CF-Connecting-IP 203.0.113.7", got)
	}

	// Without Cloudflare's header, fall back to the leftmost XFF entry.
	req.Header.Del("CF-Connecting-IP")
	if got := ProxiedIP(req); got != "198.51.100.4" {
		t.Errorf("ProxiedIP without CF header = %q, want leftmost XFF 198.51.100.4", got)
	}

	// With no headers at all, both agree on the peer.
	bare := httptest.NewRequest(http.MethodPost, "/login", nil)
	bare.RemoteAddr = "192.0.2.10:1234"
	if DirectIP(bare) != "192.0.2.10" || ProxiedIP(bare) != "192.0.2.10" {
		t.Error("with no proxy headers both resolvers should return the peer address")
	}
}

// The throttle must key on the resolved client, so a spoofed header cannot
// reset the counter when the server is NOT behind a proxy.
func TestThrottleIgnoresSpoofedHeadersWhenDirect(t *testing.T) {
	s := testServer(t, &fakeSource{}, nil) // built with DirectIP

	attempt := func(spoof string) int {
		form := url.Values{"username": {"alice"}, "password": {"wrong"}}
		req := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("CF-Connecting-IP", spoof) // attacker-supplied
		req.RemoteAddr = "192.0.2.99:5000"
		rr := httptest.NewRecorder()
		s.routes().ServeHTTP(rr, req)
		return rr.Code
	}

	// Rotate the spoofed header every time; the throttle must still engage
	// because it keys on the real peer.
	var last int
	for i := range lockoutAfter + 2 {
		last = attempt(fmt.Sprintf("203.0.113.%d", i))
	}
	if last != http.StatusTooManyRequests {
		t.Errorf("after %d attempts got %d, want 429 — rotating CF-Connecting-IP bypassed the throttle",
			lockoutAfter+2, last)
	}
}
