package app

import (
	"testing"
	"time"

	"github.com/LoneExile/herdr-tunnel/internal/herdr"
)

func pane(id, agent string, status herdr.AgentStatus) herdr.PaneInfo {
	return herdr.PaneInfo{
		PaneID:      id,
		TerminalID:  "t_" + id,
		WorkspaceID: "w1",
		TabID:       "w1:t1",
		Agent:       agent,
		AgentStatus: status,
		CWD:         "/src/" + id,
	}
}

// Only agent-bearing panes are projected; a typical herdr session is mostly
// plain shells and surfacing them would bury the agents.
func TestAgentsExcludesNonAgentPanes(t *testing.T) {
	s := NewStore()
	s.Replace([]herdr.PaneInfo{
		pane("p1", "omp", herdr.StatusWorking),
		pane("p2", "", herdr.StatusUnknown),
		pane("p3", "claude", herdr.StatusIdle),
	})

	got := s.Agents()
	if len(got) != 2 {
		t.Fatalf("got %d agents, want 2", len(got))
	}
	if ids := s.AgentPaneIDs(); len(ids) != 2 || ids[0] != "p1" || ids[1] != "p3" {
		t.Errorf("AgentPaneIDs = %v, want [p1 p3] sorted", ids)
	}
}

// Blocked agents must sort first: the whole point of the app is surfacing the
// ones that need a human.
func TestAgentsSortsBlockedFirst(t *testing.T) {
	s := NewStore()
	s.Replace([]herdr.PaneInfo{
		pane("p1", "a", herdr.StatusIdle),
		pane("p2", "b", herdr.StatusWorking),
		pane("p3", "c", herdr.StatusBlocked),
		pane("p4", "d", herdr.StatusDone),
	})

	got := s.Agents()
	want := []string{"p3", "p2", "p4", "p1"} // blocked, working, done, idle
	for i, id := range want {
		if got[i].PaneID != id {
			t.Fatalf("position %d = %s, want %s (order: %v)", i, got[i].PaneID, id, ids(got))
		}
	}
	if !got[0].NeedsAttention {
		t.Error("blocked agent should set NeedsAttention")
	}
}

func ids(as []Agent) []string {
	out := make([]string, len(as))
	for i, a := range as {
		out[i] = a.PaneID
	}
	return out
}

// SetStatus is the ONLY path by which status reaches the store, because
// herdr's global pane.updated does not fire for status transitions.
func TestSetStatusUpdatesAndReportsChange(t *testing.T) {
	s := NewStore()
	s.Replace([]herdr.PaneInfo{pane("p1", "omp", herdr.StatusWorking)})

	if !s.SetStatus("p1", herdr.StatusBlocked, "omp") {
		t.Fatal("status change should report changed")
	}
	if got := s.Agents()[0].Status; got != herdr.StatusBlocked {
		t.Errorf("status = %s, want blocked", got)
	}
	if s.SetStatus("p1", herdr.StatusBlocked, "omp") {
		t.Error("identical status should not report changed")
	}
	if s.SetStatus("ghost", herdr.StatusBlocked, "x") {
		t.Error("status for unknown pane should not report changed")
	}
}

// pane.updated fires dozens of times per second carrying churn the UI does not
// render. Those must not be reported as changes or the frontend thrashes.
func TestUpsertIgnoresNonVisualChurn(t *testing.T) {
	s := NewStore()
	p := pane("p1", "omp", herdr.StatusWorking)
	s.Replace([]herdr.PaneInfo{p})

	churn := p
	churn.Revision = 999
	churn.Scroll = &herdr.ScrollInfo{OffsetFromBottom: 12, ViewportRows: 40}
	if s.UpsertPane(churn) {
		t.Error("revision/scroll churn should not count as a change")
	}

	real := p
	real.AgentStatus = herdr.StatusBlocked
	if !s.UpsertPane(real) {
		t.Error("status change must count as a change")
	}
}

func TestCounts(t *testing.T) {
	s := NewStore()
	s.Replace([]herdr.PaneInfo{
		pane("p1", "a", herdr.StatusBlocked),
		pane("p2", "b", herdr.StatusBlocked),
		pane("p3", "c", herdr.StatusWorking),
		pane("p4", "d", herdr.StatusIdle),
		pane("p5", "", herdr.StatusWorking), // not an agent
	})

	got := s.Counts()
	want := Counts{Total: 4, Blocked: 2, Working: 1}
	if got != want {
		t.Errorf("Counts() = %+v, want %+v", got, want)
	}
}

func TestRemovePane(t *testing.T) {
	s := NewStore()
	s.Replace([]herdr.PaneInfo{pane("p1", "omp", herdr.StatusIdle)})
	if !s.RemovePane("p1") {
		t.Error("removing a known pane should report true")
	}
	if s.RemovePane("p1") {
		t.Error("removing twice should report false")
	}
	if len(s.Agents()) != 0 {
		t.Error("store should be empty")
	}
}

// A burst of notifications must produce exactly one callback.
func TestCoalescerCollapsesBursts(t *testing.T) {
	calls := make(chan struct{}, 16)
	c := NewCoalescer(30*time.Millisecond, func() { calls <- struct{}{} })
	defer c.Stop()

	for range 25 {
		c.Notify()
		time.Sleep(time.Millisecond)
	}

	select {
	case <-calls:
	case <-time.After(time.Second):
		t.Fatal("coalescer never fired")
	}
	time.Sleep(80 * time.Millisecond)
	if extra := len(calls); extra != 0 {
		t.Errorf("burst produced %d extra callbacks, want 1 total", extra)
	}
}

func TestCoalescerStopPreventsCallback(t *testing.T) {
	fired := make(chan struct{}, 1)
	c := NewCoalescer(20*time.Millisecond, func() { fired <- struct{}{} })
	c.Notify()
	c.Stop()
	select {
	case <-fired:
		t.Error("callback fired after Stop")
	case <-time.After(80 * time.Millisecond):
	}
}
