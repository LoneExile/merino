package herdr

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync/atomic"
	"time"
)

// DefaultSocket returns the conventional herdr socket path.
func DefaultSocket() string {
	if s := os.Getenv("HERDR_SOCK"); s != "" {
		return s
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "herdr.sock"
	}
	return filepath.Join(home, ".config", "herdr", "herdr.sock")
}

// Client talks to the herdr server over its unix socket.
//
// Client is safe for concurrent use: every call dials its own connection,
// which the one-shot nature of the protocol requires anyway.
type Client struct {
	socket string
	seq    atomic.Uint64

	// DialTimeout bounds connection establishment. Zero means 5s.
	DialTimeout time.Duration
	// CallTimeout bounds a single request/response. Zero means 15s.
	CallTimeout time.Duration
	// PollInterval overrides how often StreamPaneOutput re-checks a watched
	// pane. Zero means PaneOutputPollInterval. A remote endpoint reached over
	// an SSH forward pays ~110ms per call regardless of payload size, so it
	// polls slower than a local one.
	//
	// The value must be positive: zero or negative means PaneOutputPollInterval.
	// Size it against the double wait a stalled pane.get costs — a tick whose
	// get never answers blocks for CallTimeout (15s by default) and then pays
	// the read as well, because a failed get falls through to the read rather
	// than skipping the tick, so the retry cadence on exactly the remote
	// endpoints this exists for is halved for the duration of the stall.
	PollInterval time.Duration
}

// New returns a Client for the given socket path. An empty path uses
// DefaultSocket.
func New(socket string) *Client {
	if socket == "" {
		socket = DefaultSocket()
	}
	return &Client{socket: socket}
}

// Socket returns the path this client dials.
func (c *Client) Socket() string { return c.socket }

func (c *Client) dialTimeout() time.Duration {
	if c.DialTimeout > 0 {
		return c.DialTimeout
	}
	return 5 * time.Second
}

func (c *Client) callTimeout() time.Duration {
	if c.CallTimeout > 0 {
		return c.CallTimeout
	}
	return 15 * time.Second
}

func (c *Client) nextID() string {
	return fmt.Sprintf("%d", c.seq.Add(1))
}

// dial opens a connection to the herdr socket.
func (c *Client) dial(ctx context.Context) (net.Conn, error) {
	d := net.Dialer{Timeout: c.dialTimeout()}
	conn, err := d.DialContext(ctx, "unix", c.socket)
	if err != nil {
		return nil, fmt.Errorf("dial herdr socket %s: %w", c.socket, err)
	}
	return conn, nil
}

