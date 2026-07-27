package app

import (
	"bytes"
	"errors"
	"os"
	"strings"
	"testing"
)

// Minimal PNG: 1x1 transparent.
var tinyPNG = []byte{
	0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, 0x00, 0x00, 0x00, 0x0d,
	0x49, 0x48, 0x44, 0x52, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
	0x08, 0x06, 0x00, 0x00, 0x00, 0x1f, 0x15, 0xc4, 0x89, 0x00, 0x00, 0x00,
	0x0a, 0x49, 0x44, 0x41, 0x54, 0x78, 0x9c, 0x63, 0x00, 0x01, 0x00, 0x00,
	0x05, 0x00, 0x01, 0x0d, 0x0a, 0x2d, 0xb4, 0x00, 0x00, 0x00, 0x00, 0x49,
	0x45, 0x4e, 0x44, 0xae, 0x42, 0x60, 0x82,
}

func TestStageImagePNG(t *testing.T) {
	path, mime, err := StageImage("image/png", tinyPNG)
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(path)
	if mime != "image/png" {
		t.Fatalf("mime %q", mime)
	}
	if !strings.HasSuffix(path, ".png") {
		t.Fatalf("path %q", path)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, tinyPNG) {
		t.Fatal("round-trip mismatch")
	}
}

func TestStageImageRejectsNonImage(t *testing.T) {
	_, _, err := StageImage("", []byte("not an image at all!!"))
	if !errors.Is(err, ErrNotAllowed) {
		t.Fatalf("err %v", err)
	}
}

func TestStageImageRejectsTooLarge(t *testing.T) {
	big := make([]byte, MaxAttachBytes+1)
	copy(big, tinyPNG)
	_, _, err := StageImage("image/png", big)
	if !errors.Is(err, ErrTooLong) {
		t.Fatalf("err %v", err)
	}
}

func TestStageImageMIMEMismatch(t *testing.T) {
	_, _, err := StageImage("image/jpeg", tinyPNG)
	if !errors.Is(err, ErrNotAllowed) {
		t.Fatalf("err %v", err)
	}
}
