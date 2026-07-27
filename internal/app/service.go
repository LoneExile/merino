package app

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
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
	store *Store
	guard *Guard
	log   *slog.Logger

	emit      func(name string, data ...any)
	onCounts  func(Counts)
	coalescer *Coalescer

	// onBlockedUser is the optional external hook (Web Push). The store's
	// edge-triggered callback always runs publish first so the tray jumps
	// the moment a pane becomes blocked, without waiting for the coalesce
	// window — then forwards to this hook if set.
	onBlockedUser func(Agent)

	ctx  context.Context
	conn Conn

	// mu guards client, bgCancel and bgDone. SwitchSession replaces all
	// three under the lock so a request can never observe a client from one
	// herdr session paired with another session's cancel func or wait
	// group.
	mu       sync.RWMutex
	client   *herdr.Client
	bgCancel context.CancelFunc
	// bgDone is signalled by every background goroutine startSession
	// launches for the active client. SwitchSession waits on it after
	// cancelling, so a slow in-flight reconcile from the outgoing session
	// can never land after the incoming one has already reset the store.
	bgDone *sync.WaitGroup
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
	// Edge-triggered: the moment a pane becomes blocked, publish NOW so the
	// tray sheep jumps on the same event tick. The coalescer still collapses
	// high-volume pane.updated storms for ordinary churn; blocked is rare
	// and attention-critical, so it bypasses the debounce.
	store.SetOnBlocked(func(a Agent) {
		s.publish()
		if s.onBlockedUser != nil {
			s.onBlockedUser(a)
		}
	})
	return s
}

// OnBlocked registers fn to be called whenever an agent pane transitions
// into the blocked status — never while a pane merely remains blocked. Wired
// from main.go, once, before ServiceStartup launches any background
// goroutine; nil (the default) until then, which is exactly correct for
// every existing caller and test that has no notifier to attach.
//
// The tray/frontend publish on the blocked edge is already wired inside
// NewAgentsService and does not depend on this hook. fn is the external
// side-effect path (Web Push).
func (s *AgentsService) OnBlocked(fn func(Agent)) {
	s.onBlockedUser = fn
}

// ServiceName identifies the service in Wails logs.
func (s *AgentsService) ServiceName() string { return "AgentsService" }

// ServiceStartup boots the client and background streams. The context lives
// for the lifetime of the application.
func (s *AgentsService) ServiceStartup(ctx context.Context, _ application.ServiceOptions) error {
	s.ctx = ctx
	s.startSession(ctx, s.client)
	return nil
}

// startSession brings a herdr client online and launches the background
// goroutines that keep the store in sync with it: protocol handshake,
// initial pane.list, the per-pane status subscription, the global lifecycle
// stream, and the safety-net reconcile.
//
// Pulled out of ServiceStartup so SwitchSession can repeat exactly this
// sequence for a new client without duplicating it. The background
// goroutines run under a child of parent, retained as bgCancel, so a later
// switch can retire this generation before starting the next one.
func (s *AgentsService) startSession(parent context.Context, client *herdr.Client) {
	bgCtx, cancel := context.WithCancel(parent)

	// Refuse to run against a protocol we were not written for rather than
	// decoding an unknown wire format into silently wrong state. This is not
	// fatal to the app: report it and keep retrying in the background.
	if ping, err := client.CheckCompatible(bgCtx); err != nil {
		s.log.Error("herdr handshake failed", "err", err)
		s.setConn(Conn{Socket: client.Socket(), Error: err.Error()})
	} else {
		s.log.Info("connected to herdr", "version", ping.Version, "protocol", ping.Protocol)
		s.setConn(Conn{Connected: true, Version: ping.Version, Protocol: ping.Protocol, Socket: client.Socket()})
	}

	if panes, err := client.ListPanes(bgCtx); err == nil {
		s.store.Replace(panes)
	} else {
		s.log.Warn("initial pane.list failed", "err", err)
	}

	subs := NewSubManager(client, s.store, s.log, 0, s.changed)

	var wg sync.WaitGroup
	wg.Add(3)

	s.mu.Lock()
	s.client = client
	s.bgCancel = cancel
	s.bgDone = &wg
	s.mu.Unlock()

	go func() { defer wg.Done(); subs.Run(bgCtx) }()
	go func() { defer wg.Done(); s.runLifecycle(bgCtx, client, subs) }()
	go func() { defer wg.Done(); s.runSafetyNet(bgCtx, client, subs) }()

	subs.Sync(s.store.AgentPaneIDs())
	s.publish()
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
func (s *AgentsService) runLifecycle(ctx context.Context, client *herdr.Client, subs *SubManager) {
	client.Stream(ctx, herdr.StreamOptions{
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
			s.setConn(Conn{Connected: true, Version: s.conn.Version, Protocol: s.conn.Protocol, Socket: client.Socket()})
			if panes, err := client.ListPanes(ctx); err == nil && s.store.Replace(panes) {
				s.changed()
			}
			subs.Sync(s.store.AgentPaneIDs())
		},
		OnEvent: func(ev herdr.Event) { s.handleLifecycle(ctx, ev, client, subs) },
		OnError: func(err error) {
			if ctx.Err() == nil {
				s.log.Warn("lifecycle stream dropped", "err", err)
				s.setConn(Conn{Socket: client.Socket(), Error: err.Error()})
			}
		},
	})
}

