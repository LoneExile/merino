package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/LoneExile/merino/internal/app"
)

// Creating an agent pane starts a process on the operator's machine. It must
// be unreachable unless the build carries a Writer, the runtime write gate is
// open, and Policy grants spawn authority — the same three doors as typing
// into a terminal.

func TestSpawnRoutesAbsentWithoutWriter(t *testing.T) {
	s := testServer(t, &fakeSource{agents: []app.Agent{agent("p1")}}, nil)
	c := login(t, s, "alice", "correct-horse")

	// Assert the determinate status, not merely "not 200". A handler that
	// answered 201 Created — an ordinary choice for a create endpoint, and one
	// postJSON accepts — would satisfy a not-200 check while the route was
	// live and creating panes on a read-only build.
	if rr := post(t, s, c, "/api/panes", `{"kind":"omp"}`); rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST /api/panes = %d, want 405 on a read-only build: %s", rr.Code, rr.Body.String())
	}

	// The GETs fall through to the SPA. Pin that too: "not JSON" is also
	// satisfied by a 500 text/plain, which would not prove non-disclosure.
	for _, path := range []string{"/api/workspaces", "/api/agent-kinds"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.AddCookie(c)
		rr := httptest.NewRecorder()
		s.routes().ServeHTTP(rr, req)
		if rr.Code != http.StatusOK || !strings.Contains(rr.Header().Get("Content-Type"), "text/html") {
			t.Fatalf("GET %s = %d %s, want the SPA fallback: %s",
				path, rr.Code, rr.Header().Get("Content-Type"), rr.Body.String())
		}
	}
}

// The response envelopes are the contract the spawn sheet consumes. Renaming
// a key or dropping the nil-to-empty-slice normalisation breaks the sheet
// while every other test stays green, so assert the shapes and the values.
func TestSpawnListsReturnTheirEnvelopes(t *testing.T) {
	wr := &fakeWriter{}
	s, _ := writeServer(t, SingleOperator{}, wr)
	c := login(t, s, "alice", "correct-horse")

	var ws struct {
		Workspaces []app.Workspace `json:"workspaces"`
	}
	if rr := getJSONInto(t, s, c, "/api/workspaces", &ws); rr != http.StatusOK {
		t.Fatalf("GET /api/workspaces = %d", rr)
	}
	if len(ws.Workspaces) != 1 || ws.Workspaces[0].WorkspaceID != "w1" || ws.Workspaces[0].Label != "one" {
		t.Fatalf("workspaces = %+v", ws.Workspaces)
	}

	var ks struct {
		Kinds []app.AgentKind `json:"kinds"`
	}
	if rr := getJSONInto(t, s, c, "/api/agent-kinds", &ks); rr != http.StatusOK {
		t.Fatalf("GET /api/agent-kinds = %d", rr)
	}
	if len(ks.Kinds) != 1 || ks.Kinds[0].Kind != "omp" || ks.Kinds[0].Label != "Oh My Pi" {
		t.Fatalf("kinds = %+v", ks.Kinds)
	}
	// The absolute host path must not cross the HTTP boundary: the fixture
	// serves /usr/local/bin/omp and the browser may be on a public tunnel.
	if ks.Kinds[0].Path != "omp" {
		t.Fatalf("path = %q, want the basename only", ks.Kinds[0].Path)
	}
}

// An empty herd must serialise as [] rather than null, or the sheet's `?? []`
// is the only thing standing between it and a crash.
func TestSpawnListsSerialiseEmptyAsArray(t *testing.T) {
	wr := &fakeWriter{empty: true}
	s, _ := writeServer(t, SingleOperator{}, wr)
	c := login(t, s, "alice", "correct-horse")

	for path, key := range map[string]string{
		"/api/workspaces":  "workspaces",
		"/api/agent-kinds": "kinds",
	} {
		var raw map[string]json.RawMessage
		if code := getJSONInto(t, s, c, path, &raw); code != http.StatusOK {
			t.Fatalf("GET %s = %d", path, code)
		}
		if got := string(raw[key]); got != "[]" {
			t.Errorf("GET %s %q = %s, want []", path, key, got)
		}
	}
}

// Every refusal branch must be audited, on all three endpoints — including
// the two reads, whose branches nothing else exercises.
func TestSpawnAuditsEveryRefusedEndpoint(t *testing.T) {
	wr := &fakeWriter{}
	s, auditBuf := writeServer(t, SingleOperator{}, wr)
	if err := s.SetAllowWrites(false); err != nil {
		t.Fatalf("close gate: %v", err)
	}
	c := login(t, s, "alice", "correct-horse")

	getJSONInto(t, s, c, "/api/workspaces", &struct{}{})
	getJSONInto(t, s, c, "/api/agent-kinds", &struct{}{})
	post(t, s, c, "/api/panes", `{"kind":"omp"}`)

	for _, action := range []string{"workspaces_list", "agent_kinds_list", "agent_pane_create"} {
		if !strings.Contains(auditBuf.String(), action) {
			t.Errorf("%s refusal was not audited: %s", action, auditBuf.String())
		}
	}
	// Audited as refused, not merely audited.
	if strings.Contains(auditBuf.String(), `"allowed":true`) {
		t.Errorf("a refusal was recorded as allowed: %s", auditBuf.String())
	}
	if len(wr.calls) != 0 {
		t.Fatalf("writer reached with the gate closed: %v", wr.calls)
	}
}

