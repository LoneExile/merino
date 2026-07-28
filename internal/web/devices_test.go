package web

import (
	"path/filepath"
	"testing"
)

func TestDeviceStoreMintRevoke(t *testing.T) {
	dir := t.TempDir()
	s, err := OpenDeviceStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	d, id, err := s.Mint("iPhone", "pairing", nil)
	if err != nil {
		t.Fatal(err)
	}
	if id.Subject != "device:"+d.ID {
		t.Fatalf("subject %q", id.Subject)
	}
	if !s.Active(id.Subject) {
		t.Fatal("expected active")
	}
	if s.CountActive() != 1 {
		t.Fatalf("active=%d", s.CountActive())
	}
	ok, err := s.Revoke(d.ID)
	if err != nil || !ok {
		t.Fatalf("revoke %v %v", ok, err)
	}
	if s.Active(id.Subject) {
		t.Fatal("expected revoked inactive")
	}
	// reload
	s2, err := OpenDeviceStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if s2.CountActive() != 0 {
		t.Fatalf("reloaded active=%d", s2.CountActive())
	}
	if _, err := s2.RevokeAll(); err != nil {
		t.Fatal(err)
	}
	_ = filepath.Join(dir, "devices.json")
}

func TestBootstrapCreds(t *testing.T) {
	t.Setenv("MERINO_USER", "")
	t.Setenv("MERINO_PASS", "")
	t.Setenv("HERDR_TUNNEL_USER", "")
	t.Setenv("HERDR_TUNNEL_PASS", "")
	dir := t.TempDir()
	u1, p1, gen, err := LoadOrCreateBootstrap(dir)
	if err != nil || !gen || u1 == "" || p1 == "" {
		t.Fatalf("first %v %v %q %q", err, gen, u1, p1)
	}
	u2, p2, gen2, err := LoadOrCreateBootstrap(dir)
	if err != nil || gen2 || u2 != u1 || p2 != p1 {
		t.Fatalf("second %v gen=%v", err, gen2)
	}
}
