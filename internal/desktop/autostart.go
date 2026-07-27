// Package desktop holds menu-bar-only capabilities: launch-at-login and
// GitHub release updates. Kept out of internal/web so the HTTP surface does
// not grow desktop-only routes.
package desktop

import (
	"fmt"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// Autostart wraps Wails' launch-at-login registration for the Settings sheet.
type Autostart struct {
	app *application.App
	id  string
}

// NewAutostart binds to a running Wails app. identifier should be the product
// reverse-DNS id (build/config.yml productIdentifier).
func NewAutostart(app *application.App, identifier string) *Autostart {
	return &Autostart{app: app, id: identifier}
}

// Enabled reports whether the app is registered to launch at login.
func (a *Autostart) Enabled() (bool, error) {
	if a == nil || a.app == nil {
		return false, fmt.Errorf("autostart: not available")
	}
	return a.app.Autostart.IsEnabled()
}

// Set registers or clears launch-at-login.
func (a *Autostart) Set(on bool) error {
	if a == nil || a.app == nil {
		return fmt.Errorf("autostart: not available")
	}
	if on {
		return a.app.Autostart.EnableWithOptions(application.AutostartOptions{Identifier: a.id})
	}
	return a.app.Autostart.Disable()
}
