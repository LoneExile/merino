package app

import (
	"log/slog"
	"testing"

	"github.com/LoneExile/merino/internal/herdr"
)

// TestStoreNotifiesOnlyOnTransitionIntoBlocked proves the hook
// Store.SetOnBlocked registers is edge-triggered: it fires exactly once per
// transition into blocked, never while a pane merely stays blocked, and
// fires again for a genuinely new transition after leaving and re-entering
// the status.
//
// This is the load-bearing test for the whole push-notification feature: a
// hook that re-fired on every SetStatus call while a pane sat blocked would
// mean a phone getting paged again and again for one unresolved prompt.
func TestStoreNotifiesOnlyOnTransitionIntoBlocked(t *testing.T) {
	s := NewStore()
	s.Replace([]herdr.PaneInfo{pane("p1", "omp", herdr.StatusWorking)})

	var notified []Agent
	s.SetOnBlocked(func(a Agent) { notified = append(notified, a) })

	if !s.SetStatus("p1", herdr.StatusBlocked, "omp") {
		t.Fatal("status change should report changed")
	}
	if len(notified) != 1 {
		t.Fatalf("notified = %d times after first transition, want 1", len(notified))
	}
	if notified[0].PaneID != "p1" || notified[0].Status != herdr.StatusBlocked {
		t.Errorf("notified agent = %+v, want p1/blocked", notified[0])
	}

	// Re-asserting the same status is not a transition and must not re-fire,
	// however many times it happens — this is the "level" behaviour the hook
	// must reject.
	s.SetStatus("p1", herdr.StatusBlocked, "omp")
	s.SetStatus("p1", herdr.StatusBlocked, "omp")
	if len(notified) != 1 {
		t.Fatalf("notified = %d after re-asserting blocked, want still 1", len(notified))
	}

	// Leaving and re-entering blocked is a genuine second transition and
	// must fire again — it is a new edge, not a continuation of the first.
	s.SetStatus("p1", herdr.StatusWorking, "omp")
	s.SetStatus("p1", herdr.StatusBlocked, "omp")
	if len(notified) != 2 {
		t.Fatalf("notified = %d after a second real transition, want 2", len(notified))
	}
}

// TestStoreDoesNotNotifyForPaneDiscoveredAlreadyBlocked proves that Replace
// and UpsertPane only fire the hook for a pane whose PRIOR status is known
// and was not blocked — not for a pane that simply shows up already blocked
// (e.g. the very first pane.list at startup, or a brand new pane appearing
// mid-session). Without this, every restart with agents already sitting
// blocked would re-page the phone for old, unresolved prompts it already
// knows about.
func TestStoreDoesNotNotifyForPaneDiscoveredAlreadyBlocked(t *testing.T) {
	s := NewStore()
	var notified []Agent
	s.SetOnBlocked(func(a Agent) { notified = append(notified, a) })

	// Initial population: p1 shows up already blocked. The store has no
	// prior status for it to have transitioned from, so this is not an
	// observed transition and must not fire.
	if changed := s.Replace([]herdr.PaneInfo{pane("p1", "omp", herdr.StatusBlocked)}); !changed {
		t.Fatal("first Replace should report changed")
	}
	if len(notified) != 0 {
		t.Fatalf("notified = %d on initial population, want 0", len(notified))
	}

	// A later reconcile that finds a KNOWN pane now blocked — e.g. a status
	// change delivered only during the resubscribe gap Replace exists to
	// close — is a real, observed transition and must fire.
	s.Replace([]herdr.PaneInfo{pane("p1", "omp", herdr.StatusWorking)})
	if len(notified) != 0 {
		t.Fatalf("notified = %d after reverting to working, want 0", len(notified))
	}
	s.Replace([]herdr.PaneInfo{pane("p1", "omp", herdr.StatusBlocked)})
	if len(notified) != 1 {
		t.Fatalf("notified = %d after reconcile observes a real transition, want 1", len(notified))
	}

	// The same rule applies to UpsertPane: a brand new pane arriving already
	// blocked must not fire either.
	s.UpsertPane(pane("p2", "claude", herdr.StatusBlocked))
	if len(notified) != 1 {
		t.Fatalf("notified = %d after a NEW pane arrives already blocked, want still 1", len(notified))
	}
	s.UpsertPane(pane("p2", "claude", herdr.StatusWorking))
	s.UpsertPane(pane("p2", "claude", herdr.StatusBlocked))
	if len(notified) != 2 {
		t.Fatalf("notified = %d after UpsertPane observes a real transition, want 2", len(notified))
	}
}

// TestAgentsServiceAttachBlockedNotifier proves the wiring point main.go
// uses actually reaches the store's edge-detection rather than being a dead
// setter.
func TestAgentsServiceOnBlockedForwardsToStore(t *testing.T) {
	s := NewAgentsService(herdr.New("/nonexistent.sock"), slog.New(slog.DiscardHandler), nil, nil)
	s.store.Replace([]herdr.PaneInfo{pane("p1", "omp", herdr.StatusWorking)})

	var got []Agent
	AttachBlockedNotifier(s, func(a Agent) { got = append(got, a) })

	s.store.SetStatus("p1", herdr.StatusBlocked, "omp")
	if len(got) != 1 || got[0].PaneID != "p1" {
		t.Fatalf("OnBlocked callback = %v, want exactly one call naming p1", got)
	}
}

// TestBlockedEdgePublishesImmediately proves the tray does not wait for the
// coalesce window when a pane becomes blocked. Without this, a busy herd
// resets the coalescer and the sheep jumps late (or not until the burst
// ends). The edge path must call onCounts in the same SetStatus stack.
func TestBlockedEdgePublishesImmediately(t *testing.T) {
	counts := make(chan Counts, 4)
	s := NewAgentsService(
		herdr.New("/nonexistent.sock"),
		slog.New(slog.DiscardHandler),
		nil,
		func(c Counts) { counts <- c },
	)
	s.store.Replace([]herdr.PaneInfo{pane("p1", "omp", herdr.StatusWorking)})
	// Drain any publish Replace may have triggered via changed() — it goes
	// through the coalescer, so it may or may not have landed yet. We only
	// care about the blocked edge below.
	select {
	case <-counts:
	default:
	}

	s.store.SetStatus("p1", herdr.StatusBlocked, "omp")

	select {
	case c := <-counts:
		if c.Blocked != 1 {
			t.Fatalf("onCounts Blocked = %d, want 1 right after SetStatus", c.Blocked)
		}
	default:
		t.Fatal("onCounts was not called synchronously on the blocked edge")
	}
}
