package config

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// write drops a config.yml into a fresh directory and points the search at
// it, returning the path.
func write(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, FileName)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// isolate points every implicit search rung at empty temp directories so a
// real ~/.config/merino/config.yml on the developer's machine cannot decide
// a test's outcome.
func isolate(t *testing.T) {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("MERINO_CONFIG", "")
}

func TestSearchOrder(t *testing.T) {
	t.Run("explicit wins alone", func(t *testing.T) {
		got := Search("/tmp/x.yml")
		if len(got) != 1 || got[0] != "/tmp/x.yml" {
			t.Fatalf("explicit path must be the only candidate, got %v", got)
		}
	})

	t.Run("XDG then /etc", func(t *testing.T) {
		t.Setenv("XDG_CONFIG_HOME", "/xdg")
		got := Search("")
		want := []string{"/xdg/merino/config.yml", "/etc/merino/config.yml"}
		if len(got) != len(want) {
			t.Fatalf("got %v, want %v", got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("rung %d: got %q, want %q", i, got[i], want[i])
			}
		}
	})

	t.Run("/etc is always last", func(t *testing.T) {
		t.Setenv("XDG_CONFIG_HOME", "")
		got := Search("")
		if got[len(got)-1] != "/etc/merino/config.yml" {
			t.Fatalf("last rung must be /etc, got %v", got)
		}
	})
}

func TestNoFileAnywhereIsNormal(t *testing.T) {
	isolate(t)
	f, err := Load("")
	if err != nil {
		t.Fatalf("a missing config must not be an error: %v", err)
	}
	if f.Path != "" {
		t.Fatalf("Path should be empty when nothing was found, got %q", f.Path)
	}
	if f.Locked() {
		t.Fatal("no file cannot lock anything")
	}
	if f.Listen != "" || f.Access.AllowWrites != nil {
		t.Fatal("absent config must leave every key unset")
	}
}

func TestExplicitMissingFileIsFatal(t *testing.T) {
	isolate(t)
	_, err := Load(filepath.Join(t.TempDir(), "nope.yml"))
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("naming a file that does not exist must fail loudly, got %v", err)
	}
}

func TestExplicitViaEnv(t *testing.T) {
	isolate(t)
	path := write(t, "listen: \"127.0.0.1:9999\"\n")
	t.Setenv("MERINO_CONFIG", path)

	f, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if f.Path != path {
		t.Fatalf("MERINO_CONFIG should have been loaded, got %q", f.Path)
	}
	if f.Listen != "127.0.0.1:9999" {
		t.Fatalf("listen = %q", f.Listen)
	}
}

func TestEmptyAndCommentOnlyFilesAreValid(t *testing.T) {
	isolate(t)
	for name, body := range map[string]string{
		"empty":        "",
		"comment only": "# nothing to see here\n",
	} {
		t.Run(name, func(t *testing.T) {
			f, err := Load(write(t, body))
			if err != nil {
				t.Fatalf("every key is optional, so this must load: %v", err)
			}
			if f.Listen != "" {
				t.Fatalf("listen = %q, want empty", f.Listen)
			}
		})
	}
}

// A typo that silently does nothing is the failure this package exists to
// avoid, so an unknown key must refuse to start and name itself.
func TestUnknownKeyIsRejected(t *testing.T) {
	isolate(t)
	_, err := Load(write(t, "publicURL: \"https://example.com\"\n"))
	if err == nil {
		t.Fatal("a misspelled key must be rejected, not ignored")
	}
	if !strings.Contains(err.Error(), "publicURL") {
		t.Fatalf("the error must name the offending key, got %v", err)
	}
}

func TestUnhonouredKeysAreReported(t *testing.T) {
	isolate(t)
	f, err := Load(write(t, "paths:\n  stateDir: /var/lib/merino\nlog:\n  format: json\n"))
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(f.Unhonoured, ",")
	for _, want := range []string{"paths.stateDir", "log.format"} {
		if !strings.Contains(got, want) {
			t.Fatalf("%q should be reported as not yet honoured, got %v", want, f.Unhonoured)
		}
	}
	if strings.Contains(got, "listen") {
		t.Fatal("a key this build does act on must not be reported as unhonoured")
	}
}

