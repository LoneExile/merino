package app

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/LoneExile/merino/internal/herdr"
)

// EventAgentsChanged is emitted to the frontend whenever the agent projection
// changes. Payload is []Agent.
const EventAgentsChanged = "agents:changed"

// EventConnChanged is emitted when server connectivity changes. Payload is Conn.
const EventConnChanged = "conn:changed"

// coalesceWindow collapses bursts of pane.updated into a single frontend
// render. Long enough to absorb a storm, short enough to feel instant.
const coalesceWindow = 100 * time.Millisecond

// MaxPaneHistoryLines caps how much scrollback a single read/stream asks
// herdr for. herdr has no offset-based pagination — larger lines is the
// only lever for "load older content".
const MaxPaneHistoryLines = 2000

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
	// switchMu serializes SwitchSession end-to-end so two concurrent
	// switches cannot both Wait the same generation and then start dual
	// stream sets.
	switchMu sync.Mutex
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

// AttachBlockedNotifier registers fn for agent blocked-edge side effects
// (Web Push). Package function — not a method — so Wails does not bind it
// into the desktop webview JS surface. Call once from main before
// Start; nil is fine for tests.
func AttachBlockedNotifier(s *AgentsService, fn func(Agent)) {
	if s == nil {
		return
	}
	s.onBlockedUser = fn
}

// ServiceName identifies the service in Wails logs.
func (s *AgentsService) ServiceName() string { return "AgentsService" }

// Start boots the client and background streams. The context lives for the
// lifetime of the application.
//
// Named Start rather than carrying the Wails lifecycle signature
// (ServiceStartup(ctx, application.ServiceOptions)): that one desktop type in
// one signature was the entire reason package app — and therefore
// internal/web, which is on the read path of the dashboard — pulled 22 Wails
// packages into every build. service_wails.go adapts it back for the menubar.
func (s *AgentsService) Start(ctx context.Context) error {
	s.ctx = ctx
	s.startSession(ctx, s.client)
	return nil
}

// startSession brings a herdr client online and launches the background
// goroutines that keep the store in sync with it: protocol handshake,
// initial pane.list, the per-pane status subscription, the global lifecycle
// stream, and the safety-net reconcile.
//
// Pulled out of Start so SwitchSession can repeat exactly this
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
			// Handshake when we have not had a successful one yet, rather
			// than reusing whatever connMeta happens to hold. The stream
			// becoming ready proves the socket is reachable NOW, which is
			// not the same as having agreed a protocol version — and on the
			// path where startSession's own handshake failed (a forwarded
			// socket that only became usable after boot, which is the normal
			// case for an ssh sidecar) the cache is empty. Reporting
			// Connected with version "" and protocol 0 tells the dashboard
			// the herd is fine while nothing has ever answered a ping.
			ver, proto := s.connMeta()
			if ver == "" || proto == 0 {
				if ping, err := client.CheckCompatible(ctx); err == nil {
					ver, proto = ping.Version, ping.Protocol
					s.log.Info("connected to herdr", "version", ver, "protocol", proto)
				} else {
					// Reachable but not usable: say so instead of claiming
					// a working herd.
					s.log.Warn("herdr handshake failed on reconnect", "err", err)
					s.setConn(Conn{Socket: client.Socket(), Error: err.Error()})
					return
				}
			}
			s.setConn(Conn{Connected: true, Version: ver, Protocol: proto, Socket: client.Socket()})
			// A failure here used to be dropped on the floor by `err == nil &&`,
			// which left the dashboard showing a connected herd with zero
			// agents and nothing anywhere explaining why. The reconcile loop
			// is the safety net, so this stays non-fatal — but it is logged.
			panes, err := client.ListPanes(ctx)
			if err != nil {
				s.log.Warn("initial pane.list after reconnect failed; "+
					"the agent list stays stale until the next reconcile", "err", err)
			} else if s.store.Replace(panes) {
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

func (s *AgentsService) connMeta() (version string, protocol int) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.conn.Version, s.conn.Protocol
}

