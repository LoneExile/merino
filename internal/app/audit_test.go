package app

import (
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// DefaultAuditPath decides where every deployment keeps the state that must
// survive a restart — bootstrap credentials, device grants, push
// subscriptions all live beside the audit log. It shipped with the macOS
// layout hardcoded, which was invisible while Merino was a menu bar app and
// became a boot failure the first time merinod ran in a container.
//
// These pin both halves: macOS must not move, and everything else must
// follow XDG rather than growing a ~/Library directory.

func TestDefaultAuditPathHonoursXDGStateHome(t *testing.T) {
	if runtime.GOOS == "darwin" {
		t.Skip("macOS deliberately ignores XDG here; see TestDefaultAuditPathOnDarwin")
	}
	t.Setenv("XDG_STATE_HOME", "/var/lib")

	got := DefaultAuditPath()

	want := filepath.Join("/var/lib", "merino", "audit.jsonl")
	if got != want {
		t.Errorf("XDG_STATE_HOME ignored: got %q, want %q", got, want)
	}
}

func TestDefaultAuditPathFallsBackToXDGDefault(t *testing.T) {
	if runtime.GOOS == "darwin" {
		t.Skip("macOS uses ~/Library/Logs; see TestDefaultAuditPathOnDarwin")
	}
	t.Setenv("XDG_STATE_HOME", "")
	t.Setenv("HOME", "/home/someone")

	got := DefaultAuditPath()

	want := filepath.Join("/home/someone", ".local", "state", "merino", "audit.jsonl")
	if got != want {
		t.Errorf("got %q, want the XDG default %q", got, want)
	}
}

// The regression that started this: Docker sets HOME=/ for a uid with no
// passwd entry, which is every `--user 1000:1000` override — the exact thing
// deploy/compose.yaml instructs operators to use. With the macOS layout
// unconditional, the daemon tried to create /Library and exited before
// serving anything.
func TestDefaultAuditPathDoesNotUseLibraryOffDarwin(t *testing.T) {
	if runtime.GOOS == "darwin" {
		t.Skip("Library is correct on macOS")
	}
	t.Setenv("XDG_STATE_HOME", "")
	t.Setenv("HOME", "/")

	got := DefaultAuditPath()

	if strings.Contains(got, "Library") {
		t.Errorf("macOS layout leaked onto %s: %q", runtime.GOOS, got)
	}
	if strings.HasPrefix(got, "/Library") {
		t.Errorf("path resolves under the filesystem root, which no unprivileged uid can create: %q", got)
	}
}

// Moving this on macOS unpairs every phone, so it is pinned as tightly as the
// fix itself. A relative XDG_STATE_HOME is invalid per the spec and must not
// redirect anything, on any platform.
func TestDefaultAuditPathOnDarwin(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("darwin only")
	}
	t.Setenv("XDG_STATE_HOME", "/var/lib")
	t.Setenv("HOME", "/Users/someone")

	got := DefaultAuditPath()

	want := filepath.Join("/Users/someone", "Library", "Logs", "merino", "audit.jsonl")
	if got != want {
		t.Errorf("macOS state directory moved: got %q, want %q", got, want)
	}
}

func TestDefaultAuditPathIgnoresRelativeXDGStateHome(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", "relative/path")
	t.Setenv("HOME", "/home/someone")

	got := DefaultAuditPath()

	if strings.HasPrefix(got, "relative/") {
		t.Errorf("relative XDG_STATE_HOME honoured, scattering state through the working directory: %q", got)
	}
	if !filepath.IsAbs(got) {
		t.Errorf("state path is not absolute: %q", got)
	}
}