func TestStringPrecedence(t *testing.T) {
	// flag > env > file > default, and empty means "not set" at every rung.
	for _, tc := range []struct {
		name                       string
		flag, env, file, def, want string
	}{
		{"flag beats everything", "F", "E", "C", "D", "F"},
		{"env beats file", "", "E", "C", "D", "E"},
		{"file beats default", "", "", "C", "D", "C"},
		{"default is the floor", "", "", "", "D", "D"},
		{"all unset", "", "", "", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := String(tc.flag, tc.env, tc.file, tc.def); got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func ptr(b bool) *bool { return &b }

// TestResolveGate walks every rung of D1. Each case is named for the rung it
// exercises, and the inputs are chosen so that only that rung can produce the
// expected answer — a resolver that skipped it would return something else.
func TestResolveGate(t *testing.T) {
	writableFile := &File{Path: "/tmp/config.yml", Writable: true}
	readOnlyFile := &File{Path: "/tmp/config.yml", Writable: false}
	noFile := &File{}

	for _, tc := range []struct {
		name string
		file *File
		in   GateInputs
		want Gate
	}{
		{
			// The flag is force-on only, so it can only ever win by saying yes.
			name: "flag beats a read-only config that says no",
			file: readOnlyFile,
			in:   GateInputs{FlagOn: true, Config: ptr(false), SettingsExplicit: true, SettingsOn: false},
			want: Gate{On: true, Locked: false, Source: GateFlag},
		},
		{
			// D1: cannot write the file, so the panel cannot disagree with it.
			name: "read-only config pins over the panel",
			file: readOnlyFile,
			in:   GateInputs{Config: ptr(false), SettingsExplicit: true, SettingsOn: true, Default: true},
			want: Gate{On: false, Locked: true, Source: GateConfig},
		},
		{
			// D1: writable file is advice; the panel outranks it.
			name: "panel beats a writable config",
			file: writableFile,
			in:   GateInputs{Config: ptr(false), SettingsExplicit: true, SettingsOn: true, Default: false},
			want: Gate{On: true, Locked: false, Source: GateSettings},
		},
		{
			// Writable config still decides when the panel has no opinion.
			name: "writable config beats the built-in default",
			file: writableFile,
			in:   GateInputs{Config: ptr(true), SettingsExplicit: false, Default: false},
			want: Gate{On: true, Locked: false, Source: GateConfig},
		},
		{
			name: "default when nothing has an opinion",
			file: writableFile,
			in:   GateInputs{Default: true},
			want: Gate{On: true, Locked: false, Source: GateDefault},
		},
		{
			// Absent file cannot lock, so a read-only-looking zero value
			// must not be mistaken for operator intent.
			name: "no file at all falls through to the panel",
			file: noFile,
			in:   GateInputs{SettingsExplicit: true, SettingsOn: true, Default: false},
			want: Gate{On: true, Locked: false, Source: GateSettings},
		},
		{
			name: "no file and no panel is the default",
			file: noFile,
			in:   GateInputs{Default: true},
			want: Gate{On: true, Locked: false, Source: GateDefault},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.file.ResolveGate(tc.in); got != tc.want {
				t.Fatalf("got %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestLockedNeedsBothAFileAndReadOnly(t *testing.T) {
	if (&File{}).Locked() {
		t.Fatal("no file must not lock")
	}
	if (&File{Path: "/tmp/c.yml", Writable: true}).Locked() {
		t.Fatal("a writable file must not lock")
	}
	if !(&File{Path: "/tmp/c.yml", Writable: false}).Locked() {
		t.Fatal("a read-only file must lock")
	}
}

func TestReadOnlyFileLocks(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores permission bits; the mount-level case is what matters in a container")
	}
	if runtime.GOOS == "windows" {
		t.Skip("unix permission semantics")
	}
	isolate(t)
	path := write(t, "access:\n  allowWrites: true\n")
	if err := os.Chmod(path, 0o444); err != nil {
		t.Fatal(err)
	}

	f, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !f.Locked() {
		t.Fatal("a read-only config.yml must pin the access gates")
	}
	if f.Access.AllowWrites == nil || !*f.Access.AllowWrites {
		t.Fatal("the value should still have parsed")
	}
}

// The regression this package was written against: probing the DIRECTORY
// instead of the file reports a read-only file in a writable directory as
// writable — the `-v ./config.yml:/etc/merino/config.yml:ro` shape and the
// ConfigMap subPath shape. The gates would silently degrade to defaults and
// the panel would override operator intent.
func TestWritabilityProbesTheFileNotItsDirectory(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores permission bits")
	}
	if runtime.GOOS == "windows" {
		t.Skip("unix permission semantics")
	}
	isolate(t)

	dir := t.TempDir() // writable
	path := filepath.Join(dir, FileName)
	if err := os.WriteFile(path, []byte("access:\n  allowWrites: false\n"), 0o444); err != nil {
		t.Fatal(err)
	}

	// Guard the premise: the directory really is writable, so a directory
	// probe really would have got this wrong.
	probe := filepath.Join(dir, ".probe")
	if err := os.WriteFile(probe, nil, 0o600); err != nil {
		t.Fatalf("premise broken: temp dir should be writable: %v", err)
	}
	_ = os.Remove(probe)

	f, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if f.Writable {
		t.Fatal("a read-only file in a writable directory must not read as writable")
	}
	if !f.Locked() {
		t.Fatal("and it must therefore pin the gates")
	}

	// The whole point: the panel does not get to override it.
	got := f.ResolveGate(GateInputs{
		Config:           f.Access.AllowWrites,
		SettingsExplicit: true,
		SettingsOn:       true,
	})
	if got.On || !got.Locked || got.Source != GateConfig {
		t.Fatalf("read-only file must pin writes off, got %+v", got)
	}
}
