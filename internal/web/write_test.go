package web

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/LoneExile/herdr-tunnel/internal/app"
)

type fakeWriter struct {
	calls []string
	err   error
}

func (f *fakeWriter) Respond(paneID, text string) error {
	f.calls = append(f.calls, "respond:"+paneID+":"+text)
	return f.err
}
func (f *fakeWriter) SendKeys(paneID string, keys []string) error {
	f.calls = append(f.calls, "keys:"+paneID+":"+strings.Join(keys, "+"))
	return f.err
}
func (f *fakeWriter) Focus(paneID string) error {
	f.calls = append(f.calls, "focus:"+paneID)
	return f.err
}
func (f *fakeWriter) Interrupt(paneID string) error {
	f.calls = append(f.calls, "interrupt:"+paneID)
	return f.err
}

type nopCloser struct{ *bytes.Buffer }

func (nopCloser) Close() error { return nil }

func writeServer(t *testing.T, policy Policy, wr Writer) (*Server, *bytes.Buffer) {
	t.Helper()
	buf := &bytes.Buffer{}
	audit := app.NewAuditTo(nopCloser{buf})
	if policy == nil {
		policy = SingleOperator{}
	}
	s, err := New(&fakeSource{agents: []app.Agent{agent("p1")}}, Config{
		Provider: NewPasswordProvider("alice", "correct-horse", DirectIP),
		Policy:   policy,
		Assets:   fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("<head></head>")}},
		Logger:   slog.New(slog.DiscardHandler),
		Writer:   wr,
		Audit:    audit,
	})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	return s, buf
}

// Writes without an audit sink must be refused at construction. An
// internet-reachable path into a terminal with no record of who used it is the
// exact combination this must never allow.
func TestWritesRequireAudit(t *testing.T) {
	_, err := New(&fakeSource{}, Config{
		Provider: NewPasswordProvider("a", "b", DirectIP),
		Policy:   SingleOperator{},
		Writer:   &fakeWriter{},
	})
	if err == nil {
		t.Fatal("a Writer without an Audit was accepted")
	}
	if !strings.Contains(err.Error(), "Audit") {
		t.Errorf("error should name the missing Audit, got %v", err)
	}
}

