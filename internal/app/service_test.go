package app

import (
	"context"
	"errors"
	"log/slog"
	"testing"

	"github.com/LoneExile/herdr-tunnel/internal/herdr"
)

// SwitchSession must resolve id against the real session list before ever
// touching the active client or its background goroutines, so an unknown id
// leaves the current session completely undisturbed.
func TestSwitchSessionRejectsUnknownID(t *testing.T) {
	withHerdrHome(t)

	orig := herdr.New("/nonexistent/original.sock")
	s := NewAgentsService(orig, slog.New(slog.DiscardHandler), nil, nil)
	s.ctx = context.Background()

	if err := s.SwitchSession("does-not-exist"); !errors.Is(err, ErrUnknownSession) {
		t.Fatalf("SwitchSession(unknown) = %v, want ErrUnknownSession", err)
	}
	if s.currentClient() != orig {
		t.Error("client was replaced despite an unknown session id")
	}
}
