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

// paneGetEnvelope is herdr's pane.get result shape, stated once so the two
// fakes in this package cannot drift from each other or from the wire: the
// pane is nested one level under "pane", beside a "type" tag reading
// "pane_info" (the live server's value — an earlier draft of this fixture
// said "pane_get", which herdr never sends). Decoding a top-level PaneInfo
// yields a zero value with no error — the same shape of silent failure
// pane.read's "read" nesting already caused once, and enough for a fixture to
// script a wire herdr never sends and still pass.
//
// A caller wanting a different pane mutates the returned map (see
// TestGetPaneLeavesIsAgentFalseForAShellPane) rather than restating it.
func paneGetEnvelope(paneID string, revision int64) map[string]any {
	return map[string]any{
		"type": "pane_info",
		"pane": map[string]any{
			"pane_id": paneID, "terminal_id": "t1",
			"workspace_id": "w1", "tab_id": "w1:t1",
			"agent": "omp", "agent_status": "working",
			"revision": revision,
		},
	}
}

// paneGetSocket answers every pane.get on a throwaway unix socket with result,
// encoded exactly as herdr would.
func paneGetSocket(t *testing.T, result map[string]any) string {
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
					b, _ := json.Marshal(map[string]any{"id": req.ID, "result": result})
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
	got, err := herdr.New(paneGetSocket(t, paneGetEnvelope("w1:p1", 4242))).GetPane(context.Background(), "w1:p1")
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
	// Agent and PaneID arrive in the same object, so this assertion rides on
	// the pane_id check above and a decode that lost Agent is caught there.
	// The branch worth its own fixture is the opposite one, below.
	if !got.IsAgent() {
		t.Fatalf("agent field lost in decode: %+v", got)
	}
}

// A pane herdr has no agent in carries no "agent" key at all — "Most panes in
// a typical session are plain shells; only agent panes are worth tracking"
// (PaneInfo.IsAgent's own doc). This is the branch the poll loop's callers
// depend on, and the reason the assertion above cannot carry it: Agent and
// PaneID are set by the same object, so the page that empties one empties the
// other and the pane_id check fires first.
func TestGetPaneLeavesIsAgentFalseForAShellPane(t *testing.T) {
	env := paneGetEnvelope("w1:p1", 7)
	delete(env["pane"].(map[string]any), "agent")

	got, err := herdr.New(paneGetSocket(t, env)).GetPane(context.Background(), "w1:p1")
	if err != nil {
		t.Fatalf("GetPane: %v", err)
	}
	if got.IsAgent() {
		t.Fatalf("IsAgent() = true for a pane with no agent key: %+v", got)
	}
	// The revision is decoded from that same nested object whether or not the
	// pane has an agent, which is what this asserts. 7 is not a value a shell
	// pane was measured to carry — a live shell pane held revision 0 while its
	// screen changed, which is why the poll loop refuses to gate on an
	// unchanged 0 (client.go's gate comment,
	// TestStreamKeepsReadingAPaneWithNoTrackedRevision). The non-zero value is
	// deliberate: a decoder that skipped the field and a field carrying 0 are
	// indistinguishable, so asserting 0 here would assert nothing.
	if got.Revision != 7 {
		t.Fatalf("revision = %d, want 7", got.Revision)
	}
}
