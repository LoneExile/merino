// Command merino is a menubar dashboard for herdr coding agents.
//
// It holds a persistent connection to the herdr socket API and projects live
// agent state into a tray label and an attached panel window. State is driven
// entirely by the server's push event stream — the herdr CLI is never invoked.
package main

import (
	"embed"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"sync/atomic"
	"time"

	"github.com/LoneExile/merino/internal/app"
	"github.com/LoneExile/merino/internal/desktop"
	"github.com/LoneExile/merino/internal/herdr"
	"github.com/LoneExile/merino/internal/trayicon"
	"github.com/LoneExile/merino/internal/web"
	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
)

//go:embed all:frontend/dist
var assets embed.FS

func init() {
	// Registering events gives the frontend strongly typed listeners for them.
	application.RegisterEvent[[]app.Agent](app.EventAgentsChanged)
	application.RegisterEvent[app.Conn](app.EventConnChanged)
	// ui:open is emitted from the tray context menu so the panel can open
	// Settings / Pair phone without a full reload.
	application.RegisterEvent[string]("ui:open")
}

// version is injected at link time: -ldflags "-X main.version=v0.2.0".
// Falls back for local `go build` / just app so CheckUpdate stays honest.
var version = "0.1.0-dev"

func main() {
	behindProxy := flag.Bool("behind-proxy", false,
		"the server sits behind a trusted TLS-terminating proxy such as a Cloudflare tunnel; "+
			"marks cookies Secure and trusts CF-Connecting-IP for login throttling. "+
			"Never enable this while the port is also reachable directly")
	allowWrites := flag.Bool("allow-writes", false,
		"let the web dashboard approve prompts, send keys and interrupt agents. "+
			"Off by default: this is arbitrary input into live terminals, and every "+
			"action is written to the audit log")
	allowSessionSwitch := flag.Bool("allow-session-switch", false,
		"let the web dashboard repoint this process at a different herdr session's "+
			"socket. Off by default: switching changes which agents every signed-in "+
			"browser sees")
	listen := flag.String("listen", "",
		"serve the read-only web dashboard on this address, e.g. 127.0.0.1:8730 "+
			"or 0.0.0.0:8730 for other devices on your network (disabled when empty)")
	flag.Parse()

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: logLevel(),
	}))

	client := herdr.New(os.Getenv("HERDR_SOCK"))

	// The service is constructed before the application so it can be bound at
	// creation time; emit and tray callbacks are late-bound via closures over
	// variables filled in below.
	var (
		wailsApp     *application.App
		tray         *application.SystemTray
		webSrv       *web.Server
		animator     *trayicon.Animator
		pairing      *web.Pairing
		passProvider *web.PasswordProvider
		devices      *web.DeviceStore
		desk         *desktop.Settings
	)

	emit := func(name string, data ...any) {
		if wailsApp != nil {
			wailsApp.Event.Emit(name, data...)
		}
	}
	// Called after every publish, which is exactly when connected browsers
	// need waking too.
	onCounts := func(c app.Counts) {
		if tray != nil {
			tray.SetLabel(trayLabel(c))
		}
		if animator != nil {
			animator.Update(c)
		}
		if webSrv != nil {
			webSrv.Notify()
		}
	}

	agents := app.NewAgentsService(client, logger, emit, onCounts)

	// Public release: the menubar .app always starts a local dashboard so
	// phone QR pairing works with zero flags. CLI --listen still overrides
	// the bind address (and is required for headless/tunnel-only runs).
	webAddr := *listen
	if webAddr == "" {
		// LAN bind so a phone on the same Wi‑Fi can open the QR URL. Pairing
		// tokens are one-shot + short TTL; device grants are revocable.
		webAddr = "0.0.0.0:8730"
	}
	// Session-switch gate:
	//   CLI --allow-session-switch → on
	//   else session-switch.json from Mac Settings (default off when missing)
	//   menubar GUI also treats missing file as on so phone switch works
	//   zero-config (matches pre-toggle behaviour); toggle still forces off.
	stateDirForSwitch := filepath.Dir(app.DefaultAuditPath())
	switchOK := *allowSessionSwitch
	if !*allowSessionSwitch {
		if web.SessionSwitchExplicit(stateDirForSwitch) {
			switchOK = web.SessionSwitchEnabled(stateDirForSwitch)
		} else if *listen == "" {
			// Menubar double-click path, no explicit preference yet.
			switchOK = true
		}
	}
	var startErr error
	srv, pair, passProv, devStore, startErr := startWeb(agents, webAddr, *behindProxy, *allowWrites, switchOK, assets, logger)
	if startErr != nil {
		logger.Error("web dashboard failed to start", "err", startErr)
		os.Exit(1)
	}
	webSrv = srv
	pairing = pair
	passProvider = passProv
	devices = devStore

	desk = desktop.NewSettings(nil, "dev.apinant.merino", version, "LoneExile/merino", pairing, devices, filepath.Dir(app.DefaultAuditPath()), webAddr, passProvider)
	desk.SetWebServer(srv)
	// Re-apply gate after the server exists so disk toggle and CLI flag cannot
	// drift from the live switchOn bit (phone canSwitch reads this).
	if err := srv.SetSessionSwitch(switchOK); err != nil {
		logger.Debug("session switch gate not applied", "err", err)
	} else {
		logger.Info("session switch gate", "enabled", switchOK)
	}

	wailsApp = application.New(application.Options{
		Name:        "Merino",
		Description: "Merino — herdr from the menu bar",
		Services: []application.Service{
			application.NewService(agents),
			application.NewService(desk),
		},
		Assets: application.AssetOptions{
			Handler: application.AssetFileServerFS(assets),
		},
		Mac: application.MacOptions{
			// A menubar app owns no dock icon and must outlive its panel.
			ActivationPolicy: application.ActivationPolicyAccessory,
			ApplicationShouldTerminateAfterLastWindowClosed: false,
		},
		Windows: application.WindowsOptions{
			DisableQuitOnLastWindowClosed: true,
		},
	})

	// Autostart needs the live App pointer (SMAppService / LaunchAgent).
	desk.Auto = desktop.NewAutostart(wailsApp, "dev.apinant.merino")

	// A menubar panel, not an app window: frameless (no traffic lights),
	// fixed size, floating above normal windows and visible on whichever
	// Space is active.
	//
	// Min/Max size are deliberately NOT clamped. DisableResize already stops
	// the user resizing, and hard clamps would also block the programmatic
	// SetSize that showPanel uses to re-assert the size and force a repaint.
	//
	// BackgroundColour is fully transparent so CSS border-radius on #root can
	// show the desktop through the corners (otherwise the opaque NSWindow
	// fill paints a square halo behind the rounded webview).
	firstRun := desk != nil && desk.FirstRunPending()
	panelURL := "/"
	if firstRun {
		panelURL = "/?pair=1"
	}
	panel := wailsApp.Window.NewWithOptions(application.WebviewWindowOptions{
		Name:   "panel",
		Title:  "Merino",
		Width:  panelW,
		Height: panelH,
		// First run: show immediately so the QR is the first paint after install.
		Hidden:           !firstRun,
		AlwaysOnTop:      true,
		Frameless:        true,
		DisableResize:    true,
		BackgroundColour: application.NewRGBA(0, 0, 0, 0),
		URL:              panelURL,
		Mac: application.MacWindow{
			// Transparent backdrop so the CSS-rounded panel corners are not
			// painted over by an opaque window fill. The stylesheet supplies
			// the paper colour inside the rounded clip.
			Backdrop:           application.MacBackdropTransparent,
			TitleBar:           application.MacTitleBarHidden,
			WindowLevel:        application.MacWindowLevelFloating,
			CollectionBehavior: application.MacWindowCollectionBehaviorCanJoinAllSpaces | application.MacWindowCollectionBehaviorTransient,
		},
		Windows: application.WindowsWindow{
			HiddenOnTaskbar: true,
		},
	})

	// Popover behaviour: clicking away dismisses the panel. Wails does not do
	// this for an attached window, so wire it explicitly.
	//
	// blurHiddenAt records when a focus loss hid the panel. Clicking the tray
	// icon while the panel is open moves focus to the menubar, which fires
	// WindowLostFocus BEFORE the tray click handler runs. Without this guard
	// the click sees an already-hidden window and immediately reopens it, so
	// the icon could never dismiss the panel.
	var blurHiddenAt atomic.Int64
	panel.OnWindowEvent(events.Common.WindowLostFocus, func(*application.WindowEvent) {
		blurHiddenAt.Store(time.Now().UnixNano())
		panel.Hide()
	})

	tray = wailsApp.SystemTray.New()
	tray.SetTooltip("Merino")
	tray.SetLabel(trayLabel(app.Counts{}))

	// setTrayIcon dispatches an animation frame to the platform-appropriate
	// tray API: a template icon on darwin, so the sheep silhouette adopts
	// the menubar's light/dark appearance, and a plain icon everywhere
	// else, mirroring how the previous static icon was set.
	// frameCount lets a live run prove the animation is actually ticking.
	// The tray is the one surface no test and no screenshot can reach from
	// this environment, so without a counter "is it animating?" is
	// unanswerable except by staring at the menu bar.
	var frameCount atomic.Int64
	setTrayIcon := func(icon []byte) {
		if n := frameCount.Add(1); logLevel() <= slog.LevelDebug {
			logger.Debug("tray frame", "n", n, "bytes", len(icon))
		}
		if runtime.GOOS == "darwin" {
			tray.SetTemplateIcon(icon)
		} else {
			tray.SetIcon(icon)
		}
	}
	animator = trayicon.New(setTrayIcon)

	menu := wailsApp.NewMenu()
	menu.Add("Show agents").OnClick(func(*application.Context) {
		showPanel(tray, panel)
		if wailsApp != nil {
			wailsApp.Event.Emit("ui:open", "agents")
		}
	})
	menu.Add("Settings").OnClick(func(*application.Context) {
		showPanel(tray, panel)
		if wailsApp != nil {
			wailsApp.Event.Emit("ui:open", "settings")
		}
	})
	menu.Add("Pair phone…").OnClick(func(*application.Context) {
		showPanel(tray, panel)
		if wailsApp != nil {
			wailsApp.Event.Emit("ui:open", "pair")
		}
	})
	menu.AddSeparator()
	menu.Add("Quit").OnClick(func(*application.Context) { wailsApp.Quit() })
	tray.SetMenu(menu)

	// AttachWindow gives us positioning under the icon. Its automatic
	// click-toggle is deliberately replaced below: setting OnClick suppresses
	// the smart default, which cannot know about the blur race above.
	tray.AttachWindow(panel).WindowOffset(6)
	tray.OnClick(func() {
		if since := time.Since(time.Unix(0, blurHiddenAt.Load())); since < blurDismissWindow {
			return // this click is what caused the blur; treat it as dismissal
		}
		if panel.IsVisible() {
			panel.Hide()
			return
		}
		showPanel(tray, panel)
	})

	// Wails runs configured trays itself once the app is running, so no
	// explicit tray.Run() is needed here.
	runErr := wailsApp.Run()
	// Stop before any exit path: it halts the animation ticker, which
	// matters even though the process is about to end, because os.Exit
	// below would otherwise skip a deferred Stop entirely.
	animator.Stop()
	if runErr != nil {
		logger.Error("application exited", "err", runErr)
		os.Exit(1)
	}
}

