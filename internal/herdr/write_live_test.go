package herdr_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/LoneExile/herdr-tunnel/internal/herdr"
)

// The write path (send_text / send_keys) is the riskiest surface in the app:
// it is unrestricted input to a live terminal. These tests exercise it against
// a throwaway tab created and destroyed by the test, never against a pane the
// user is working in.

type probePane struct {
	client *herdr.Client
	tabID  string
	paneID string
}

const probeLabel = "herdr-tunnel-selftest"

// newProbePane creates an isolated tab and returns its pane. Cleanup is
// registered with t.Cleanup so the tab is destroyed even if the test fails.
func newProbePane(t *testing.T, c *herdr.Client) *probePane {
	t.Helper()
	ctx := context.Background()

	var created struct {
		Tab struct {
			TabID string `json:"tab_id"`
		} `json:"tab"`
	}
	params := map[string]any{"focus": false, "label": probeLabel}
	if err := c.Call(ctx, "tab.create", params, &created); err != nil {
		t.Fatalf("tab.create: %v", err)
	}
	p := &probePane{client: c, tabID: created.Tab.TabID}

	t.Cleanup(func() {
		if p.tabID == "" {
			return
		}
		if err := c.Call(context.Background(), "tab.close",
			map[string]string{"tab_id": p.tabID}, nil); err != nil {
			t.Errorf("cleanup: tab.close %s: %v", p.tabID, err)
		}
	})

	// The pane appears asynchronously after the tab is created.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		panes, err := c.ListPanes(ctx)
		if err != nil {
			t.Fatalf("pane.list: %v", err)
		}
		for _, pane := range panes {
			if pane.TabID == p.tabID {
				p.paneID = pane.PaneID
				return p
			}
		}
		time.Sleep(150 * time.Millisecond)
	}
	t.Fatalf("probe pane never appeared for tab %s", p.tabID)
	return nil
}

// send_text must be accepted by the server for a valid pane.
//
// This deliberately does NOT assert the text echoes back: a freshly created
// pane's shell may not have started, and herdr reports empty output for it.
// Echo timing is the shell's business, not this client's contract.
func TestLiveSendTextAccepted(t *testing.T) {
	c := liveClient(t)
	p := newProbePane(t, c)

	if err := c.SendText(context.Background(), p.paneID, "# selftest\n"); err != nil {
		t.Fatalf("send_text: %v", err)
	}
}

// ReadPane must return the pane's text, not an empty string.
//
// Uses ReadVisible deliberately: ReadRecent returns output since the last
// read, so it is empty for any settled pane and would make this assertion
// pass or fail depending on whether an agent happened to be mid-output.
//
// Regression test: pane.read nests its payload as
// {"type":"pane_read","read":{"text":...}}. An earlier implementation looked
// for a top-level "text" field and silently returned "" for every pane, which
// no unit test could catch. Read against a pane that genuinely has output.
func TestLiveReadPaneReturnsActualText(t *testing.T) {
	c := liveClient(t)
	ctx := context.Background()

	panes, err := c.ListAgentPanes(ctx)
	if err != nil {
		t.Fatalf("pane.list: %v", err)
	}
	if len(panes) == 0 {
		t.Skip("no agent panes with output to read")
	}

	var sawText bool
	for _, pane := range panes {
		full, err := c.ReadPaneFull(ctx, pane.PaneID, herdr.ReadVisible, 50)
		if err != nil {
			t.Fatalf("pane.read %s: %v", pane.PaneID, err)
		}
		if full.PaneID != pane.PaneID {
			t.Errorf("read returned pane_id %q, want %q — payload unwrapped from the wrong level",
				full.PaneID, pane.PaneID)
		}
		if full.Text != "" {
			sawText = true
			if strings.Contains(full.Text, "\x1b[") {
				t.Errorf("pane %s: raw ANSI escapes despite strip_ansi=true", pane.PaneID)
			}
		}
	}
	if !sawText {
		t.Errorf("every one of %d agent panes read back empty; ReadPane is likely unwrapping the wrong field", len(panes))
	}
}

// Every key the guard allowlists must be accepted by the server. A key that
// herdr rejects is dead code in the UI: the button exists but never works.
func TestLiveAllowlistedKeysAreAccepted(t *testing.T) {
	c := liveClient(t)
	p := newProbePane(t, c)
	ctx := context.Background()

	// Mirror of app.safeKeys. Kept here rather than imported to avoid an
	// import cycle; a divergence shows up as a failure, which is the point.
	keys := []string{
		"y", "n", "a",
		"Enter", "enter",
		"Tab", "Space",
		"Backspace", "backspace",
		"esc", "escape", "Escape",
		"Up", "Down", "Left", "Right",
		"Ctrl+c", "C-c", "ctrl+c",
	}
	for _, k := range keys {
		if err := c.SendKeys(ctx, p.paneID, k); err != nil {
			t.Errorf("allowlisted key %q rejected by herdr: %v", k, err)
		}
	}
}

// Guards against re-adding a key name herdr does not understand.
func TestLiveKnownBadKeysAreRejected(t *testing.T) {
	c := liveClient(t)
	p := newProbePane(t, c)
	ctx := context.Background()

	for _, k := range []string{"BSpace", "^C", "Home", "End", "PageUp"} {
		if err := c.SendKeys(ctx, p.paneID, k); err == nil {
			t.Errorf("key %q was accepted; the allowlist comments claim herdr rejects it", k)
		}
	}
}

// Writing to a pane that does not exist must fail rather than silently
// succeed, since the guard's unknown-pane check depends on that contract.
func TestLiveWriteToUnknownPaneFails(t *testing.T) {
	c := liveClient(t)
	ctx := context.Background()

	if err := c.SendText(ctx, "w0:pDOESNOTEXIST", "hello\n"); err == nil {
		t.Error("send_text to a nonexistent pane unexpectedly succeeded")
	}
}
