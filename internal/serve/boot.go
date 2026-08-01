package serve

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/LoneExile/merino/internal/app"
	"github.com/LoneExile/merino/internal/config"
	"github.com/LoneExile/merino/internal/herdr"
	"github.com/LoneExile/merino/internal/web"
)

// Kind is which entry point is booting. It exists because exactly one
// decision differs between them, and naming it here keeps that difference in
// one reviewable place instead of duplicated in two mains.
type Kind int

const (
	// Daemon is merinod. Gates default OFF: somebody wrote a unit file or a
	// manifest, so anything they did not ask for they do not get.
	Daemon Kind = iota

	// Menubar is the macOS app. When nothing anywhere asked for a specific
	// bind, writes and session switching default ON, because this is a
	// double-click and answering a blocked agent from a phone has to work
	// with no setup. A config.yml that sets `listen` is a deployment and
	// drops out of that default exactly like --listen does.
	Menubar
)

// Flags is what an entry point parsed from its own command line. Every field
// is the raw flag value; resolution against env, config.yml and the built-in
// defaults happens in Prepare, once, for both entry points.
type Flags struct {
	ConfigPath string
	Listen     string

	BehindProxy bool
	// BehindProxyGiven distinguishes "-behind-proxy=false" from "not
	// given". A bool flag cannot, and for this key the difference decides
	// Secure cookies and whether a client-IP header from the network is
	// believed — so an explicit false must beat a config.yml that says true.
	BehindProxyGiven bool

	// AllowWrites and AllowSessionSwitch are force-on only: false means
	// "not given". That matches the flags' documented behaviour today.
	AllowWrites        bool
	AllowSessionSwitch bool
}

// Gates are the three access decisions, kept with the rung that made each so
// a caller can log "why" and a UI can say "set by config.yml (read-only)".
type Gates struct {
	Writes        config.Gate
	SessionSwitch config.Gate
	PasswordLogin config.Gate
}

// Boot is everything resolved before the dashboard starts.
type Boot struct {
	Config   *config.File
	Logger   *slog.Logger
	Level    slog.Level
	Client   *herdr.Client
	StateDir string
	Gates    Gates
	Options  Options
}

