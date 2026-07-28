package web

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/LoneExile/herdr-tunnel/internal/app"
)

// StateDir is the conventional on-disk home for dashboard secrets that are not
// the audit log itself (bootstrap password, devices, first-run stamp, VAPID).
func StateDir() string {
	return filepath.Dir(app.DefaultAuditPath())
}

// BootstrapCreds is the break-glass local operator account used when
// HERDR_TUNNEL_USER/PASS are not set (GUI double-click path).
type BootstrapCreds struct {
	User string `json:"user"`
	Pass string `json:"pass"`
}

// LoadOrCreateBootstrap returns env creds when set, otherwise a persisted
// random pair so the GUI can start a dashboard with zero config.
func LoadOrCreateBootstrap(dir string) (user, pass string, generated bool, err error) {
	if u, p := os.Getenv("HERDR_TUNNEL_USER"), os.Getenv("HERDR_TUNNEL_PASS"); u != "" && p != "" {
		return u, p, false, nil
	}
	if dir == "" {
		dir = StateDir()
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", "", false, fmt.Errorf("bootstrap: mkdir: %w", err)
	}
	path := filepath.Join(dir, "bootstrap-creds.json")
	if raw, readErr := os.ReadFile(path); readErr == nil {
		var c BootstrapCreds
		if json.Unmarshal(raw, &c) == nil && c.User != "" && c.Pass != "" {
			return c.User, c.Pass, false, nil
		}
	}
	passBytes := make([]byte, 18)
	if _, err := rand.Read(passBytes); err != nil {
		return "", "", false, fmt.Errorf("bootstrap: entropy: %w", err)
	}
	c := BootstrapCreds{
		User: "local",
		Pass: base64.RawURLEncoding.EncodeToString(passBytes),
	}
	raw, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return "", "", false, err
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		return "", "", false, fmt.Errorf("bootstrap: write: %w", err)
	}
	return c.User, c.Pass, true, nil
}

// OptionalPasswordPath is where a user-set phone password may live.
func OptionalPasswordPath(dir string) string {
	if dir == "" {
		dir = StateDir()
	}
	return filepath.Join(dir, "optional-password.json")
}

// OptionalPassword is a user-chosen password for phone/browser login without QR.
type OptionalPassword struct {
	User string `json:"user"`
	Pass string `json:"pass"`
}

// LoadOptionalPassword returns credentials if the user set them in Settings.
func LoadOptionalPassword(dir string) (user, pass string, ok bool) {
	raw, err := os.ReadFile(OptionalPasswordPath(dir))
	if err != nil {
		return "", "", false
	}
	var c OptionalPassword
	if json.Unmarshal(raw, &c) != nil || c.User == "" || c.Pass == "" {
		return "", "", false
	}
	return c.User, c.Pass, true
}

// SaveOptionalPassword persists a user-chosen password (plaintext on disk under
// 0600 home logs — same trust boundary as bootstrap-creds). Empty pass deletes.
func SaveOptionalPassword(dir, user, pass string) error {
	path := OptionalPasswordPath(dir)
	if pass == "" {
		_ = os.Remove(path)
		return nil
	}
	if user == "" {
		user = "phone"
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(OptionalPassword{User: user, Pass: pass}, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, raw, 0o600)
}

// FirstRunStampPath marks that the user finished (or skipped) first-run pairing.
func FirstRunStampPath(dir string) string {
	if dir == "" {
		dir = StateDir()
	}
	return filepath.Join(dir, "first-run-done")
}

// FirstRunPending is true until the stamp exists.
func FirstRunPending(dir string) bool {
	_, err := os.Stat(FirstRunStampPath(dir))
	return os.IsNotExist(err)
}

// MarkFirstRunDone writes the stamp so later launches stay quiet menubar.
func MarkFirstRunDone(dir string) error {
	path := FirstRunStampPath(dir)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, []byte("ok\n"), 0o600)
}
