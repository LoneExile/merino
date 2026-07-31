// Package serve owns the boot path of the browser dashboard.
//
// It was lifted out of main.go so there is exactly one place that decides how
// the dashboard comes up: state directory, credentials, audit log, the three
// gates, pairing, push and the listener. An entry point's job is to parse its
// own input and call Start — nothing else.
package serve

import (
	"errors"
	"fmt"
	"log/slog"
	"net"
	"path/filepath"

	"github.com/LoneExile/merino/internal/app"
	"github.com/LoneExile/merino/internal/assets"
	"github.com/LoneExile/merino/internal/web"
)

// Options is everything an entry point chooses. Named fields rather than the
// positional (behindProxy, allowWrites, allowSessionSwitch bool) triple this
// replaced: three same-typed booleans across a package boundary is a swap the
// compiler cannot catch, and each one opens a different door.
type Options struct {
	// Source is the live agent projection. Whether writes and session
	// switching are actually available is decided by what it implements,
	// not by the gates below.
	Source web.Source
	Addr   string

	// BehindProxy marks cookies Secure and trusts the proxy's client-IP
	// header. Only true when the operator declared a TLS terminator.
	BehindProxy bool

	// AllowWrites and AllowSessionSwitch set the *initial* gate state. Both
	// remain live-togglable afterwards, so these are a starting position,
	// not a ceiling.
	AllowWrites        bool
	AllowSessionSwitch bool

	Logger *slog.Logger
}

// Dashboard is what a running dashboard exposes to its caller: the server
// plus the three pieces the macOS Settings sheet drives directly.
type Dashboard struct {
	Server   *web.Server
	Pairing  *web.Pairing
	Password *web.PasswordProvider
	Devices  *web.DeviceStore
}