// trayLabel renders the herd summary shown next to the menubar icon.
func trayLabel(c app.Counts) string {
	switch {
	case c.Blocked > 0:
		return fmt.Sprintf("%d!", c.Blocked)
	case c.Working > 0:
		return fmt.Sprintf("%d", c.Working)
	case c.Total > 0:
		return ""
	default:
		return ""
	}
}

func logLevel() slog.Level {
	if app.Env("DEBUG") != "" {
		return slog.LevelDebug
	}
	return slog.LevelInfo
}

// panelW and panelH are the fixed panel dimensions, re-asserted on every show.
const panelW, panelH = 420, 560

// blurDismissWindow is how long after a focus-loss hide a tray click is
// treated as "dismiss" rather than "open". Long enough to cover the gap
// between the blur event and the click handler, short enough that a genuine
// second click still reopens the panel.
const blurDismissWindow = 300 * time.Millisecond

// showPanel positions the panel under the tray icon and reveals it.
func showPanel(tray *application.SystemTray, panel *application.WebviewWindow) {
	// Re-assert the size on every show. macOS restores a window's previous
	// frame, so a panel that ever ended up resized or zoomed would otherwise
	// keep coming back at the wrong size.
	panel.SetSize(panelW, panelH)

	if err := tray.PositionWindow(panel, 6); err != nil {
		// Positioning fails only before the tray is running; showing the panel
		// at its default location still beats doing nothing.
		slog.Debug("position panel", "err", err)
	}
	panel.Show().Focus()

	// WKWebView suspends compositing for an occluded window and does not
	// always repaint on the next show, which surfaces as a panel painted only
	// in the window background colour. A one-pixel NATIVE resize forces the
	// window server to recomposite; a JS resize event would not, since it only
	// runs JS listeners and never reaches the compositor.
	//
	// This is a repaint fix, not a load fix: the webview is confirmed to fetch
	// index.html, the JS and the CSS at startup while still hidden, so the
	// content is present and merely unpainted. If a blank panel ever appears
	// WITH the "Loading…" boot text visible, that is a different failure — the
	// bundle did not execute — and index.html reports it.
	panel.SetSize(panelW, panelH+1)
	panel.SetSize(panelW, panelH)
}