// Call performs a single request/response round trip.
//
// A fresh connection is dialled for every call because the server closes the
// connection after responding; reusing one yields EPIPE. out may be nil to
// discard the result.
func (c *Client) Call(ctx context.Context, method string, params, out any) error {
	ctx, cancel := context.WithTimeout(ctx, c.callTimeout())
	defer cancel()

	conn, err := c.dial(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()

	if dl, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(dl)
	}
	// Stop the blocking read as soon as the caller's context is done.
	stop := context.AfterFunc(ctx, func() { _ = conn.SetDeadline(time.Now()) })
	defer stop()

	if params == nil {
		params = struct{}{}
	}
	// Encode appends '\n', which is exactly the framing the server expects.
	id := c.nextID()
	if err := json.NewEncoder(conn).Encode(request{ID: id, Method: method, Params: params}); err != nil {
		return fmt.Errorf("herdr: write %s: %w", method, err)
	}

	var resp response
	if err := json.NewDecoder(conn).Decode(&resp); err != nil {
		return fmt.Errorf("herdr: read %s: %w", method, err)
	}
	// One-shot-per-conn today, but reject a mismatched id so a framing
	// bug can never be accepted as the method result.
	if resp.ID != "" && resp.ID != id {
		return fmt.Errorf("herdr: response id %q does not match request %q", resp.ID, id)
	}
	if resp.Error != nil {
		return resp.Error
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(resp.Result, out); err != nil {
		return fmt.Errorf("herdr: decode %s result: %w", method, err)
	}
	return nil
}

// --- typed methods ---

// PingResult is the server's identity and capability advertisement.
type PingResult struct {
	Type     string `json:"type"`
	Version  string `json:"version"`
	Protocol int    `json:"protocol"`
	// Capabilities is not map[string]bool: herdr 0.8.2 sends only booleans,
	// but 0.9 sends values that are not flags at all — the live 0.9.0 server
	// advertises {"endpoint_protocol_generation":1,...}, which a bool map
	// fails to decode, taking the whole ping down with it.
	//
	// Read a capability with a comma-ok assertion — v, ok :=
	// r.Capabilities["health_check"]; ok && v == true — because values are
	// not all booleans. An absent key means the server does not advertise it,
	// and `r.Capabilities["health_check"] == false` cannot tell that apart.
	Capabilities map[string]any `json:"capabilities"`
}

// ErrProtocolMismatch is returned when the server speaks a protocol this
// client was not written against.
var ErrProtocolMismatch = errors.New("herdr: protocol mismatch")

// Ping returns server identity. It does not validate the protocol; use
// CheckCompatible for that.
func (c *Client) Ping(ctx context.Context) (PingResult, error) {
	var r PingResult
	err := c.Call(ctx, "ping", struct{}{}, &r)
	return r, err
}

// CheckCompatible verifies the server protocol matches this client. Failing
// loudly here beats decoding an unknown wire format into silently wrong state.
func (c *Client) CheckCompatible(ctx context.Context) (PingResult, error) {
	r, err := c.Ping(ctx)
	if err != nil {
		return r, err
	}
	if !ProtocolAccepted(r.Protocol) {
		return r, fmt.Errorf("%w: server speaks %d, client accepts %v (herdr %s)",
			ErrProtocolMismatch, r.Protocol, AcceptedProtocols, r.Version)
	}
	return r, nil
}

// ListPanes returns every pane in the session, agent-bearing or not.
func (c *Client) ListPanes(ctx context.Context) ([]PaneInfo, error) {
	var r paneListResult
	if err := c.Call(ctx, "pane.list", paneListParams{}, &r); err != nil {
		return nil, err
	}
	return r.Panes, nil
}

// ListAgentPanes returns only panes hosting an agent.
func (c *Client) ListAgentPanes(ctx context.Context) ([]PaneInfo, error) {
	panes, err := c.ListPanes(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]PaneInfo, 0, len(panes))
	for _, p := range panes {
		if p.IsAgent() {
			out = append(out, p)
		}
	}
	return out, nil
}

// ListWorkspaces returns every workspace in the session.
func (c *Client) ListWorkspaces(ctx context.Context) ([]WorkspaceInfo, error) {
	var r workspaceListResult
	if err := c.Call(ctx, "workspace.list", struct{}{}, &r); err != nil {
		return nil, err
	}
	return r.Workspaces, nil
}

// GetPane returns one pane's metadata without its screen contents.
//
// This is the cheap half of the output poll. Measured here against a herdr
// 0.9.0 server speaking protocol 22: a pane.get response is 665-870 bytes,
// while an 800-line ANSI pane.read of a working agent pane runs 57-441 KB
// (~297 KB typical). Over a unix socket that gap does not matter. Over an
// SSH-forwarded socket every call costs ~110ms regardless of payload, so a
// 300ms tick is really a ~410ms cycle, and re-reading the screen on every one
// of them regardless of change sustains ~700 KB/s per watched pane. That is
// why the poll loop compares Revision from here before paying for a read.

func (c *Client) GetPane(ctx context.Context, paneID string) (PaneInfo, error) {
	var r paneGetResult
	if err := c.Call(ctx, "pane.get", paneTarget{PaneID: paneID}, &r); err != nil {
		return PaneInfo{}, err
	}
	return r.Pane, nil
}

