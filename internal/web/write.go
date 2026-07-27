package web

import (
	"encoding/json"
	"net/http"

	"github.com/LoneExile/herdr-tunnel/internal/app"
)

// Writer performs the operations that reach a pane.
//
// Deliberately a separate interface from Source. A Server built without one
// has no write routes at all — read-only is the absence of code, not a
// disabled flag — and enabling writes is a visible act at the call site.
type Writer interface {
	Respond(paneID, text string) error
	// SendText writes arbitrary text. Higher trust than Respond: bounded by
	// length rather than an allowlist, because replying to an agent is the
	// whole point of the product and canned answers cannot express it.
	SendText(paneID, text string) error
	SendKeys(paneID string, keys []string) error
	Focus(paneID string) error
	Interrupt(paneID string) error
}

// mountWrites registers the write routes. Called only when a Writer is set.
func (s *Server) mountWrites(mux *http.ServeMux) {
	mux.Handle("POST /api/panes/{id}/respond", s.authed(s.handleRespond))
	mux.Handle("POST /api/panes/{id}/text", s.authed(s.handleText))
	mux.Handle("POST /api/panes/{id}/keys", s.authed(s.handleKeys))
	mux.Handle("POST /api/panes/{id}/focus", s.authed(s.handleFocus))
	mux.Handle("POST /api/panes/{id}/interrupt", s.authed(s.handleInterrupt))
}

// authorizeWrite resolves the pane and checks the identity may control it.
//
// The pane is looked up from the store rather than trusted from the request:
// the client says which pane it wants, the server decides whether that pane
// exists and whether this identity may touch it.
func (s *Server) authorizeWrite(w http.ResponseWriter, r *http.Request, id Identity, action string) (string, bool) {
	paneID := r.PathValue("id")

	var target *app.Agent
	for _, a := range s.src.List() {
		if a.PaneID == paneID {
			target = &a
			break
		}
	}
	if target == nil || !s.cfg.Policy.CanControl(id, *target) {
		s.audit(r, id, action, paneID, "", false, "not permitted")
		// Identical response for "no such pane" and "not yours", so a caller
		// cannot enumerate panes by probing.
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no such pane"})
		return "", false
	}
	return paneID, true
}

func (s *Server) audit(r *http.Request, id Identity, action, paneID, detail string, allowed bool, errMsg string) {
	s.cfg.Audit.Record(app.AuditEntry{
		Actor:      id.Name,
		Source:     "web",
		RemoteAddr: s.clientIP(r),
		Action:     action,
		PaneID:     paneID,
		Detail:     detail,
		Allowed:    allowed,
		Error:      errMsg,
	})
}

// finish records the outcome and answers. Every write is audited on both the
// success and failure path; a log that only records successes hides exactly
// the attempts worth reviewing.
func (s *Server) finish(w http.ResponseWriter, r *http.Request, id Identity, action, paneID, detail string, err error) {
	if err != nil {
		s.audit(r, id, action, paneID, detail, false, err.Error())
		s.log.Warn("web write refused", "action", action, "pane", paneID, "actor", id.Name, "err", err)
		// The guard's messages name the allowlist rule that refused, which is
		// useful to the operator and harmless to disclose.
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	s.audit(r, id, action, paneID, detail, true, "")
	s.log.Info("web write", "action", action, "pane", paneID, "actor", id.Name)
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func decode[T any](w http.ResponseWriter, r *http.Request) (T, bool) {
	var body T
	// Bound the body: this endpoint is reachable from the internet.
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "malformed request body"})
		var zero T
		return zero, false
	}
	return body, true
}

func (s *Server) handleRespond(w http.ResponseWriter, r *http.Request, id Identity) {
	paneID, ok := s.authorizeWrite(w, r, id, "respond")
	if !ok {
		return
	}
	body, ok := decode[struct {
		Text string `json:"text"`
	}](w, r)
	if !ok {
		return
	}
	s.finish(w, r, id, "respond", paneID, body.Text, s.cfg.Writer.Respond(paneID, body.Text))
}

// handleText sends free-form text to a pane.
//
// This is the widest write in the API: unlike Respond it is not allowlisted,
// only length-bounded by the guard. It is audited with the full payload,
// because "sent something" is useless after the fact — what was sent is the
// entire record.
func (s *Server) handleText(w http.ResponseWriter, r *http.Request, id Identity) {
	paneID, ok := s.authorizeWrite(w, r, id, "text")
	if !ok {
		return
	}
	body, ok := decode[struct {
		Text string `json:"text"`
	}](w, r)
	if !ok {
		return
	}
	s.finish(w, r, id, "text", paneID, body.Text, s.cfg.Writer.SendText(paneID, body.Text))
}

func (s *Server) handleKeys(w http.ResponseWriter, r *http.Request, id Identity) {
	paneID, ok := s.authorizeWrite(w, r, id, "keys")
	if !ok {
		return
	}
	body, ok := decode[struct {
		Keys []string `json:"keys"`
	}](w, r)
	if !ok {
		return
	}
	detail, _ := json.Marshal(body.Keys)
	s.finish(w, r, id, "keys", paneID, string(detail), s.cfg.Writer.SendKeys(paneID, body.Keys))
}

func (s *Server) handleFocus(w http.ResponseWriter, r *http.Request, id Identity) {
	paneID, ok := s.authorizeWrite(w, r, id, "focus")
	if !ok {
		return
	}
	s.finish(w, r, id, "focus", paneID, "", s.cfg.Writer.Focus(paneID))
}

func (s *Server) handleInterrupt(w http.ResponseWriter, r *http.Request, id Identity) {
	paneID, ok := s.authorizeWrite(w, r, id, "interrupt")
	if !ok {
		return
	}
	s.finish(w, r, id, "interrupt", paneID, app.InterruptKey, s.cfg.Writer.Interrupt(paneID))
}
