package web

import (
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/LoneExile/merino/internal/app"
)

// mountPaste serves images so the dashboard can render them inline
// (Kitty graphics never arrive over pane.read — only the path text).
//
//	GET /api/paste/{name}     — Merino staged paste-N.ext under AttachDir
//	GET /api/local-image?path= — other image files under the operator's home
//	  (agent-generated paths like ~/generated-images/donut.jpg)
func (s *Server) mountPaste(mux *http.ServeMux) {
	// Paste/local image GETs are read-only display. Mount even when the write
	// gate is closed so a phone can still see what the agent drew.
	mux.Handle("GET /api/paste/{name}", s.authed(s.handlePasteGet))
	mux.Handle("GET /api/local-image", s.authed(s.handleLocalImageGet))
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
	serveImageFile(w, path)
}

func (s *Server) handleLocalImageGet(w http.ResponseWriter, r *http.Request, id Identity) {
	_ = id
	raw := r.URL.Query().Get("path")
	if raw == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing path"})
		return
	}
	// Clients may double-encode; accept once-decoded.
	if u, err := url.QueryUnescape(raw); err == nil {
		raw = u
	}
	path, err := resolveHomeImagePath(raw)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no such image"})
		return
	}
	serveImageFile(w, path)
}

func serveImageFile(w http.ResponseWriter, path string) {
	data, err := os.ReadFile(path)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no such image"})
		return
	}
	// Cap response size (same order as attach max).
	if len(data) > app.MaxAttachBytes {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "image too large"})
		return
	}
	mime, _, ok := app.SniffImageMIME(data)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not an image"})
		return
	}
	w.Header().Set("Content-Type", mime)
	w.Header().Set("Cache-Control", "private, max-age=3600")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

// resolveHomeImagePath expands ~ and requires the file to live under $HOME
// with an image extension. Prevents reading arbitrary system paths.
func resolveHomeImagePath(p string) (string, error) {
	p = strings.TrimSpace(p)
	if p == "" {
		return "", os.ErrNotExist
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "", os.ErrNotExist
	}
	homeAbs, err := filepath.Abs(home)
	if err != nil {
		return "", err
	}
	if strings.HasPrefix(p, "~/") {
		p = filepath.Join(homeAbs, p[2:])
	} else if p == "~" {
		return "", os.ErrNotExist
	}
	// Reject obvious escapes before Abs.
	if strings.Contains(p, "\x00") {
		return "", os.ErrNotExist
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", err
	}
	// Must be under home (prefix boundary).
	sep := string(os.PathSeparator)
	if abs != homeAbs && !strings.HasPrefix(abs, homeAbs+sep) {
		return "", os.ErrNotExist
	}
	ext := strings.ToLower(filepath.Ext(abs))
	switch ext {
	case ".png", ".jpg", ".jpeg", ".gif", ".webp":
	default:
		return "", os.ErrNotExist
	}
	st, err := os.Stat(abs)
	if err != nil || st.IsDir() {
		return "", os.ErrNotExist
	}
	return abs, nil
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