func TestWriteEndpointsRequireAuth(t *testing.T) {
	wr := &fakeWriter{}
	s, _ := writeServer(t, nil, wr)

	for _, path := range []string{
		"/api/panes/p1/respond", "/api/panes/p1/keys",
		"/api/panes/p1/focus", "/api/panes/p1/interrupt",
	} {
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{}`))
		rr := httptest.NewRecorder()
		s.routes().ServeHTTP(rr, req)
		if rr.Code != http.StatusUnauthorized {
			t.Errorf("POST %s unauthenticated = %d, want 401", path, rr.Code)
		}
	}
	if len(wr.calls) != 0 {
		t.Errorf("unauthenticated requests reached the pane: %v", wr.calls)
	}
}

func post(t *testing.T, s *Server, c *http.Cookie, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if c != nil {
		req.AddCookie(c)
	}
	rr := httptest.NewRecorder()
	s.routes().ServeHTTP(rr, req)
	return rr
}

func TestWriteHappyPath(t *testing.T) {
	wr := &fakeWriter{}
	s, auditBuf := writeServer(t, nil, wr)
	c := login(t, s, "alice", "correct-horse")

	if rr := post(t, s, c, "/api/panes/p1/respond", `{"text":"yes, single permission"}`); rr.Code != http.StatusOK {
		t.Fatalf("respond = %d: %s", rr.Code, rr.Body.String())
	}
	if rr := post(t, s, c, "/api/panes/p1/interrupt", `{}`); rr.Code != http.StatusOK {
		t.Fatalf("interrupt = %d: %s", rr.Code, rr.Body.String())
	}
	want := []string{"respond:p1:yes, single permission", "interrupt:p1"}
	if strings.Join(wr.calls, "|") != strings.Join(want, "|") {
		t.Errorf("calls = %v, want %v", wr.calls, want)
	}

	// Both actions must be in the audit log, attributed and allowed.
	lines := strings.Split(strings.TrimSpace(auditBuf.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("audit has %d lines, want 2: %s", len(lines), auditBuf.String())
	}
	var e app.AuditEntry
	if err := json.Unmarshal([]byte(lines[0]), &e); err != nil {
		t.Fatalf("audit line is not JSON: %v", err)
	}
	if e.Actor != "alice" || e.Action != "respond" || e.PaneID != "p1" || !e.Allowed {
		t.Errorf("audit entry = %+v", e)
	}
	if !strings.Contains(e.Detail, "yes, single permission") {
		t.Errorf("audit did not record what was sent: %q", e.Detail)
	}
}

// A policy refusal must not reach the pane, must look identical to a missing
// pane, and must still be recorded.
func TestWriteDeniedByPolicy(t *testing.T) {
	wr := &fakeWriter{}
	s, auditBuf := writeServer(t, denyAll{}, wr)
	c := login(t, s, "alice", "correct-horse")

	rr := post(t, s, c, "/api/panes/p1/respond", `{"text":"yes"}`)
	if rr.Code != http.StatusNotFound {
		t.Errorf("denied write = %d, want 404 (indistinguishable from missing)", rr.Code)
	}
	if len(wr.calls) != 0 {
		t.Errorf("policy-denied write still reached the pane: %v", wr.calls)
	}

	var e app.AuditEntry
	if err := json.Unmarshal([]byte(strings.TrimSpace(auditBuf.String())), &e); err != nil {
		t.Fatalf("refusal not audited: %v", err)
	}
	if e.Allowed {
		t.Error("refusal recorded as allowed")
	}
}

// Writing to a pane the store does not know must fail identically.
func TestWriteToUnknownPane(t *testing.T) {
	wr := &fakeWriter{}
	s, _ := writeServer(t, nil, wr)
	c := login(t, s, "alice", "correct-horse")

	if rr := post(t, s, c, "/api/panes/nope/respond", `{"text":"yes"}`); rr.Code != http.StatusNotFound {
		t.Errorf("unknown pane = %d, want 404", rr.Code)
	}
	if len(wr.calls) != 0 {
		t.Errorf("write to unknown pane reached the writer: %v", wr.calls)
	}
}

// The backend guard rejects payloads outside the allowlist; the web layer must
// surface that rather than bypassing it.
func TestWriteGuardErrorSurfaces(t *testing.T) {
	wr := &fakeWriter{err: errors.New("input not allowed: response \"rm -rf /\" is not in the allowlist")}
	s, auditBuf := writeServer(t, nil, wr)
	c := login(t, s, "alice", "correct-horse")

	rr := post(t, s, c, "/api/panes/p1/respond", `{"text":"rm -rf /"}`)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("guard refusal = %d, want 400", rr.Code)
	}
	if !strings.Contains(auditBuf.String(), `"allowed":false`) {
		t.Error("guard refusal was not audited as denied")
	}
}

func TestWriteRejectsOversizedAndMalformedBody(t *testing.T) {
	wr := &fakeWriter{}
	s, _ := writeServer(t, nil, wr)
	c := login(t, s, "alice", "correct-horse")

	if rr := post(t, s, c, "/api/panes/p1/respond", `{"text":`); rr.Code != http.StatusBadRequest {
		t.Errorf("malformed body = %d, want 400", rr.Code)
	}
	if rr := post(t, s, c, "/api/panes/p1/respond", `{"text":"y","evil":1}`); rr.Code != http.StatusBadRequest {
		t.Errorf("unknown field = %d, want 400 (DisallowUnknownFields)", rr.Code)
	}
	big := `{"text":"` + strings.Repeat("a", 32<<10) + `"}`
	if rr := post(t, s, c, "/api/panes/p1/respond", big); rr.Code != http.StatusBadRequest {
		t.Errorf("oversized body = %d, want 400", rr.Code)
	}
	if len(wr.calls) != 0 {
		t.Errorf("bad requests reached the pane: %v", wr.calls)
	}
}

// With no Writer the routes must not exist at all.
func TestReadOnlyServerHasNoWriteRoutes(t *testing.T) {
	s := testServer(t, &fakeSource{agents: []app.Agent{agent("p1")}}, nil)
	c := login(t, s, "alice", "correct-horse")

	for _, path := range []string{
		"/api/panes/p1/respond", "/api/panes/p1/keys",
		"/api/panes/p1/focus", "/api/panes/p1/interrupt",
	} {
		rr := post(t, s, c, path, `{"text":"yes"}`)
		if rr.Code >= 200 && rr.Code < 300 {
			t.Errorf("POST %s succeeded on a read-only server (%d)", path, rr.Code)
		}
	}
}

// readOnly must report the truth so the UI matches what the server accepts.
func TestSessionReportsWriteMode(t *testing.T) {
	check := func(s *Server, want bool) {
		t.Helper()
		c := login(t, s, "alice", "correct-horse")
		req := httptest.NewRequest(http.MethodGet, "/api/session", nil)
		req.AddCookie(c)
		rr := httptest.NewRecorder()
		s.routes().ServeHTTP(rr, req)
		body, _ := io.ReadAll(rr.Body)
		var got struct {
			ReadOnly bool `json:"readOnly"`
		}
		if err := json.Unmarshal(body, &got); err != nil {
			t.Fatal(err)
		}
		if got.ReadOnly != want {
			t.Errorf("readOnly = %v, want %v", got.ReadOnly, want)
		}
	}
	rw, _ := writeServer(t, nil, &fakeWriter{})
	check(rw, false)
	check(testServer(t, &fakeSource{}, nil), true)
}
