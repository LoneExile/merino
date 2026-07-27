package desktop

import (
	"context"
	"fmt"
	"time"

	"github.com/LoneExile/herdr-tunnel/internal/web"
	"github.com/wailsapp/wails/v3/pkg/application"
)

// Settings is the Wails-bound surface for desktop-only preferences:
// launch-at-login, GitHub updates, and phone pairing QR tickets.
type Settings struct {
	Auto    *Autostart
	Update  *Updater
	Pairing *web.Pairing
}

// NewSettings wires the three optional backends. Any may be nil; methods then
// return a clear error the Settings sheet can show.
func NewSettings(app *application.App, productID, version, repo string, pairing *web.Pairing) *Settings {
	var auto *Autostart
	if app != nil {
		auto = NewAutostart(app, productID)
	}
	return &Settings{
		Auto:    auto,
		Update:  &Updater{Repo: repo, Current: version},
		Pairing: pairing,
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

// CheckUpdate looks up the latest GitHub release.
func (s *Settings) CheckUpdate() (UpdateInfo, error) {
	if s.Update == nil {
		return UpdateInfo{}, fmt.Errorf("updates not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	return s.Update.Check(ctx)
}

// MintPairing returns a short-lived QR ticket for phone login.
func (s *Settings) MintPairing() (web.PairingTicket, error) {
	if s.Pairing == nil {
		return web.PairingTicket{}, fmt.Errorf("pairing requires the web dashboard (--listen)")
	}
	return s.Pairing.Mint()
}

// SetPairingBaseURL sets the public origin encoded into QR links
// (e.g. https://herdr-tunnel.0dl.me).
func (s *Settings) SetPairingBaseURL(base string) error {
	if s.Pairing == nil {
		return fmt.Errorf("pairing requires the web dashboard (--listen)")
	}
	s.Pairing.SetBaseURL(base)
	return nil
}