// CreateTab opens a tab in a workspace and returns it with its root pane.
//
// workspaceID may be empty, in which case herdr uses the focused workspace.
// The new tab is NOT focused: creating a pane from a phone must not yank the
// desktop's view to a different workspace mid-task.
func (c *Client) CreateTab(ctx context.Context, workspaceID, label string) (TabInfo, PaneInfo, error) {
	var r tabCreateResult
	p := tabCreateParams{WorkspaceID: workspaceID, Label: label, Focus: false}
	if err := c.Call(ctx, "tab.create", p, &r); err != nil {
		return TabInfo{}, PaneInfo{}, err
	}
	return r.Tab, r.RootPane, nil
}

// CloseTab closes a tab and everything in it. Used to roll back a tab whose
// agent failed to start, so a failed spawn leaves no empty shell behind.
func (c *Client) CloseTab(ctx context.Context, tabID string) error {
	return c.Call(ctx, "tab.close", tabTarget{TabID: tabID}, nil)
}

// AgentStartBudget is how long herdr may spend waiting for a freshly started
// agent to become ready for input. Cold agent binaries routinely take tens of
// seconds on first launch.
const AgentStartBudget = 45 * time.Second

// StartAgent launches a supported interactive agent in an existing pane and
// blocks until herdr confirms it is ready for input.
//
// The pane must be sitting at its shell prompt — herdr types the agent's
// command into it.
//
// This call gets its own client because the default 15s CallTimeout is
// SHORTER than herdr's readiness wait. Hitting the client deadline first
// would abandon a start that is still running, and the caller would roll
// back a tab whose agent then appears seconds later. The sibling client
// shares the socket; Client cannot be copied (atomic seq counter).
func (c *Client) StartAgent(ctx context.Context, paneID, kind, name string) error {
	starter := New(c.socket)
	starter.DialTimeout = c.DialTimeout
	starter.CallTimeout = AgentStartBudget + 10*time.Second

	ms := int(AgentStartBudget / time.Millisecond)
	p := agentStartParams{Name: name, Kind: kind, PaneID: paneID, TimeoutMS: &ms}
	return starter.Call(ctx, "agent.start", p, nil)
}

// PaneRead is the payload of a pane.read response.
type PaneRead struct {
	PaneID string `json:"pane_id"`
	Source string `json:"source"`
	Format string `json:"format"`
	Text   string `json:"text"`
	// Revision is always 0 in practice — measured against herdr 0.9.0 for
	// both read sources, both line windows and both an idle and a working
	// pane, 8 combinations — and nothing reads it: the poll loop's baseline
	// comes from GetPane, because a baseline taken from here never equals the
	// live revision and the gate then never skips. Kept because the wire
	// carries the key.
	Revision  int64 `json:"revision"`
	Truncated bool  `json:"truncated"`
}

// ReadPane returns what is currently on a pane's screen, as plain text.
//
// Uses ReadVisible, not ReadRecent. The sources are not interchangeable:
// "recent" means recent lines including scrollback past the viewport, so it returns an empty string for
// any pane that has settled — which is most of them, most of the time. A
// viewer asking "show me this pane" wants the screen, not a diff.
//
// The response also nests the payload one level deep as
// {"type":"pane_read","read":{...,"text":"..."}}; reading a top-level "text"
// field silently yields an empty string for every pane.
func (c *Client) ReadPane(ctx context.Context, paneID string, lines int) (string, error) {
	r, err := c.ReadPaneFull(ctx, paneID, ReadVisible, lines)
	if err != nil {
		return "", err
	}
	return r.Text, nil
}

// ReadPaneFull returns the complete pane.read payload, including whether the
// output was truncated.
func (c *Client) ReadPaneFull(ctx context.Context, paneID string, source ReadSource, lines int) (PaneRead, error) {
	return c.readPane(ctx, paneID, source, lines, FormatText)
}

// ReadPaneANSI asks herdr for recent output (including scrollback) with
// ANSI/SGR escapes preserved. Uses ReadRecent rather than ReadVisible so
// the dashboard can scroll up past the current viewport. Every other
// caller that wants the on-screen slice only should use ReadPane.
func (c *Client) ReadPaneANSI(ctx context.Context, paneID string, lines int) (string, error) {
	r, err := c.readPane(ctx, paneID, ReadRecent, lines, FormatANSI)
	if err != nil {
		return "", err
	}
	return r.Text, nil
}

