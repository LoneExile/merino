package herdr_test

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/LoneExile/herdr-tunnel/internal/herdr"
)

// scriptedPane is a herdr socket that answers pane.read with a scripted
// sequence of screens, one per call, repeating the last forever.
//
// It exists to cover the contract that a live probe caught and the previous
// fake could not: the earlier implementation subscribed to
// pane.output_matched, which delivers exactly ONE event per subscription
// against a real herdr and then goes silent. Its test scripted a stream of
// events onto the wire, so it passed while the feature was dead in practice.
// This fake models what herdr actually offers — a readable screen — so a
// regression to any once-only mechanism fails here.
type scriptedPane struct {
	path string

	mu      sync.Mutex
	screens []string
	reads   int
}

func newScriptedPane(t *testing.T, screens ...string) *scriptedPane {
	t.Helper()
	dir, err := os.MkdirTemp("", "sp")
	if err != nil {
		t.Fatalf("tempdir: %v", err)
	}
	p := &scriptedPane{path: filepath.Join(dir, "s.sock"), screens: screens}

	ln, err := net.Listen("unix", p.path)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { ln.Close(); os.RemoveAll(dir) })

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go p.serve(conn)
		}
	}()
	return p
}

func (p *scriptedPane) next() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	i := p.reads
	p.reads++
	if i >= len(p.screens) {
		i = len(p.screens) - 1
	}
	return p.screens[i]
}

func (p *scriptedPane) readCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.reads
}

func (p *scriptedPane) serve(conn net.Conn) {
	defer conn.Close()
	sc := bufio.NewScanner(conn)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		var req struct {
			ID     string `json:"id"`
			Method string `json:"method"`
		}
		if err := json.Unmarshal(sc.Bytes(), &req); err != nil {
			return
		}
		var resp any
		switch req.Method {
		case "pane.read":
			resp = map[string]any{"id": req.ID, "result": map[string]any{
				"read": map[string]any{"type": "pane_read", "text": p.next()},
			}}
		default:
			resp = map[string]any{"id": req.ID, "result": map[string]any{"type": "ok"}}
		}
		b, _ := json.Marshal(resp)
		if _, err := conn.Write(append(b, '\n')); err != nil {
			return
		}
	}
}

// collect runs StreamPaneOutput until it has seen want payloads or ctx expires.
func collect(t *testing.T, sock string, want int, budget time.Duration) []string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), budget)
	defer cancel()

	var mu sync.Mutex
	var got []string
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = herdr.New(sock).StreamPaneOutput(ctx, "w1:p1", 200, func(s string) {
			mu.Lock()
			got = append(got, s)
			n := len(got)
			mu.Unlock()
			if n >= want {
				cancel()
			}
		})
	}()
	<-done
	mu.Lock()
	defer mu.Unlock()
	return append([]string(nil), got...)
}

// The whole point of the feature: a watcher keeps receiving as the screen keeps
// changing. A once-only mechanism delivers the first screen and then nothing,
// which is exactly the bug this replaces.
func TestStreamPaneOutputKeepsDeliveringAsScreenChanges(t *testing.T) {
	p := newScriptedPane(t, "screen one", "screen two", "screen three")

	got := collect(t, p.path, 3, 5*time.Second)

	if len(got) < 3 {
		t.Fatalf("delivered %d screens (%q), want 3 — a stream that stops after "+
			"the first change is the one-shot bug", len(got), got)
	}
	for i, want := range []string{"screen one", "screen two", "screen three"} {
		if got[i] != want {
			t.Errorf("screen %d = %q, want %q", i, got[i], want)
		}
	}
}

// An unchanged screen must not be pushed. On a phone over a tunnel, re-sending
// an identical terminal three times a second is the difference between an idle
// dashboard and a data bill.
func TestStreamPaneOutputSuppressesUnchangedScreens(t *testing.T) {
	p := newScriptedPane(t, "same", "same", "same", "same", "same")

	got := collect(t, p.path, 2, 2500*time.Millisecond)

	if len(got) != 1 {
		t.Errorf("delivered %d payloads for an unchanging screen (%q), want 1", len(got), got)
	}
	if p.readCount() < 3 {
		t.Errorf("polled only %d times in the window — the test is not exercising "+
			"the suppression path", p.readCount())
	}
}

// Cancelling must stop the poll loop, or every closed browser tab leaks a
// goroutine reading a socket forever.
func TestStreamPaneOutputStopsOnCancel(t *testing.T) {
	p := newScriptedPane(t, "a", "b", "c")
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() { done <- herdr.New(p.path).StreamPaneOutput(ctx, "w1:p1", 200, func(string) {}) }()

	time.Sleep(700 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("cancel returned %v, want nil", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("StreamPaneOutput did not return within 2s of cancel")
	}

	before := p.readCount()
	time.Sleep(700 * time.Millisecond)
	if after := p.readCount(); after != before {
		t.Errorf("kept polling after cancel: %d -> %d reads", before, after)
	}
}

// The rename wire field is "label", not "name".
//
// This is a regression test for a bug that shipped green: the params were
// written as {"pane_id":…,"name":…} by analogy with the other calls. Against a
// real herdr, tab.rename and workspace.rename reject that with `missing field
// label`, while pane.rename — where label is optional — returns success and
// renames nothing. A test asserting "the call was made" would have passed on
// all three; only asserting the actual field catches it.
func TestRenameSendsLabelNotName(t *testing.T) {
	f := newFakeHerdr(t, "")
	c := f.client(t)
	ctx := context.Background()

	if err := c.RenamePane(ctx, "w1:p1", "alpha"); err != nil {
		t.Fatalf("rename pane: %v", err)
	}
	if err := c.RenameTab(ctx, "w1:t1", "beta"); err != nil {
		t.Fatalf("rename tab: %v", err)
	}
	if err := c.RenameWorkspace(ctx, "w1", "gamma"); err != nil {
		t.Fatalf("rename workspace: %v", err)
	}

	want := []struct {
		method string
		idKey  string
		id     string
		label  string
	}{
		{"pane.rename", "pane_id", "w1:p1", "alpha"},
		{"tab.rename", "tab_id", "w1:t1", "beta"},
		{"workspace.rename", "workspace_id", "w1", "gamma"},
	}
	if len(f.calls) != len(want) {
		t.Fatalf("made %d calls, want %d", len(f.calls), len(want))
	}
	for i, w := range want {
		got := f.calls[i]
		if got.Method != w.method {
			t.Errorf("call %d method = %q, want %q", i, got.Method, w.method)
		}
		var params map[string]any
		if err := json.Unmarshal(got.Params, &params); err != nil {
			t.Fatalf("call %d params: %v", i, err)
		}
		if _, bad := params["name"]; bad {
			t.Errorf("%s sent a \"name\" field — herdr wants \"label\"", w.method)
		}
		if params["label"] != w.label {
			t.Errorf("%s label = %v, want %q", w.method, params["label"], w.label)
		}
		if params[w.idKey] != w.id {
			t.Errorf("%s %s = %v, want %q", w.method, w.idKey, params[w.idKey], w.id)
		}
	}
}
