package desktop

import (
	"context"
	"fmt"
	"time"

	"github.com/LoneExile/merino/internal/web"
	"github.com/wailsapp/wails/v3/pkg/application"
)

// Settings is the Wails-bound surface for desktop-only preferences:
// launch-at-login, GitHub updates, and phone pairing QR tickets.
type Settings struct {
	Auto       *Autostart
	Update     *Updater
	Pairing    *web.Pairing
	Devices    *web.DeviceStore
	StateDir   string
	ListenAddr string // dashboard bind, e.g. 0.0.0.0:8730 — for LAN origin chips
	// Password is the live HTTP password provider (same process as web server).
	// Nil when web is down; file persistence still works.
	Password *web.PasswordProvider
	// webServer is set after start so Settings can flip runtime gates.
	webServer *web.Server
}

// NewSettings wires the three optional backends. Any may be nil; methods then
// return a clear error the Settings sheet can show.
func NewSettings(app *application.App, productID, version, repo string, pairing *web.Pairing, devices *web.DeviceStore, stateDir, listenAddr string, password *web.PasswordProvider) *Settings {
	var auto *Autostart
	if app != nil {
		auto = NewAutostart(app, productID)
	}
	return &Settings{
		Auto:       auto,
		Update:     &Updater{Repo: repo, Current: version},
		Pairing:    pairing,
		Devices:    devices,
		StateDir:   stateDir,
		ListenAddr: listenAddr,
		Password:   password,
	}
}

// ServiceName identifies the service in Wails logs.
func (s *Settings) ServiceName() string { return "DesktopSettings" }

// LaunchAtLogin reports whether the app opens at login.
func (s *Settings) LaunchAtLogin() (bool, error) {
	if s.Auto == nil {
		return false, fmt.Errorf("launch at login is only available in the menu-bar app")
	}
	return s.Auto.Enabled()
}

// SetLaunchAtLogin enables or disables open-at-login.
func (s *Settings) SetLaunchAtLogin(on bool) error {
	if s.Auto == nil {
		return fmt.Errorf("launch at login is only available in the menu-bar app")
	}
	return s.Auto.Set(on)
}

