// Command merino is a menubar dashboard for herdr coding agents.
//
// It holds a persistent connection to the herdr socket API and projects live
// agent state into a tray label and an attached panel window. State is driven
// entirely by the server's push event stream — the herdr CLI is never invoked.
package main

import (
	"flag"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"time"

	"github.com/LoneExile/merino/internal/app"
	"github.com/LoneExile/merino/internal/assets"
	"github.com/LoneExile/merino/internal/config"
	"github.com/LoneExile/merino/internal/desktop"
	"github.com/LoneExile/merino/internal/herdr"
	"github.com/LoneExile/merino/internal/serve"
	"github.com/LoneExile/merino/internal/trayicon"
	"github.com/LoneExile/merino/internal/web"
	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
	"github.com/wailsapp/wails/v3/pkg/updater"
	"github.com/wailsapp/wails/v3/pkg/updater/providers/github"
)

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
var version = "0.1.10-dev"

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
	configPath := flag.String("config", "",
		"path to config.yml. Naming a file that does not exist is an error, not a "+
			"fallback. When empty, $MERINO_CONFIG then ~/.config/merino/config.yml "+
			"then /etc/merino/config.yml are tried, and finding none is normal")
	flag.Parse()

	// Which flags were actually typed. flag.Bool cannot distinguish "not
	// given" from "-flag=false", and for behind-proxy that difference is
	// security-relevant: it decides Secure cookies and whether a
	// client-IP header from the network is believed. Without this,
	// `-behind-proxy=false` against a config.yml that says true would be
	// silently ignored.
	given := map[string]bool{}
	flag.Visit(func(f *flag.Flag) { given[f.Name] = true })

	// Loaded before the logger because log.level is one of its keys. Failing
	// here is fatal on purpose: a config that cannot be parsed means the
	// operator's intent is unknown, and guessing it is worse than stopping.
	cfg, cfgErr := config.Load(*configPath)
	if cfgErr != nil {
		// No logger yet, and this is the one message that must survive
		// whatever level the file was trying to set.
		fmt.Fprintln(os.Stderr, cfgErr)
		os.Exit(1)
	}

	// Resolved once and reused: the tray-frame counter below consults it on
	// a hot path, and re-deriving it per frame would re-read the env.
	level := logLevel(cfg.Log.Level)
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: level,
	}))
	// Helpers outside main (showPanel, showPanelWhenReady) log through the
	// package-level slog functions; without this they would write to Go's
	// default INFO handler and their debug lines would vanish.
	slog.SetDefault(logger)

	if cfg.Path != "" {
		// Writability is reported, not just used: it is what decides whether
		// the access keys are defaults or pins, and §6's honest hole is that
		// a writable-looking path can still be ephemeral. Printing it makes
		// that inspectable instead of inferred.
		logger.Info("config loaded", "path", cfg.Path, "writable", cfg.Writable, "gatesPinned", cfg.Locked())
	}
	for _, key := range cfg.Unhonoured {
		logger.Warn("config key is not honoured by this build yet", "key", key)
	}

	// herdr.socket: env stays above the file, because HERDR_SOCK is
	// documented today and `just web` / `just tunnel` set it.
	client := herdr.New(config.String("", os.Getenv("HERDR_SOCK"), cfg.Herdr.Socket, ""))

	// Spawn-sheet agents. Autodetection probes this machine's login shell,
	// which is the wrong machine whenever herdr lives elsewhere.
	if dropped := app.PinAgentKinds(cfg.Herdr.Agents); len(dropped) > 0 {
		logger.Warn("config named agent kinds herdr does not support; ignoring them",
			"kinds", dropped)
	}

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

	// herdReachable mirrors the latest Conn so the tray label can distinguish
	// "no agents running" from "cannot reach herdr at all". Both render as an
	// empty herd otherwise, and only one of them is the operator's problem.
	var herdReachable atomic.Bool
	herdReachable.Store(true)
	var lastCounts atomic.Pointer[app.Counts]

	refreshTray := func() {
		if tray == nil {
			return
		}
		c := app.Counts{}
		if p := lastCounts.Load(); p != nil {
			c = *p
		}
		up := herdReachable.Load()
		tray.SetLabel(trayLabel(c, up))
		tray.SetTooltip(trayTooltip(c, up, client.Socket()))
	}

	emit := func(name string, data ...any) {
		if name == app.EventConnChanged && len(data) > 0 {
			if c, ok := data[0].(app.Conn); ok {
				herdReachable.Store(c.Connected)
				refreshTray()
				// Wake browsers too: setConn does not run through publish, so
				// without this a phone keeps showing the last good herd.
				if webSrv != nil {
					webSrv.Notify()
				}
			}
		}
		if wailsApp != nil {
			wailsApp.Event.Emit(name, data...)
		}
	}
	// Called after every publish, which is exactly when connected browsers
	// need waking too.
	onCounts := func(c app.Counts) {
		lastCounts.Store(&c)
		refreshTray()
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
	// Bind address: flag > config.yml > LAN default. No env rung for this key.
	webAddr := config.String(*listen, "", cfg.Listen, "")
	if webAddr == "" {
		// LAN bind so a phone on the same Wi‑Fi can open the QR URL. Pairing
		// tokens are one-shot + short TTL; device grants are revocable.
		webAddr = "0.0.0.0:8730"
	}
	// zeroConfigGUI is the menubar double-click path: nobody asked for a
	// specific bind, so this is someone opening an app rather than an
	// operator scripting a daemon. It is what makes writes and session
	// switching default ON, so answering a blocked agent from a phone works
	// with no setup. A config.yml that sets `listen` is a deployment, so it
	// drops out of that default exactly like --listen does.
	zeroConfigGUI := *listen == "" && cfg.Listen == ""
	stateDir := filepath.Dir(app.DefaultAuditPath())

	// D1 lives in config.ResolveGate: a writable config.yml is a default the
	// panel outranks, a read-only one pins. The three gates differ only in
	// which flag and which side-file they read.
	switchGate := cfg.ResolveGate(config.GateInputs{
		FlagOn:           *allowSessionSwitch,
		Config:           cfg.Access.AllowSessionSwitch,
		SettingsExplicit: web.SessionSwitchExplicit(stateDir),
		SettingsOn:       web.SessionSwitchEnabled(stateDir),
		Default:          zeroConfigGUI,
	})
	writesGate := cfg.ResolveGate(config.GateInputs{
		FlagOn:           *allowWrites,
		Config:           cfg.Access.AllowWrites,
		SettingsExplicit: web.AllowWritesExplicit(stateDir),
		SettingsOn:       web.AllowWritesEnabled(stateDir),
		Default:          zeroConfigGUI,
	})
	// No flag and no GUI default: password sign-in is the weakest door this
	// app has and stays off until somebody opens it deliberately.
	passwordGate := cfg.ResolveGate(config.GateInputs{
		Config:           cfg.Access.PasswordLogin,
		SettingsExplicit: web.PasswordLoginExplicit(stateDir),
		SettingsOn:       web.PasswordLoginEnabled(stateDir),
		Default:          false,
	})
	logGate(logger, cfg, "session switch", switchGate, cfg.Access.AllowSessionSwitch)
	logGate(logger, cfg, "write", writesGate, cfg.Access.AllowWrites)

	switchOK := switchGate.On
	writesOK := writesGate.On

	// behind-proxy is a plain bool, not a gate: there is no panel toggle for
	// it, so the ladder is just flag > config.yml > false. An explicitly
	// typed flag wins in BOTH directions — -behind-proxy=false against a
	// config that says true must actually turn it off, because the operator
	// who typed it is standing in front of the deployment.
	behindProxyOn := cfg.BehindProxy != nil && *cfg.BehindProxy
	if given["behind-proxy"] {
		behindProxyOn = *behindProxy
	}

	dash, startErr := serve.Start(serve.Options{
		Source:             agents,
		Addr:               webAddr,
		PublicURL:          config.String("", app.Env("PUBLIC_URL"), cfg.PublicURL, ""),
		BehindProxy:        behindProxyOn,
		AllowWrites:        writesOK,
		AllowSessionSwitch: switchOK,
		PasswordLogin:      passwordGate,
		Logger:             logger,
	})
	if startErr != nil {
		logger.Error("web dashboard failed to start", "err", startErr)
		os.Exit(1)
	}
	srv := dash.Server
	webSrv = srv
	pairing = dash.Pairing
	passProvider = dash.Password
	devices = dash.Devices

	desk = desktop.NewSettings(nil, "dev.apinant.merino", version, "LoneExile/merino", pairing, devices, filepath.Dir(app.DefaultAuditPath()), webAddr, passProvider)
	desk.SetWebServer(srv)
	// Re-apply the gates after the server exists so a disk toggle and a CLI
	// flag cannot drift from the live switchOn bit (phone canSwitch reads
	// this). Neither call logs its success here: logGate above already
	// reported the decision with the rung that made it, and web.Server logs
	// the application itself. Only the failure is worth a line from here.
	if err := srv.SetSessionSwitch(switchOK); err != nil {
		logger.Debug("session switch gate not applied", "err", err)
	}
	if err := srv.SetAllowWrites(writesOK); err != nil {
		logger.Debug("write gate not applied", "err", err)
	}

	wailsApp = application.New(application.Options{
		Name:        "Merino",
		Description: "Merino — herdr from the menu bar",
		Services: []application.Service{
			application.NewService(agents),
			application.NewService(desk),
		},
		Assets: application.AssetOptions{
			Handler: application.AssetFileServerFS(assets.FS),
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

	// In-app updates from GitHub Releases (macOS .app zip + SHA256SUMS).
	// WindowNone: Settings sheet owns the UX; Check vs Install are separate.
	if err := initAppUpdater(wailsApp, desk, logger); err != nil {
		logger.Warn("updater init failed", "err", err)
	}

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
	logger.Debug("first run check", "pending", firstRun, "stateDir", desk.StateDir)
	panelURL := "/"
	if firstRun {
		panelURL = "/?pair=1"
	}
	panel := wailsApp.Window.NewWithOptions(application.WebviewWindowOptions{
		Name:   "panel",
		Title:  "Merino",
		Width:  panelW,
		Height: panelH,
		// Always born hidden. A non-Hidden window is placed by macOS before the
		// tray exists to position it, which is what put the first-run panel in
		// the middle of the screen; showPanelWhenReady reveals it instead.
		Hidden:           true,
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
	// One path sets label and tooltip, so the two can never disagree.
	refreshTray()

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
		if n := frameCount.Add(1); level <= slog.LevelDebug {
			logger.Debug("tray frame", "n", n, "bytes", len(icon))
		}
		if runtime.GOOS == "darwin" {
			tray.SetTemplateIcon(icon)
		} else {
			tray.SetIcon(icon)
		}
	}
	animator = trayicon.New(setTrayIcon)

	// Tray menu, opened by RIGHT click (left click toggles the panel — see the
	// OnClick below). Every item names its exact destination: Settings has
	// tabs now, so "Check for Updates…" must land on the tab that carries the
	// update button rather than on whichever tab happened to be open last.
	openUI := func(which string) {
		showPanel(tray, panel)
		if wailsApp != nil {
			wailsApp.Event.Emit("ui:open", which)
		}
	}

	menu := wailsApp.NewMenu()
	menu.Add("Show agents").OnClick(func(*application.Context) { openUI("agents") })
	menu.Add("New agent…").OnClick(func(*application.Context) { openUI("spawn") })
	menu.Add("Settings").OnClick(func(*application.Context) { openUI("settings") })
	menu.Add("Pair phone…").OnClick(func(*application.Context) { openUI("pair") })
	menu.Add("Check for Updates…").OnClick(func(*application.Context) {
		openUI("settings:system")
	})
	menu.AddSeparator()
	menu.Add("Quit").OnClick(func(*application.Context) { wailsApp.Quit() })
	tray.SetMenu(menu)

	// AttachWindow gives us positioning under the icon. Its automatic
	// click-toggle is deliberately replaced below: setting OnClick suppresses
	// the smart default, which cannot know about the blur race above.
	tray.AttachWindow(panel).WindowOffset(trayWindowOffset)
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

	// First run: greet with the QR pairing panel, but only once the tray can
	// place it under its own icon. Run() below blocks, so this must be a
	// goroutine started before it.
	if firstRun {
		go showPanelWhenReady(tray, panel)
	}

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
//
// The unreachable case is spelled out rather than left blank. An empty label
// already means "an idle herd, nothing wants you", so reusing it for "herdr
// is not running" would hide the one state where the whole app is useless
// behind the one state where everything is fine.
func trayLabel(c app.Counts, herdReachable bool) string {
	if !herdReachable {
		return "no herd"
	}
	switch {
	case c.Blocked > 0:
		return fmt.Sprintf("%d!", c.Blocked)
	case c.Working > 0:
		return fmt.Sprintf("%d", c.Working)
	default:
		return ""
	}
}

// trayTooltip carries what the label has no room for. The socket path is the
// first thing worth knowing when herdr is unreachable: it is usually a herd
// running under a different HERDR_SOCK, not a herd that is down.
func trayTooltip(c app.Counts, herdReachable bool, socket string) string {
	if !herdReachable {
		return fmt.Sprintf("Merino — herdr not reachable at %s", socket)
	}
	switch {
	case c.Blocked > 0:
		return fmt.Sprintf("Merino — %d of %d agents need you", c.Blocked, c.Total)
	case c.Working > 0:
		return fmt.Sprintf("Merino — %d of %d agents working", c.Working, c.Total)
	case c.Total > 0:
		return fmt.Sprintf("Merino — %d agents idle", c.Total)
	default:
		return "Merino — no agents running"
	}
}

// logLevel resolves the handler level: env > config.yml > info.
//
// MERINO_DEBUG stays on top because it is documented today and `just web` /
// `just tunnel` set it — demoting it below a file would silently break the
// dev loop. An unrecognised level in the file is reported by falling back to
// info rather than refusing to boot; the file has already been accepted by
// then, and a typo here should not cost you the process.
func logLevel(fileLevel string) slog.Level {
	if app.Env("DEBUG") != "" {
		return slog.LevelDebug
	}
	switch strings.ToLower(strings.TrimSpace(fileLevel)) {
	case "debug":
		return slog.LevelDebug
	case "info", "":
		return slog.LevelInfo
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// logGate reports how one access gate was decided. Which rung won is worth a
// line: "the toggle will not move" and "the toggle is off" look identical
// from the panel, and only one of them is a config file doing its job.
func logGate(logger *slog.Logger, cfg *config.File, name string, g config.Gate, key *bool) {
	logger.Info(name+" gate", "enabled", g.On, "source", g.Source, "locked", g.Locked)
	if g.Source == config.GateFlag && key != nil && cfg.Locked() && *key != g.On {
		logger.Warn("a CLI flag overrode a read-only config.yml",
			"gate", name, "config", *key, "effective", g.On)
	}
}

// panelW and panelH are the fixed panel dimensions, re-asserted on every show.
const panelW, panelH = 420, 560

// blurDismissWindow is how long after a focus-loss hide a tray click is
// treated as "dismiss" rather than "open". Long enough to cover the gap
// between the blur event and the click handler, short enough that a genuine
// second click still reopens the panel.
const blurDismissWindow = 300 * time.Millisecond

// trayWindowOffset is the gap in points between the menubar and the panel's
// top edge. Used for both the attached-window offset and every explicit
// PositionWindow call, so the panel lands in the same place either way.
const trayWindowOffset = 6

// trayReadyTries and trayReadyPoll bound the wait for the system tray to come
// up before a programmatic first show.
const (
	trayReadyTries = 60
	trayReadyPoll  = 50 * time.Millisecond
)

// showPanelWhenReady reveals the panel under the tray icon once the tray can
// actually place it there.
//
// Two separate races have to clear first:
//
//   - App.Run starts pending runnables — the system tray among them — in
//     goroutines, so at the end of main() the tray impl does not exist yet and
//     PositionWindow fails with "system tray not running".
//   - The impl appears a beat before its NSStatusItem button has a laid-out
//     frame. Positioning against that zero frame puts the panel's centre at
//     x = -panelW/2, which the native code clamps to the left screen edge
//     (systemtray_darwin.m). PositionWindow still reports success, so err ==
//     nil is not proof of placement — the resulting frame is.
//
// Creating the window non-Hidden loses both races at once, which is what
// parked the first-run panel in the middle of the screen. The tray icon lives
// at the right of the menubar, so a real placement is never flush left: treat
// x <= 0 as "not laid out yet" and keep polling.
func showPanelWhenReady(tray *application.SystemTray, panel *application.WebviewWindow) {
	for i := range trayReadyTries {
		if err := tray.PositionWindow(panel, trayWindowOffset); err == nil {
			if x, y := panel.Position(); x > 0 {
				showPanel(tray, panel)
				// The frame is the only evidence that the first show landed
				// under the icon; no screenshot is reachable from a headless
				// session.
				slog.Debug("first-run panel shown", "x", x, "y", y,
					"waited", time.Duration(i)*trayReadyPoll)
				return
			}
		}
		time.Sleep(trayReadyPoll)
	}
	slog.Warn("tray icon never reported a frame; showing panel unpositioned")
	showPanel(tray, panel)
}

// showPanel positions the panel under the tray icon and reveals it.
func showPanel(tray *application.SystemTray, panel *application.WebviewWindow) {
	// Re-assert the size on every show. macOS restores a window's previous
	// frame, so a panel that ever ended up resized or zoomed would otherwise
	// keep coming back at the wrong size.
	panel.SetSize(panelW, panelH)

	if err := tray.PositionWindow(panel, trayWindowOffset); err != nil {
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

// initAppUpdater wires Wails app.Updater to GitHub Releases for in-app install.
// Check stays on DesktopSettings (light API); Install uses Framework DownloadAndInstall+Restart.
func initAppUpdater(wailsApp *application.App, desk *desktop.Settings, logger *slog.Logger) error {
	if wailsApp == nil || desk == nil || desk.Update == nil {
		return fmt.Errorf("updater: missing app or settings")
	}
	gh, err := github.New(github.Config{
		Repository:    desk.Update.Repo,
		ChecksumAsset: "SHA256SUMS",
		AssetMatcher:  desktop.MerinoZipAssetMatcher,
	})
	if err != nil {
		return err
	}
	ver := desktop.NormalizeVersion(version)
	if err := wailsApp.Updater.Init(updater.Config{
		CurrentVersion: ver,
		Providers:      []updater.Provider{gh},
		Window:         updater.WindowNone,
	}); err != nil {
		return err
	}
	desk.Update.Framework = wailsApp.Updater
	desk.Update.Current = ver
	logger.Info("updater ready", "version", ver, "repo", desk.Update.Repo)
	return nil
}
