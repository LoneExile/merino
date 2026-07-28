//go:build live

package app

import (
	"context"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/LoneExile/merino/internal/herdr"
)

// Live proof that SendText on an agent pane goes through agent.prompt and that
// a slash command is accepted on the tunnel-test session.
//
//	HERDR_SOCK=~/.config/herdr/sessions/tunnel-test/herdr.sock \
//	  go test -tags=live ./internal/app -run TestLiveSendTextSlash -v -count=1
func TestLiveSendTextSlash(t *testing.T) {
	sock := os.Getenv("HERDR_SOCK")
	if sock == "" {
		t.Skip("HERDR_SOCK not set")
	}
	cli := herdr.New(sock)
	s := NewAgentsService(cli, slog.New(slog.DiscardHandler), nil, nil)
	s.ctx = context.Background()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	panes, err := cli.ListPanes(ctx)
	if err != nil {
		t.Fatalf("ListPanes: %v", err)
	}
	s.store.Replace(panes)

	// Prefer one of each harness when present; always hit at least one.
	wantKinds := []string{"omp", "pi", "claude", "grok"}
	byKind := map[string]string{}
	for _, p := range panes {
		// idle or done both accept typed input; working/blocked are mid-turn.
		if !p.IsAgent() || (p.AgentStatus != herdr.StatusIdle && p.AgentStatus != herdr.StatusDone) {
			continue
		}
		k := p.Agent
		if k == "" {
			k = p.DisplayAgent
		}
		if _, ok := byKind[k]; !ok {
			byKind[k] = p.PaneID
		}
	}
	if len(byKind) == 0 {
		t.Fatal("no idle/done agent panes in session")
	}
	t.Logf("ready agents: %v", byKind)

	var tested int
	for _, kind := range wantKinds {
		paneID, ok := byKind[kind]
		if !ok {
			t.Logf("skip %s: not present", kind)
			continue
		}
		tested++
		_ = cli.SendKeys(ctx, paneID, "Escape")
		time.Sleep(150 * time.Millisecond)
		if err := s.SendText(paneID, "/help"); err != nil {
			t.Errorf("%s (%s) SendText(/help): %v", kind, paneID, err)
			continue
		}
		time.Sleep(2 * time.Second)
		text, err := cli.ReadPane(ctx, paneID, 50)
		if err != nil {
			t.Errorf("%s ReadPane: %v", kind, err)
			continue
		}
		low := strings.ToLower(text)
		okHit := strings.Contains(low, "help") ||
			strings.Contains(low, "command") ||
			strings.Contains(low, "shortcut") ||
			strings.Contains(low, "/help") ||
			strings.Contains(low, "working") ||
			strings.Contains(low, "tutorial")
		if !okHit {
			t.Errorf("%s screen after /help unexpected:\n%s", kind, text)
			continue
		}
		t.Logf("OK %s /help on %s", kind, paneID)
		_ = cli.SendKeys(ctx, paneID, "Escape")
	}
	if tested == 0 {
		t.Fatal("none of omp/pi/claude/grok were ready")
	}
}