// readPane is the shared implementation behind ReadPaneFull and ReadPaneANSI.
// StripANSI is derived from format rather than taken as its own parameter:
// asking herdr for ANSI-preserved text while also asking it to strip ANSI
// would be a contradiction on the wire, so no caller can express that
// combination by construction.
func (c *Client) readPane(ctx context.Context, paneID string, source ReadSource, lines int, format ReadFormat) (PaneRead, error) {
	p := paneReadParams{
		PaneID:    paneID,
		Source:    source,
		Format:    format,
		StripANSI: format != FormatANSI,
	}
	if lines > 0 {
		p.Lines = &lines
	}
	var resp struct {
		Type string   `json:"type"`
		Read PaneRead `json:"read"`
	}
	if err := c.Call(ctx, "pane.read", p, &resp); err != nil {
		return PaneRead{}, err
	}
	return resp.Read, nil
}

// PaneOutputPollInterval is how often StreamPaneOutput re-checks a watched
// pane. A pane.get over a local unix socket measures ~0.1ms and a pane.read
// ~1.4ms, so a local watcher is free at this cadence. Over an SSH-forwarded
// socket every call costs ~110ms fixed regardless of payload, which is what
// Client.PollInterval exists to relax.
const PaneOutputPollInterval = 300 * time.Millisecond

// paneOutputFullReadEvery forces an unconditional read every Nth tick.
//
// The poll loop's gate trusts herdr to move a pane's revision whenever its
// screen changes. That held for every tracked pane measured, but it is an
// assumption about another process: a pane whose revision stalls would
// otherwise never update again, and this belt bounds that to N ticks of
// staleness. (Panes that report revision 0 are not tracked at all and are
// excluded from the gate entirely — see the loop.)
const paneOutputFullReadEvery = 20

func (c *Client) pollInterval() time.Duration {
	if c.PollInterval > 0 {
		return c.PollInterval
	}
	return PaneOutputPollInterval
}

// StreamPaneOutput calls onText with a pane's visible text whenever it
// changes, until ctx is cancelled. It returns nil on cancellation.
//
// This POLLS, deliberately, because herdr has no continuous output-push
// primitive. The obvious candidate, a pane.output_matched subscription with a
// catch-all regex, is not one — measured against herdr 0.7.5:
//
//   - It is ONE-SHOT. Three separate output changes on a subscribed pane
//     deliver exactly one event; the connection stays open and never fires
//     again. It is a "wait until output matches X" primitive, for things like
//     blocking on a build to print PASS.
//   - Re-subscribing after each event does not rescue it: `.+` matches the
//     screen that is ALREADY there, so a resubscribe loop fires continuously
//     regardless of output — 163 events across 4 real changes.
//   - pane.scroll_changed is genuinely continuous but only reports scrolling,
//     so it misses in-place repaints, which is most of what a TUI agent does.
//
// Polling catches every kind of change, pushes only on a real diff, and costs
// nothing while a pane is idle.
func (c *Client) StreamPaneOutput(ctx context.Context, paneID string, lines int, onText func(string)) error {
	return c.streamPaneOutput(ctx, paneID, lines, FormatText, onText)
}

// StreamPaneOutputANSI is StreamPaneOutput but polls with ANSI/SGR escape
// sequences preserved instead of stripped — see ReadPaneANSI. Used only by
// the web dashboard's terminal view.
func (c *Client) StreamPaneOutputANSI(ctx context.Context, paneID string, lines int, onText func(string)) error {
	return c.streamPaneOutput(ctx, paneID, lines, FormatANSI, onText)
}

