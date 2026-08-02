package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/LoneExile/merino/internal/app"
)

// TestHealthzReachableWithoutSession is the load-bearing assertion: a probe
// behind the login wall is useless. Swap s.public(s.handleHealthz) for
// s.authed(s.handleHealthz) in routes() and this test goes red — verified by
// hand while writing it.
func TestHealthzReachableWithoutSession(t *testing.T) {
	s := testServer(t, &fakeSource{conn: app.Conn{Connected: true}}, nil)
	srv := httptest.NewServer(s.routes())
	defer srv.Close()

	client := &http.Client{
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	resp, err := client.Get(srv.URL + "/healthz")
	if err != nil {
		t.Fatalf("GET /healthz: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /healthz with no session = %d, want 200 — the endpoint must not sit behind auth", resp.StatusCode)
	}
	var got healthzResponse
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if got.Status != "ok" {
		t.Errorf("status = %q, want ok", got.Status)
	}
}

// TestHealthzOK covers the connected path: 200 with status "ok" and the herd
// fields populated from Connection().
func TestHealthzOK(t *testing.T) {
	src := &fakeSource{
		agents: []app.Agent{agent("p1"), agent("p2")},
		conn: app.Conn{
			Connected: true,
			Version:   "1.2.3",
			Protocol:  7,
			Socket:    "/Users/alice/.config/herdr/herdr.sock",
		},
	}
	s := testServer(t, src, nil)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)

	s.handleHealthz(rec, req, Identity{})

	if rec.Code != http.StatusOK {
		t.Fatalf("status code = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}

	var got healthzResponse
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if got.Status != "ok" {
		t.Errorf("status = %q, want ok", got.Status)
	}
	if !got.Herd.Connected {
		t.Error("herd.connected = false, want true")
	}
	if got.Herd.Version != "1.2.3" {
		t.Errorf("herd.version = %q, want 1.2.3", got.Herd.Version)
	}
	if got.Herd.Protocol != 7 {
		t.Errorf("herd.protocol = %d, want 7", got.Herd.Protocol)
	}
	if got.Herd.Error != "" {
		t.Errorf("herd.error = %q, want empty when connected", got.Herd.Error)
	}
	if got.Agents != 2 {
		t.Errorf("agents = %d, want 2", got.Agents)
	}

	// The socket path is a filesystem path — it must never reach an
	// unauthenticated caller. Assert against the raw body, not just the
	// decoded struct, so this test would fail even if a future edit added
	// a Socket field back to healthzHerd but forgot to populate it in a way
	// that happened to stay empty.
	if raw := rec.Body.String(); jsonContains(raw, src.conn.Socket) {
		t.Errorf("body leaks the herd socket path: %s", raw)
	}
}

// jsonContains reports whether needle appears verbatim in haystack. A tiny
// helper so the leak check above reads as intent, not a raw strings.Contains
// call buried in assertion noise.
func jsonContains(haystack, needle string) bool {
	return needle != "" && len(haystack) >= len(needle) && indexOf(haystack, needle) >= 0
}

func indexOf(haystack, needle string) int {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return i
		}
	}
	return -1
}

// TestHealthzDegraded covers the disconnected path: still 200 (see the
// comment on handleHealthz for why), status "degraded", and the herd error
// surfaced so an operator curl'ing the endpoint can see why.
func TestHealthzDegraded(t *testing.T) {
	src := &fakeSource{
		conn: app.Conn{
			Connected: false,
			Error:     "dial unix /tmp/herdr.sock: connect: no such file or directory",
		},
	}
	s := testServer(t, src, nil)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)

	s.handleHealthz(rec, req, Identity{})

	if rec.Code != http.StatusOK {
		t.Fatalf("status code = %d, want 200 even when the herd is unreachable — the process itself is healthy", rec.Code)
	}

	var got healthzResponse
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if got.Status != "degraded" {
		t.Errorf("status = %q, want degraded", got.Status)
	}
	if got.Herd.Connected {
		t.Error("herd.connected = true, want false")
	}
	if got.Herd.Error != src.conn.Error {
		t.Errorf("herd.error = %q, want %q", got.Herd.Error, src.conn.Error)
	}
}
