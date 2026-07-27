package app

import (
	"context"
	"log/slog"
	"slices"
	"sync"
	"time"

	"github.com/LoneExile/herdr-tunnel/internal/herdr"
)

// SubManager owns the per-pane agent-status subscription.
//
// Why this exists: herdr fixes a subscription set at events.subscribe time —
// the connection *is* the subscription — and agent status transitions are
// ONLY delivered through per-pane pane.agent_status_changed subscriptions.
// Global pane.updated does not fire for them (verified: four forced status
// changes produced zero pane.updated for the subject pane while 58 fired for
// unrelated panes).
//
// So the set of panes we watch changes as panes come and go, and every change
// requires tearing down the connection and dialling a new one. Two mitigations
// make that safe:
//
//   - Debounce, so opening five panes causes one reconnect rather than five.
//   - Reconcile from pane.list after every successful resubscribe, because
//     events during the gap are not buffered anywhere and are simply lost.
//
// Pane lifecycle lives on a separate, never-restarted connection (see
// AgentsService) so that churn here can never make us miss a pane appearing.
type SubManager struct {
	client   *herdr.Client
	store    *Store
	log      *slog.Logger
	debounce time.Duration

	// onChange fires when a resubscribe or reconcile altered visible state.
	onChange func()

	mu      sync.Mutex
	desired []string

	dirty chan struct{}
}

// NewSubManager builds a SubManager. A zero debounce defaults to 250ms.
func NewSubManager(c *herdr.Client, store *Store, log *slog.Logger, debounce time.Duration, onChange func()) *SubManager {
	if debounce <= 0 {
		debounce = 250 * time.Millisecond
	}
	return &SubManager{
		client:   c,
		store:    store,
		log:      log,
		debounce: debounce,
		onChange: onChange,
		dirty:    make(chan struct{}, 1),
	}
}

// Sync records the desired pane set and wakes the run loop. Non-blocking and
// safe to call from event handlers.
func (m *SubManager) Sync(paneIDs []string) {
	ids := slices.Clone(paneIDs)
	slices.Sort(ids)

	m.mu.Lock()
	m.desired = ids
	m.mu.Unlock()

	select {
	case m.dirty <- struct{}{}:
	default: // a wake is already pending
	}
}

func (m *SubManager) snapshot() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return slices.Clone(m.desired)
}

// Run drives the status subscription until ctx is cancelled.
func (m *SubManager) Run(ctx context.Context) {
	var (
		streamCancel context.CancelFunc
		active       []string
	)
	defer func() {
		if streamCancel != nil {
			streamCancel()
		}
	}()

	for {
		select {
		case <-ctx.Done():
			return
		case <-m.dirty:
		}

		if !m.waitQuiet(ctx) {
			return
		}

		want := m.snapshot()
		if slices.Equal(want, active) {
			continue
		}

		if streamCancel != nil {
			streamCancel()
			streamCancel = nil
		}
		active = want

		if len(active) == 0 {
			m.log.Debug("no agent panes; status subscription idle")
			continue
		}

		subs := make([]herdr.Subscription, 0, len(active))
		for _, id := range active {
			subs = append(subs, herdr.PaneSub(herdr.SubPaneAgentStatusChanged, id))
		}

		sctx, cancel := context.WithCancel(ctx)
		streamCancel = cancel
		m.log.Info("subscribing to agent status", "panes", len(subs))
		go m.client.Stream(sctx, herdr.StreamOptions{
			Subscriptions: subs,
			OnReady:       func() { m.reconcile(sctx) },
			OnEvent:       m.handle,
			OnError: func(err error) {
				if sctx.Err() == nil {
					m.log.Warn("status stream dropped", "err", err)
				}
			},
		})
	}
}

// waitQuiet blocks until the dirty signal stops arriving for a full debounce
// window. Returns false if ctx ended.
func (m *SubManager) waitQuiet(ctx context.Context) bool {
	t := time.NewTimer(m.debounce)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return false
		case <-m.dirty:
			if !t.Stop() {
				<-t.C
			}
			t.Reset(m.debounce)
		case <-t.C:
			return true
		}
	}
}

func (m *SubManager) handle(ev herdr.Event) {
	if ev.Event != herdr.EvPaneAgentStatusChanged {
		return
	}
	s, err := ev.Status()
	if err != nil {
		m.log.Warn("bad status event", "err", err)
		return
	}
	if m.store.SetStatus(s.PaneID, s.AgentStatus, s.Agent) {
		m.onChange()
	}
}

// reconcile closes the resubscribe gap by re-reading authoritative state.
func (m *SubManager) reconcile(ctx context.Context) {
	panes, err := m.client.ListPanes(ctx)
	if err != nil {
		if ctx.Err() == nil {
			m.log.Warn("reconcile failed", "err", err)
		}
		return
	}
	if m.store.Replace(panes) {
		m.onChange()
	}
}
