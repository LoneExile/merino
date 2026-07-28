package app

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/LoneExile/merino/internal/herdr"
)

// SwitchSession must resolve id against the real session list before ever
// touching the active client or its background goroutines, so an unknown id
// leaves the current session completely undisturbed.
func TestSwitchSessionRejectsUnknownID(t *testing.T) {
	withHerdrHome(t)

	orig := herdr.New("/nonexistent/original.sock")
	s := NewAgentsService(orig, slog.New(slog.DiscardHandler), nil, nil)
	s.ctx = context.Background()

	if err := s.SwitchSession("does-not-exist"); !errors.Is(err, ErrUnknownSession) {
		t.Fatalf("SwitchSession(unknown) = %v, want ErrUnknownSession", err)
	}
	if s.currentClient() != orig {
		t.Error("client was replaced despite an unknown session id")
	}
}

// SendText must use agent.prompt for panes that host a coding agent so slash
// commands (/help, /status, …) work across omp/pi/claude/grok harnesses.
// Plain shell panes still go through send_text + Enter.
func TestSendTextRoutesAgentPanesThroughAgentPrompt(t *testing.T) {
	// Stand up a fake herdr that records methods.
	// macOS caps AF_UNIX path length (~104 bytes); t.TempDir() is too deep.
	sock := filepath.Join(os.TempDir(), fmt.Sprintf("ht-%d.sock", time.Now().UnixNano()%1_000_000_000))
	t.Cleanup(func() { _ = os.Remove(sock) })

	type call struct {
		Method string
		Params json.RawMessage
	}
	var (
		mu    sync.Mutex
		calls []call
	)

	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				sc := bufio.NewScanner(c)
				// allow large frames
				buf := make([]byte, 0, 64*1024)
				sc.Buffer(buf, 1024*1024)
				for sc.Scan() {
					var req struct {
						ID     string          `json:"id"`
						Method string          `json:"method"`
						Params json.RawMessage `json:"params"`
					}
					if json.Unmarshal(sc.Bytes(), &req) != nil {
						continue
					}
					mu.Lock()
					calls = append(calls, call{Method: req.Method, Params: req.Params})
					mu.Unlock()
					resp, _ := json.Marshal(map[string]any{"id": req.ID, "result": map[string]any{}})
					_, _ = c.Write(append(resp, '\n'))
				}
			}(conn)
		}
	}()

	cli := herdr.New(sock)
	s := NewAgentsService(cli, slog.New(slog.DiscardHandler), nil, nil)
	s.ctx = context.Background()
	s.store.Replace([]herdr.PaneInfo{
		{PaneID: "w1:pA", Agent: "omp", AgentStatus: herdr.StatusIdle},
		{PaneID: "w1:pS", Agent: "", AgentStatus: herdr.StatusUnknown},
	})

	if err := s.SendText("w1:pA", "/help"); err != nil {
		t.Fatalf("agent pane: %v", err)
	}
	if err := s.SendText("w1:pS", "echo hi"); err != nil {
		t.Fatalf("shell pane: %v", err)
	}

	// Wait briefly for both round-trips.
	deadline := time.Now().Add(2 * time.Second)
	for {
		mu.Lock()
		n := len(calls)
		mu.Unlock()
		if n >= 3 || time.Now().After(deadline) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	mu.Lock()
	defer mu.Unlock()
	methods := make([]string, 0, len(calls))
	for _, c := range calls {
		methods = append(methods, c.Method)
	}
	// Agent pane → agent.prompt only.
	// Shell pane → pane.send_text then pane.send_keys.
	wantPrefix := []string{"agent.prompt", "pane.send_text", "pane.send_keys"}
	if len(methods) < 3 {
		t.Fatalf("methods = %v, want at least %v", methods, wantPrefix)
	}
	for i, w := range wantPrefix {
		if methods[i] != w {
			t.Fatalf("methods = %v, want start with %v", methods, wantPrefix)
		}
	}
	var params map[string]any
	if err := json.Unmarshal(calls[0].Params, &params); err != nil {
		t.Fatal(err)
	}
	if params["target"] != "w1:pA" || params["text"] != "/help" {
		t.Fatalf("agent.prompt params = %v", params)
	}
}
