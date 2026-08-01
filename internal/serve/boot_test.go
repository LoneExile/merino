package serve

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// isolate points config discovery and the state directory at empty temp
// dirs, so a real ~/.config/merino/config.yml or a real ~/Library/Logs/merino
// side-file on the developer's machine cannot decide a test's outcome.
func isolate(t *testing.T) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("MERINO_CONFIG", "")
	t.Setenv("MERINO_DEBUG", "")
	t.Setenv("MERINO_PUBLIC_URL", "")
	t.Setenv("HERDR_TUNNEL_PUBLIC_URL", "")
	t.Setenv("HERDR_SOCK", "")
}

// The §11 risk this test exists for: two entry points, one flag added to only
// one of them, and a daemon that quietly resolves differently from the
// menubar. Prepare is the single producer of Options, so given identical
// fully-specified input both Kinds must agree exactly.
func TestBothEntryPointsResolveIdentically(t *testing.T) {
	isolate(t)

	f := Flags{
		Listen:             "127.0.0.1:9001",
		BehindProxy:        true,
		BehindProxyGiven:   true,
		AllowWrites:        true,
		AllowSessionSwitch: true,
	}

	daemon, err := Prepare(Daemon, f)
	if err != nil {
		t.Fatal(err)
	}
	menubar, err := Prepare(Menubar, f)
	if err != nil {
		t.Fatal(err)
	}

	// Logger is a pointer to a freshly built handler and is never equal
	// across two calls; everything else must be.
	d, m := daemon.Options, menubar.Options
	d.Logger, m.Logger = nil, nil
	if !reflect.DeepEqual(d, m) {
		t.Fatalf("entry points disagree:\n daemon  %+v\n menubar %+v", d, m)
	}
}

// The one documented divergence, asserted so it stays the ONLY one: with
// nothing configured anywhere, the menubar opens the phone-facing gates and
// the daemon does not.
func TestUnconfiguredMenubarOpensGatesAndDaemonDoesNot(t *testing.T) {
	isolate(t)

	daemon, err := Prepare(Daemon, Flags{})
	if err != nil {
		t.Fatal(err)
	}
	menubar, err := Prepare(Menubar, Flags{})
	if err != nil {
		t.Fatal(err)
	}

	if daemon.Options.AllowWrites || daemon.Options.AllowSessionSwitch {
		t.Fatal("a headless daemon must not open write or session-switch gates nobody asked for")
	}
	if !menubar.Options.AllowWrites || !menubar.Options.AllowSessionSwitch {
		t.Fatal("an unconfigured double-click must answer blocked agents from a phone with no setup")
	}
	// Password sign-in is the weakest door and is not part of that default.
	if menubar.Options.PasswordLogin.On {
		t.Fatal("password sign-in must stay shut until opened deliberately")
	}
	// Everything except the two gates must still agree.
	if daemon.Options.Addr != menubar.Options.Addr {
		t.Fatalf("addr diverged: %q vs %q", daemon.Options.Addr, menubar.Options.Addr)
	}
}

// A config.yml that sets `listen` is a deployment, so it drops the menubar
// out of the zero-config default exactly like --listen does. Without this,
// an operator who configured a bind would still silently get open gates.
func TestConfiguredListenDropsTheMenubarDefault(t *testing.T) {
	isolate(t)

	dir := t.TempDir()
	path := filepath.Join(dir, "config.yml")
	if err := os.WriteFile(path, []byte("listen: \"127.0.0.1:9002\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	b, err := Prepare(Menubar, Flags{ConfigPath: path})
	if err != nil {
		t.Fatal(err)
	}
	if b.Options.Addr != "127.0.0.1:9002" {
		t.Fatalf("addr = %q", b.Options.Addr)
	}
	if b.Options.AllowWrites || b.Options.AllowSessionSwitch {
		t.Fatal("a configured bind means a deployment; the double-click default must not apply")
	}
}

// Catches the other half of the drift risk: a field added to Options and
// never wired into Prepare. Every field must be reachable from configuration.
func TestPrepareSetsEveryOptionsField(t *testing.T) {
	isolate(t)

	dir := t.TempDir()
	path := filepath.Join(dir, "config.yml")
	body := "listen: \"127.0.0.1:9003\"\n" +
		"publicUrl: \"https://merino.example\"\n" +
		"behindProxy: true\n" +
		"access:\n  allowWrites: true\n  allowSessionSwitch: true\n  passwordLogin: true\n" +
		"auth:\n  user: \"operator\"\n  passwordFile: \"/run/secrets/merino\"\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	b, err := Prepare(Daemon, Flags{ConfigPath: path})
	if err != nil {
		t.Fatal(err)
	}

	// Source is filled by the entry point after Prepare (it owns the agent
	// service), so it is the one field legitimately still zero here.
	v := reflect.ValueOf(b.Options)
	for i := range v.NumField() {
		name := v.Type().Field(i).Name
		if name == "Source" {
			continue
		}
		if v.Field(i).IsZero() {
			t.Errorf("Options.%s is zero: a config that sets every key must reach every field, "+
				"so this is either an unwired field or one that needs a documented exemption", name)
		}
	}
}

func TestBehindProxyExplicitFalseBeatsConfig(t *testing.T) {
	isolate(t)

	dir := t.TempDir()
	path := filepath.Join(dir, "config.yml")
	if err := os.WriteFile(path, []byte("behindProxy: true\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	// Not given: the file decides.
	b, err := Prepare(Daemon, Flags{ConfigPath: path})
	if err != nil {
		t.Fatal(err)
	}
	if !b.Options.BehindProxy {
		t.Fatal("config.yml should decide when the flag was not typed")
	}

	// Explicitly false: the operator standing in front of the deployment
	// wins, in the direction a plain bool cannot express.
	b, err = Prepare(Daemon, Flags{ConfigPath: path, BehindProxy: false, BehindProxyGiven: true})
	if err != nil {
		t.Fatal(err)
	}
	if b.Options.BehindProxy {
		t.Fatal("-behind-proxy=false must turn it off against a config that says true")
	}
}
