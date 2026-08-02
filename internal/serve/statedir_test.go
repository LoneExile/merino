package serve

import (
	"os"
	"path/filepath"
	"testing"
)

// The README tells operators running one merinod per herd to give each its
// own paths.stateDir, or the instances fight over credentials, the device
// store and the three gate files. That advice was wrong for one release:
// the key parsed and did nothing, so two instances silently shared a
// directory and the docs described a safety that did not exist.
//
// This asserts the whole chain — config.yml → Prepare → Options.StateDir —
// because a key honoured in Prepare but ignored in Start is worse than one
// ignored everywhere: gate decisions would read the configured directory
// while credentials and devices were written to the default.
func TestStateDirFlowsFromConfigToOptions(t *testing.T) {
	isolate(t)

	want := t.TempDir()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yml")
	body := "paths:\n  stateDir: " + want + "\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	b, err := Prepare(Daemon, Flags{ConfigPath: path})
	if err != nil {
		t.Fatal(err)
	}
	if b.StateDir != want {
		t.Fatalf("Boot.StateDir = %q, want %q", b.StateDir, want)
	}
	if b.Options.StateDir != want {
		t.Fatalf("Options.StateDir = %q, want %q — Prepare resolved it but never "+
			"passed it on, so Start would use the default and split the state in two",
			b.Options.StateDir, want)
	}
}

// Two daemons on one host, each with its own stateDir, must not collide.
// This is the entire reason the key exists.
func TestSeparateStateDirsDoNotCollide(t *testing.T) {
	isolate(t)

	mk := func(state string) *Boot {
		t.Helper()
		dir := t.TempDir()
		path := filepath.Join(dir, "config.yml")
		if err := os.WriteFile(path, []byte("paths:\n  stateDir: "+state+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		b, err := Prepare(Daemon, Flags{ConfigPath: path})
		if err != nil {
			t.Fatal(err)
		}
		return b
	}

	a, bb := mk(t.TempDir()), mk(t.TempDir())
	if a.Options.StateDir == bb.Options.StateDir {
		t.Fatal("two instances resolved the same state directory; they would share " +
			"credentials, the device store and every gate file")
	}
}

// Absent means the platform default, unchanged. Moving an existing install's
// state directory orphans every paired device and every push subscription
// silently, so the default must never drift.
func TestAbsentStateDirKeepsThePlatformDefault(t *testing.T) {
	isolate(t)

	withKey, err := Prepare(Daemon, Flags{})
	if err != nil {
		t.Fatal(err)
	}
	if withKey.StateDir == "" {
		t.Fatal("an absent paths.stateDir must resolve to the platform default, not empty")
	}
	// $HOME is redirected by isolate, so the default must sit under it —
	// proving it is derived rather than hardcoded to a real user's home.
	if home := os.Getenv("HOME"); home != "" && !filepath.HasPrefix(withKey.StateDir, home) {
		t.Fatalf("default state dir %q is not under HOME %q", withKey.StateDir, home)
	}
}
