package web

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/LoneExile/merino/internal/app"
)

// mountPaste serves staged paste images so the dashboard can render them
// inline (Kitty graphics never arrive over pane.read — only the path text).
func (s *Server) mountPaste(mux *http.ServeMux) {
	if s.cfg.Writer == nil {
		return
	}
	mux.Handle("GET /api/paste/{name}", s.authed(s.handlePasteGet))
}

func (s *Server) handlePasteGet(w http.ResponseWriter, r *http.Request, id Identity) {
	_ = id
	name := r.PathValue("name")
	if !safePasteName(name) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no such image"})
		return
	}
	dir, err := app.AttachDir()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "paste store unavailable"})
		return
	}
	absDir, err := filepath.Abs(dir)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "paste store unavailable"})
		return
	}
	path := filepath.Join(absDir, name)
	if filepath.Dir(path) != absDir {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no such image"})
		return
	}
	data, err := os.ReadFile(path)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no such image"})
		return
	}
	mime, _, ok := app.SniffImageMIME(data)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no such image"})
		return
	}
	w.Header().Set("Content-Type", mime)
	w.Header().Set("Cache-Control", "private, max-age=3600")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

func safePasteName(name string) bool {
	if name == "" || len(name) > 80 {
		return false
	}
	if strings.Contains(name, "/") || strings.Contains(name, "\\") || strings.Contains(name, "..") {
		return false
	}
	if !strings.HasPrefix(name, "paste-") {
		return false
	}
	rest := name[len("paste-"):]
	dot := strings.LastIndexByte(rest, '.')
	if dot <= 0 {
		return false
	}
	num, ext := rest[:dot], strings.ToLower(rest[dot+1:])
	switch ext {
	case "png", "jpg", "jpeg", "gif", "webp":
	default:
		return false
	}
	if num == "" {
		return false
	}
	for _, r := range num {
		if !unicode.IsDigit(r) {
			return false
		}
	}
	return true
}