func (s *AgentsService) handleLifecycle(ctx context.Context, ev herdr.Event, client *herdr.Client, subs *SubManager) {
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
		dirty = s.reconcile(ctx, client)
	default:
		return
	}

	// Re-sync subscriptions only when the watched set actually moved.
	after := s.store.AgentPaneIDs()
	if !equalStrings(before, after) {
		subs.Sync(after)
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

// reconcile re-reads authoritative state from client. Returns true if the
// UI projection changed. ctx is the generation's background context, not
// s.ctx: once it is cancelled by SwitchSession, an in-flight ListPanes must
// return promptly rather than run to its full timeout and land after the
// next generation has already reset the store.
func (s *AgentsService) reconcile(ctx context.Context, client *herdr.Client) bool {
	panes, err := client.ListPanes(ctx)
	if err != nil {
		if ctx.Err() == nil {
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
func (s *AgentsService) runSafetyNet(ctx context.Context, client *herdr.Client, subs *SubManager) {
	t := time.NewTicker(safetyNetInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			before := s.store.AgentPaneIDs()
			if s.reconcile(ctx, client) {
				s.log.Debug("safety-net reconcile corrected drift")
				s.changed()
			}
			if after := s.store.AgentPaneIDs(); !equalStrings(before, after) {
				subs.Sync(after)
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
	return s.currentClient().ReadPane(s.ctx, paneID, lines)
}

// StreamOutput streams a pane's live output until ctx is cancelled, calling
// onText with the latest rendering on every matching change.
//
// ctx is the CALLER's context, not s.ctx: unlike the service's own
// background streams, which live for the whole app, a pane subscription
// must end the moment its one subscriber — an HTTP request, say — goes
// away, not when the app itself shuts down.
func (s *AgentsService) StreamOutput(ctx context.Context, paneID string, lines int, onText func(string)) error {
	if err := s.guard.CheckPane(paneID); err != nil {
		return err
	}
	if lines <= 0 {
		lines = 200
	}
	return s.currentClient().StreamPaneOutput(ctx, paneID, lines, onText)
}

// ReadANSI is Read but preserves ANSI/SGR colour and style escapes instead
// of stripping them, for a renderer that can display them. Used only by the
// web dashboard's terminal view; the desktop panel calls Read.
func (s *AgentsService) ReadANSI(paneID string, lines int) (string, error) {
	if err := s.guard.CheckPane(paneID); err != nil {
		return "", err
	}
	if lines <= 0 {
		lines = 50
	}
	return s.currentClient().ReadPaneANSI(s.ctx, paneID, lines)
}

// StreamOutputANSI is StreamOutput but preserves ANSI/SGR colour and style
// escapes instead of stripping them. See ReadANSI.
func (s *AgentsService) StreamOutputANSI(ctx context.Context, paneID string, lines int, onText func(string)) error {
	if err := s.guard.CheckPane(paneID); err != nil {
		return err
	}
	if lines <= 0 {
		lines = 200
	}
	return s.currentClient().StreamPaneOutputANSI(ctx, paneID, lines, onText)
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
	return s.currentClient().SubmitText(s.ctx, paneID, text)
}

// SendText writes arbitrary text and submits it, which is what a person typing
// a reply expects. Higher trust than Respond: bounded by length only. Prefer
// Respond where the answer is one of the canned options.
//
// Routing:
//   - Pane hosts a coding agent (omp/pi/claude/grok/…) → herdr agent.prompt.
//     That path is harness-aware, so slash commands (/help, /status, …) work
//     the same way they do when typed in the agent TUI.
//   - Plain shell pane → SubmitText (send_text + Enter).
func (s *AgentsService) SendText(paneID, text string) error {
	if err := s.guard.CheckPane(paneID); err != nil {
		return err
	}
	if err := s.guard.CheckFreeText(text); err != nil {
		return err
	}
	cli := s.currentClient()
	if a, ok := s.store.Get(paneID); ok && a.Agent != "" {
		s.log.Info("agent_prompt", "pane", paneID, "agent", a.Agent, "bytes", len(text))
		return cli.AgentPrompt(s.ctx, paneID, text)
	}
	s.log.Info("send_text", "pane", paneID, "bytes", len(text))
	return cli.SubmitText(s.ctx, paneID, text)
}

// SlashCommands returns typeahead hits for the composer when the user types
// "/…" in a pane. agent is the herdr agent label (omp/pi/claude/grok/…).
// query may include the leading slash.
func (s *AgentsService) SlashCommands(agent, query, cwd string) []SlashCommand {
	return FilterSlashCommands(agent, query, cwd)
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
	return s.currentClient().SendKeys(s.ctx, paneID, keys...)
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
	return s.currentClient().FocusPane(s.ctx, paneID)
}

// currentClient returns the herdr client for whichever session is currently
// active. Guarded because SwitchSession replaces it while requests may be in
// flight.
func (s *AgentsService) currentClient() *herdr.Client {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.client
}

// Sessions enumerates every herdr session this machine knows about,
// best-effort probed for reachability and pane/agent counts.
func (s *AgentsService) Sessions(ctx context.Context) ([]SessionInfo, error) {
	return ListSessions(ctx, s.currentClient().Socket())
}

// SwitchSession repoints the service at a different herdr session's socket.
//
// The previous session's background goroutines are cancelled before the new
// generation's are started, so the store is never fed events from two
// sessions at once. id is resolved through Sessions rather than trusted as a
// path, so a caller can never point the server at an arbitrary socket.
func (s *AgentsService) SwitchSession(id string) error {
	target, err := resolveSession(s.ctx, s.currentClient().Socket(), id)
	if err != nil {
		return err
	}

	s.mu.Lock()
	cancel := s.bgCancel
	done := s.bgDone
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if done != nil {
		// Wait for the previous generation's goroutines to actually exit —
		// including any reconcile already in flight — before touching the
		// store, so it can never be resurrected with stale data the instant
		// after the new session starts.
		done.Wait()
	}

	// Clear projected state before the new session's own reconcile runs, so
	// a slow or unreachable target never leaves the previous session's panes
	// on screen under the new session's identity.
	if s.store.Replace(nil) {
		s.changed()
	}

	s.startSession(s.ctx, herdr.New(target.Socket))
	s.log.Info("switched herdr session", "id", id, "socket", target.Socket)
	return nil
}

// MaxRenameLen bounds a pane, tab or workspace name.
const MaxRenameLen = 120

// checkRenameName rejects a blank or oversized name.
//
// A separate, small check rather than an addition to Guard: renames are
// metadata, not text typed into a live terminal, so they do not share
// Guard's threat model of an allowlisted or length-bounded input stream —
// but a blank or unbounded name is still nonsensical input worth rejecting
// before it reaches herdr.
func checkRenameName(name string) error {
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("%w: empty name", ErrNotAllowed)
	}
	if len(name) > MaxRenameLen {
		return fmt.Errorf("%w: %d bytes exceeds %d", ErrTooLong, len(name), MaxRenameLen)
	}
	return nil
}

// RenamePane sets a pane's display name via herdr.
func (s *AgentsService) RenamePane(paneID, name string) error {
	if err := s.guard.CheckPane(paneID); err != nil {
		return err
	}
	if err := checkRenameName(name); err != nil {
		return err
	}
	s.log.Info("rename_pane", "pane", paneID, "name", name)
	return s.currentClient().RenamePane(s.ctx, paneID, name)
}

// RenameTab sets a tab's display name via herdr.
//
// Unlike pane writes there is no Guard.CheckPane-equivalent here: tabs are
// not tracked as first-class entities in Store, only as a field on the panes
// within them. The web layer authorises the request against an agent
// occupying the tab before this is ever called (see
// Server.authorizeControl), which is also what stands in for "does this tab
// exist".
func (s *AgentsService) RenameTab(tabID, name string) error {
	if err := checkRenameName(name); err != nil {
		return err
	}
	s.log.Info("rename_tab", "tab", tabID, "name", name)
	return s.currentClient().RenameTab(s.ctx, tabID, name)
}

// RenameWorkspace sets a workspace's display name via herdr. See RenameTab
// for why there is no store-backed existence check here.
func (s *AgentsService) RenameWorkspace(workspaceID, name string) error {
	if err := checkRenameName(name); err != nil {
		return err
	}
	s.log.Info("rename_workspace", "workspace", workspaceID, "name", name)
	return s.currentClient().RenameWorkspace(s.ctx, workspaceID, name)
}
