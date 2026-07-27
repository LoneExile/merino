package herdr_test

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/LoneExile/herdr-tunnel/internal/herdr"
)

// These tests run against a real herdr server. They skip when no socket is
// present so CI stays green on machines without herdr.
func liveClient(t *testing.T) *herdr.Client {
	t.Helper()
	sock := herdr.DefaultSocket()
	if _, err := os.Stat(sock); err != nil {
		t.Skipf("no herdr socket at %s", sock)
	}
	return herdr.New(sock)
}

func TestLivePingProtocol(t *testing.T) {
	c := liveClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	r, err := c.CheckCompatible(ctx)
	if err != nil {
		if errors.Is(err, herdr.ErrProtocolMismatch) {
			t.Fatalf("protocol drift, client needs updating: %v", err)
		}
		t.Fatalf("ping: %v", err)
	}
	if r.Type != "pong" {
		t.Errorf("type = %q, want pong", r.Type)
	}
	t.Logf("herdr %s protocol %d caps=%v", r.Version, r.Protocol, r.Capabilities)
}

// A second call must succeed even though the server closes each connection
// after responding. This guards the one-shot dial-per-call invariant.
func TestLiveSequentialCallsDoNotReuseConnection(t *testing.T) {
	c := liveClient(t)
	ctx := context.Background()
	for i := range 3 {
		if _, err := c.Ping(ctx); err != nil {
			t.Fatalf("ping %d: %v", i, err)
		}
	}
}

func TestLiveListPanes(t *testing.T) {
	c := liveClient(t)
	ctx := context.Background()

	all, err := c.ListPanes(ctx)
	if err != nil {
		t.Fatalf("pane.list: %v", err)
	}
	if len(all) == 0 {
		t.Skip("no panes in session")
	}
	agents, err := c.ListAgentPanes(ctx)
	if err != nil {
		t.Fatalf("agent panes: %v", err)
	}
	if len(agents) > len(all) {
		t.Fatalf("agent panes %d exceeds total %d", len(agents), len(all))
	}
	for _, p := range all {
		if p.PaneID == "" || p.WorkspaceID == "" || p.TabID == "" {
			t.Errorf("pane missing required identity fields: %+v", p)
		}
		if p.AgentStatus == "" {
			t.Errorf("pane %s has empty agent_status", p.PaneID)
		}
	}
	t.Logf("%d panes, %d agent panes", len(all), len(agents))
}

// Per-pane kinds must carry a pane_id. Sending one without must be rejected,
// and Stream must not retry a rejection forever.
func TestLiveSubscribeRejectsPerPaneKindWithoutPaneID(t *testing.T) {
	c := liveClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	err := c.Subscribe(ctx,
		[]herdr.Subscription{{Type: herdr.SubPaneAgentStatusChanged}}, // no pane_id
		nil,
		func(herdr.Event) {},
	)
	if !errors.Is(err, herdr.ErrSubscribeRejected) {
		t.Fatalf("got %v, want ErrSubscribeRejected", err)
	}
}

// A global subscription must ack and hold the connection open.
func TestLiveSubscribeGlobalStaysOpen(t *testing.T) {
	c := liveClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()

	ready := make(chan struct{})
	events := 0
	err := c.Subscribe(ctx,
		[]herdr.Subscription{
			herdr.GlobalSub(herdr.SubPaneCreated),
			herdr.GlobalSub(herdr.SubPaneClosed),
			herdr.GlobalSub(herdr.SubPaneUpdated),
		},
		func() { close(ready) },
		func(herdr.Event) { events++ },
	)
	select {
	case <-ready:
	default:
		t.Fatal("subscription never acked")
	}
	// Ending by context deadline is the expected outcome: the stream stayed
	// open until we cancelled it.
	if err != nil && !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("stream ended unexpectedly: %v", err)
	}
	t.Logf("observed %d events in 4s", events)
}
