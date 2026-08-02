// Command merinod is Merino without a desktop: the same browser dashboard
// internal/web serves for the menubar app, run as a background daemon under
// systemd, Docker or Kubernetes.
//
// It links no Wails packages at all — CI asserts that with a go list -deps
// check, because the regression is a single convenience import that nobody
// would flag in review.
// The command tree is deliberately small. Pairing a phone, revoking a device
// and toggling password sign-in all live in the macOS Settings sheet, and
// that works because the sheet runs INSIDE the server process: those three
// stores are in-memory-authoritative once the server boots (Pairing.tokens
// never touches disk at all, and DeviceStore.Active reads its in-memory map).
// A separate CLI process mutating the files underneath a running daemon would
// print success and change nothing — worst of all for `device revoke`, where
// the operator believes a stolen phone was cut off. Those commands need an
// admin socket or live-reloading stores, and shipping them before that is
// shipping a lie. A headless install admits its operator through
// auth.passwordFile + access.passwordLogin in config.yml instead, which is
// read at boot and therefore actually works.
package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

// version is injected at link time: -ldflags "-X main.version=v0.2.0".
var version = "0.0.0-dev"

func main() {
	if err := root().Execute(); err != nil {
		// Cobra has already printed the error; exit non-zero without
		// repeating it.
		os.Exit(1)
	}
}

func root() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "merinod",
		Short: "Merino's browser dashboard for herdr, without a desktop",
		Long: "merinod serves the Merino dashboard against a herdr socket.\n\n" +
			"It must be able to OPEN a socket file: the same host, a bind mount, or\n" +
			"an ssh -L forward. herdr never listens on a port, so there is nothing\n" +
			"to dial over the network.",
		// An error returned by RunE otherwise prints the full usage block
		// underneath it, burying the actual message.
		SilenceUsage: true,
		// Cobra keeps printing the error itself, because main() deliberately
		// does not: doing both would double every failure.
		SilenceErrors: false,
		// Running `merinod` bare is the common case in a unit file and a
		// container ENTRYPOINT, so it serves rather than printing help.
		RunE: func(cmd *cobra.Command, args []string) error {
			return runServe(cmd, serveFlags)
		},
	}

	// Registered on the root so both `merinod` and `merinod serve` accept
	// them; a container ENTRYPOINT and a unit file each pick a different one
	// and neither should be wrong.
	bindServeFlags(cmd.PersistentFlags())

	cmd.AddCommand(
		serveCmd(),
		configCmd(),
		qrCmd(),
		versionCmd(),
	)
	return cmd
}

func versionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the version and the herdr protocol this build speaks",
		RunE: func(cmd *cobra.Command, _ []string) error {
			// The protocol range is here because an image pinned to an old
			// tag beside an upgraded herdr connects and then does nothing
			// useful; `merinod version` should make that diagnosable without
			// reading logs.
			_, err := fmt.Fprintf(cmd.OutOrStdout(), "merinod %s\nherdr protocol %d\n",
				version, supportedProtocol)
			return err
		},
	}
}
