package app

import (
	"errors"
	"fmt"
	"strings"
)

// Writes into a pane are input to a live terminal running with the user's
// privileges. Everything in this file exists to keep that surface narrow.

var (
	// ErrUnknownPane rejects a pane ID that did not originate from the server.
	ErrUnknownPane = errors.New("unknown pane")
	// ErrNotAllowed rejects input outside the allowlist.
	ErrNotAllowed = errors.New("input not allowed")
	// ErrTooLong rejects oversized free text.
	ErrTooLong = errors.New("text too long")
)

// MaxFreeTextLen caps arbitrary text sent to a pane.
const MaxFreeTextLen = 1000

// safeResponses are the canned answers to an agent's approval prompt. Anything
// outside this set must go through SendText, which is explicitly the
// higher-trust path.
var safeResponses = map[string]struct{}{
	"y": {}, "n": {}, "a": {},
	"yes": {}, "no": {}, "trust": {},
	"yes, single permission":  {},
	"trust, always allow":     {},
	"no (tab to edit)":        {},
	"approve all pending":     {},
	"configure individually":  {},
	"exit (cancel subagents)": {},
}

// safeKeys are key names verified to be accepted by herdr 0.7.5 pane.send_keys.
//
// Verified empirically against a live server, because the key vocabulary is
// not in the API schema and the CLI documents only the Escape aliases.
//
// Two findings worth preserving, both non-obvious:
//   - "BSpace" is REJECTED ("unsupported key"). Use "Backspace".
//   - "^C" is rejected; "Ctrl+c", "C-c" and "ctrl+c" are all accepted.
//
// Ctrl+d and Ctrl+z are accepted by herdr but deliberately excluded here:
// EOF and SIGTSTP can destroy or wedge an agent session, whereas Ctrl+c is the
// intended, recoverable interrupt.
var safeKeys = map[string]struct{}{
	"y": {}, "n": {}, "a": {},
	"Enter": {}, "enter": {},
	"Tab":       {},
	"Space":     {},
	"Backspace": {}, "backspace": {},
	"esc": {}, "escape": {}, "Escape": {},
	"Up": {}, "Down": {}, "Left": {}, "Right": {},
	"Ctrl+c": {}, "C-c": {}, "ctrl+c": {},
}

// InterruptKey is the canonical interrupt sent by Interrupt().
const InterruptKey = "Ctrl+c"

// Guard validates writes before they reach a pane.
type Guard struct {
	store *Store
}

func NewGuard(store *Store) *Guard { return &Guard{store: store} }

// CheckPane rejects pane IDs the server never told us about, so a compromised
// or buggy frontend cannot address arbitrary panes.
func (g *Guard) CheckPane(paneID string) error {
	if paneID == "" {
		return fmt.Errorf("%w: empty pane id", ErrUnknownPane)
	}
	if !g.store.Has(paneID) {
		return fmt.Errorf("%w: %s", ErrUnknownPane, paneID)
	}
	return nil
}

// CheckResponse validates a canned approval response.
func (g *Guard) CheckResponse(text string) error {
	if _, ok := safeResponses[strings.ToLower(strings.TrimSpace(text))]; !ok {
		return fmt.Errorf("%w: response %q is not in the allowlist", ErrNotAllowed, text)
	}
	return nil
}

// CheckKeys validates a key sequence.
func (g *Guard) CheckKeys(keys []string) error {
	if len(keys) == 0 {
		return fmt.Errorf("%w: no keys given", ErrNotAllowed)
	}
	for _, k := range keys {
		if _, ok := safeKeys[k]; !ok {
			return fmt.Errorf("%w: key %q is not in the allowlist", ErrNotAllowed, k)
		}
	}
	return nil
}

// CheckFreeText bounds arbitrary text. This path is intentionally weaker than
// CheckResponse; callers should prefer Respond where the answer is canned.
func (g *Guard) CheckFreeText(text string) error {
	if strings.TrimSpace(text) == "" {
		return fmt.Errorf("%w: empty text", ErrNotAllowed)
	}
	if len(text) > MaxFreeTextLen {
		return fmt.Errorf("%w: %d bytes exceeds %d", ErrTooLong, len(text), MaxFreeTextLen)
	}
	return nil
}
