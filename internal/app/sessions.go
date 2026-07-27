package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/LoneExile/herdr-tunnel/internal/herdr"
)

// SessionInfo describes one herdr session this machine can reach: the
// default socket herdr uses when none is named, plus every named session
// under ~/.config/herdr/sessions/*. ListSessions is the only thing that
// constructs these, so a caller of SwitchSession can never point the server
// at a socket outside this list.
type SessionInfo struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Socket    string `json:"socket"`
	Panes     int    `json:"panes"`
	Agents    int    `json:"agents"`
	Reachable bool   `json:"reachable"`
	Current   bool   `json:"current"`
}

// ErrUnknownSession rejects a session id ListSessions did not report.
var ErrUnknownSession = errors.New("unknown session")

// sessionProbeTimeout bounds how long ListSessions waits for a single
// session's socket to answer pane.list. Short and strict: one dead session
// must never make the whole listing slow, and every call here probes every
// session found on disk.
const sessionProbeTimeout = 1 * time.Second

// herdrConfigDir returns ~/.config/herdr, the root that both the default
// socket and every named session live under.
func herdrConfigDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory: %w", err)
	}
	return filepath.Join(home, ".config", "herdr"), nil
}

// ListSessions enumerates every herdr session on this machine: "default"
// (whatever herdr.DefaultSocket resolves to, honouring $HERDR_SOCK) plus one
// entry per ~/.config/herdr/sessions/*/herdr.sock. currentSocket marks which
// entry is the caller's active session.
//
// Every socket is best-effort probed, concurrently, with a short pane.list
// call. A session that does not answer is still returned — marked
// unreachable, with zero counts — rather than dropped: this list is a picker
// for a phone glancing at the herd, and one dead session silently hiding the
// rest (or making the whole request wait on it) is worse than showing it as
// dead.
func ListSessions(ctx context.Context, currentSocket string) ([]SessionInfo, error) {
	dir, err := herdrConfigDir()
	if err != nil {
		return nil, err
	}

	// The literal default socket, NOT herdr.DefaultSocket(): that honours
	// $HERDR_SOCK, so whenever the operator points this process at a named
	// session the "default" row silently became a second copy of it — same
	// socket, same pane counts, and BOTH rows marked current. "default" means
	// the session herdr uses when none is named; which one is active is
	// currentSocket's job to say, and conflating the two made the picker lie.
	sessions := []SessionInfo{{ID: "default", Name: "default", Socket: filepath.Join(dir, "herdr.sock")}}

	entries, err := os.ReadDir(filepath.Join(dir, "sessions"))
	switch {
	case err == nil:
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			sessions = append(sessions, SessionInfo{
				ID:     e.Name(),
				Name:   e.Name(),
				Socket: filepath.Join(dir, "sessions", e.Name(), "herdr.sock"),
			})
		}
	case !os.IsNotExist(err):
		// A real error, e.g. permissions. Most installs never create this
		// directory at all, which must not fail the whole request.
		return nil, fmt.Errorf("list herdr sessions: %w", err)
	}

	var wg sync.WaitGroup
	for i := range sessions {
		sessions[i].Current = sessions[i].Socket == currentSocket
		wg.Add(1)
		go func(s *SessionInfo) {
			defer wg.Done()
			probeSession(ctx, s)
		}(&sessions[i])
	}
	wg.Wait()

	return sessions, nil
}

// probeSession best-effort connects to s.Socket and calls pane.list with a
// short timeout, filling in reachability and counts. Left at its zero value
// (unreachable, zero counts) on any failure.
func probeSession(ctx context.Context, s *SessionInfo) {
	pctx, cancel := context.WithTimeout(ctx, sessionProbeTimeout)
	defer cancel()

	c := herdr.New(s.Socket)
	c.DialTimeout = sessionProbeTimeout
	c.CallTimeout = sessionProbeTimeout

	panes, err := c.ListPanes(pctx)
	if err != nil {
		return
	}
	s.Reachable = true
	s.Panes = len(panes)
	for _, p := range panes {
		if p.IsAgent() {
			s.Agents++
		}
	}
}

// resolveSession looks up id among ListSessions and returns its socket path.
// Session switching is resolved this way rather than by trusting id as a
// filesystem path directly, so a caller can only ever land on a socket
// ListSessions would itself have reported.
func resolveSession(ctx context.Context, currentSocket, id string) (SessionInfo, error) {
	sessions, err := ListSessions(ctx, currentSocket)
	if err != nil {
		return SessionInfo{}, err
	}
	for _, s := range sessions {
		if s.ID == id {
			return s, nil
		}
	}
	return SessionInfo{}, fmt.Errorf("%w: %q", ErrUnknownSession, id)
}