// Prepare turns parsed flags into everything an entry point needs, applying
// flag > env > config.yml > built-in default throughout.
//
// This is the single producer of serve.Options. Two mains that each built
// their own would drift the first time somebody added a flag to one of them,
// and the drift would be silent — a daemon quietly missing a gate the menubar
// has. Entry points parse; this resolves; serve.Start runs.
func Prepare(kind Kind, f Flags) (*Boot, error) {
	cfg, err := config.Load(f.ConfigPath)
	if err != nil {
		return nil, err
	}

	// Resolved once: the menubar's tray-frame counter consults it on a hot
	// path, and re-deriving it per frame would re-read the environment.
	level := LogLevel(cfg.Log.Level)
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}))

	if cfg.Path != "" {
		// Writability is reported, not merely used: it decides whether the
		// access keys are defaults or pins, and a writable-looking path can
		// still be ephemeral (a hostPath, an emptyDir seeded by an init
		// container). Printing it makes that inspectable, not inferred.
		logger.Info("config loaded",
			"path", cfg.Path, "writable", cfg.Writable, "gatesPinned", cfg.Locked())
	}
	for _, key := range cfg.Unhonoured {
		logger.Warn("config key is not honoured by this build yet", "key", key)
	}

	// herdr.socket: env stays above the file because HERDR_SOCK is
	// documented today and `just web` / `just tunnel` set it.
	client := herdr.New(config.String("", os.Getenv("HERDR_SOCK"), cfg.Herdr.Socket, ""))

	// Autodetection probes THIS machine's login shell for the agent
	// binaries, which is the wrong machine whenever herdr lives elsewhere —
	// under an ssh-forwarded socket that is always.
	if dropped := app.PinAgentKinds(cfg.Herdr.Agents); len(dropped) > 0 {
		logger.Warn("config named agent kinds herdr does not support; ignoring them",
			"kinds", dropped)
	}

	addr := config.String(f.Listen, "", cfg.Listen, "")
	if addr == "" {
		// LAN bind so a phone on the same Wi-Fi can open the QR URL. Pairing
		// tokens are one-shot with a short TTL; device grants are revocable.
		addr = "0.0.0.0:8730"
	}
	// True only when nobody asked for a bind at all — see Menubar.
	unconfigured := kind == Menubar && f.Listen == "" && cfg.Listen == ""

	// paths.stateDir relocates credentials, paired devices, VAPID keys, push
	// subscriptions, the three gate files and the audit log. Empty keeps
	// today's path — moving it on an existing install orphans every paired
	// device and every push subscription SILENTLY, so it is opt-in and the
	// default never changes.
	//
	// Two merinod on one host need separate values here, or they fight over
	// all of the above.
	stateDir := cfg.Paths.StateDir
	if stateDir == "" {
		stateDir = filepath.Dir(app.DefaultAuditPath())
	}
	gates := Gates{
		SessionSwitch: cfg.ResolveGate(config.GateInputs{
			FlagOn:           f.AllowSessionSwitch,
			Config:           cfg.Access.AllowSessionSwitch,
			SettingsExplicit: web.SessionSwitchExplicit(stateDir),
			SettingsOn:       web.SessionSwitchEnabled(stateDir),
			Default:          unconfigured,
		}),
		Writes: cfg.ResolveGate(config.GateInputs{
			FlagOn:           f.AllowWrites,
			Config:           cfg.Access.AllowWrites,
			SettingsExplicit: web.AllowWritesExplicit(stateDir),
			SettingsOn:       web.AllowWritesEnabled(stateDir),
			Default:          unconfigured,
		}),
		// No flag and no GUI default: password sign-in is the weakest door
		// this app has and stays shut until somebody opens it deliberately.
		PasswordLogin: cfg.ResolveGate(config.GateInputs{
			Config:           cfg.Access.PasswordLogin,
			SettingsExplicit: web.PasswordLoginExplicit(stateDir),
			SettingsOn:       web.PasswordLoginEnabled(stateDir),
			Default:          false,
		}),
	}

	// behind-proxy is a plain bool, not a gate: no panel toggle exists for
	// it, so the ladder is just flag > config.yml > false.
	behindProxy := cfg.BehindProxy != nil && *cfg.BehindProxy
	if f.BehindProxyGiven {
		behindProxy = f.BehindProxy
	}

	return &Boot{
		Config:   cfg,
		Logger:   logger,
		Level:    level,
		Client:   client,
		StateDir: stateDir,
		Gates:    gates,
		Options: Options{
			Addr:               addr,
			PublicURL:          config.String("", app.Env("PUBLIC_URL"), cfg.PublicURL, ""),
			BehindProxy:        behindProxy,
			AllowWrites:        gates.Writes.On,
			AllowSessionSwitch: gates.SessionSwitch.On,
			PasswordLogin:      gates.PasswordLogin,
			AuthUser:           cfg.Auth.User,
			AuthPasswordFile:   cfg.Auth.PasswordFile,
			StateDir:           stateDir,
			Logger:             logger,
		},
	}, nil
}

// LogGates reports how each access gate was decided. Which rung won is worth
// a line: "the toggle will not move" and "the toggle is off" look identical
// from the panel, and only one of them is a config file doing its job.
func (b *Boot) LogGates() {
	for _, g := range []struct {
		name string
		gate config.Gate
		key  *bool
	}{
		{"session switch", b.Gates.SessionSwitch, b.Config.Access.AllowSessionSwitch},
		{"write", b.Gates.Writes, b.Config.Access.AllowWrites},
	} {
		b.Logger.Info(g.name+" gate", "enabled", g.gate.On, "source", g.gate.Source, "locked", g.gate.Locked)
		if g.gate.Source == config.GateFlag && g.key != nil && b.Config.Locked() && *g.key != g.gate.On {
			b.Logger.Warn("a CLI flag overrode a read-only config.yml",
				"gate", g.name, "config", *g.key, "effective", g.gate.On)
		}
	}
}

// LogLevel resolves the handler level: env > config.yml > info.
//
// MERINO_DEBUG stays on top because it is documented today and `just web` /
// `just tunnel` set it — demoting it below a file would silently break the
// dev loop. An unrecognised level falls back to info rather than refusing to
// boot: the file has already been accepted by then, and a typo here should
// not cost the operator the process.
func LogLevel(fileLevel string) slog.Level {
	if app.Env("DEBUG") != "" {
		return slog.LevelDebug
	}
	switch strings.ToLower(strings.TrimSpace(fileLevel)) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// Fatal reports a boot failure before a logger exists and exits.
//
// config.Load runs before the handler is built, because log.level is one of
// its keys, so its failure is the one message that must survive whatever
// level the unreadable file was trying to set.
func Fatal(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
