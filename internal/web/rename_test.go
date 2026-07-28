package web

import (
	"bytes"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/LoneExile/merino/internal/app"
)

// renameServer is writeServer with a caller-supplied agent fixture: tab and
// workspace renames need an agent that actually carries the target tab or
// workspace id, which writeServer's fixed single-pane fixture does not.
func renameServer(t *testing.T, policy Policy, wr Writer, agents []app.Agent) (*Server, *bytes.Buffer) {
	t.Helper()
	buf := &bytes.Buffer{}
	audit := app.NewAuditTo(nopCloser{buf})
	if policy == nil {
		policy = SingleOperator{}
	}
	s, err := New(&fakeSource{agents: agents}, Config{
		Provider:    NewPasswordProvider("alice", "correct-horse", DirectIP, false),
		Policy:      policy,
		Assets:      fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("<head></head>")}},
		Logger:      slog.New(slog.DiscardHandler),
		Writer:      wr,
		AllowWrites: true,
		Audit:       audit,
	})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	return s, buf
}

// renameFixture is one agent carrying a pane, tab and workspace id so all
// three rename targets resolve against it.
func renameFixture() []app.Agent {
	return []app.Agent{{PaneID: "p1", Agent: "omp", WorkspaceID: "w1", TabID: "w1:t1"}}
}

// lastAuditEntry decodes the final line of an audit log.
func lastAuditEntry(t *testing.T, buf *bytes.Buffer) app.AuditEntry {
	t.Helper()
	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	var e app.AuditEntry
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &e); err != nil {
		t.Fatalf("audit line is not JSON: %v (log: %s)", err, buf.String())
	}
	return e
}

// Renaming a pane, tab or workspace is a write like any other: authenticated,
// reaches the writer with the posted name, and is recorded in the audit log
// with that name.
func TestRenameHappyPathIsAudited(t *testing.T) {
	tests := []struct {
		path   string
		name   string
		action string
		call   string
	}{
		{"/api/panes/p1/rename", "build fix", "rename_pane", "rename_pane:p1:build fix"},
		{"/api/tabs/w1:t1/rename", "review", "rename_tab", "rename_tab:w1:t1:review"},
		{"/api/workspaces/w1/rename", "backend", "rename_workspace", "rename_workspace:w1:backend"},
	}
	for _, tt := range tests {
		t.Run(tt.action, func(t *testing.T) {
			wr := &fakeWriter{}
			s, auditBuf := renameServer(t, nil, wr, renameFixture())
			c := login(t, s, "alice", "correct-horse")

			rr := post(t, s, c, tt.path, `{"name":"`+tt.name+`"}`)
			if rr.Code != http.StatusOK {
				t.Fatalf("rename = %d: %s", rr.Code, rr.Body.String())
			}
			if len(wr.calls) != 1 || wr.calls[0] != tt.call {
				t.Errorf("calls = %v, want [%s]", wr.calls, tt.call)
			}

			e := lastAuditEntry(t, auditBuf)
			if e.Actor != "alice" || e.Action != tt.action || !e.Allowed {
				t.Errorf("audit entry = %+v", e)
			}
			if !strings.Contains(e.Detail, tt.name) {
				t.Errorf("audit did not record the new name: %q", e.Detail)
			}
		})
	}
}

// A policy refusal must not reach herdr, must be indistinguishable from the
// target not existing, and must still be recorded as denied.
func TestRenameDeniedByPolicy(t *testing.T) {
	for _, path := range []string{
		"/api/panes/p1/rename", "/api/tabs/w1:t1/rename", "/api/workspaces/w1/rename",
	} {
		t.Run(path, func(t *testing.T) {
			wr := &fakeWriter{}
			s, auditBuf := renameServer(t, denyAll{}, wr, renameFixture())
			c := login(t, s, "alice", "correct-horse")

			rr := post(t, s, c, path, `{"name":"nope"}`)
			if rr.Code != http.StatusNotFound {
				t.Errorf("denied rename = %d, want 404", rr.Code)
			}
			if len(wr.calls) != 0 {
				t.Errorf("policy-denied rename reached the writer: %v", wr.calls)
			}
			e := lastAuditEntry(t, auditBuf)
			if e.Allowed {
				t.Error("refusal recorded as allowed")
			}
		})
	}
}

// Renaming a tab or workspace id that no known agent belongs to must fail
// identically to a policy refusal, and must never reach the writer.
func TestRenameToUnknownTargetFails(t *testing.T) {
	wr := &fakeWriter{}
	s, _ := renameServer(t, nil, wr, renameFixture())
	c := login(t, s, "alice", "correct-horse")

	for _, path := range []string{
		"/api/panes/nope/rename", "/api/tabs/nope/rename", "/api/workspaces/nope/rename",
	} {
		if rr := post(t, s, c, path, `{"name":"x"}`); rr.Code != http.StatusNotFound {
			t.Errorf("POST %s = %d, want 404", path, rr.Code)
		}
	}
	if len(wr.calls) != 0 {
		t.Errorf("rename to an unknown target reached the writer: %v", wr.calls)
	}
}

// A guard-level refusal (e.g. checkRenameName rejecting a blank name inside
// AgentsService) must surface through the same 400-and-audit path as every
// other write's guard error; see TestWriteGuardErrorSurfaces.
func TestRenameGuardErrorSurfaces(t *testing.T) {
	wr := &fakeWriter{err: errors.New("input not allowed: empty name")}
	s, auditBuf := renameServer(t, nil, wr, renameFixture())
	c := login(t, s, "alice", "correct-horse")

	rr := post(t, s, c, "/api/panes/p1/rename", `{"name":"   "}`)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("guard refusal = %d, want 400", rr.Code)
	}
	if !strings.Contains(auditBuf.String(), `"allowed":false`) {
		t.Error("guard refusal was not audited as denied")
	}
}

// With no Writer the rename routes must not exist at all, exactly like the
// other write routes.
func TestRenameRoutesAbsentWithoutWriter(t *testing.T) {
	s := testServer(t, &fakeSource{agents: renameFixture()}, nil)
	c := login(t, s, "alice", "correct-horse")

	for _, path := range []string{
		"/api/panes/p1/rename", "/api/tabs/w1:t1/rename", "/api/workspaces/w1/rename",
	} {
		rr := post(t, s, c, path, `{"name":"x"}`)
		if rr.Code >= 200 && rr.Code < 300 {
			t.Errorf("POST %s succeeded on a read-only server (%d)", path, rr.Code)
		}
	}
}

// Rename routes require authentication like every other write.
func TestRenameEndpointsRequireAuth(t *testing.T) {
	wr := &fakeWriter{}
	s, _ := renameServer(t, nil, wr, renameFixture())

	for _, path := range []string{
		"/api/panes/p1/rename", "/api/tabs/w1:t1/rename", "/api/workspaces/w1/rename",
	} {
		rr := post(t, s, nil, path, `{"name":"x"}`)
		if rr.Code != http.StatusUnauthorized {
			t.Errorf("POST %s unauthenticated = %d, want 401", path, rr.Code)
		}
	}
	if len(wr.calls) != 0 {
		t.Errorf("unauthenticated requests reached the writer: %v", wr.calls)
	}
}
