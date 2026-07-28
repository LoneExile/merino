package web

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/LoneExile/merino/internal/app"
)

// fakeSessions is a SessionSource returning a fixed list.
type fakeSessions struct {
	sessions []app.SessionInfo
	err      error
}

func (f *fakeSessions) Sessions(context.Context) ([]app.SessionInfo, error) {
	return f.sessions, f.err
}

// fakeSwitcher is a SessionSwitcher recording every id it was asked to
// switch to.
type fakeSwitcher struct {
	calls []string
	err   error
}

func (f *fakeSwitcher) SwitchSession(id string) error {
	f.calls = append(f.calls, id)
	return f.err
}

// sessionsServer builds a server with the given session capabilities wired
// in, reusing the same fixtures as writeServer/testServer.
func sessionsServer(t *testing.T, sessions SessionSource, switcher SessionSwitcher) *Server {
	t.Helper()
	t.Helper()
	s, err := New(&fakeSource{agents: []app.Agent{agent("p1")}}, Config{
		Provider: NewPasswordProvider("alice", "correct-horse", DirectIP, false),
		Policy:   SingleOperator{},
		Assets:   fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("<head></head>")}},
		Logger:   slog.New(slog.DiscardHandler),
		Sessions: sessions,
		Switcher: switcher, SessionSwitch: switcher != nil,
	})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	return s
}

// getJSON issues an authenticated GET and, on a 200, decodes the JSON body
// into a generic map so tests can inspect individual fields.
func getJSON(t *testing.T, s *Server, c *http.Cookie, path string) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if c != nil {
		req.AddCookie(c)
	}
	rr := httptest.NewRecorder()
	s.routes().ServeHTTP(rr, req)
	var body map[string]any
	if rr.Code == http.StatusOK {
		if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode %s response: %v (%s)", path, err, rr.Body.String())
		}
	}
	return rr, body
}

// /api/sessions must return 200 and list a session whose socket is
// unreachable rather than dropping it or failing the whole request.
func TestSessionsListsUnreachableSession(t *testing.T) {
	src := &fakeSessions{sessions: []app.SessionInfo{
		{ID: "default", Name: "default", Socket: "/tmp/default.sock", Panes: 4, Agents: 2, Reachable: true, Current: true},
		{ID: "dead", Name: "dead", Socket: "/tmp/dead.sock", Reachable: false},
	}}
	s := sessionsServer(t, src, nil)
	c := login(t, s, "alice", "correct-horse")

	rr, body := getJSON(t, s, c, "/api/sessions")
	if rr.Code != http.StatusOK {
		t.Fatalf("GET /api/sessions = %d: %s", rr.Code, rr.Body.String())
	}
	if body["current"] != "default" {
		t.Errorf("current = %v, want \"default\"", body["current"])
	}
	sessions, ok := body["sessions"].([]any)
	if !ok || len(sessions) != 2 {
		t.Fatalf("sessions = %v, want 2 entries", body["sessions"])
	}
	dead, ok := sessions[1].(map[string]any)
	if !ok || dead["id"] != "dead" {
		t.Fatalf("second session = %v, want id \"dead\"", sessions[1])
	}
	if dead["reachable"] != false {
		t.Errorf("dead session reachable = %v, want false", dead["reachable"])
	}
	if dead["panes"] != float64(0) || dead["agents"] != float64(0) {
		t.Errorf("dead session counts = panes=%v agents=%v, want 0/0", dead["panes"], dead["agents"])
	}
}

// canSwitch must reflect whether a Switcher is configured, independent of
// how many sessions were found.
func TestSessionsReportsCanSwitch(t *testing.T) {
	src := &fakeSessions{sessions: []app.SessionInfo{{ID: "default", Name: "default"}}}

	without := sessionsServer(t, src, nil)
	_, withoutBody := getJSON(t, without, login(t, without, "alice", "correct-horse"), "/api/sessions")
	if withoutBody["canSwitch"] != false {
		t.Errorf("canSwitch = %v, want false with no Switcher", withoutBody["canSwitch"])
	}

	with := sessionsServer(t, src, &fakeSwitcher{})
	_, withBody := getJSON(t, with, login(t, with, "alice", "correct-horse"), "/api/sessions")
	if withBody["canSwitch"] != true {
		t.Errorf("canSwitch = %v, want true with a Switcher configured", withBody["canSwitch"])
	}
}

// A failure enumerating sessions must not crash the endpoint.
func TestSessionsListErrorSurfaces(t *testing.T) {
	s := sessionsServer(t, &fakeSessions{err: errors.New("boom")}, nil)
	c := login(t, s, "alice", "correct-horse")

	req := httptest.NewRequest(http.MethodGet, "/api/sessions", nil)
	req.AddCookie(c)
	rr := httptest.NewRecorder()
	s.routes().ServeHTTP(rr, req)
	if rr.Code != http.StatusBadGateway {
		t.Errorf("GET /api/sessions with a failing source = %d, want 502", rr.Code)
	}
}