func (s *AgentsService) setConn(c Conn) {
	s.mu.Lock()
	if s.conn == c {
		s.mu.Unlock()
		return
	}
	s.conn = c
	s.mu.Unlock()
	s.emit(EventConnChanged, c)
}

// --- frontend API ---

// List returns the current agents, most urgent first.
func (s *AgentsService) List() []Agent { return s.store.Agents() }

// Counts returns a summary of the herd.
func (s *AgentsService) Counts() Counts { return s.store.Counts() }

// Connection reports herdr connectivity.
func (s *AgentsService) Connection() Conn {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.conn
}

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
// of stripping them, for a renderer that can display them. Both the web
// dashboard and the desktop panel use it — the panel used plain Read until
// its terminal was noticed to be monochrome next to the browser's.
func (s *AgentsService) ReadANSI(paneID string, lines int) (string, error) {
	if err := s.guard.CheckPane(paneID); err != nil {
		return "", err
	}
	if lines <= 0 {
		lines = 300
	}
	if lines > MaxPaneHistoryLines {
		lines = MaxPaneHistoryLines
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
		lines = 300
	}
	if lines > MaxPaneHistoryLines {
		lines = MaxPaneHistoryLines
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
	s.log.Info("respond", "pane", paneID, "bytes", len(text))
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
// "/…" in a pane. paneID is the active pane: project skills are loaded from
// that pane's known CWD only — never from a client-supplied path.
// agent/query may still be passed for label matching; empty agent falls back
// to the store projection for the pane.
func (s *AgentsService) SlashCommands(paneID, agent, query string) []SlashCommand {
	cwd := ""
	if a, ok := s.store.Get(paneID); ok {
		if agent == "" {
			agent = a.Agent
		}
		cwd = a.CWD
	}
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
	// Entire switch is one critical section so concurrent callers cannot
	// both Wait the outgoing generation and then launch dual stream sets.
	s.switchMu.Lock()
	defer s.switchMu.Unlock()

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

// Workspace is one herdr workspace, as the UI sees it.
type Workspace struct {
	WorkspaceID string `json:"workspaceId"`
	Number      int    `json:"number"`
	Label       string `json:"label"`
	Focused     bool   `json:"focused"`
	PaneCount   int    `json:"paneCount"`
	TabCount    int    `json:"tabCount"`
	AgentStatus string `json:"agentStatus"`
}

// NewPane identifies the pane a spawn produced, so the caller can open it.
type NewPane struct {
	PaneID      string `json:"paneId"`
	TabID       string `json:"tabId"`
	WorkspaceID string `json:"workspaceId"`
	Kind        string `json:"kind"`
	Name        string `json:"name"`
}

// Workspaces lists the session's workspaces, so a spawn can name where it
// should land instead of silently taking whichever one happens to be focused.
func (s *AgentsService) Workspaces() ([]Workspace, error) {
	list, err := s.currentClient().ListWorkspaces(s.ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Workspace, 0, len(list))
	for _, w := range list {
		out = append(out, Workspace{
			WorkspaceID: w.WorkspaceID,
			Number:      w.Number,
			Label:       w.Label,
			Focused:     w.Focused,
			PaneCount:   w.PaneCount,
			TabCount:    w.TabCount,
			AgentStatus: string(w.AgentStatus),
		})
	}
	return out, nil
}

// AgentKinds lists the interactive agents installed on this machine.
func (s *AgentsService) AgentKinds() ([]AgentKind, error) {
	return AvailableAgentKinds(s.ctx), nil
}

// StartAgentPane opens a tab in a workspace and starts an agent in it.
//
// Two herdr calls, and the second one can fail after the first succeeded —
// the agent binary is missing, or it never reaches a prompt inside the
// readiness budget. A half-completed spawn would leave an empty shell tab
// the user did not ask for and did not see created, so a failed start rolls
// the tab back. Rollback failure is logged, not returned: the caller needs
// the reason the START failed, which is the actionable one.
//
// label is optional; empty means herdr names the tab.
func (s *AgentsService) StartAgentPane(workspaceID, kind, label string) (NewPane, error) {
	// Checked against the compile-time allowlist, NOT against what the PATH
	// probe found installed. The probe asks a login shell and degrades in
	// several environments (see AvailableAgentKinds); gating a start on it
	// would answer "not installed" for an agent herdr can start perfectly
	// well. herdr validates the kind itself and says unsupported_agent_kind,
	// so this check exists only to keep a typo from creating a tab first.
	canonical, ok := supportedKind(kind)
	if !ok {
		// Name what the caller actually sent, not the empty canonical form.
		return NewPane{}, fmt.Errorf("%w: agent %q is not a supported kind", ErrNotAllowed, kind)
	}
	kind = canonical
	if label != "" {
		if err := checkRenameName(label); err != nil {
			return NewPane{}, err
		}
	}
	name := agentNameFrom(label, kind)

	client := s.currentClient()
	tab, pane, err := client.CreateTab(s.ctx, workspaceID, label)
	if err != nil {
		return NewPane{}, err
	}
	s.log.Info("tab_created", "tab", tab.TabID, "pane", pane.PaneID, "workspace", tab.WorkspaceID)

	if err := client.StartAgent(s.ctx, pane.PaneID, kind, name); err != nil {
		s.log.Warn("agent_start_failed", "pane", pane.PaneID, "kind", kind, "err", err)
		// Rollback gets its own context. s.ctx is the service's lifetime
		// context, and a quit mid-start cancels it — the very case that
		// produced the failure would then also skip the cleanup and leak an
		// empty tab into the user's herd.
		ctx, cancel := context.WithTimeout(context.WithoutCancel(s.ctx), rollbackBudget)
		defer cancel()
		if cerr := client.CloseTab(ctx, tab.TabID); cerr != nil {
			s.log.Error("rollback_tab_failed", "tab", tab.TabID, "err", cerr)
		}
		return NewPane{}, err
	}

	s.log.Info("agent_started", "pane", pane.PaneID, "kind", kind, "name", name)
	return NewPane{
		PaneID:      pane.PaneID,
		TabID:       tab.TabID,
		WorkspaceID: tab.WorkspaceID,
		Kind:        kind,
		Name:        name,
	}, nil
}

// rollbackBudget bounds the cleanup call that closes a tab whose agent failed
// to start. Short: the user is already waiting on an error.
const rollbackBudget = 5 * time.Second

// agentNameFrom derives a herdr agent name from the user's tab label.
//
// The two are NOT the same string, which is the bug this exists to prevent.
// tab.create takes a free-form display label, but agent.start enforces
// ^[a-z][a-z0-9_-]{0,31}$ and rejects anything else outright — so passing the
// label straight through meant a perfectly ordinary "Scratch Pad" created a
// tab, failed the start, rolled back, and showed the user herdr's complaint
// about lowercase letters.
//
// Falls back to the kind, which is always a valid name by construction.
func agentNameFrom(label, kind string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(label) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '_':
			b.WriteRune(r)
			dash = false
		case b.Len() > 0 && !dash:
			// Any run of unsupported characters collapses to one separator,
			// never a leading one.
			b.WriteByte('-')
			dash = true
		}
	}
	name := strings.TrimRight(b.String(), "-_")

	// Must START with a letter: "2nd try" would otherwise yield "2nd-try".
	name = strings.TrimLeft(name, "0123456789_-")
	if len(name) > maxAgentNameLen {
		name = strings.TrimRight(name[:maxAgentNameLen], "-_")
	}
	if name == "" {
		return kind
	}
	return name
}

// maxAgentNameLen is herdr's own cap on an agent name.
const maxAgentNameLen = 32
