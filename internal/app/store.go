package app

import (
	"cmp"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"github.com/LoneExile/herdr-tunnel/internal/herdr"
)

// Agent is the frontend-facing view of an agent pane.
//
// This is deliberately a projection rather than herdr.PaneInfo: it carries
// derived fields the UI needs and shields the frontend from wire churn when
// herdr's schema moves.
type Agent struct {
	PaneID         string            `json:"paneId"`
	Agent          string            `json:"agent"`
	Status         herdr.AgentStatus `json:"status"`
	Project        string            `json:"project"`
	CWD            string            `json:"cwd"`
	WorkspaceID    string            `json:"workspaceId"`
	TabID          string            `json:"tabId"`
	Focused        bool              `json:"focused"`
	NeedsAttention bool              `json:"needsAttention"`
}

func agentFromPane(p herdr.PaneInfo) Agent {
	cwd := p.CWD
	if cwd == "" {
		cwd = p.ForegroundCWD
	}
	name := p.DisplayAgent
	if name == "" {
		name = p.Agent
	}
	return Agent{
		PaneID:         p.PaneID,
		Agent:          name,
		Status:         p.AgentStatus,
		Project:        filepath.Base(cwd),
		CWD:            cwd,
		WorkspaceID:    p.WorkspaceID,
		TabID:          p.TabID,
		Focused:        p.Focused,
		NeedsAttention: p.AgentStatus.NeedsAttention(),
	}
}

// statusRank orders statuses by how much they want a human's attention.
func statusRank(s herdr.AgentStatus) int {
	switch s {
	case herdr.StatusBlocked:
		return 0
	case herdr.StatusWorking:
		return 1
	case herdr.StatusDone:
		return 2
	case herdr.StatusIdle:
		return 3
	default:
		return 4
	}
}

// Store is the authoritative view of herdr's agent panes.
//
// The Go side owns state and the frontend is a pure projection of it: React
// never merges deltas, it replaces its list wholesale. At realistic pane
// counts the copy is free and it removes a whole class of sync bugs.
type Store struct {
	mu    sync.RWMutex
	panes map[string]herdr.PaneInfo

	// onBlocked, if set, is called for every agent pane observed
	// transitioning from a known non-blocked status into blocked — never for
	// a pane merely discovered already blocked, and never again while it
	// stays blocked. See SetOnBlocked.
	onBlocked func(Agent)
}

func NewStore() *Store {
	return &Store{panes: make(map[string]herdr.PaneInfo)}
}

// SetOnBlocked registers fn to be called whenever an agent pane transitions
// into the blocked status — the edge, not the level: fn fires once per
// transition and never again while a pane merely remains blocked. Wired
// once at startup, before any background goroutine can mutate the store
// (see AgentsService.OnBlocked and main.go); unsynchronized with concurrent
// mutation for that reason, matching every other one-time wiring callback
// in this package (emit, onCounts).
func (s *Store) SetOnBlocked(fn func(Agent)) { s.onBlocked = fn }

// Replace swaps in a full snapshot, returning true if anything differs from
// what was held. Used to seed at startup and to reconcile after a resubscribe
// gap, during which events were dropped on the floor by the server.
func (s *Store) Replace(panes []herdr.PaneInfo) bool {
	next := make(map[string]herdr.PaneInfo, len(panes))
	for _, p := range panes {
		next[p.PaneID] = p
	}
	s.mu.Lock()
	changed := len(next) != len(s.panes)
	var becameBlocked []herdr.PaneInfo
	for id, p := range next {
		old, existed := s.panes[id]
		if !existed || !sameForUI(old, p) {
			changed = true
		}
		// Only a pane whose PRIOR status we actually held counts as an
		// observed transition. A pane arriving already blocked — the very
		// first pane.list at startup, most commonly — has no prior status to
		// have transitioned from, so it must not fire (see
		// TestStoreDoesNotNotifyForPaneDiscoveredAlreadyBlocked).
		if existed && p.IsAgent() && p.AgentStatus == herdr.StatusBlocked && old.AgentStatus != herdr.StatusBlocked {
			becameBlocked = append(becameBlocked, p)
		}
	}
	s.panes = next
	hook := s.onBlocked
	s.mu.Unlock()

	// Fired outside the lock: hook is a caller-supplied callback (in
	// practice, Server.NotifyBlocked, which itself just launches a
	// goroutine and returns) and must never be run while holding s.mu.
	if hook != nil {
		for _, p := range becameBlocked {
			hook(agentFromPane(p))
		}
	}
	return changed
}

// UpsertPane applies a pane lifecycle event. Returns true if the UI-visible
// projection changed.
func (s *Store) UpsertPane(p herdr.PaneInfo) bool {
	s.mu.Lock()
	old, existed := s.panes[p.PaneID]
	s.panes[p.PaneID] = p
	changed := !existed || !sameForUI(old, p)
	becameBlocked := existed && p.IsAgent() && p.AgentStatus == herdr.StatusBlocked && old.AgentStatus != herdr.StatusBlocked
	hook := s.onBlocked
	s.mu.Unlock()

	if becameBlocked && hook != nil {
		hook(agentFromPane(p))
	}
	return changed
}