func getJSONInto(t *testing.T, s *Server, c *http.Cookie, path string, into any) int {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.AddCookie(c)
	rr := httptest.NewRecorder()
	s.routes().ServeHTTP(rr, req)
	if rr.Code == http.StatusOK {
		if err := json.Unmarshal(rr.Body.Bytes(), into); err != nil {
			t.Fatalf("decode %s: %v (%s)", path, err, rr.Body.String())
		}
	}
	return rr.Code
}

func TestSpawnRefusedWhenWritesOff(t *testing.T) {
	wr := &fakeWriter{}
	s, _ := writeServer(t, SingleOperator{}, wr)
	if err := s.SetAllowWrites(false); err != nil {
		t.Fatalf("close gate: %v", err)
	}
	c := login(t, s, "alice", "correct-horse")

	if rr := post(t, s, c, "/api/panes", `{"kind":"omp"}`); rr.Code != http.StatusForbidden {
		t.Fatalf("spawn with writes off = %d, want 403", rr.Code)
	}
	if len(wr.calls) != 0 {
		t.Fatalf("writer reached while the write gate was closed: %v", wr.calls)
	}
}

func TestSpawnRefusedWithoutPolicyAuthority(t *testing.T) {
	wr := &fakeWriter{}
	// denyAll refuses everything, CanSpawn included.
	s, _ := writeServer(t, denyAll{}, wr)
	c := login(t, s, "alice", "correct-horse")

	if rr := post(t, s, c, "/api/panes", `{"kind":"omp"}`); rr.Code != http.StatusForbidden {
		t.Fatalf("spawn without authority = %d, want 403", rr.Code)
	}
	if len(wr.calls) != 0 {
		t.Fatalf("writer reached despite policy refusal: %v", wr.calls)
	}
}

func TestSpawnPassesFieldsAndReturnsPane(t *testing.T) {
	wr := &fakeWriter{}
	s, _ := writeServer(t, SingleOperator{}, wr)
	c := login(t, s, "alice", "correct-horse")

	rr := post(t, s, c, "/api/panes", `{"workspaceId":"w2","kind":"omp","label":"  scratch  "}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("spawn = %d: %s", rr.Code, rr.Body.String())
	}

	// Assert the values on the wire, not merely that a call happened. The
	// label is trimmed before it reaches the host, or herdr names a tab with
	// the operator's stray spaces.
	want := "start_agent_pane:w2:omp:scratch"
	if len(wr.calls) != 1 || wr.calls[0] != want {
		t.Fatalf("writer calls = %v, want [%s]", wr.calls, want)
	}

	var pane app.NewPane
	if err := json.Unmarshal(rr.Body.Bytes(), &pane); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if pane.PaneID == "" {
		t.Fatalf("response carried no pane id, so the UI cannot open it: %s", rr.Body.String())
	}
}

func TestSpawnRejectsMissingKind(t *testing.T) {
	wr := &fakeWriter{}
	s, _ := writeServer(t, SingleOperator{}, wr)
	c := login(t, s, "alice", "correct-horse")

	if rr := post(t, s, c, "/api/panes", `{"workspaceId":"w2"}`); rr.Code != http.StatusBadRequest {
		t.Fatalf("empty kind = %d, want 400", rr.Code)
	}
	if len(wr.calls) != 0 {
		t.Fatalf("writer reached with no kind: %v", wr.calls)
	}
}

// A refused spawn must leave a record: the audit log is the only place an
// operator can later see that a phone tried to start an agent.
func TestSpawnAuditsRefusal(t *testing.T) {
	wr := &fakeWriter{}
	s, auditBuf := writeServer(t, SingleOperator{}, wr)
	if err := s.SetAllowWrites(false); err != nil {
		t.Fatalf("close gate: %v", err)
	}
	c := login(t, s, "alice", "correct-horse")

	post(t, s, c, "/api/panes", `{"kind":"omp"}`)

	if !strings.Contains(auditBuf.String(), "agent_pane_create") {
		t.Fatalf("refused spawn was not audited: %s", auditBuf.String())
	}
}

// canSpawn drives whether the UI offers the affordance at all. It must track
// the live gate, not merely whether the build could ever write.
func TestSessionReportsCanSpawn(t *testing.T) {
	wr := &fakeWriter{}
	s, _ := writeServer(t, SingleOperator{}, wr)
	c := login(t, s, "alice", "correct-horse")

	read := func() bool {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, "/api/session", nil)
		req.AddCookie(c)
		rr := httptest.NewRecorder()
		s.routes().ServeHTTP(rr, req)
		var body struct {
			CanSpawn bool `json:"canSpawn"`
		}
		if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode session: %v", err)
		}
		return body.CanSpawn
	}

	if !read() {
		t.Fatal("canSpawn false while writes are on")
	}
	if err := s.SetAllowWrites(false); err != nil {
		t.Fatalf("close gate: %v", err)
	}
	if read() {
		t.Fatal("canSpawn stayed true after the write gate closed")
	}
}
