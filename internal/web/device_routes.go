package web

import (
	"encoding/json"
	"net/http"
	"strings"
)

// isOperator reports identities that may manage devices / optional password.
// Paired phones (device:*) can use the dashboard but cannot revoke siblings
// or change the shared phone password.
func isOperator(id Identity) bool {
	return !IsDeviceSubject(id.Subject)
}

// mountDevices registers authenticated device inventory + revoke endpoints.
func (s *Server) mountDevices(mux *http.ServeMux) {
	if s.cfg.Devices == nil {
		return
	}
	mux.Handle("GET /api/devices", s.authed(s.handleDevicesList))
	mux.Handle("POST /api/devices/revoke", s.authed(s.handleDeviceRevoke))
	mux.Handle("POST /api/devices/revoke-all", s.authed(s.handleDeviceRevokeAll))
	mux.Handle("POST /api/auth/password", s.authed(s.handleSetOptionalPassword))
	mux.Handle("POST /api/first-run/done", s.authed(s.handleFirstRunDone))
}

func (s *Server) handleDevicesList(w http.ResponseWriter, r *http.Request, id Identity) {
	_ = r
	if !isOperator(id) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "only the desktop operator can list devices"})
		return
	}
	list := s.cfg.Devices.List()
	writeJSON(w, http.StatusOK, map[string]any{
		"devices":         list,
		"activeCount":     s.cfg.Devices.CountActive(),
		"firstRunPending": FirstRunPending(s.stateDir()),
	})
}

func (s *Server) handleDeviceRevoke(w http.ResponseWriter, r *http.Request, id Identity) {
	if !isOperator(id) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "only the desktop operator can revoke devices"})
		return
	}
	var body struct {
		ID string `json:"id"`
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10))
	if err := dec.Decode(&body); err != nil || strings.TrimSpace(body.ID) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "id required"})
		return
	}
	ok, err := s.cfg.Devices.Revoke(body.ID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown device"})
		return
	}
	s.audit(r, id, "device_revoke", "", "id="+body.ID, true, "")
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleDeviceRevokeAll(w http.ResponseWriter, r *http.Request, id Identity) {
	if !isOperator(id) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "only the desktop operator can revoke devices"})
		return
	}
	n, err := s.cfg.Devices.RevokeAll()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	s.audit(r, id, "device_revoke_all", "", "", true, "")
	writeJSON(w, http.StatusOK, map[string]any{"revoked": n})
}

func (s *Server) handleSetOptionalPassword(w http.ResponseWriter, r *http.Request, id Identity) {
	if !isOperator(id) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "only the desktop operator can set the phone password"})
		return
	}
	var body struct {
		User string `json:"user"`
		Pass string `json:"pass"`
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10))
	if err := dec.Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad json"})
		return
	}
	if err := SaveOptionalPassword(s.stateDir(), body.User, body.Pass); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if pp, ok := s.cfg.Provider.(*PasswordProvider); ok {
		pp.SetOptionalPassword(body.User, body.Pass)
	}
	s.audit(r, id, "optional_password_set", "", "", true, "")
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "enabled": body.Pass != ""})
}

func (s *Server) handleFirstRunDone(w http.ResponseWriter, r *http.Request, id Identity) {
	_ = id
	_ = r
	if err := MarkFirstRunDone(s.stateDir()); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) stateDir() string {
	if s.cfg.StateDir != "" {
		return s.cfg.StateDir
	}
	return StateDir()
}