// RemovePane drops a pane. Returns true if it was present.
func (s *Store) RemovePane(paneID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.panes[paneID]
	delete(s.panes, paneID)
	return ok
}

// SetStatus applies a pane.agent_status_changed event.
//
// This is the ONLY path by which status transitions reach the store: global
// pane.updated does not fire for them.
func (s *Store) SetStatus(paneID string, status herdr.AgentStatus, agent string) bool {
	s.mu.Lock()
	p, ok := s.panes[paneID]
	if !ok {
		// Status for a pane we have not seen yet; a reconcile will fill it in.
		s.mu.Unlock()
		return false
	}
	if p.AgentStatus == status && (agent == "" || p.Agent == agent) {
		s.mu.Unlock()
		return false
	}
	becameBlocked := status == herdr.StatusBlocked && p.AgentStatus != herdr.StatusBlocked
	p.AgentStatus = status
	if agent != "" {
		p.Agent = agent
	}
	s.panes[paneID] = p
	hook := s.onBlocked
	s.mu.Unlock()

	if becameBlocked && hook != nil {
		hook(agentFromPane(p))
	}
	return true
}

// Agents returns the agent panes, most-urgent first.
func (s *Store) Agents() []Agent {
	s.mu.RLock()
	out := make([]Agent, 0, len(s.panes))
	for _, p := range s.panes {
		if p.IsAgent() {
			out = append(out, agentFromPane(p))
		}
	}
	s.mu.RUnlock()

	slices.SortFunc(out, func(a, b Agent) int {
		if c := cmp.Compare(statusRank(a.Status), statusRank(b.Status)); c != 0 {
			return c
		}
		if c := cmp.Compare(a.WorkspaceID, b.WorkspaceID); c != 0 {
			return c
		}
		return cmp.Compare(a.PaneID, b.PaneID)
	})
	return out
}

// AgentPaneIDs returns the sorted pane IDs that warrant a per-pane status
// subscription. Sorted so callers can compare sets cheaply.
func (s *Store) AgentPaneIDs() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	ids := make([]string, 0, len(s.panes))
	for id, p := range s.panes {
		if p.IsAgent() {
			ids = append(ids, id)
		}
	}
	slices.Sort(ids)
	return ids
}

// Has reports whether a pane is known. Used to reject writes to pane IDs that
// did not come from the server.
func (s *Store) Has(paneID string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, ok := s.panes[paneID]
	return ok
}

// Get returns the agent projection for a pane, if known.
func (s *Store) Get(paneID string) (Agent, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	p, ok := s.panes[paneID]
	if !ok {
		return Agent{}, false
	}
	return agentFromPane(p), true
}

// Counts summarises the herd for the tray label.
type Counts struct {
	Total   int `json:"total"`
	Blocked int `json:"blocked"`
	Working int `json:"working"`
}

func (s *Store) Counts() Counts {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var c Counts
	for _, p := range s.panes {
		if !p.IsAgent() {
			continue
		}
		c.Total++
		switch p.AgentStatus {
		case herdr.StatusBlocked:
			c.Blocked++
		case herdr.StatusWorking:
			c.Working++
		}
	}
	return c
}

// sameForUI compares only the fields the UI projects, so high-frequency
// pane.updated churn (cursor, revision, scroll) does not trigger renders.
func sameForUI(a, b herdr.PaneInfo) bool {
	return a.Agent == b.Agent &&
		a.DisplayAgent == b.DisplayAgent &&
		a.AgentStatus == b.AgentStatus &&
		a.CWD == b.CWD &&
		a.ForegroundCWD == b.ForegroundCWD &&
		a.WorkspaceID == b.WorkspaceID &&
		a.TabID == b.TabID &&
		a.Focused == b.Focused
}

// Coalescer collapses a burst of change notifications into one callback.
//
// pane.updated is high volume — dozens of events per second across active
// agents — so emitting per event would thrash the frontend for no benefit.
type Coalescer struct {
	window time.Duration
	fn     func()

	mu      sync.Mutex
	timer   *time.Timer
	stopped bool
}

func NewCoalescer(window time.Duration, fn func()) *Coalescer {
	return &Coalescer{window: window, fn: fn}
}

// Notify schedules fn to run once the burst subsides.
func (c *Coalescer) Notify() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.stopped {
		return
	}
	if c.timer == nil {
		c.timer = time.AfterFunc(c.window, func() {
			c.mu.Lock()
			c.timer = nil
			stopped := c.stopped
			c.mu.Unlock()
			if !stopped {
				c.fn()
			}
		})
		return
	}
	c.timer.Reset(c.window)
}

// Stop cancels any pending callback.
func (c *Coalescer) Stop() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.stopped = true
	if c.timer != nil {
		c.timer.Stop()
		c.timer = nil
	}
}
