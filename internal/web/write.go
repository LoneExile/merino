package web

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/LoneExile/merino/internal/app"
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
	// RenamePane, RenameTab and RenameWorkspace set a display name. Gated
	// behind the same Writer as every other write: a rename reaches the
	// same herdr session as an approval or a keystroke, so it is no less
	// sensitive.
	RenamePane(paneID, name string) error
	RenameTab(tabID, name string) error
	RenameWorkspace(workspaceID, name string) error
	// AttachImage stages image bytes on the host and returns the absolute
	// path. Used for clipboard-paste / file-picker images so agents can open
	// the file the same way a terminal Ctrl+V paste does.
	AttachImage(paneID, mime string, data []byte) (path string, err error)
}

// mountWrites registers the write routes. Called only when a Writer is set.
func (s *Server) mountWrites(mux *http.ServeMux) {
	mux.Handle("POST /api/panes/{id}/respond", s.authed(s.handleRespond))
	mux.Handle("POST /api/panes/{id}/text", s.authed(s.handleText))
	mux.Handle("POST /api/panes/{id}/keys", s.authed(s.handleKeys))
	mux.Handle("POST /api/panes/{id}/focus", s.authed(s.handleFocus))
	mux.Handle("POST /api/panes/{id}/interrupt", s.authed(s.handleInterrupt))
	mux.Handle("POST /api/panes/{id}/rename", s.authed(s.handleRenamePane))
	mux.Handle("POST /api/tabs/{id}/rename", s.authed(s.handleRenameTab))
	mux.Handle("POST /api/workspaces/{id}/rename", s.authed(s.handleRenameWorkspace))
	mux.Handle("POST /api/panes/{id}/attach", s.authed(s.handleAttach))
}

// authorizeWrite resolves the pane and checks the identity may control it.
//
// The pane is looked up from the store rather than trusted from the request:
// the client says which pane it wants, the server decides whether that pane
// exists and whether this identity may touch it.
func (s *Server) authorizeWrite(w http.ResponseWriter, r *http.Request, id Identity, action string) (string, bool) {
	paneID := r.PathValue("id")
	ok := s.authorizeControl(w, r, id, action, paneID, "no such pane", func(a app.Agent) bool {
		return a.PaneID == paneID
	})
	return paneID, ok
}

