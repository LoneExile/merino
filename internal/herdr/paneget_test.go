package herdr_test

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/LoneExile/merino/internal/herdr"
)

// paneGetSocket answers pane.get with herdr's real envelope: the pane is
// nested one level under "pane", beside a "type" tag. Decoding a top-level
// PaneInfo yields a zero value with no error — the same shape of silent
// failure pane.read's "read" nesting already caused once.
func paneGetSocket(t *testing.T, revision int64) string {
	t.Helper()
	// Not t.TempDir(): that embeds the test name and pushes the socket path
	// past macOS's 104-byte AF_UNIX cap, which fails the listen with EINVAL.
	dir, err := os.MkdirTemp("", "gp")
	if err != nil {
		t.Fatalf("tempdir: %v", err)
	}
	path := filepath.Join(dir, "g.sock")
	ln, err := net.Listen("unix", path)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close(); _ = os.RemoveAll(dir) })

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				sc := bufio.NewScanner(conn)
				for sc.Scan() {
					var req struct {
						ID string `json:"id"`
					}
					if err := json.Unmarshal(sc.Bytes(), &req); err != nil {
						return
					}
					b, _ := json.Marshal(map[string]any{"id": req.ID, "result": map[string]any{
						"type": "pane_get",
						"pane": map[string]any{
							"pane_id": "w1:p1", "terminal_id": "t1",
							"workspace_id": "w1", "tab_id": "w1:t1",
							"agent": "omp", "agent_status": "working",
							"revision": revision,
						},
					}})
					if _, err := conn.Write(append(b, '\n')); err != nil {
						return
					}
				}
			}()
		}
	}()
	return path
}

func TestGetPaneDecodesTheNestedEnvelope(t *testing.T) {
	got, err := herdr.New(paneGetSocket(t, 4242)).GetPane(context.Background(), "w1:p1")
	if err != nil {
		t.Fatalf("GetPane: %v", err)
	}
	if got.PaneID != "w1:p1" {
		t.Fatalf("pane_id = %q, want w1:p1 — the payload nests under \"pane\"", got.PaneID)
	}
	// Revision is the whole point: it is what the poll loop compares.
	if got.Revision != 4242 {
		t.Fatalf("revision = %d, want 4242", got.Revision)
	}
	if !got.IsAgent() {
		t.Fatalf("agent field lost in decode: %+v", got)
	}
}
