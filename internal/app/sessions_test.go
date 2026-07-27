package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// withHerdrHome points $HOME (and clears $HERDR_SOCK) at a fresh temp
// directory for the duration of the test, so ListSessions' filesystem scan
// and default-socket resolution are hermetic instead of reading whatever the
// machine running the test actually has under ~/.config/herdr.
func withHerdrHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("HERDR_SOCK", "")
	return home
}

// A machine that has never created a named session must still list
// "default", marked unreachable since nothing is listening at the temp
// socket path.
func TestListSessionsDefaultOnlyWhenNoSessionsDir(t *testing.T) {
	withHerdrHome(t)

	sessions, err := ListSessions(context.Background(), "")
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(sessions) != 1 || sessions[0].ID != "default" {
		t.Fatalf("sessions = %+v, want exactly [default]", sessions)
	}
	if sessions[0].Reachable {
		t.Error("default marked reachable with nothing listening")
	}
	if sessions[0].Panes != 0 || sessions[0].Agents != 0 {
		t.Errorf("unreachable session reported nonzero counts: %+v", sessions[0])
	}
}

// Named sessions are discovered from the sessions/ directory. Being equally
// unlistened-to in this test, they are reported unreachable rather than
// dropped or failing the whole call.
func TestListSessionsDiscoversNamedSessions(t *testing.T) {
	home := withHerdrHome(t)
	sessionsDir := filepath.Join(home, ".config", "herdr", "sessions")
	if err := os.MkdirAll(filepath.Join(sessionsDir, "tunnel-test"), 0o700); err != nil {
		t.Fatal(err)
	}
	// A stray file alongside the session directories must be ignored.
	if err := os.WriteFile(filepath.Join(sessionsDir, "not-a-session.txt"), nil, 0o600); err != nil {
		t.Fatal(err)
	}

	sessions, err := ListSessions(context.Background(), "")
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(sessions) != 2 {
		t.Fatalf("sessions = %+v, want [default tunnel-test]", sessions)
	}

	var found bool
	for _, s := range sessions {
		if s.ID != "tunnel-test" {
			continue
		}
		found = true
		if s.Reachable {
			t.Error("tunnel-test marked reachable with nothing listening")
		}
		wantSocket := filepath.Join(sessionsDir, "tunnel-test", "herdr.sock")
		if s.Socket != wantSocket {
			t.Errorf("socket = %q, want %q", s.Socket, wantSocket)
		}
	}
	if !found {
		t.Fatalf("tunnel-test session not discovered: %+v", sessions)
	}
}

// The entry whose socket matches currentSocket must be marked Current, and
// no other entry should be.
func TestListSessionsMarksCurrent(t *testing.T) {
	home := withHerdrHome(t)
	defaultSocket := filepath.Join(home, ".config", "herdr", "herdr.sock")

	sessions, err := ListSessions(context.Background(), defaultSocket)
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if !sessions[0].Current {
		t.Errorf("default session not marked current: %+v", sessions[0])
	}
}

// A session id that ListSessions never reported must be rejected rather than
// resolved to a guessed path.
func TestResolveSessionUnknownID(t *testing.T) {
	withHerdrHome(t)

	if _, err := resolveSession(context.Background(), "", "does-not-exist"); !errors.Is(err, ErrUnknownSession) {
		t.Errorf("resolveSession(unknown) = %v, want ErrUnknownSession", err)
	}
}

func TestResolveSessionKnownID(t *testing.T) {
	withHerdrHome(t)

	got, err := resolveSession(context.Background(), "", "default")
	if err != nil {
		t.Fatalf("resolveSession(default): %v", err)
	}
	if got.ID != "default" {
		t.Errorf("resolved session = %+v, want id \"default\"", got)
	}
}

// "default" must mean the default socket, not whatever $HERDR_SOCK points at.
//
// Regression test for a picker that lied. herdr.DefaultSocket() honours
// $HERDR_SOCK, so running the server against a named session made the
// "default" row resolve to that same named socket: two rows, one socket,
// identical pane counts, and BOTH marked current. Observed live with
// HERDR_SOCK=.../sessions/tunnel-test/herdr.sock — "default" reported
// tunnel-test's 1 pane / 1 agent while the real default session had 23 / 9.
func TestDefaultSessionIgnoresHerdrSockOverride(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	cfg := filepath.Join(home, ".config", "herdr")
	named := filepath.Join(cfg, "sessions", "work")
	if err := os.MkdirAll(named, 0o755); err != nil {
		t.Fatal(err)
	}
	// Point the process at the named session, exactly as the operator does.
	t.Setenv("HERDR_SOCK", filepath.Join(named, "herdr.sock"))

	got, err := ListSessions(context.Background(), filepath.Join(named, "herdr.sock"))
	if err != nil {
		t.Fatalf("list: %v", err)
	}

	byID := map[string]SessionInfo{}
	for _, s := range got {
		byID[s.ID] = s
	}
	def, ok := byID["default"]
	if !ok {
		t.Fatal("no default session listed")
	}
	wantDefault := filepath.Join(cfg, "herdr.sock")
	if def.Socket != wantDefault {
		t.Errorf("default socket = %q, want %q — it followed $HERDR_SOCK", def.Socket, wantDefault)
	}

	var current []string
	for _, s := range got {
		if s.Current {
			current = append(current, s.ID)
		}
	}
	if len(current) != 1 || current[0] != "work" {
		t.Errorf("current sessions = %v, want exactly [work]", current)
	}
}
