package app

import (
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// MaxAttachBytes caps a single staged image (8 MiB). Matches what phone
// cameras produce after modest compression; larger blobs are refused.
const MaxAttachBytes = 8 << 20

// MaxAttachBatch caps how many images one send may stage.
const MaxAttachBatch = 4

// attachDir is where staged paste images land. Under the user cache so
// reboot/OS cleaners may reclaim, but stable for the agent to open.
func AttachDir() (string, error) {
	base, err := os.UserCacheDir()
	if err != nil || base == "" {
		base = os.TempDir()
	}
	dir := filepath.Join(base, "merino", "paste")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("attach dir: %w", err)
	}
	// Best-effort: drop files older than 24h so paste debris does not grow forever.
	pruneAttachDir(dir, 24*time.Hour)
	return dir, nil
}

func pruneAttachDir(dir string, olderThan time.Duration) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	cutoff := time.Now().Add(-olderThan)
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		info, err := e.Info()
		if err != nil || info.ModTime().After(cutoff) {
			continue
		}
		_ = os.Remove(filepath.Join(dir, e.Name()))
	}
}

// sniffsMIME from magic bytes. Only image types agents commonly accept.
func SniffImageMIME(data []byte) (mime, ext string, ok bool) {
	if len(data) < 12 {
		return "", "", false
	}
	switch {
	case data[0] == 0x89 && data[1] == 0x50 && data[2] == 0x4e && data[3] == 0x47:
		return "image/png", ".png", true
	case data[0] == 0xff && data[1] == 0xd8 && data[2] == 0xff:
		return "image/jpeg", ".jpg", true
	case len(data) >= 6 && (string(data[0:6]) == "GIF87a" || string(data[0:6]) == "GIF89a"):
		return "image/gif", ".gif", true
	case string(data[0:4]) == "RIFF" && string(data[8:12]) == "WEBP":
		return "image/webp", ".webp", true
	default:
		return "", "", false
	}
}

// StageImage writes image bytes to a host temp file and returns the absolute
// path. Agents (omp/claude/…) accept the path the same way a terminal paste
// of a clipboard image does — they open the file, not a wire protocol.
//
// declaredMIME is advisory; the on-disk type is always taken from magic bytes.
func StageImage(declaredMIME string, data []byte) (path string, mime string, err error) {
	if len(data) == 0 {
		return "", "", fmt.Errorf("%w: empty image", ErrNotAllowed)
	}
	if len(data) > MaxAttachBytes {
		return "", "", fmt.Errorf("%w: image %d bytes exceeds %d", ErrTooLong, len(data), MaxAttachBytes)
	}
	mime, ext, ok := SniffImageMIME(data)
	if !ok {
		return "", "", fmt.Errorf("%w: not a supported image (png/jpeg/gif/webp)", ErrNotAllowed)
	}
	// If the client claimed a type, it must agree with the bytes.
	if declaredMIME != "" {
		d := strings.ToLower(strings.TrimSpace(declaredMIME))
		if i := strings.IndexByte(d, ';'); i >= 0 {
			d = strings.TrimSpace(d[:i])
		}
		if d != mime && !(d == "image/jpg" && mime == "image/jpeg") {
			return "", "", fmt.Errorf("%w: declared %s but bytes are %s", ErrNotAllowed, d, mime)
		}
	}

	dir, err := AttachDir()
	if err != nil {
		return "", "", err
	}
	name := fmt.Sprintf("paste-%d%s", time.Now().UnixNano(), ext)
	path = filepath.Join(dir, name)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return "", "", fmt.Errorf("write image: %w", err)
	}
	return path, mime, nil
}

// AttachImage stages one image for a known pane and returns the host path.
// The caller is expected to send that path into the agent (SendText / prompt).
func (s *AgentsService) AttachImage(paneID, declaredMIME string, data []byte) (string, error) {
	if err := s.guard.CheckPane(paneID); err != nil {
		return "", err
	}
	path, mime, err := StageImage(declaredMIME, data)
	if err != nil {
		return "", err
	}
	s.log.Info("attach_image", "pane", paneID, "mime", mime, "bytes", len(data), "path", path)
	return path, nil
}

// AttachImageB64 is the Wails-friendly form: base64-encoded image bytes.
func (s *AgentsService) AttachImageB64(paneID, mime, b64 string) (string, error) {
	raw, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		raw, err = base64.RawURLEncoding.DecodeString(b64)
	}
	if err != nil {
		return "", fmt.Errorf("%w: invalid base64", ErrNotAllowed)
	}
	return s.AttachImage(paneID, mime, raw)
}
