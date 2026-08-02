package main

import (
	"strings"
	"testing"

	"github.com/LoneExile/merino/internal/app"
)

// The menubar is the one surface a headless session cannot look at, so what
// it says has to be provable somewhere that is not the menubar.
//
// The case that matters: an idle herd and an unreachable herdr both produce
// zero agents, and before this distinction existed they rendered the same
// empty label. One of them means "nothing needs you" and the other means
// "this app is showing you nothing at all".

func TestTrayLabelSeparatesIdleHerdFromDeadHerdr(t *testing.T) {
	idle := trayLabel(app.Counts{Total: 4}, true)
	down := trayLabel(app.Counts{}, false)

	if idle == down {
		t.Fatalf("idle herd and unreachable herdr render identically as %q", idle)
	}
	if down == "" {
		t.Error("unreachable herdr must not render as an empty label")
	}
}

func TestTrayLabel(t *testing.T) {
	tests := []struct {
		name      string
		counts    app.Counts
		reachable bool
		want      string
	}{
		{"blocked wins", app.Counts{Total: 5, Blocked: 2, Working: 3}, true, "2!"},
		{"working when none blocked", app.Counts{Total: 5, Working: 3}, true, "3"},
		{"idle herd is quiet", app.Counts{Total: 4}, true, ""},
		{"empty herd is quiet", app.Counts{}, true, ""},
		{"unreachable is loud", app.Counts{}, false, "no herd"},
		// Unreachable outranks any remembered counts: the numbers are from
		// the last good poll and saying "2!" about a herd we cannot see
		// would send someone to answer an agent that may already be gone.
		{"unreachable outranks stale counts", app.Counts{Total: 5, Blocked: 2}, false, "no herd"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := trayLabel(tt.counts, tt.reachable); got != tt.want {
				t.Errorf("trayLabel(%+v, %v) = %q, want %q", tt.counts, tt.reachable, got, tt.want)
			}
		})
	}
}

// The label has room for about seven characters; the tooltip is where the
// operator finds out which socket was tried. That path is the actual answer
// most of the time, because the usual cause is a herd running under a
// different HERDR_SOCK rather than no herd at all.
func TestTrayTooltipNamesTheSocketWhenUnreachable(t *testing.T) {
	const sock = "/Users/lex/.config/herdr/herdr.sock"
	got := trayTooltip(app.Counts{}, false, sock)
	if !strings.Contains(got, sock) {
		t.Errorf("tooltip %q does not name the socket it failed to reach", got)
	}

	up := trayTooltip(app.Counts{Total: 5, Blocked: 2}, true, sock)
	if strings.Contains(up, sock) {
		t.Errorf("a reachable herd should not advertise its socket path: %q", up)
	}
	if !strings.Contains(up, "2") {
		t.Errorf("tooltip %q omits the blocked count", up)
	}
}
