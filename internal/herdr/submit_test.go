package herdr_test

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/LoneExile/herdr-tunnel/internal/herdr"
)

// call is one request the client made to the socket.
type call struct {
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
}

// fakeHerdr is a herdr socket that records requests and answers them.
// It exists so the submit contract is covered in CI, where no herdr runs.
type fakeHerdr struct {
	path  string
	calls []call
	fail  string // method that should return an error
}

func newFakeHerdr(t *testing.T, failMethod string) *fakeHerdr {
	t.Helper()
	// Unix socket paths are length-limited, so keep it short.
	dir, err := os.MkdirTemp("", "fh")
	if err != nil {
		t.Fatalf("tempdir: %v", err)
	}
	f := &fakeHerdr{path: filepath.Join(dir, "s.sock"), fail: failMethod}

	ln, err := net.Listen("unix", f.path)
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
			go f.serve(conn)
		}
	}()
	return f
}

func (f *fakeHerdr) serve(conn net.Conn) {
	defer conn.Close()
	sc := bufio.NewScanner(conn)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		var req struct {
			ID     string          `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if err := json.Unmarshal(sc.Bytes(), &req); err != nil {
			return
		}
		f.calls = append(f.calls, call{Method: req.Method, Params: req.Params})

		var resp any
		if req.Method == f.fail {
			resp = map[string]any{
				"id": req.ID, "error": map[string]any{"code": "boom", "message": "refused"},
			}
		} else {
			resp = map[string]any{"id": req.ID, "result": map[string]any{"type": "ok"}}
		}
		b, _ := json.Marshal(resp)
		conn.Write(append(b, '\n'))
	}
}

func (f *fakeHerdr) client(t *testing.T) *herdr.Client {
	t.Helper()
	return herdr.New(f.path)
}

// methods returns the ordered method names the client called.
func (f *fakeHerdr) methods() []string {
	out := make([]string, 0, len(f.calls))
	for _, c := range f.calls {
		out = append(out, c.Method)
	}
	return out
}

// Submitting must type the text and then press Enter as a key.
//
// This is the CI-visible guard for a bug that a live agent exposed and no unit
// test could: appending "\n" to the text leaves it unsubmitted in a TUI's
// prompt, because a raw-mode reader expects CR. Asserting the call sequence
// catches a regression to the "\n" shortcut without needing a running herdr.
func TestSubmitTextTypesThenPressesEnter(t *testing.T) {
	f := newFakeHerdr(t, "")
	if err := f.client(t).SubmitText(context.Background(), "w1:p1", "run the tests"); err != nil {
		t.Fatalf("submit: %v", err)
	}

	want := []string{"pane.send_text", "pane.send_keys"}
	if got := f.methods(); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("call sequence = %v, want %v", got, want)
	}

	var textArgs struct {
		Text string `json:"text"`
	}
	json.Unmarshal(f.calls[0].Params, &textArgs)
	// The newline must be gone: it is the bug, and a TUI would render it as a
	// stray character rather than a submission.
	if strings.Contains(textArgs.Text, "\n") {
		t.Errorf("text carries a newline %q — Enter must be a key, not a character", textArgs.Text)
	}
	if textArgs.Text != "run the tests" {
		t.Errorf("text = %q, want %q", textArgs.Text, "run the tests")
	}

	var keyArgs struct {
		Keys []string `json:"keys"`
	}
	json.Unmarshal(f.calls[1].Params, &keyArgs)
	if len(keyArgs.Keys) != 1 || keyArgs.Keys[0] != "Enter" {
		t.Errorf("keys = %v, want [Enter]", keyArgs.Keys)
	}
}

// A failure to press Enter must say the text was already delivered.
//
// The two halves are not atomic. An error reading only "refused" would let an
// audit record imply nothing reached the pane, while the text is in fact
// sitting in the prompt waiting for someone to hit return.
func TestSubmitTextReportsPartialDelivery(t *testing.T) {
	f := newFakeHerdr(t, "pane.send_keys")
	err := f.client(t).SubmitText(context.Background(), "w1:p1", "hello")
	if err == nil {
		t.Fatal("submit succeeded despite send_keys failing")
	}
	if !strings.Contains(err.Error(), "left in the prompt") {
		t.Errorf("error %q does not say the text was delivered but unsubmitted", err)
	}
}

// If the text never lands, Enter must not be pressed: submitting an empty
// prompt to an agent awaiting approval is a real action taken on the user's
// behalf, from a call that already failed.
func TestSubmitTextDoesNotPressEnterWhenTextFails(t *testing.T) {
	f := newFakeHerdr(t, "pane.send_text")
	if err := f.client(t).SubmitText(context.Background(), "w1:p1", "hello"); err == nil {
		t.Fatal("submit succeeded despite send_text failing")
	}
	if got := f.methods(); len(got) != 1 || got[0] != "pane.send_text" {
		t.Errorf("calls = %v, want only [pane.send_text]", got)
	}
}

// AgentPrompt must hit the harness-aware agent.prompt method, not the
// send_text + Enter sequence used for plain shells.
func TestAgentPromptCallsAgentPrompt(t *testing.T) {
	f := newFakeHerdr(t, "")
	if err := f.client(t).AgentPrompt(context.Background(), "w1:p1", "/help"); err != nil {
		t.Fatalf("AgentPrompt: %v", err)
	}
	if got := f.methods(); len(got) != 1 || got[0] != "agent.prompt" {
		t.Fatalf("calls = %v, want [agent.prompt]", got)
	}
	// Params shape.
	var params map[string]any
	if err := json.Unmarshal(f.calls[0].Params, &params); err != nil {
		t.Fatalf("params: %v", err)
	}
	if params["target"] != "w1:p1" || params["text"] != "/help" {
		t.Fatalf("params = %v, want target=w1:p1 text=/help", params)
	}
}
