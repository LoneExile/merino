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
	if err := json.NewEncoder(conn).Encode(request{ID: c.nextID(), Method: method, Params: params}); err != nil {
		return fmt.Errorf("herdr: write %s: %w", method, err)
	}

	var resp response
	if err := json.NewDecoder(conn).Decode(&resp); err != nil {
		return fmt.Errorf("herdr: read %s: %w", method, err)
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
	Type         string          `json:"type"`
	Version      string          `json:"version"`
	Protocol     int             `json:"protocol"`
	Capabilities map[string]bool `json:"capabilities"`
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
	if r.Protocol != Protocol {
		return r, fmt.Errorf("%w: server speaks %d, client targets %d (herdr %s)",
			ErrProtocolMismatch, r.Protocol, Protocol, r.Version)
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

// PaneRead is the payload of a pane.read response.
type PaneRead struct {
	PaneID    string `json:"pane_id"`
	Source    string `json:"source"`
	Format    string `json:"format"`
	Text      string `json:"text"`
	Revision  int64  `json:"revision"`
	Truncated bool   `json:"truncated"`
}

// ReadPane returns what is currently on a pane's screen, as plain text.
//
// Uses ReadVisible, not ReadRecent. The sources are not interchangeable:
// "recent" means output since the last read, so it returns an empty string for
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

// PaneOutputPollInterval is how often StreamPaneOutput re-reads a watched
// pane. A pane.read over the unix socket measures ~1.4ms p50, so one watcher
// costs roughly 0.5% of a core — cheap enough to sit well inside human
// perception without an accelerator.
const PaneOutputPollInterval = 300 * time.Millisecond

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
	tick := time.NewTicker(PaneOutputPollInterval)
	defer tick.Stop()

	var last string
	var primed bool
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-tick.C:
		}

		r, err := c.readPane(ctx, paneID, ReadRecent, lines, format)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			// herdr restarts routinely and a pane can close under us. Neither
			// is fatal to a watcher: keep polling and recover when it returns.
			continue
		}
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
// The field is "label", NOT "name". Verified against herdr 0.7.5's own schema
// and a live socket: tab.rename and workspace.rename reject "name" outright
// with `missing field \`label\``, and pane.rename — where label is optional —
// accepts the call and silently renames nothing, which is the worse failure of
// the two because it reports success.

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
