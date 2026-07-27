package app

import (
	"context"
	"log/slog"
	"time"

	"github.com/LoneExile/herdr-tunnel/internal/herdr"
	"github.com/wailsapp/wails/v3/pkg/application"
)

// EventAgentsChanged is emitted to the frontend whenever the agent projection
// changes. Payload is []Agent.
const EventAgentsChanged = "agents:changed"

// EventConnChanged is emitted when server connectivity changes. Payload is Conn.
const EventConnChanged = "conn:changed"

// coalesceWindow collapses bursts of pane.updated into a single frontend
// render. Long enough to absorb a storm, short enough to feel instant.
const coalesceWindow = 100 * time.Millisecond

// Conn describes connectivity to the herdr server.
type Conn struct {
	Connected bool   `json:"connected"`
	Version   string `json:"version"`
	Protocol  int    `json:"protocol"`
	Socket    string `json:"socket"`
	Error     string `json:"error,omitempty"`
}

// AgentsService is the frontend-facing API and the owner of the background
// plumbing that keeps the store fresh.
type AgentsService struct {
	client *herdr.Client
	store  *Store
	guard  *Guard
	subs   *SubManager
	log    *slog.Logger

	emit      func(name string, data ...any)
	onCounts  func(Counts)
	coalescer *Coalescer

	ctx  context.Context
	conn Conn
}

// NewAgentsService wires the service. emit publishes to the frontend and
// onCounts lets the tray track the herd; both may be nil in tests.
func NewAgentsService(
	client *herdr.Client,
	log *slog.Logger,
	emit func(name string, data ...any),
	onCounts func(Counts),
) *AgentsService {
	if emit == nil {
		emit = func(string, ...any) {}
	}
	if onCounts == nil {
		onCounts = func(Counts) {}
	}
	store := NewStore()
	s := &AgentsService{
		client:   client,
		store:    store,
		guard:    NewGuard(store),
		log:      log,
		emit:     emit,
		onCounts: onCounts,
		conn:     Conn{Socket: client.Socket()},
	}
	s.coalescer = NewCoalescer(coalesceWindow, s.publish)
	s.subs = NewSubManager(client, store, log, 0, s.changed)
	return s
}

// ServiceName identifies the service in Wails logs.
func (s *AgentsService) ServiceName() string { return "AgentsService" }

// ServiceStartup boots the client and background streams. The context lives
// for the lifetime of the application.
func (s *AgentsService) ServiceStartup(ctx context.Context, _ application.ServiceOptions) error {
	s.ctx = ctx

	// Refuse to run against a protocol we were not written for rather than
	// decoding an unknown wire format into silently wrong state. This is not
	// fatal to the app: report it and keep retrying in the background.
	if ping, err := s.client.CheckCompatible(ctx); err != nil {
		s.log.Error("herdr handshake failed", "err", err)
		s.setConn(Conn{Socket: s.client.Socket(), Error: err.Error()})
	} else {
		s.log.Info("connected to herdr", "version", ping.Version, "protocol", ping.Protocol)
		s.setConn(Conn{Connected: true, Version: ping.Version, Protocol: ping.Protocol, Socket: s.client.Socket()})
	}

	if panes, err := s.client.ListPanes(ctx); err == nil {
		s.store.Replace(panes)
	} else {
		s.log.Warn("initial pane.list failed", "err", err)
	}

	go s.subs.Run(ctx)
	go s.runLifecycle(ctx)
	go s.runSafetyNet(ctx)

	s.subs.Sync(s.store.AgentPaneIDs())
	s.publish()
	return nil
}

// ServiceShutdown releases background resources.
func (s *AgentsService) ServiceShutdown() error {
	s.coalescer.Stop()
	return nil
}

// runLifecycle maintains the global pane-lifecycle subscription.
//
// This connection is never restarted for subscription changes — its set is
// static — so pane creation and destruction can never be missed while the
// per-pane status connection churns.
func (s *AgentsService) runLifecycle(ctx context.Context) {
	s.client.Stream(ctx, herdr.StreamOptions{
		Subscriptions: []herdr.Subscription{
			herdr.GlobalSub(herdr.SubPaneCreated),
			herdr.GlobalSub(herdr.SubPaneClosed),
			herdr.GlobalSub(herdr.SubPaneExited),
			herdr.GlobalSub(herdr.SubPaneUpdated),
			herdr.GlobalSub(herdr.SubPaneAgentDetected),
			herdr.GlobalSub(herdr.SubTabClosed),
			herdr.GlobalSub(herdr.SubWorkspaceClosed),
		},
		OnReady: func() {
			s.setConn(Conn{Connected: true, Version: s.conn.Version, Protocol: s.conn.Protocol, Socket: s.client.Socket()})
			if panes, err := s.client.ListPanes(ctx); err == nil && s.store.Replace(panes) {
				s.changed()
			}
			s.subs.Sync(s.store.AgentPaneIDs())
		},
		OnEvent: s.handleLifecycle,
		OnError: func(err error) {
			if ctx.Err() == nil {
				s.log.Warn("lifecycle stream dropped", "err", err)
				s.setConn(Conn{Socket: s.client.Socket(), Error: err.Error()})
			}
		},
	})
}

