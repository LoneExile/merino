package herdr_test

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/LoneExile/merino/internal/herdr"
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

// The agent-name rule is herdr's own boundary, not merino's: an invalid name
// is rejected with invalid_agent_name before anything is typed into the pane.
// Pin it against the live server so the derived-name contract in
// internal/app (agentname_test.go) cannot silently drift with a herdr update.
func TestLiveAgentStartRejectsInvalidName(t *testing.T) {
	c := liveClient(t)
	p := newProbePane(t, c)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	err := c.StartAgent(ctx, p.paneID, "omp", "Bad Name")
	var apiErr *herdr.APIError
	if !errors.As(err, &apiErr) || apiErr.Code != "invalid_agent_name" {
		t.Fatalf("agent.start with invalid name = %v, want APIError invalid_agent_name", err)
	}
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

// Closing a tab destroys its panes but emits NO pane.closed event — only
// tab.closed. Merino's store depends on this: it subscribes to
// tab.closed/workspace.closed to drop panes (see types.go SubTabClosed), and
// would leak them forever if a herdr update started emitting pane.closed for
// closed tabs instead. Pin the behavior against the live server.
func TestLiveTabCloseEmitsNoPaneClosed(t *testing.T) {
	c := liveClient(t)
	p := newProbePane(t, c)

	var mu sync.Mutex
	paneClosed := false
	done := make(chan struct{})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		_ = c.Subscribe(ctx,
			[]herdr.Subscription{
				herdr.GlobalSub(herdr.SubPaneClosed),
				herdr.GlobalSub(herdr.SubTabClosed),
			},
			func() {},
			func(e herdr.Event) {
				mu.Lock()
				defer mu.Unlock()
				switch e.Event {
				case herdr.EvPaneClosed:
					paneClosed = true
				case herdr.EvTabClosed:
					close(done)
				}
			})
	}()

	// Let the subscription ack before the tab is torn down; a close that
	// races the subscribe is this test asserting nothing.
	time.Sleep(500 * time.Millisecond)
	if err := c.CloseTab(ctx, p.tabID); err != nil {
		t.Fatalf("tab.close: %v", err)
	}
	p.tabID = "" // already closed; the probe's cleanup must not close it again

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("tab_closed never arrived")
	}
	mu.Lock()
	defer mu.Unlock()
	if paneClosed {
		t.Fatal("herdr emitted pane.closed for a closed tab; the tab.closed/workspace.closed leak fix in types.go is obsolete")
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