// /api/sessions requires authentication like every other API route.
func TestSessionsRequireAuth(t *testing.T) {
	s := sessionsServer(t, &fakeSessions{}, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/sessions", nil)
	rr := httptest.NewRecorder()
	s.routes().ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("unauthenticated /api/sessions = %d, want 401", rr.Code)
	}
}

// When no Sessions capability is configured at all, the route must not
// exist, mirroring how Writer leaves the write routes absent. Unlike a
// POST-only write route, there is a registered "GET /" catch-all
// (handleStatic's SPA fallback), so an absent GET route does not surface as
// a non-2xx status — it falls through to the SPA and answers 200 with HTML.
// The absence signal here is the response shape instead.
func TestSessionsRouteAbsentWithoutCapability(t *testing.T) {
	s := sessionsServer(t, nil, nil)
	c := login(t, s, "alice", "correct-horse")

	req := httptest.NewRequest(http.MethodGet, "/api/sessions", nil)
	req.AddCookie(c)
	rr := httptest.NewRecorder()
	s.routes().ServeHTTP(rr, req)

	if ct := rr.Header().Get("Content-Type"); strings.HasPrefix(ct, "application/json") {
		t.Errorf("GET /api/sessions answered as JSON with no Sessions configured: %s", rr.Body.String())
	}
}

// The switch route must not exist at all when no Switcher is configured —
// the --allow-session-switch=false default.
func TestSessionSwitchRouteAbsentByDefault(t *testing.T) {
	s := sessionsServer(t, &fakeSessions{}, nil)
	c := login(t, s, "alice", "correct-horse")

	rr := post(t, s, c, "/api/sessions/switch", `{"id":"default"}`)
	if rr.Code >= 200 && rr.Code < 300 {
		t.Errorf("POST /api/sessions/switch succeeded with switching disabled (%d)", rr.Code)
	}
}

// With a Switcher configured, a switch request must reach it with the
// posted id and answer 200.
func TestSessionSwitchHappyPath(t *testing.T) {
	sw := &fakeSwitcher{}
	s := sessionsServer(t, &fakeSessions{}, sw)
	c := login(t, s, "alice", "correct-horse")

	rr := post(t, s, c, "/api/sessions/switch", `{"id":"tunnel-test"}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("switch = %d: %s", rr.Code, rr.Body.String())
	}
	if len(sw.calls) != 1 || sw.calls[0] != "tunnel-test" {
		t.Errorf("switcher calls = %v, want [tunnel-test]", sw.calls)
	}
}

// A switch failure (e.g. an unknown session id) must surface as 400 without
// crashing the handler.
func TestSessionSwitchSurfacesError(t *testing.T) {
	sw := &fakeSwitcher{err: errors.New("unknown session")}
	s := sessionsServer(t, &fakeSessions{}, sw)
	c := login(t, s, "alice", "correct-horse")

	rr := post(t, s, c, "/api/sessions/switch", `{"id":"nope"}`)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("failed switch = %d, want 400", rr.Code)
	}
}

// The switch route requires authentication like every other write.
func TestSessionSwitchRequiresAuth(t *testing.T) {
	sw := &fakeSwitcher{}
	s := sessionsServer(t, &fakeSessions{}, sw)

	rr := post(t, s, nil, "/api/sessions/switch", `{"id":"default"}`)
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("unauthenticated switch = %d, want 401", rr.Code)
	}
	if len(sw.calls) != 0 {
		t.Errorf("unauthenticated request reached the switcher: %v", sw.calls)
	}
}

// GET /api/session must expose canSwitchSession and canRename alongside the
// existing readOnly flag, tracking Switcher/Writer independently.
func TestSessionEndpointReportsCapabilityFlags(t *testing.T) {
	full, err := New(&fakeSource{agents: []app.Agent{agent("p1")}}, Config{
		Provider: NewPasswordProvider("alice", "correct-horse", DirectIP, false),
		Policy:   SingleOperator{},
		Assets:   fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("<head></head>")}},
		Logger:   slog.New(slog.DiscardHandler),
		Writer:   &fakeWriter{},
		Audit:    app.NewAuditTo(nopCloser{&bytes.Buffer{}}),
		Sessions: &fakeSessions{},
		Switcher: &fakeSwitcher{}, SessionSwitch: true,
	})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	_, body := getJSON(t, full, login(t, full, "alice", "correct-horse"), "/api/session")
	if body["canRename"] != true {
		t.Errorf("canRename = %v, want true with a Writer configured", body["canRename"])
	}
	if body["canSwitchSession"] != true {
		t.Errorf("canSwitchSession = %v, want true with a Switcher configured", body["canSwitchSession"])
	}

	plain := sessionsServer(t, &fakeSessions{}, nil)
	_, body2 := getJSON(t, plain, login(t, plain, "alice", "correct-horse"), "/api/session")
	if body2["canRename"] != false {
		t.Errorf("canRename = %v, want false with no Writer", body2["canRename"])
	}
	if body2["canSwitchSession"] != false {
		t.Errorf("canSwitchSession = %v, want false with no Switcher", body2["canSwitchSession"])
	}
}