func (s *AgentsService) handleLifecycle(ev herdr.Event) {
	s.log.Debug("lifecycle event", "kind", ev.Event)
	before := s.store.AgentPaneIDs()
	var dirty bool

	switch ev.Event {
	case herdr.EvPaneCreated, herdr.EvPaneUpdated:
		if p, err := ev.Pane(); err == nil {
			dirty = s.store.UpsertPane(p.Pane)
		}
	case herdr.EvPaneClosed, herdr.EvPaneExited:
		if p, err := ev.Pane(); err == nil && p.Pane.PaneID != "" {
			dirty = s.store.RemovePane(p.Pane.PaneID)
		}
	case herdr.EvPaneAgentDetected, herdr.EvTabClosed, herdr.EvWorkspaceClosed:
		// None of these carry a usable pane payload: agent_detected reports
		// only ids, and tab/workspace closes destroy panes without naming
		// them. Re-read authoritative state instead of guessing.
		dirty = s.reconcile()
	default:
		return
	}

	// Re-sync subscriptions only when the watched set actually moved.
	after := s.store.AgentPaneIDs()
	if !equalStrings(before, after) {
		s.subs.Sync(after)
	}
	if dirty {
		s.changed()
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// reconcile re-reads authoritative state from the server. Returns true if the
// UI projection changed.
func (s *AgentsService) reconcile() bool {
	panes, err := s.client.ListPanes(s.ctx)
	if err != nil {
		if s.ctx.Err() == nil {
			s.log.Warn("reconcile failed", "err", err)
		}
		return false
	}
	return s.store.Replace(panes)
}

// safetyNetInterval is how often state is re-read regardless of events.
const safetyNetInterval = 60 * time.Second

// runSafetyNet periodically reconciles against pane.list.
//
// Events remain the primary mechanism — this is not a return to polling. It
// exists because herdr's event surface has proven to have undocumented gaps:
// agent status transitions do not emit pane_updated, and closing a tab
// destroys panes without emitting pane_closed. Both were found only by
// running against a live server. A slow reconcile bounds the damage of the
// next such gap to one interval instead of forever, at negligible cost.
func (s *AgentsService) runSafetyNet(ctx context.Context) {
	t := time.NewTicker(safetyNetInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			before := s.store.AgentPaneIDs()
			if s.reconcile() {
				s.log.Debug("safety-net reconcile corrected drift")
				s.changed()
			}
			if after := s.store.AgentPaneIDs(); !equalStrings(before, after) {
				s.subs.Sync(after)
			}
		}
	}
}

// changed marks the projection dirty; publication is coalesced.
func (s *AgentsService) changed() { s.coalescer.Notify() }

func (s *AgentsService) publish() {
	agents := s.store.Agents()
	s.emit(EventAgentsChanged, agents)
	s.onCounts(s.store.Counts())
}

func (s *AgentsService) setConn(c Conn) {
	if s.conn == c {
		return
	}
	s.conn = c
	s.emit(EventConnChanged, c)
}

// --- frontend API ---

// List returns the current agents, most urgent first.
func (s *AgentsService) List() []Agent { return s.store.Agents() }

// Counts returns a summary of the herd.
func (s *AgentsService) Counts() Counts { return s.store.Counts() }

// Connection reports herdr connectivity.
func (s *AgentsService) Connection() Conn { return s.conn }

// Read returns recent plain-text output for a pane.
func (s *AgentsService) Read(paneID string, lines int) (string, error) {
	if err := s.guard.CheckPane(paneID); err != nil {
		return "", err
	}
	if lines <= 0 {
		lines = 50
	}
	return s.client.ReadPane(s.ctx, paneID, lines)
}

// Respond answers an agent's approval prompt with a canned reply.
func (s *AgentsService) Respond(paneID, text string) error {
	if err := s.guard.CheckPane(paneID); err != nil {
		return err
	}
	if err := s.guard.CheckResponse(text); err != nil {
		return err
	}
	s.log.Info("respond", "pane", paneID, "text", text)
	return s.client.SendText(s.ctx, paneID, text+"\n")
}

// SendText writes arbitrary text. Higher trust than Respond: bounded by length
// only. Prefer Respond where the answer is one of the canned options.
func (s *AgentsService) SendText(paneID, text string) error {
	if err := s.guard.CheckPane(paneID); err != nil {
		return err
	}
	if err := s.guard.CheckFreeText(text); err != nil {
		return err
	}
	s.log.Info("send_text", "pane", paneID, "bytes", len(text))
	return s.client.SendText(s.ctx, paneID, text)
}

// SendKeys presses allowlisted keys in a pane.
func (s *AgentsService) SendKeys(paneID string, keys []string) error {
	if err := s.guard.CheckPane(paneID); err != nil {
		return err
	}
	if err := s.guard.CheckKeys(keys); err != nil {
		return err
	}
	s.log.Info("send_keys", "pane", paneID, "keys", keys)
	return s.client.SendKeys(s.ctx, paneID, keys...)
}

// Interrupt sends Ctrl+c to a pane.
func (s *AgentsService) Interrupt(paneID string) error {
	return s.SendKeys(paneID, []string{InterruptKey})
}

// Focus brings a pane to the foreground in herdr.
func (s *AgentsService) Focus(paneID string) error {
	if err := s.guard.CheckPane(paneID); err != nil {
		return err
	}
	return s.client.FocusPane(s.ctx, paneID)
}
