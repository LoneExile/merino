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
	"os"
	"path/filepath"
	"strings"

	"github.com/LoneExile/merino/internal/app"
	"github.com/LoneExile/merino/internal/assets"
	"github.com/LoneExile/merino/internal/config"
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

	// PasswordLogin is the third gate, already resolved by the caller so
	// config.yml gets a say. Its zero value keeps today's behaviour: fall
	// back to the panel's side-file, which defaults off.
	PasswordLogin config.Gate

	// PublicURL is the base a pairing QR points at. Empty autodetects a LAN
	// address, which inside a container is the container's own address — a
	// URL no phone can open.
	PublicURL string

	// AuthUser and AuthPasswordFile name the operator credential
	// explicitly. This is how a headless install admits anyone at all: a
	// Kubernetes Secret mount is a file, and merinod deliberately ships no
	// command that could set a password on a running daemon.
	//
	// The password is read from the FILE, never from a config key and never
	// from argv. A literal password in config.yml would be copied around
	// with the rest of the deployment.
	AuthUser         string
	AuthPasswordFile string

	// StateDir holds credentials, paired devices, VAPID keys, push
	// subscriptions, the three gate files and the audit log. Empty means the
	// platform default; Prepare resolves paths.stateDir into it.
	//
	// Moving it on an existing install orphans every paired device and every
	// push subscription SILENTLY — the phone simply stops being signed in —
	// so nothing here ever changes it on the operator's behalf.
	StateDir string

	Logger *slog.Logger

	// OAuth* are the config.yml-derived OAuth layer, resolved by Prepare
	// (which reads the secret file). *Set is true when config.yml owns the
	// provider, which pins it above the Settings UI.
	OAuthGitHub    web.GitHubConfig
	OAuthGitHubSet bool
	OAuthOIDC      web.OIDCConfig
	OAuthOIDCSet   bool
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

	// Empty means the platform default, which is what every caller that has
	// not been through Prepare passes. Prepare resolves paths.stateDir.
	stateDir := opts.StateDir
	if stateDir == "" {
		stateDir = filepath.Dir(app.DefaultAuditPath())
	}
	// The audit log follows the state directory rather than staying at the
	// platform default: two daemons pointed at separate stateDirs must not
	// interleave their write records into one file.
	auditPath := filepath.Join(stateDir, "audit.jsonl")
	user, pass, generated, bootErr := credentials(opts, stateDir)
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
	if a, auditErr := app.NewAudit(auditPath); auditErr != nil {
		if allowWrites {
			return nil, auditErr
		}
		logger.Warn("could not open audit log; push subscriptions will not be recorded",
			"path", auditPath, "err", auditErr)
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
				"audit", auditPath,
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

	// Resolved by the caller (env > config.yml); empty still means autodetect.
	publicURL := opts.PublicURL
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
	// The caller resolves this gate so config.yml gets a say; the zero Gate
	// means nobody did, in which case the panel's side-file decides as before.
	passwordGate := opts.PasswordLogin
	if passwordGate.Source == "" {
		passwordGate = config.Gate{On: web.PasswordLoginEnabled(stateDir), Source: config.GateSettings}
	}
	provider.SetPasswordLogin(passwordGate.On)
	// Reported like the write and session-switch gates beside it. Without
	// this line the app's weakest door is the only one whose state a startup
	// log does not name, and an install that lost password sign-in to the
	// default change has nothing to read.
	logger.Info("password sign-in gate", "enabled", passwordGate.On,
		"source", passwordGate.Source, "locked", passwordGate.Locked)
	if ou, op, ok := web.LoadOptionalPassword(stateDir); ok {
		provider.SetOptionalPassword(ou, op)
		logger.Info("optional phone password enabled", "user", ou)
	}

	// OAuth rungs: the store is the live, editable source (env → oauth.json),
	// and drives both the Settings UI and the login page. Providers read it on
	// every request, so a Settings edit takes effect without a restart. The
	// redirect URLs derive from the public base, so an empty public URL simply
	// leaves every provider disabled (no valid redirect) rather than erroring.
	oauthStore := web.NewOAuthStore(stateDir, opts.PublicURL, web.OAuthConfigLayer{
		GitHub:    opts.OAuthGitHub,
		GitHubSet: opts.OAuthGitHubSet,
		OIDC:      opts.OAuthOIDC,
		OIDCSet:   opts.OAuthOIDCSet,
	})
	oauth := []web.OAuthProvider{
		&web.GitHubProvider{Config: oauthStore.GitHub, Log: logger},
		&web.OIDCProvider{Config: oauthStore.OIDC, Log: logger},
	}
	if st := oauthStore.Status(); st.GitHub.Configured || st.OIDC.Configured {
		logger.Info("OAuth login enabled",
			"github", st.GitHub.Configured, "oidc", st.OIDC.Configured)
	} else if opts.PublicURL == "" {
		logger.Debug("OAuth login off: no public URL configured (set publicUrl / MERINO_PUBLIC_URL)")
	}

	srv, err := web.New(src, web.Config{
		Addr:       addr,
		Provider:   provider,
		OAuth:      oauth,
		OAuthStore: oauthStore,
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
		// The explicit value only, never the LAN autodetect: an absent
		// public base means "there isn't one", which is not the same as
		// "here is a guess that works on this Wi-Fi".
		PublicBaseURL: opts.PublicURL,
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

// Credentials resolves the operator identity a running daemon accepts, for
// entry points that must authenticate TO it rather than be it — `merinod qr`
// signs in over loopback to ask the live process for a pairing ticket.
//
// Exported as a thin wrapper over the same resolver the daemon uses, rather
// than reimplemented in cmd/: two copies of an env > file > bootstrap
// precedence chain would drift the first time one gained a source, and the
// symptom would be a CLI that cannot log into its own daemon.
func Credentials(b *Boot) (user, pass string, err error) {
	u, p, _, err := credentials(b.Options, b.StateDir)
	return u, p, err
}

// credentials resolves the operator identity: env > auth.passwordFile >
// generated bootstrap file.
//
// env stays on top because MERINO_USER/MERINO_PASS are documented today.
// auth.passwordFile sits above the generated file because naming one is a
// deliberate act and the generated one is a fallback nobody chose.
//
// The password comes from a FILE, never a config key and never argv: a
// Kubernetes Secret mount is a file, argv is world-readable through /proc,
// and a literal in config.yml gets copied around with the deployment.
func credentials(opts Options, stateDir string) (user, pass string, generated bool, err error) {
	if u, p := app.Env("USER"), app.Env("PASS"); u != "" && p != "" {
		return u, p, false, nil
	}
	if opts.AuthPasswordFile == "" {
		return web.LoadOrCreateBootstrap(stateDir)
	}

	raw, readErr := os.ReadFile(opts.AuthPasswordFile)
	if readErr != nil {
		// Fatal rather than falling back to a generated password: an
		// operator who named a file and silently got a random credential
		// they have never seen cannot log in and has no idea why.
		return "", "", false, fmt.Errorf("auth.passwordFile: %w", readErr)
	}
	// Secret mounts and `echo > file` both leave a trailing newline, which
	// is not part of the password and would make it untypeable.
	pass = strings.TrimRight(string(raw), "\r\n")
	if pass == "" {
		return "", "", false, fmt.Errorf("auth.passwordFile %s is empty", opts.AuthPasswordFile)
	}
	user = opts.AuthUser
	if user == "" {
		user = "operator"
	}
	return user, pass, false, nil
}
