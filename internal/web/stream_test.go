package web

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/LoneExile/merino/internal/app"
)

// readSSEEvent reads one Server-Sent Events frame ("event: ...\ndata:
// ...\n\n"), skipping pure keepalive comment frames (": ping\n\n") so callers
// only ever see real events.
func readSSEEvent(t *testing.T, r *bufio.Reader) (event, data string) {
	t.Helper()
	for {
		event, data = "", ""
		for {
			line, err := r.ReadString('\n')
			if err != nil {
				t.Fatalf("read SSE frame: %v", err)
			}
			line = strings.TrimRight(line, "\r\n")
			if line == "" {
				break // end of frame
			}
			switch {
			case strings.HasPrefix(line, "event: "):
				event = strings.TrimPrefix(line, "event: ")
			case strings.HasPrefix(line, "data: "):
				data = strings.TrimPrefix(line, "data: ")
			}
		}
		if event != "" || data != "" {
			return event, data
		}
		// A comment-only frame (keepalive): read the next one.
	}
}

// The stream endpoint is authenticated exactly like every other API route: it
// must 401 before it ever touches the pane store.
func TestStreamRequiresAuth(t *testing.T) {
	s := testServer(t, &fakeSource{agents: []app.Agent{agent("p1")}}, nil)

	rr := httptest.NewRecorder()
	s.routes().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/panes/p1/stream", nil))
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("GET stream unauthenticated = %d, want 401", rr.Code)
	}
}

// A policy refusal must behave exactly like the one-shot output endpoint:
// 404, no leaked text, and the herdr subscription must never start.
func TestStreamRespectsPolicy(t *testing.T) {
	started := make(chan struct{})
	src := &fakeSource{agents: []app.Agent{agent("p1")}, text: "secret output", started: started}
	s := testServer(t, src, denyAll{})
	c := login(t, s, "alice", "correct-horse")

	req := httptest.NewRequest(http.MethodGet, "/api/panes/p1/stream", nil)
	req.AddCookie(c)
	rr := httptest.NewRecorder()
	s.routes().ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Errorf("denied pane stream = %d, want 404", rr.Code)
	}
	if strings.Contains(rr.Body.String(), "secret output") {
		t.Error("policy-denied pane stream leaked its output")
	}
	select {
	case <-started:
		t.Error("policy-denied stream still started a herdr subscription")
	default:
	}
}

// Connecting must paint the current screen immediately, before any live
// output event has occurred — that is the entire point of a snapshot.
func TestStreamEmitsInitialSnapshot(t *testing.T) {
	src := &fakeSource{agents: []app.Agent{agent("p1")}, text: "hello screen"}
	s := testServer(t, src, nil)
	ts := httptest.NewServer(s.routes())
	defer ts.Close()

	c := login(t, s, "alice", "correct-horse")
	req, err := http.NewRequest(http.MethodGet, ts.URL+"/api/panes/p1/stream", nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.AddCookie(c)

	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("GET stream: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("stream = %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Errorf("content-type = %q, want text/event-stream", ct)
	}

	event, data := readSSEEvent(t, bufio.NewReader(resp.Body))
	if event != "output" {
		t.Fatalf("first event = %q, want %q", event, "output")
	}
	var payload struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal([]byte(data), &payload); err != nil {
		t.Fatalf("decode data %q: %v", data, err)
	}
	if payload.Text != "hello screen" {
		t.Errorf("initial snapshot text = %q, want %q", payload.Text, "hello screen")
	}
}

// Bursts of live output must collapse to the latest text rather than
// flushing per event — otherwise a fast-scrolling pane (`seq 1 100000`) would
// flood the connection instead of settling on what is actually on screen.
func TestStreamCoalescesBurstsToLatest(t *testing.T) {
	src := &fakeSource{
		agents:       []app.Agent{agent("p1")},
		text:         "initial",
		streamEvents: []string{"line 1", "line 2", "line 3"},
	}
	s := testServer(t, src, nil)
	ts := httptest.NewServer(s.routes())
	defer ts.Close()

	c := login(t, s, "alice", "correct-horse")
	req, err := http.NewRequest(http.MethodGet, ts.URL+"/api/panes/p1/stream", nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.AddCookie(c)

	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("GET stream: %v", err)
	}
	defer resp.Body.Close()

	br := bufio.NewReader(resp.Body)
	if event, _ := readSSEEvent(t, br); event != "output" {
		t.Fatalf("first event = %q, want the initial snapshot", event)
	}

	// All three bursted events land well inside one 100ms coalescing window,
	// so exactly one further frame should surface, carrying only the last.
	event, data := readSSEEvent(t, br)
	if event != "output" {
		t.Fatalf("second event = %q, want %q", event, "output")
	}
	var payload struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal([]byte(data), &payload); err != nil {
		t.Fatalf("decode data %q: %v", data, err)
	}
	if payload.Text != "line 3" {
		t.Errorf("coalesced frame = %q, want %q (the latest, not an earlier member of the burst)", payload.Text, "line 3")
	}
}

// Disconnecting must tear down the herdr subscription. A dashboard opened
// from a phone that then locks or backgrounds the browser must not leave a
// subscription — and the herdr connection behind it — running forever.
func TestStreamCancelStopsSubscription(t *testing.T) {
	started := make(chan struct{})
	stopped := make(chan struct{})
	src := &fakeSource{agents: []app.Agent{agent("p1")}, text: "hello", started: started, stopped: stopped}
	s := testServer(t, src, nil)
	ts := httptest.NewServer(s.routes())
	defer ts.Close()

	c := login(t, s, "alice", "correct-horse")

	ctx, cancel := context.WithCancel(context.Background())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, ts.URL+"/api/panes/p1/stream", nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.AddCookie(c)

	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("GET stream: %v", err)
	}
	defer resp.Body.Close()

	// Wait for the herdr subscription to actually start before disconnecting,
	// so this proves teardown rather than racing a subscription that never
	// began.
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("StreamOutput was never called")
	}

	cancel()
	resp.Body.Close()

	select {
	case <-stopped:
	case <-time.After(2 * time.Second):
		t.Fatal("herdr subscription was not stopped after the client disconnected — goroutine leak")
	}
}