// streamPaneOutput is the shared poll loop behind StreamPaneOutput and
// StreamPaneOutputANSI. format only changes what herdr sends back; the
// change-detection contract above — suppress unless the text actually
// differs — applies identically to both, since it compares whatever bytes
// came back regardless of what they encode.
func (c *Client) streamPaneOutput(ctx context.Context, paneID string, lines int, format ReadFormat, onText func(string)) error {
	tick := time.NewTicker(c.pollInterval())
	defer tick.Stop()

	var last string
	var primed bool
	var lastRev int64
	var ticks int
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-tick.C:
		}
		ticks++

		// Ask the cheap question first, on every tick. Neither call below
		// treats a failure as fatal: herdr restarts routinely and a pane can
		// close under us, so a watcher that keeps polling recovers when it
		// returns.
		//
		// A failed pane.get falls through to the read rather than skipping
		// the tick. Skipping would freeze the view for as long as the failure
		// lasts — and the belt could not recover it, because this error
		// returns before the belt is consulted. For a pane that has actually
		// gone, the read fails the same way and the tick costs one extra
		// round trip; for a pane whose pane.get is failing on its own, the
		// read is what keeps it on screen.
		//
		// rev is the revision this tick's read will describe. It reaches
		// lastRev only once that read has delivered, so lastRev cannot mean
		// anything but "the revision whose screen we actually got" — see the
		// read-error branch below.
		var rev int64
		var skip bool
		info, err := c.GetPane(ctx, paneID)
		switch {
		case err != nil && ctx.Err() != nil:
			return nil
		case err != nil:
			// rev stays 0, which the gate refuses to skip on, so the read
			// below happens and the baseline stays where it was.
		case primed && info.Revision != 0 && info.Revision == lastRev && ticks%paneOutputFullReadEvery != 0:
			// The gate. Not applied before the stream has primed (the first
			// payload must arrive without waiting for a change) or on a belt
			// tick (paneOutputFullReadEvery above).
			//
			// Nor to a pane reporting revision 0, which is herdr saying it is
			// not tracking this pane: measured in a throwaway session, a plain
			// shell pane held revision 0 through three writes while its
			// visible text grew each time (616 -> 747 -> 878 bytes, the later
			// of two runs). 8 of the 26 panes on the development machine
			// reported 0, so they read every tick — no reduction for them, and
			// the ~14x fewer bytes measured on a tracked pane applies only to
			// tracked panes. Gating on an unchanged zero would hand those
			// panes to the belt instead, six seconds stale at the shipped
			// cadence, which is worse than what every pane did before this
			// gate existed.
			skip = true
		default:
			// The baseline comes from here, not from the read below.
			// pane.read reports its own revision as 0 — measured against
			// herdr 0.9.0 for both read sources, both line windows and both
			// an idle and a working pane — so a baseline taken from the read
			// never equals the live revision, the gate never skips, and every
			// tick pays for a full read plus the pane.get that was supposed
			// to prevent it.
			rev = info.Revision
		}
		if skip {
			continue
		}

		r, err := c.readPane(ctx, paneID, ReadRecent, lines, format)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			// lastRev is deliberately NOT advanced. Committing rev here would
			// leave the next tick comparing against a revision whose screen
			// never arrived: it would skip, and that screen would stay
			// invisible until the belt fired, up to paneOutputFullReadEvery
			// ticks later, where the loop without a gate recovered on the very
			// next tick.
			continue
		}
		lastRev = rev
		if primed && r.Text == last {
			continue
		}
		last, primed = r.Text, true
		onText(r.Text)
	}
}

// SendText writes literal text into a pane. Callers MUST validate the text
// against an allowlist first; this is unrestricted input to a live terminal.
func (c *Client) SendText(ctx context.Context, paneID, text string) error {
	return c.Call(ctx, "pane.send_text", paneSendTextParams{PaneID: paneID, Text: text}, nil)
}

