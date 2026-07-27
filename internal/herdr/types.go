// Package herdr is a client for the herdr terminal-multiplexer socket API.
//
// Wire protocol (verified against herdr 0.7.5, protocol 17):
//
//   - Transport is a unix socket, default ~/.config/herdr/herdr.sock.
//   - Messages are newline-delimited JSON: {"id","method","params"}.
//   - Ordinary calls are ONE-SHOT. The server closes the connection after
//     writing the response; a second request on the same connection fails
//     with EPIPE. Dial per call (see Client.Call).
//   - events.subscribe is the exception: the server acks with
//     {"result":{"type":"subscription_started"}} and then holds the
//     connection open, streaming events until the client closes it.
//     The connection *is* the subscription.
package herdr

import (
	"encoding/json"
	"fmt"
)

// Protocol is the herdr socket API protocol version this client is written
// against. Client.Ping compares it to the server and refuses a mismatch
// rather than misbehaving against an unknown wire format.
const Protocol = 17

// AgentStatus is the OBSERVED lifecycle state of an agent, as reported by the
// server on pane.agent_status_changed and in PaneInfo.
type AgentStatus string

const (
	StatusIdle    AgentStatus = "idle"
	StatusWorking AgentStatus = "working"
	StatusBlocked AgentStatus = "blocked"
	StatusDone    AgentStatus = "done"
	StatusUnknown AgentStatus = "unknown"
)

// NeedsAttention reports whether a human has to intervene for the agent to
// make progress.
func (s AgentStatus) NeedsAttention() bool { return s == StatusBlocked }

// AgentState is the REPORTED lifecycle state accepted by pane.report_agent.
//
// This is deliberately a separate type from AgentStatus. The two enums do not
// match: AgentState has no "done", and the server derives status from state
// rather than echoing it — reporting StateIdle is observed as StatusDone.
// Conflating them produces state machines that silently disagree with herdr.
type AgentState string

const (
	StateIdle    AgentState = "idle"
	StateWorking AgentState = "working"
	StateBlocked AgentState = "blocked"
	StateUnknown AgentState = "unknown"
)

// AgentSessionInfo identifies the agent's own session backing a pane.
type AgentSessionInfo struct {
	Agent  string `json:"agent"`
	Kind   string `json:"kind"`
	Source string `json:"source"`
	Value  string `json:"value"`
}

// ScrollInfo describes a pane's scrollback viewport.
type ScrollInfo struct {
	OffsetFromBottom    int `json:"offset_from_bottom"`
	MaxOffsetFromBottom int `json:"max_offset_from_bottom"`
	ViewportRows        int `json:"viewport_rows"`
}

// PaneInfo is a single herdr pane. Only pane_id, terminal_id, workspace_id,
// tab_id, focused, agent_status and revision are guaranteed present; the rest
// are nullable on the wire and therefore pointers or zero-valued here.
type PaneInfo struct {
	PaneID      string      `json:"pane_id"`
	TerminalID  string      `json:"terminal_id"`
	WorkspaceID string      `json:"workspace_id"`
	TabID       string      `json:"tab_id"`
	Focused     bool        `json:"focused"`
	AgentStatus AgentStatus `json:"agent_status"`
	Revision    int64       `json:"revision"`

	Agent         string `json:"agent,omitempty"`
	DisplayAgent  string `json:"display_agent,omitempty"`
	Label         string `json:"label,omitempty"`
	Title         string `json:"title,omitempty"`
	CWD           string `json:"cwd,omitempty"`
	ForegroundCWD string `json:"foreground_cwd,omitempty"`

	AgentSession *AgentSessionInfo `json:"agent_session,omitempty"`
	Scroll       *ScrollInfo       `json:"scroll,omitempty"`
}

// IsAgent reports whether this pane hosts a coding agent. Most panes in a
// typical session are plain shells; only agent panes are worth tracking or
// subscribing to.
func (p PaneInfo) IsAgent() bool { return p.Agent != "" }

// --- wire envelopes ---

type request struct {
	ID     string `json:"id"`
	Method string `json:"method"`
	Params any    `json:"params"`
}

