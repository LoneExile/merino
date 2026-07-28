package web

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveHomeImagePath(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(home, "generated-images")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	f := filepath.Join(dir, "merino-test-donut.jpg")
	// minimal jpeg header-ish — sniff may fail; write real tiny jpeg bytes from attach tests if needed
	// Use PNG magic so Sniff passes when served; resolve only checks ext+under home.
	if err := os.WriteFile(f, []byte{0xff, 0xd8, 0xff, 0xd9}, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(f) })

	got, err := resolveHomeImagePath("~/generated-images/merino-test-donut.jpg")
	if err != nil {
		t.Fatal(err)
	}
	if got != f {
		t.Fatalf("got %q want %q", got, f)
	}
	if _, err := resolveHomeImagePath("/etc/passwd"); err == nil {
		t.Fatal("expected reject outside home")
	}
	if _, err := resolveHomeImagePath("~/generated-images/nope.txt"); err == nil {
		t.Fatal("expected reject non-image ext")
	}
}