// Start boots the browser dashboard.
//
// Public-release GUI launches always call this (default bind 0.0.0.0:8730) so
// QR pairing works after drag-to-Applications with zero flags. Safety is the
// login wall + one-shot pairing tokens + revocable device grants — not "did
// the operator remember --listen". CLI users can still pass an explicit
// --listen address (including 127.0.0.1) to narrow the bind.
func Start(opts Options) (*Dashboard, error) {
	var (
		src                = opts.Source
		addr               = opts.Addr
		behindProxy        = opts.BehindProxy
		allowWrites        = opts.AllowWrites
		allowSessionSwitch = opts.AllowSessionSwitch
		logger             = opts.Logger
	)

	stateDir := filepath.Dir(app.DefaultAuditPath())
	user, pass, generated, bootErr := web.LoadOrCreateBootstrap(stateDir)
	if bootErr != nil {
		return nil, bootErr
	}
	if generated {
		// Not "zero-config start" any more: password sign-in defaults OFF, so
		// these credentials do nothing until the operator opens that door in
		// Settings. Saying otherwise sends people to a login form that will
		// refuse them.
		logger.Info("stored local operator credentials",
			"user", user, "path", filepath.Join(stateDir, "bootstrap-creds.json"),
			"note", "usable only once password sign-in is enabled in Settings")
	}

	dist, err := assets.Dist()
	if err != nil {
		return nil, fmt.Errorf("locate frontend assets: %w", err)
	}

	// The audit log is opened whenever the dashboard runs, not only when
	// writes are enabled: subscribing to push notifications (below) is
	// itself an authenticated state change and deserves the same durable
	// record pane writes get. A read-only dashboard ran for a long time
	// before push existed without ever needing this directory to be
	// writable, so a failure here is only fatal when writes are also on —
	// otherwise push subscriptions simply go unaudited (logged once) rather
	// than taking the whole dashboard down.
	var (
		writer web.Writer
		audit  *app.Audit
	)
	if a, auditErr := app.NewAudit(app.DefaultAuditPath()); auditErr != nil {
		if allowWrites {
			return nil, auditErr
		}
		logger.Warn("could not open audit log; push subscriptions will not be recorded",
			"path", app.DefaultAuditPath(), "err", auditErr)
	} else {
		audit = a
	}
	// Always wire Writer when the source supports it and audit is open, so
	// Mac Settings can flip the live gate without restart. allowWrites only
	// sets the initial gate (CLI / disk / menubar default).
	if w, castOK := src.(web.Writer); castOK && audit != nil {
		writer = w
		if allowWrites {
			logger.Warn("web dashboard can write to your agents",
				"audit", app.DefaultAuditPath(),
				"note", "approvals, keys and interrupts are accepted from any signed-in browser")
		} else {
			logger.Info("web dashboard writes available but off",
				"note", "enable from Mac Settings or --allow-writes")
		}
	} else if allowWrites {
		if _, castOK := src.(web.Writer); !castOK {
			return nil, errors.New("source does not support writes")
		}
		return nil, errors.New("writes require an audit log")
	}

	// Session discovery is always offered: it is read-only, and knowing
	// which herdr socket a dashboard is even looking at is useful whether or
	// not switching between them is allowed.
	var sessions web.SessionSource
	if ss, ok := src.(web.SessionSource); ok {
		sessions = ss
	}

	var switcher web.SessionSwitcher
	if sw, castOK := src.(web.SessionSwitcher); castOK {
		switcher = sw
		if allowSessionSwitch {
			logger.Warn("web dashboard session switch enabled",
				"note", "repointing the session changes which agents every signed-in browser sees")
		} else {
			logger.Info("web dashboard session switch available but off",
				"note", "enable from Mac Settings or --allow-session-switch")
		}
	} else if allowSessionSwitch {
		return nil, errors.New("source does not support session switching")
	}

	publicURL := app.Env("PUBLIC_URL")
	if publicURL == "" {
		// Zero-config: QR targets this Mac on the LAN, not a missing tunnel host.
		publicURL = web.PreferLANBase(addr)
	}
	pairing := web.NewPairing(publicURL)
	provider := web.NewPasswordProvider(user, pass, ipResolver(behindProxy), behindProxy)
	provider.SetPairing(pairing)

	devices, devErr := web.OpenDeviceStore(stateDir)
	if devErr != nil {
		return nil, devErr
	}
	provider.SetDevices(devices)
	passwordLogin := web.PasswordLoginEnabled(stateDir)
	provider.SetPasswordLogin(passwordLogin)
	// Reported like the write and session-switch gates beside it. Without
	// this line the app's weakest door is the only one whose state a startup
	// log does not name, and an install that lost password sign-in to the
	// default change has nothing to read.
	logger.Info("password sign-in gate", "enabled", passwordLogin)
	if ou, op, ok := web.LoadOptionalPassword(stateDir); ok {
		provider.SetOptionalPassword(ou, op)
		logger.Info("optional phone password enabled", "user", ou)
	}

	// OAuth rung: mount scaffold routes only when explicitly configured AND
	// we have a public HTTPS base (redirect URIs). Full code flow is TODO.
	if oidc := web.OIDCFromEnv(); oidc.Enabled() && app.Env("PUBLIC_URL") != "" {
		logger.Info("OIDC scaffold enabled (authorization code not implemented yet)",
			"issuer", oidc.Issuer)
		// Keep password as the Mount provider; OIDC routes are registered via
		// a tiny side mount below after New — see attachOIDCScaffold.
		_ = oidc
	}

	srv, err := web.New(src, web.Config{
		Addr:     addr,
		Provider: provider,
		// Single operator still: paired devices carry view+control roles for
		// RequireRole later; today every authenticated identity is trusted.
		Policy:        web.SingleOperator{},
		BehindProxy:   behindProxy,
		Assets:        dist,
		Logger:        logger,
		Writer:        writer,
		Audit:         audit,
		Sessions:      sessions,
		Switcher:      switcher,
		SessionSwitch: allowSessionSwitch,
		AllowWrites:   allowWrites,
		Pairing:       pairing,
		PublicBaseURL: app.Env("PUBLIC_URL"),
		// Same directory the audit log above resolves to, so an operator who
		// already knows where one lives knows where to find the other.
		PushDir:  stateDir,
		Devices:  devices,
		StateDir: stateDir,
	})
	if err != nil {
		return nil, err
	}

	// Wire the edge-triggered blocked-transition hook straight into push.
	// AttachBlockedNotifier is a package function (not a Wails-bound method).
	// NotifyBlocked itself is a no-op whenever push failed to initialise or
	// was never configured, so wiring it unconditionally is safe.
	if agents, ok := src.(*app.AgentsService); ok {
		app.AttachBlockedNotifier(agents, srv.NotifyBlocked)
	}

	if err := srv.Start(); err != nil {
		return nil, err
	}

	if h, _, splitErr := net.SplitHostPort(addr); splitErr == nil && (h == "0.0.0.0" || h == "" || h == "::") {
		if behindProxy {
			logger.Warn("web dashboard is published to the public internet",
				"addr", addr,
				"note", "a single password is the only barrier; put an identity proxy in front of it")
		} else {
			logger.Warn("web dashboard is reachable from your whole network",
				"addr", addr,
				"note", "traffic is unencrypted HTTP; use a tunnel before exposing it beyond the LAN")
		}
	}
	return &Dashboard{Server: srv, Pairing: pairing, Password: provider, Devices: devices}, nil
}

// ipResolver picks how the client address is determined. Proxy headers are
// only believed when the operator has declared a proxy, because reached
// directly they are just strings the caller chose.
func ipResolver(behindProxy bool) web.IPResolver {
	if behindProxy {
		return web.ProxiedIP
	}
	return web.DirectIP
}