// authorizeControl generalises authorizeWrite to targets that are not a
// pane id. Tabs and workspaces are not first-class entities in Store — only
// panes are, each carrying a tab and workspace id — so an agent matching
// pred stands in for "does this target exist" exactly as the pane occupying
// it does for authorizeWrite, and the same identical-on-refusal response
// applies for the same reason: neither existence nor permission should be
// distinguishable by probing.
func (s *Server) authorizeControl(w http.ResponseWriter, r *http.Request, id Identity, action, targetID, notFoundMsg string, pred func(app.Agent) bool) bool {
	if !s.WritesAllowed() {
		s.audit(r, id, action, targetID, "", false, "writes disabled")
		writeJSON(w, http.StatusForbidden, map[string]string{
			"error": "writes are disabled; enable Allow phone writes in Mac Settings",
		})
		return false
	}
	var target *app.Agent
	for _, a := range s.src.List() {
		if pred(a) {
			target = &a
			break
		}
	}
	if target == nil || !s.cfg.Policy.CanControl(id, *target) {
		s.audit(r, id, action, targetID, "", false, "not permitted")
		writeJSON(w, http.StatusNotFound, map[string]string{"error": notFoundMsg})
		return false
	}
	return true
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

// handleRenamePane sets a pane's display name.
func (s *Server) handleRenamePane(w http.ResponseWriter, r *http.Request, id Identity) {
	paneID, ok := s.authorizeWrite(w, r, id, "rename_pane")
	if !ok {
		return
	}
	body, ok := decode[struct {
		Name string `json:"name"`
	}](w, r)
	if !ok {
		return
	}
	s.finish(w, r, id, "rename_pane", paneID, body.Name, s.cfg.Writer.RenamePane(paneID, body.Name))
}

// handleRenameTab and handleRenameWorkspace authorise via authorizeControl
// rather than authorizeWrite: a tab or workspace id is matched against the
// agents occupying it, since neither is tracked as a first-class entity in
// Store.
func (s *Server) handleRenameTab(w http.ResponseWriter, r *http.Request, id Identity) {
	tabID := r.PathValue("id")
	if !s.authorizeControl(w, r, id, "rename_tab", tabID, "no such tab", func(a app.Agent) bool {
		return a.TabID == tabID
	}) {
		return
	}
	body, ok := decode[struct {
		Name string `json:"name"`
	}](w, r)
	if !ok {
		return
	}
	s.finish(w, r, id, "rename_tab", tabID, body.Name, s.cfg.Writer.RenameTab(tabID, body.Name))
}

func (s *Server) handleRenameWorkspace(w http.ResponseWriter, r *http.Request, id Identity) {
	workspaceID := r.PathValue("id")
	if !s.authorizeControl(w, r, id, "rename_workspace", workspaceID, "no such workspace", func(a app.Agent) bool {
		return a.WorkspaceID == workspaceID
	}) {
		return
	}
	body, ok := decode[struct {
		Name string `json:"name"`
	}](w, r)
	if !ok {
		return
	}
	s.finish(w, r, id, "rename_workspace", workspaceID, body.Name, s.cfg.Writer.RenameWorkspace(workspaceID, body.Name))
}

// handleAttach stages an image on the host for a pane and returns the path.
//
// Accepts either multipart/form-data (field "file") or JSON
// {"mime":"image/png","data":"<base64>"}. The staged path is what the agent
// will open — matching terminal clipboard-image paste behaviour.
func (s *Server) handleAttach(w http.ResponseWriter, r *http.Request, id Identity) {
	paneID, ok := s.authorizeWrite(w, r, id, "attach")
	if !ok {
		return
	}

	const maxBody = app.MaxAttachBytes + 512<<10 // room for multipart overhead
	r.Body = http.MaxBytesReader(w, r.Body, maxBody)

	var (
		data []byte
		mime string
		err  error
	)

	ct := r.Header.Get("Content-Type")
	if strings.HasPrefix(ct, "multipart/form-data") {
		if err = r.ParseMultipartForm(maxBody); err != nil {
			s.audit(r, id, "attach", paneID, "", false, "multipart: "+err.Error())
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "malformed multipart body"})
			return
		}
		f, hdr, ferr := r.FormFile("file")
		if ferr != nil {
			s.audit(r, id, "attach", paneID, "", false, "file: "+ferr.Error())
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing file field"})
			return
		}
		defer f.Close()
		data, err = io.ReadAll(io.LimitReader(f, app.MaxAttachBytes+1))
		if err != nil {
			s.finish(w, r, id, "attach", paneID, "", err)
			return
		}
		mime = hdr.Header.Get("Content-Type")
		if mime == "" {
			mime = r.FormValue("mime")
		}
	} else {
		// Do NOT use decode[T] here — it caps the body at 8 KiB (form-sized).
		// A base64 PNG is often megabytes; MaxBytesReader above already bounds us.
		var body struct {
			MIME string `json:"mime"`
			Data string `json:"data"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			s.audit(r, id, "attach", paneID, "", false, "json: "+err.Error())
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "malformed request body"})
			return
		}
		raw, berr := base64.StdEncoding.DecodeString(body.Data)
		if berr != nil {
			raw, berr = base64.RawURLEncoding.DecodeString(body.Data)
		}
		if berr != nil {
			s.audit(r, id, "attach", paneID, "", false, "base64: "+berr.Error())
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid base64 data"})
			return
		}
		data = raw
		mime = body.MIME
	}

	path, err := s.cfg.Writer.AttachImage(paneID, mime, data)
	if err != nil {
		s.finish(w, r, id, "attach", paneID, fmt.Sprintf("bytes=%d mime=%s", len(data), mime), err)
		return
	}
	s.audit(r, id, "attach", paneID, fmt.Sprintf("bytes=%d mime=%s path=%s", len(data), mime, filepath.Base(path)), true, "")
	writeJSON(w, http.StatusOK, map[string]string{"path": path, "mime": mime})
}