// startWeb boots the browser dashboard.
//
// Public-release GUI launches always call this (default bind 0.0.0.0:8730) so
// QR pairing works after drag-to-Applications with zero flags. Safety is the
// login wall + one-shot pairing tokens + revocable device grants — not "did
// the operator remember --listen". CLI users can still pass an explicit
// --listen address (including 127.0.0.1) to narrow the bind.
func startWeb(src web.Source, addr string, behindProxy, allowWrites, allowSessionSwitch bool, assets embed.FS, logger *slog.Logger) (*web.Server, *web.Pairing, *web.PasswordProvider, *web.DeviceStore, error) {
	stateDir := filepath.Dir(app.DefaultAuditPath())
	user, pass, generated, bootErr := web.LoadOrCreateBootstrap(stateDir)
	if bootErr != nil {
		return nil, nil, nil, nil, bootErr
	}
	if generated {
		logger.Info("generated local operator credentials for zero-config start",
			"user", user, "path", filepath.Join(stateDir, "bootstrap-creds.json"))
	}

	dist, err := fs.Sub(assets, "frontend/dist")
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("locate frontend assets: %w", err)
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
			return nil, nil, nil, nil, auditErr
		}
		logger.Warn("could not open audit log; push subscriptions will not be recorded",
			"path", app.DefaultAuditPath(), "err", auditErr)
	} else {
		audit = a
	}
	if allowWrites {
		w, castOK := src.(web.Writer)
		if !castOK {
			return nil, nil, nil, nil, errors.New("source does not support writes")
		}
		writer = w
		logger.Warn("web dashboard can write to your agents",
			"audit", app.DefaultAuditPath(),
			"note", "approvals, keys and interrupts are accepted from any signed-in browser")
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
		return nil, nil, nil, nil, errors.New("source does not support session switching")
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
		return nil, nil, nil, nil, devErr
	}
	provider.SetDevices(devices)
	provider.SetPasswordLogin(web.PasswordLoginEnabled(stateDir))
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
		Pairing:       pairing,
		PublicBaseURL: app.Env("PUBLIC_URL"),
		// Same directory the audit log above resolves to, so an operator who
		// already knows where one lives knows where to find the other.
		PushDir:  stateDir,
		Devices:  devices,
		StateDir: stateDir,
	})
	if err != nil {
		return nil, nil, nil, nil, err
	}

	// Wire the edge-triggered blocked-transition hook straight into push.
	// AttachBlockedNotifier is a package function (not a Wails-bound method).
	// NotifyBlocked itself is a no-op whenever push failed to initialise or
	// was never configured, so wiring it unconditionally is safe.
	if agents, ok := src.(*app.AgentsService); ok {
		app.AttachBlockedNotifier(agents, srv.NotifyBlocked)
	}

	if err := srv.Start(); err != nil {
		return nil, nil, nil, nil, err
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
	return srv, pairing, provider, devices, nil
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
