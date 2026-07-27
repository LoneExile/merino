package web

import (
	"context"
	"net/http"

	"github.com/LoneExile/herdr-tunnel/internal/app"
)

// SessionSource lists the herdr sessions this server can see.
//
// Deliberately not part of Source, which answers "what do the agents look
// like": this answers "which herdr socket are we even looking at", a
// question that makes sense even for a server that can never write to a
// pane. A nil Sessions means the route does not exist — the same absence
// convention Writer uses for the write routes.
type SessionSource interface {
	// Sessions enumerates every known session, best-effort probed for
	// reachability and pane/agent counts.
	Sessions(ctx context.Context) ([]app.SessionInfo, error)
}

// SessionSwitcher repoints the server at a different herdr session.
//
// Kept separate from SessionSource so a server can offer the read-only
// session list without allowing a switch: that split is exactly what
// --allow-session-switch controls in main.go.
type SessionSwitcher interface {
	SwitchSession(id string) error
}

// handleSessions lists every known herdr session.
func (s *Server) handleSessions(w http.ResponseWriter, r *http.Request, _ Identity) {
	sessions, err := s.cfg.Sessions.Sessions(r.Context())
	if err != nil {
		s.log.Warn("list sessions", "err", err)
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "could not list sessions"})
		return
	}

	current := ""
	for _, sess := range sessions {
		if sess.Current {
			current = sess.ID
			break
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"current":   current,
		"canSwitch": s.cfg.Switcher != nil,
		"sessions":  sessions,
	})
}

// handleSessionSwitch repoints the server at a different herdr session.
// Mounted only when a Switcher is configured (see routes), so reaching this
// handler at all already implies the operator opted in with
// --allow-session-switch.
func (s *Server) handleSessionSwitch(w http.ResponseWriter, r *http.Request, id Identity) {
	body, ok := decode[struct {
		ID string `json:"id"`
	}](w, r)
	if !ok {
		return
	}

	if err := s.cfg.Switcher.SwitchSession(body.ID); err != nil {
		s.log.Warn("session switch refused", "id", body.ID, "actor", id.Name, "err", err)
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	s.log.Info("session switch", "id", body.ID, "actor", id.Name)
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