type response struct {
	ID     string          `json:"id"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *APIError       `json:"error,omitempty"`
}

// APIError is a structured error returned by the herdr server.
type APIError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *APIError) Error() string { return fmt.Sprintf("herdr: %s: %s", e.Code, e.Message) }

// --- subscriptions ---

// SubKind is the name used when REQUESTING a subscription. These are DOTTED.
//
// Do not confuse these with EventKind below. herdr uses two different
// vocabularies and mixing them yields a subscription that succeeds but whose
// events never match your switch — a silent, total failure.
type SubKind string

// Global subscriptions. These take no arguments and report on every pane.
//
// NOTE: SubPaneUpdated does NOT fire on agent status transitions. Verified
// empirically: four forced status changes on a probe pane produced zero
// pane_updated events for that pane while 58 fired for unrelated panes.
// Status is ONLY observable via the per-pane SubPaneAgentStatusChanged below.
const (
	SubPaneCreated       SubKind = "pane.created"
	SubPaneClosed        SubKind = "pane.closed"
	SubPaneExited        SubKind = "pane.exited"
	SubPaneUpdated       SubKind = "pane.updated"
	SubPaneFocused       SubKind = "pane.focused"
	SubPaneAgentDetected SubKind = "pane.agent_detected"

	// Closing a tab or workspace destroys its panes but emits NO pane.closed
	// event — only tab_closed / workspace_closed. Verified against herdr
	// 0.7.5. Subscribe to these or panes leak in local state forever.
	SubTabClosed       SubKind = "tab.closed"
	SubWorkspaceClosed SubKind = "workspace.closed"
)

// Per-pane subscriptions. These REQUIRE a pane_id; subscribing without one is
// rejected with invalid_request.
const (
	SubPaneAgentStatusChanged SubKind = "pane.agent_status_changed"
	SubPaneOutputMatched      SubKind = "pane.output_matched"
	SubPaneScrollChanged      SubKind = "pane.scroll_changed"
)

// EventKind is the name in the "event" field of a DELIVERED event.
//
// The wire uses two spellings and which one you get depends on how you
// subscribed:
//
//   - Global subscriptions deliver SNAKE_CASE names (schema: event.EventKind),
//     e.g. subscribing to "pane.updated" delivers events named "pane_updated".
//   - Per-pane subscriptions deliver DOTTED names (schema:
//     subscription_event.SubscriptionEventKind), e.g. "pane.agent_status_changed".
//
// Verified against a running herdr 0.7.5.
type EventKind string

// Delivered by global subscriptions.
const (
	EvPaneCreated       EventKind = "pane_created"
	EvPaneClosed        EventKind = "pane_closed"
	EvPaneExited        EventKind = "pane_exited"
	EvPaneUpdated       EventKind = "pane_updated"
	EvPaneFocused       EventKind = "pane_focused"
	EvPaneAgentDetected EventKind = "pane_agent_detected"

	// Structural removals. These carry no pane payload, so treat them as a
	// signal to re-read pane.list rather than as a targeted delete.
	EvTabClosed       EventKind = "tab_closed"
	EvWorkspaceClosed EventKind = "workspace_closed"
)

// Delivered by per-pane subscriptions.
const (
	EvPaneAgentStatusChanged EventKind = "pane.agent_status_changed"
	EvPaneOutputMatched      EventKind = "pane.output_matched"
	EvPaneScrollChanged      EventKind = "pane.scroll_changed"
)

// Subscription is one entry in an events.subscribe request. PaneID must be set
// for per-pane kinds and omitted for global ones.
type Subscription struct {
	Type   SubKind `json:"type"`
	PaneID string  `json:"pane_id,omitempty"`
}

// GlobalSub builds a subscription for a global event kind.
func GlobalSub(k SubKind) Subscription { return Subscription{Type: k} }

// PaneSub builds a per-pane subscription.
func PaneSub(k SubKind, paneID string) Subscription {
	return Subscription{Type: k, PaneID: paneID}
}

// Event is a streamed event envelope.
//
// Discriminate on Event. Do NOT use data.type: it is present on pane lifecycle
// payloads but absent on pane.agent_status_changed.
type Event struct {
	Event EventKind       `json:"event"`
	Data  json.RawMessage `json:"data"`
}

// PaneEvent is the payload of pane lifecycle events (created/updated/closed/…).
type PaneEvent struct {
	Type string   `json:"type"`
	Pane PaneInfo `json:"pane"`
}

// StatusEvent is the payload of pane.agent_status_changed.
type StatusEvent struct {
	PaneID      string      `json:"pane_id"`
	WorkspaceID string      `json:"workspace_id"`
	Agent       string      `json:"agent"`
	AgentStatus AgentStatus `json:"agent_status"`
}

// Pane decodes the event as a pane lifecycle payload.
func (e Event) Pane() (PaneEvent, error) {
	var p PaneEvent
	err := json.Unmarshal(e.Data, &p)
	return p, err
}

// Status decodes the event as an agent status payload.
func (e Event) Status() (StatusEvent, error) {
	var s StatusEvent
	err := json.Unmarshal(e.Data, &s)
	return s, err
}

// --- method params ---

// ReadSource selects which part of a pane's output to read.
//
// These are the only values herdr accepts; anything else is rejected with
// "unknown variant". In particular there is no "scrollback" source.
type ReadSource string

const (
	// ReadVisible returns only what is currently on screen.
	ReadVisible ReadSource = "visible"
	// ReadRecent returns recent output including lines scrolled off screen.
	ReadRecent ReadSource = "recent"
	// ReadRecentUnwrapped is ReadRecent without soft-wrap line breaks.
	ReadRecentUnwrapped ReadSource = "recent_unwrapped"
	// ReadDetection is the slice herdr uses for agent-state detection.
	ReadDetection ReadSource = "detection"
)

type paneListParams struct {
	WorkspaceID string `json:"workspace_id,omitempty"`
}

type paneListResult struct {
	Panes []PaneInfo `json:"panes"`
}

type paneReadParams struct {
	PaneID    string     `json:"pane_id"`
	Source    ReadSource `json:"source"`
	Lines     *int       `json:"lines,omitempty"`
	StripANSI bool       `json:"strip_ansi"`
}

type paneSendTextParams struct {
	PaneID string `json:"pane_id"`
	Text   string `json:"text"`
}

type paneSendKeysParams struct {
	PaneID string   `json:"pane_id"`
	Keys   []string `json:"keys"`
}

type paneTarget struct {
	PaneID string `json:"pane_id"`
}

type subscribeParams struct {
	Subscriptions []Subscription `json:"subscriptions"`
}
