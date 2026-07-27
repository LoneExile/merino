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
	p := paneReadParams{PaneID: paneID, Source: source, StripANSI: true}
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

// SendText writes literal text into a pane. Callers MUST validate the text
// against an allowlist first; this is unrestricted input to a live terminal.
func (c *Client) SendText(ctx context.Context, paneID, text string) error {
	return c.Call(ctx, "pane.send_text", paneSendTextParams{PaneID: paneID, Text: text}, nil)
}

// SendKeys presses keys in a pane.
func (c *Client) SendKeys(ctx context.Context, paneID string, keys ...string) error {
	return c.Call(ctx, "pane.send_keys", paneSendKeysParams{PaneID: paneID, Keys: keys}, nil)
}

// FocusPane brings a pane to the foreground.
func (c *Client) FocusPane(ctx context.Context, paneID string) error {
	return c.Call(ctx, "pane.focus", paneTarget{PaneID: paneID}, nil)
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
