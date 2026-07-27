package app

import (
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/LoneExile/herdr-tunnel/internal/herdr"
)

func TestCheckRenameNameBounds(t *testing.T) {
	if err := checkRenameName("build fix"); err != nil {
		t.Errorf("ordinary name rejected: %v", err)
	}
	for _, bad := range []string{"", "   ", "\t\n"} {
		if err := checkRenameName(bad); !errors.Is(err, ErrNotAllowed) {
			t.Errorf("checkRenameName(%q) = %v, want ErrNotAllowed", bad, err)
		}
	}
	if err := checkRenameName(strings.Repeat("a", MaxRenameLen+1)); !errors.Is(err, ErrTooLong) {
		t.Errorf("oversized name = %v, want ErrTooLong", err)
	}
	if err := checkRenameName(strings.Repeat("a", MaxRenameLen)); err != nil {
		t.Errorf("name at the limit rejected: %v", err)
	}
}

// RenamePane must reject a pane id the store never told it about, exactly
// like the other write methods' Guard.CheckPane calls — and it must do so
// before ever touching the herdr client.
func TestRenamePaneRejectsUnknownPane(t *testing.T) {
	s := NewAgentsService(herdr.New("/nonexistent.sock"), slog.New(slog.DiscardHandler), nil, nil)
	if err := s.RenamePane("nope", "x"); !errors.Is(err, ErrUnknownPane) {
		t.Errorf("RenamePane(unknown) = %v, want ErrUnknownPane", err)
	}
}
