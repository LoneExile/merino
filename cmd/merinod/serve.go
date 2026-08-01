package main

import (
	"context"
	"fmt"
	"os/signal"
	"syscall"

	"github.com/LoneExile/merino/internal/app"
	"github.com/LoneExile/merino/internal/herdr"
	"github.com/LoneExile/merino/internal/serve"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// supportedProtocol is the herdr wire version this build was written for.
// Surfaced by `merinod version` so an image pinned beside an upgraded herdr
// is diagnosable without reading logs.
const supportedProtocol = herdr.Protocol

// serveFlags is shared by the root command and `serve` so both accept the
// same set — a unit file and a container ENTRYPOINT each pick a different
// spelling and neither should be the wrong one.
var serveFlags = &flagSet{}

type flagSet struct {
	config             string
	listen             string
	behindProxy        bool
	allowWrites        bool
	allowSessionSwitch bool
}

func bindServeFlags(f *pflag.FlagSet) {
	f.StringVar(&serveFlags.config, "config", "",
		"path to config.yml; naming a file that does not exist is an error, not a fallback")
	f.StringVar(&serveFlags.listen, "listen", "",
		"bind address, e.g. 0.0.0.0:8730 (default 0.0.0.0:8730)")
	f.BoolVar(&serveFlags.behindProxy, "behind-proxy", false,
		"the server sits behind a trusted TLS-terminating proxy; marks cookies Secure "+
			"and trusts the proxy's client-IP header. Never enable this while the port "+
			"is also reachable directly")
	f.BoolVar(&serveFlags.allowWrites, "allow-writes", false,
		"let the dashboard approve prompts, send keys and interrupt agents. Off by "+
			"default: arbitrary input into live terminals, and every action is audited")
	f.BoolVar(&serveFlags.allowSessionSwitch, "allow-session-switch", false,
		"let the dashboard repoint this process at a different herdr session's socket")
}

func serveCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "serve",
		Short: "Run the dashboard (default when no subcommand is given)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runServe(cmd, serveFlags)
		},
	}
}

func runServe(cmd *cobra.Command, f *flagSet) error {
	boot, err := prepare(cmd, f)
	if err != nil {
		return err
	}
	boot.LogGates()

	// The daemon owns its own agent projection; the menubar's is bound into
	// Wails, which is the only part of this that differs.
	agents := app.NewAgentsService(boot.Client, boot.Logger, nil, nil)

	opts := boot.Options
	opts.Source = agents
	dash, err := serve.Start(opts)
	if err != nil {
		return fmt.Errorf("start dashboard: %w", err)
	}

	// Run until told to stop. NotifyContext rather than a bare channel so a
	// second signal kills a hung shutdown rather than being swallowed.
	ctx, stop := signal.NotifyContext(cmd.Context(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := agents.Start(ctx); err != nil {
		return fmt.Errorf("connect to herdr: %w", err)
	}

	<-ctx.Done()
	boot.Logger.Info("shutting down")
	return shutdown(dash)
}

// shutdown closes the listener so in-flight requests finish and the port is
// released before the process exits. A container that leaves the socket in
// TIME_WAIT makes its own restart look like a bind failure.
func shutdown(dash *serve.Dashboard) error {
	if dash == nil || dash.Server == nil {
		return nil
	}
	return dash.Server.Stop(context.Background())
}

// prepare wires cobra's flags into the resolver both entry points share.
// Every non-serve subcommand needs it too: pairing, device and password work
// all read the state directory config.yml may have moved.
func prepare(cmd *cobra.Command, f *flagSet) (*serve.Boot, error) {
	return serve.Prepare(serve.Daemon, serve.Flags{
		ConfigPath:  f.config,
		Listen:      f.listen,
		BehindProxy: f.behindProxy,
		// pflag CAN tell "not given" from "=false", which stdlib flag
		// cannot. behind-proxy decides Secure cookies and whether a
		// client-IP header from the network is believed, so an explicit
		// false must beat a config.yml that says true.
		BehindProxyGiven:   cmd.Flags().Changed("behind-proxy"),
		AllowWrites:        f.allowWrites,
		AllowSessionSwitch: f.allowSessionSwitch,
	})
}