// SubmitText types text into a pane and presses Enter.
//
// Appending "\n" to the text does NOT work for agents. A TUI reads the
// terminal in raw mode, where Enter arrives as CR (0x0D) and a bare LF is
// ignored — the text lands in the input box and simply sits there. Verified
// against a live omp agent: text plus "\n" left the prompt unsubmitted, while
// a subsequent send_keys ["Enter"] submitted it and the agent replied.
//
// Line-based shells accept "\n", which is why this bug looked fine when tested
// against a plain zsh pane and failed on the thing that actually matters.
func (c *Client) SubmitText(ctx context.Context, paneID, text string) error {
	if err := c.SendText(ctx, paneID, text); err != nil {
		return err
	}
	if err := c.SendKeys(ctx, paneID, "Enter"); err != nil {
		// The two halves are not atomic, and the caller must be told which one
		// happened: the text is now sitting in the prompt, unsubmitted. An
		// audit line reading only "failed" would misdescribe the pane's state.
		return fmt.Errorf("text delivered but not submitted, it is left in the prompt: %w", err)
	}
	return nil
}

// SendKeys presses keys in a pane.
func (c *Client) SendKeys(ctx context.Context, paneID string, keys ...string) error {
	return c.Call(ctx, "pane.send_keys", paneSendKeysParams{PaneID: paneID, Keys: keys}, nil)
}

// AgentPrompt submits text through herdr's harness-aware agent.prompt path.
//
// Prefer this over SubmitText for panes that host a coding agent (omp, pi,
// claude, grok, …). agent.prompt understands each harness's input model, so
// slash commands (/help, /status, /clear, …) and ordinary prompts both land
// correctly. SubmitText (send_text + Enter) is the right tool for plain shell
// panes and for canned approval keys that are not agent prompts.
func (c *Client) AgentPrompt(ctx context.Context, target, text string) error {
	return c.Call(ctx, "agent.prompt", agentPromptParams{Target: target, Text: text}, nil)
}

// FocusPane brings a pane to the foreground.
func (c *Client) FocusPane(ctx context.Context, paneID string) error {
	return c.Call(ctx, "pane.focus", paneTarget{PaneID: paneID}, nil)
}

// paneRenameParams, tabRenameParams and workspaceRenameParams carry the target
// id alongside the new label.
//
// The field is "label", NOT "name". Verified against herdr 0.8.2's own schema
// and a live socket, and re-checked against 0.9.x's schema (protocol 22), where
// the required sets are the same: tab.rename and workspace.rename reject "name"
// outright with `missing field \`label\``, and pane.rename — where label is
// optional — accepts the call and silently renames nothing, which is the worse
// failure of the two because it reports success.

type paneRenameParams struct {
	PaneID string `json:"pane_id"`
	Label  string `json:"label"`
}

type tabRenameParams struct {
	TabID string `json:"tab_id"`
	Label string `json:"label"`
}

type workspaceRenameParams struct {
	WorkspaceID string `json:"workspace_id"`
	Label       string `json:"label"`
}

// RenamePane sets a pane's display name.
func (c *Client) RenamePane(ctx context.Context, paneID, name string) error {
	return c.Call(ctx, "pane.rename", paneRenameParams{PaneID: paneID, Label: name}, nil)
}

// RenameTab sets a tab's display name.
func (c *Client) RenameTab(ctx context.Context, tabID, name string) error {
	return c.Call(ctx, "tab.rename", tabRenameParams{TabID: tabID, Label: name}, nil)
}

// RenameWorkspace sets a workspace's display name.
func (c *Client) RenameWorkspace(ctx context.Context, workspaceID, name string) error {
	return c.Call(ctx, "workspace.rename", workspaceRenameParams{WorkspaceID: workspaceID, Label: name}, nil)
}

// newLineReader returns a scanner sized for herdr payloads.
//
// The default bufio.Scanner token cap is 64 KiB. Pane payloads carry full
// PaneInfo including agent session paths and terminal titles, and silently
// exceeding the cap drops events with no error, so raise it explicitly.
func newLineReader(conn net.Conn) *bufio.Scanner {
	sc := bufio.NewScanner(conn)
	sc.Buffer(make([]byte, 0, 64*1024), maxLine)
	return sc
}

const maxLine = 4 << 20 // 4 MiB
