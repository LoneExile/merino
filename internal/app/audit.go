package app

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Audit records every write that reaches a pane.
//
// Writes are unrestricted input to a terminal running with the user's
// privileges, and once the dashboard is published they can originate from a
// phone on the far side of the internet. Without a durable record there is no
// way to answer "what typed that?" after the fact — which is the question that
// matters most when something unexpected happens.
//
// The format is JSONL: one self-contained object per line, append-only, cheap
// to grep and safe to truncate from the front.
type Audit struct {
	mu sync.Mutex
	w  io.WriteCloser
}

// AuditEntry is one recorded action.
type AuditEntry struct {
	Time time.Time `json:"ts"`
	// Actor identifies who asked. "desktop" for the local panel, or the
	// authenticated username for a browser.
	Actor string `json:"actor"`
	// Source distinguishes the surface the request came through.
	Source string `json:"source"`
	// RemoteAddr is the caller's address for remote requests.
	RemoteAddr string `json:"remoteAddr,omitempty"`
	Action     string `json:"action"`
	PaneID     string `json:"paneId"`
	// Detail is the payload, truncated. Recorded because "approved something"
	// is not a useful audit line; which answer was sent is the whole point.
	Detail string `json:"detail,omitempty"`
	// Allowed is false when the guard or policy refused the request. Refusals
	// are recorded too: a burst of them is the signal worth alerting on.
	Allowed bool   `json:"allowed"`
	Error   string `json:"error,omitempty"`
}

const auditDetailMax = 200

// DefaultAuditPath returns the conventional log location.
func DefaultAuditPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "herdr-tunnel-audit.jsonl"
	}
	return filepath.Join(home, "Library", "Logs", "herdr-tunnel", "audit.jsonl")
}

// NewAudit opens the log, creating parent directories as needed.
func NewAudit(path string) (*Audit, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("audit: create log directory: %w", err)
	}
	// 0600: the log contains approval text and pane identifiers.
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, fmt.Errorf("audit: open %s: %w", path, err)
	}
	return &Audit{w: f}, nil
}

// NewAuditTo writes to an arbitrary sink. Used by tests.
func NewAuditTo(w io.WriteCloser) *Audit { return &Audit{w: w} }

// Record appends an entry. Failures to write are deliberately silent at the
// call site — an audit problem must never block or break the action being
// audited — but the entry is flushed synchronously so a crash cannot lose the
// record of what caused it.
func (a *Audit) Record(e AuditEntry) {
	if a == nil || a.w == nil {
		return
	}
	if e.Time.IsZero() {
		e.Time = time.Now().UTC()
	}
	if len(e.Detail) > auditDetailMax {
		e.Detail = e.Detail[:auditDetailMax] + "…"
	}
	line, err := json.Marshal(e)
	if err != nil {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	_, _ = a.w.Write(append(line, '\n'))
}

// Close releases the log file.
func (a *Audit) Close() error {
	if a == nil || a.w == nil {
		return nil
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.w.Close()
}
