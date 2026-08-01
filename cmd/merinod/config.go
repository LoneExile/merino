package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/LoneExile/merino/internal/config"
	"github.com/spf13/cobra"
)

func configCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Inspect the config file and the values it produces",
	}
	cmd.AddCommand(configPathCmd(), configShowCmd(), configInitCmd())
	return cmd
}

// configPathCmd answers "which file, and can it be written".
//
// The second half is not a curiosity: writability is what decides whether the
// access keys are defaults the panel can override or pins it cannot, and a
// writable-looking path can still be ephemeral (a hostPath, an emptyDir
// seeded by an init container, some subPath arrangements). Printing the
// resolved path and its probed writability makes that inspectable instead of
// something the operator has to infer from behaviour.
func configPathCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "path",
		Short: "Print which config file was loaded and whether it is writable",
		RunE: func(cmd *cobra.Command, _ []string) error {
			f, err := config.Load(serveFlags.config)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if f.Path == "" {
				fmt.Fprintln(out, "no config file (this is a supported, normal state)")
				fmt.Fprintln(out, "searched, in order:")
				for _, p := range config.Search(serveFlags.config) {
					fmt.Fprintf(out, "  %s\n", p)
				}
				return nil
			}
			fmt.Fprintf(out, "path:      %s\n", f.Path)
			fmt.Fprintf(out, "writable:  %t\n", f.Writable)
			if f.Locked() {
				fmt.Fprintln(out, "gates:     PINNED — access keys in this file override the Settings panel")
			} else {
				fmt.Fprintln(out, "gates:     defaults — the Settings panel overrides access keys in this file")
			}
			for _, key := range f.Unhonoured {
				fmt.Fprintf(out, "warning:   %s is not honoured by this build yet\n", key)
			}
			return nil
		},
	}
}

// configShowCmd prints what the process actually resolved, not what the file
// says. Those differ whenever env or a flag outranks the file, which is
// exactly when somebody is confused enough to run this.
func configShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show",
		Short: "Print the effective configuration, secrets redacted",
		RunE: func(cmd *cobra.Command, _ []string) error {
			boot, err := prepare(cmd, serveFlags)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			source := boot.Config.Path
			if source == "" {
				source = "(no file; built-in defaults)"
			}
			fmt.Fprintf(out, "config file:          %s\n", source)
			fmt.Fprintf(out, "listen:               %s\n", boot.Options.Addr)
			fmt.Fprintf(out, "publicUrl:            %s\n", orNone(boot.Options.PublicURL))
			fmt.Fprintf(out, "behindProxy:          %t\n", boot.Options.BehindProxy)
			fmt.Fprintf(out, "herdr socket:         %s\n", boot.Client.Socket())
			fmt.Fprintf(out, "state directory:      %s\n", boot.StateDir)
			fmt.Fprintln(out)
			for _, g := range []struct {
				name string
				gate config.Gate
			}{
				{"allowWrites", boot.Gates.Writes},
				{"allowSessionSwitch", boot.Gates.SessionSwitch},
				{"passwordLogin", boot.Gates.PasswordLogin},
			} {
				lock := ""
				if g.gate.Locked {
					lock = "  (locked by a read-only config.yml)"
				}
				fmt.Fprintf(out, "%-20s  %-5t  from %s%s\n", g.name+":", g.gate.On, g.gate.Source, lock)
			}
			// auth.passwordFile is named but never read here: printing a
			// path is fine, printing what is in it is not.
			if boot.Config.Auth.PasswordFile != "" {
				fmt.Fprintf(out, "\nauth.passwordFile:    %s (contents never printed)\n",
					boot.Config.Auth.PasswordFile)
			}
			return nil
		},
	}
}

func orNone(s string) string {
	if s == "" {
		return "(autodetect a LAN address)"
	}
	return s
}

// configInitCmd writes the commented schema. It refuses to overwrite: this
// file is hand-edited and the comments in it are the documentation, so
// clobbering one is destroying the operator's work.
func configInitCmd() *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:   "init [path]",
		Short: "Write a commented config.yml",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			path := config.Search("")[0]
			if len(args) == 1 {
				path = args[0]
			}
			if _, err := os.Stat(path); err == nil && !force {
				return fmt.Errorf("%s already exists; pass --force to overwrite it", path)
			}
			dir := path[:strings.LastIndex(path, "/")+1]
			if dir != "" {
				if err := os.MkdirAll(dir, 0o700); err != nil {
					return err
				}
			}
			if err := os.WriteFile(path, []byte(configTemplate), 0o600); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "wrote %s\n", path)
			return nil
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "overwrite an existing file")
	return cmd
}

// configTemplate is every key, commented, all of them optional. It is written
// out rather than generated from the struct because the comments carry the
// reasoning, and that is the part an operator actually needs.
const configTemplate = `# Merino — every key is optional.
# Precedence:  flag > env > this file > built-in default

listen: "0.0.0.0:8730"

# REQUIRED in a container: with this empty Merino autodetects a LAN address,
# which inside a container is the container's own address — a URL no phone
# can open.
publicUrl: ""

# Secure cookies + trust the proxy's client-IP header. Required behind a TLS
# terminator. Never enable while the port is also reachable directly.
behindProxy: false

herdr:
  # Empty means ~/.config/herdr/herdr.sock. Always a FILE both processes can
  # open: same host, a bind mount, or an ssh -L forward. herdr never listens
  # on a port, so there is nothing to dial over the network.
  socket: ""

  # Which agent kinds the spawn sheet may offer. Empty means autodetect, which
  # probes Merino's OWN login shell — right on a workstation, wrong the moment
  # herdr lives elsewhere. Under an ssh-forwarded socket that is always.
  # Valid: omp pi claude codex gemini cursor opencode copilot grok amp droid
  #        kimi kiro kilo cline devin agy hermes maki mastracode qodercli
  agents: []

# The only three keys that collide with the Settings panel's own toggles.
#
# If THIS FILE is writable, they are defaults and the panel wins.
# If it is read-only (a ConfigMap, -v ...:ro, readOnlyRootFilesystem), they
# pin and the panel is told it cannot change them. Merino never writes here.
access:
  allowWrites: false          # arbitrary input into live terminals; audited
  allowSessionSwitch: false   # changes which agents every browser sees
  passwordLogin: false        # QR pairing works regardless

auth:
  user: "operator"
  # A file — which is what a Kubernetes Secret mount gives you. $MERINO_PASS
  # still works. A literal password never belongs here, and there is no
  # --password flag anywhere in this program.
  passwordFile: ""

paths:
  # Credentials, VAPID keys, push subscriptions, paired devices, gate state.
  #   macOS      ~/Library/Logs/merino   (moving it unpairs every phone)
  #   Linux      $XDG_STATE_HOME/merino
  #   Container  MUST be a volume
  stateDir: ""
  auditLog: ""   # "-" writes JSONL to stdout for a log collector

log:
  level: info    # debug | info | warn | error
  format: text   # text | json
`
