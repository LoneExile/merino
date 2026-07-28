package web

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/LoneExile/merino/internal/app"
)

// StateDir is the conventional on-disk home for dashboard secrets that are not
// the audit log itself (bootstrap password, devices, first-run stamp, VAPID).
func StateDir() string {
	return filepath.Dir(app.DefaultAuditPath())
}

// BootstrapCreds is the break-glass local operator account used when
// MERINO_USER/PASS (or legacy HERDR_TUNNEL_*) are not set (GUI path).
type BootstrapCreds struct {
	User string `json:"user"`
	Pass string `json:"pass"`
}

// LoadOrCreateBootstrap returns env creds when set, otherwise a persisted
// random pair so the GUI can start a dashboard with zero config.
func LoadOrCreateBootstrap(dir string) (user, pass string, generated bool, err error) {
	if u, p := app.Env("USER"), app.Env("PASS"); u != "" && p != "" {
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

// passwordLoginPath stores whether HTTP username/password sign-in is allowed.
func passwordLoginPath(dir string) string {
	if dir == "" {
		dir = StateDir()
	}
	return filepath.Join(dir, "password-login.json")
}

type passwordLoginFile struct {
	Enabled bool `json:"enabled"`
}

// PasswordLoginEnabled reports whether user/pass HTTP login is on.
// Missing file ⇒ enabled (legacy default).
func PasswordLoginEnabled(dir string) bool {
	raw, err := os.ReadFile(passwordLoginPath(dir))
	if err != nil {
		return true
	}
	var f passwordLoginFile
	if json.Unmarshal(raw, &f) != nil {
		return true
	}
	return f.Enabled
}

// SetPasswordLoginEnabled persists the HTTP password-login toggle.
func SetPasswordLoginEnabled(dir string, enabled bool) error {
	if err := os.MkdirAll(filepath.Dir(passwordLoginPath(dir)), 0o700); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(passwordLoginFile{Enabled: enabled}, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(passwordLoginPath(dir), raw, 0o600)
}

func sessionSwitchPath(dir string) string {
	if dir == "" {
		dir = StateDir()
	}
	return filepath.Join(dir, "session-switch.json")
}

type sessionSwitchFile struct {
	Enabled bool `json:"enabled"`
}

// SessionSwitchExplicit is true when the operator has saved a preference.
func SessionSwitchExplicit(dir string) bool {
	_, err := os.ReadFile(sessionSwitchPath(dir))
	return err == nil
}

// SessionSwitchEnabled reports whether phone/web may switch herdr sessions.
// Missing file ⇒ false (caller may choose a GUI default).
func SessionSwitchEnabled(dir string) bool {
	raw, err := os.ReadFile(sessionSwitchPath(dir))
	if err != nil {
		return false
	}
	var f sessionSwitchFile
	if json.Unmarshal(raw, &f) != nil {
		return false
	}
	return f.Enabled
}

// SetSessionSwitchEnabled persists the phone session-switch toggle.
func SetSessionSwitchEnabled(dir string, enabled bool) error {
	if err := os.MkdirAll(filepath.Dir(sessionSwitchPath(dir)), 0o700); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(sessionSwitchFile{Enabled: enabled}, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(sessionSwitchPath(dir), raw, 0o600)
}

func allowWritesPath(dir string) string {
	if dir == "" {
		dir = StateDir()
	}
	return filepath.Join(dir, "allow-writes.json")
}

type allowWritesFile struct {
	Enabled bool `json:"enabled"`
}

// AllowWritesExplicit is true when the operator has saved a preference.
func AllowWritesExplicit(dir string) bool {
	_, err := os.ReadFile(allowWritesPath(dir))
	return err == nil
}

// AllowWritesEnabled reports whether phone/web may write to panes.
// Missing file ⇒ false for explicit reads; main.go may default menubar on.
func AllowWritesEnabled(dir string) bool {
	raw, err := os.ReadFile(allowWritesPath(dir))
	if err != nil {
		return false
	}
	var f allowWritesFile
	if json.Unmarshal(raw, &f) != nil {
		return false
	}
	return f.Enabled
}

// SetAllowWritesEnabled persists the phone write toggle.
func SetAllowWritesEnabled(dir string, enabled bool) error {
	if err := os.MkdirAll(filepath.Dir(allowWritesPath(dir)), 0o700); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(allowWritesFile{Enabled: enabled}, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(allowWritesPath(dir), raw, 0o600)
}

