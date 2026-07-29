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

	// POST has no route at all on a read-only build; the mux answers 405.
	if rr := post(t, s, c, "/api/panes", `{"kind":"omp"}`); rr.Code == http.StatusOK {
		t.Fatalf("POST /api/panes succeeded on a read-only build: %s", rr.Body.String())
	}

	// The GETs fall through to the SPA, which serves HTML — never the JSON
	// payload that would disclose the operator's workspaces and agents.
	for _, path := range []string{"/api/workspaces", "/api/agent-kinds"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.AddCookie(c)
		rr := httptest.NewRecorder()
		s.routes().ServeHTTP(rr, req)
		if strings.Contains(rr.Header().Get("Content-Type"), "json") {
			t.Fatalf("GET %s served JSON on a read-only build: %s", path, rr.Body.String())
		}
	}
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