// CheckUpdate looks up the latest GitHub release (read-only; does not download).
func (s *Settings) CheckUpdate() (UpdateInfo, error) {
	if s.Update == nil {
		return UpdateInfo{}, fmt.Errorf("updates not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	return s.Update.Check(ctx)
}

// InstallUpdate downloads the latest macOS .app zip via Wails updater, verifies
// SHA256SUMS, swaps the bundle, and relaunches. Call only after CheckUpdate
// reported canInstall (or it re-checks). Blocks until staged; process then quits.
func (s *Settings) InstallUpdate() (InstallResult, error) {
	if s.Update == nil {
		return InstallResult{}, fmt.Errorf("updates not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	return s.Update.Install(ctx)
}

// MintPairing returns a short-lived QR ticket for phone login.
func (s *Settings) MintPairing() (web.PairingTicket, error) {
	if s.Pairing == nil {
		return web.PairingTicket{}, fmt.Errorf("pairing requires the web dashboard")
	}
	return s.Pairing.Mint()
}

// SetPairingBaseURL sets the public origin encoded into QR links
// (e.g. https://merino.example).
func (s *Settings) SetPairingBaseURL(base string) error {
	if s.Pairing == nil {
		return fmt.Errorf("pairing requires the web dashboard")
	}
	s.Pairing.SetBaseURL(base)
	return nil
}

// ListDevices returns paired phones (including revoked).
func (s *Settings) ListDevices() ([]web.Device, error) {
	if s.Devices == nil {
		return nil, fmt.Errorf("device store unavailable")
	}
	// Active only — revoked grants are pruned or hidden so the list does not
	// accumulate dead UA rows from re-pairs.
	return s.Devices.ListActive(), nil
}

// RevokeDevice marks one paired device revoked.
func (s *Settings) RevokeDevice(id string) error {
	if s.Devices == nil {
		return fmt.Errorf("device store unavailable")
	}
	ok, err := s.Devices.Revoke(id)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("unknown device")
	}
	// Drop the row so the list does not keep "revoked" forever.
	_, _ = s.Devices.PruneRevoked()
	return nil
}

// RevokeAllDevices panic-revokes every paired phone session grant.
func (s *Settings) RevokeAllDevices() (int, error) {
	if s.Devices == nil {
		return 0, fmt.Errorf("device store unavailable")
	}
	// Panic: drop every grant immediately so Settings stays empty and phones
	// must re-pair. Session cookies die on next API call (device missing).
	return s.Devices.RemoveAll()
}

// FirstRunPending reports whether the first-run pairing splash should show.
func (s *Settings) FirstRunPending() bool {
	return web.FirstRunPending(s.StateDir)
}

// MarkFirstRunDone suppresses the first-run pairing splash on later launches.
func (s *Settings) MarkFirstRunDone() error {
	return web.MarkFirstRunDone(s.StateDir)
}

// SetOptionalPassword enables username/password phone login without QR.
func (s *Settings) SetOptionalPassword(user, pass string) error {
	return web.SaveOptionalPassword(s.StateDir, user, pass)
}

// OptionalPasswordEnabled reports whether a user-set phone password exists.
func (s *Settings) OptionalPasswordEnabled() bool {
	_, _, ok := web.LoadOptionalPassword(s.StateDir)
	return ok
}

// AccessOrigins returns localhost + LAN (and never invents a Cloudflare URL).
// The Settings sheet uses these as one-tap QR bases before any tunnel setup.
func (s *Settings) AccessOrigins() []web.AccessOrigin {
	return web.LocalAccessOrigins(s.ListenAddr)
}

// DefaultPairBase is the best LAN/local origin for a first QR.
func (s *Settings) DefaultPairBase() string {
	return web.PreferLANBase(s.ListenAddr)
}

// PasswordLoginEnabled reports whether HTTP username/password sign-in is on.
func (s *Settings) PasswordLoginEnabled() bool {
	if s.Password != nil {
		return s.Password.PasswordLogin()
	}
	return web.PasswordLoginEnabled(s.StateDir)
}

// SetPasswordLoginEnabled turns HTTP user/pass sign-in on or off.
// QR pairing is unaffected. Re-enable anytime from this desktop Settings UI.
func (s *Settings) SetPasswordLoginEnabled(on bool) error {
	if err := web.SetPasswordLoginEnabled(s.StateDir, on); err != nil {
		return err
	}
	if s.Password != nil {
		s.Password.SetPasswordLogin(on)
	}
	return nil
}

// SetWebServer wires the live web server for runtime Settings toggles.
func (s *Settings) SetWebServer(srv *web.Server) {
	if s == nil {
		return
	}
	s.webServer = srv
}

// SessionSwitchEnabled reports whether phone/web may switch herdr sessions.
func (s *Settings) SessionSwitchEnabled() bool {
	if s != nil && s.webServer != nil {
		return s.webServer.SessionSwitchAllowed()
	}
	return web.SessionSwitchEnabled(s.StateDir)
}

// SetSessionSwitchEnabled turns phone session switching on or off.
// Persists to disk and updates the live gate immediately.
func (s *Settings) SetSessionSwitchEnabled(on bool) error {
	if err := web.SetSessionSwitchEnabled(s.StateDir, on); err != nil {
		return err
	}
	if s.webServer != nil {
		return s.webServer.SetSessionSwitch(on)
	}
	return nil
}

// AllowWritesEnabled reports whether phone/web may write to panes.
func (s *Settings) AllowWritesEnabled() bool {
	if s != nil && s.webServer != nil {
		return s.webServer.WritesAllowed()
	}
	return web.AllowWritesEnabled(s.StateDir)
}

// SetAllowWritesEnabled turns phone pane writes on or off.
// Persists to disk and updates the live gate immediately.
func (s *Settings) SetAllowWritesEnabled(on bool) error {
	if err := web.SetAllowWritesEnabled(s.StateDir, on); err != nil {
		return err
	}
	if s.webServer != nil {
		return s.webServer.SetAllowWrites(on)
	}
	return nil
}
