package web

import (
	"net/http"
	"strings"

	"github.com/LoneExile/merino/internal/app"
)

// Creating an agent pane starts a process on the operator's machine. It is
// therefore a write in every sense that matters, and lives behind the same
// three gates as typing into a terminal: the build must carry a Writer, the
// runtime gate must be open, and the identity must be permitted — here by
// Policy.CanSpawn rather than CanControl, because there is no pane yet to
// scope the question to.
//
// The two GETs live here too. They exist only to fill in this form, and
// telling an unauthorised browser which agents the operator has installed is
// a small but pointless disclosure.

// authorizeSpawn is authorizeControl for an action with no target pane.
func (s *Server) authorizeSpawn(w http.ResponseWriter, r *http.Request, id Identity, action string) bool {
	if !s.WritesAllowed() {
		s.audit(r, id, action, "", "", false, "writes disabled")
		writeJSON(w, http.StatusForbidden, map[string]string{
			"error": "writes are disabled; enable Allow phone writes in Mac Settings",
		})
		return false
	}
	if !s.cfg.Policy.CanSpawn(id) {
		s.audit(r, id, action, "", "", false, "not permitted")
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "not permitted"})
		return false
	}
	return true
}

func (s *Server) handleWorkspaces(w http.ResponseWriter, r *http.Request, id Identity) {
	if !s.authorizeSpawn(w, r, id, "workspaces_list") {
		return
	}
	list, err := s.cfg.Writer.Workspaces()
	if err != nil {
		s.log.Warn("workspaces list failed", "err", err)
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	if list == nil {
		list = []app.Workspace{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"workspaces": list})
}

func (s *Server) handleAgentKinds(w http.ResponseWriter, r *http.Request, id Identity) {
	if !s.authorizeSpawn(w, r, id, "agent_kinds_list") {
		return
	}
	list, err := s.cfg.Writer.AgentKinds()
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	if list == nil {
		list = []app.AgentKind{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"kinds": list})
}

type startAgentPaneBody struct {
	WorkspaceID string `json:"workspaceId"`
	Kind        string `json:"kind"`
	Label       string `json:"label"`
}

func (s *Server) handleStartAgentPane(w http.ResponseWriter, r *http.Request, id Identity) {
	if !s.authorizeSpawn(w, r, id, "agent_pane_create") {
		return
	}
	body, ok := decode[startAgentPaneBody](w, r)
	if !ok {
		return
	}
	body.Kind = strings.TrimSpace(body.Kind)
	body.Label = strings.TrimSpace(body.Label)
	if body.Kind == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "kind is required"})
		return
	}

	detail := "kind=" + body.Kind + " workspace=" + body.WorkspaceID
	pane, err := s.cfg.Writer.StartAgentPane(body.WorkspaceID, body.Kind, body.Label)
	if err != nil {
		// Audited as a failure with the reason, like every other refused
		// write. This one can also fail slowly (the agent never reached a
		// prompt), which is worth having in the record.
		s.audit(r, id, "agent_pane_create", "", detail, false, err.Error())
		s.log.Warn("agent pane create failed", "actor", id.Name, "kind", body.Kind, "err", err)
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	s.audit(r, id, "agent_pane_create", pane.PaneID, detail, true, "")
	s.log.Info("agent pane created", "actor", id.Name, "pane", pane.PaneID, "kind", pane.Kind)
	writeJSON(w, http.StatusOK, pane)
}
