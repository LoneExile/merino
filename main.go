// Command herdr-tunnel is a menubar dashboard for herdr coding agents.
//
// It holds a persistent connection to the herdr socket API and projects live
// agent state into a tray label and an attached panel window. State is driven
// entirely by the server's push event stream — the herdr CLI is never invoked.
package main

import (
	"embed"
	"fmt"
	"log/slog"
	"os"
	"runtime"

	"github.com/LoneExile/herdr-tunnel/internal/app"
	"github.com/LoneExile/herdr-tunnel/internal/herdr"
	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/icons"
)

//go:embed all:frontend/dist
var assets embed.FS

func init() {
	// Registering events gives the frontend strongly typed listeners for them.
	application.RegisterEvent[[]app.Agent](app.EventAgentsChanged)
	application.RegisterEvent[app.Conn](app.EventConnChanged)
}

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: logLevel(),
	}))

	client := herdr.New(os.Getenv("HERDR_SOCK"))

	// The service is constructed before the application so it can be bound at
	// creation time; emit and tray callbacks are late-bound via closures over
	// variables filled in below.
	var (
		wailsApp *application.App
		tray     *application.SystemTray
	)

	emit := func(name string, data ...any) {
		if wailsApp != nil {
			wailsApp.Event.Emit(name, data...)
		}
	}
	onCounts := func(c app.Counts) {
		if tray != nil {
			tray.SetLabel(trayLabel(c))
		}
	}

	agents := app.NewAgentsService(client, logger, emit, onCounts)

	wailsApp = application.New(application.Options{
		Name:        "Herdr Tunnel",
		Description: "Menubar dashboard for herdr agents",
		Services: []application.Service{
			application.NewService(agents),
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

	panel := wailsApp.Window.NewWithOptions(application.WebviewWindowOptions{
		Name:             "panel",
		Title:            "Herdr Tunnel",
		Width:            420,
		Height:           560,
		Hidden:           true, // revealed by clicking the tray icon
		AlwaysOnTop:      true,
		DisableResize:    false,
		BackgroundColour: application.NewRGB(12, 14, 20),
		URL:              "/",
		Mac: application.MacWindow{
			InvisibleTitleBarHeight: 36,
			Backdrop:                application.MacBackdropTranslucent,
			TitleBar:                application.MacTitleBarHiddenInset,
		},
		Windows: application.WindowsWindow{
			HiddenOnTaskbar: true,
		},
	})

	tray = wailsApp.SystemTray.New()
	tray.SetTooltip("Herdr Tunnel")
	tray.SetLabel(trayLabel(app.Counts{}))
	if runtime.GOOS == "darwin" {
		// Template icons adopt the menubar's light/dark appearance.
		tray.SetTemplateIcon(icons.SystrayMacTemplate)
	} else {
		tray.SetIcon(icons.SystrayLight)
	}

	menu := wailsApp.NewMenu()
	menu.Add("Show agents").OnClick(func(*application.Context) { tray.ShowWindow() })
	menu.AddSeparator()
	menu.Add("Quit").OnClick(func(*application.Context) { wailsApp.Quit() })
	tray.SetMenu(menu)

	// Attaching a window makes a left click toggle the panel and positions it
	// under the tray icon; Wails wires the click handler automatically.
	tray.AttachWindow(panel).WindowOffset(6)

	// Wails runs configured trays itself once the app is running, so no
	// explicit tray.Run() is needed here.
	if err := wailsApp.Run(); err != nil {
		logger.Error("application exited", "err", err)
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
	if os.Getenv("HERDR_TUNNEL_DEBUG") != "" {
		return slog.LevelDebug
	}
	return slog.LevelInfo
}
