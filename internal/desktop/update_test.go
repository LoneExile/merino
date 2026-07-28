package desktop

import (
	"testing"

	"github.com/wailsapp/wails/v3/pkg/updater"
	"github.com/wailsapp/wails/v3/pkg/updater/providers/github"
)

func TestVersionLess(t *testing.T) {
	cases := []struct {
		a, b string
		less bool
	}{
		{"0.1.0", "0.1.1", true},
		{"0.1.0", "0.2.0", true},
		{"0.1.0", "1.0.0", true},
		{"1.0.0", "0.9.9", false},
		{"1.2.0", "1.10.0", true},
		{"1.10.0", "1.2.0", false},
		{"0.1.0", "0.1.0", false},
		{"0.1.2-dev", "0.1.2", true},
		{"0.1.2", "0.1.2-dev", false},
	}
	for _, tc := range cases {
		if got := versionLess(tc.a, tc.b); got != tc.less {
			t.Errorf("versionLess(%q,%q)=%v want %v", tc.a, tc.b, got, tc.less)
		}
	}
}

func TestMerinoZipAssetMatcher(t *testing.T) {
	assets := []github.ReleaseAsset{
		{Name: "merino-0.1.2-darwin-arm64"},
		{Name: "SHA256SUMS"},
		{Name: "Merino-0.1.2-macos-arm64.zip"},
		{Name: "merino-macos-arm64.zip"},
	}
	idx := MerinoZipAssetMatcher(updater.CheckRequest{Platform: "darwin", Arch: "arm64"}, assets)
	if idx < 0 {
		t.Fatal("expected a zip match")
	}
	if assets[idx].Name != "Merino-0.1.2-macos-arm64.zip" {
		t.Fatalf("got %q, want Merino product zip (not bare binary)", assets[idx].Name)
	}
}

func TestMerinoZipAssetMatcherRejectsBareBinaryOnly(t *testing.T) {
	assets := []github.ReleaseAsset{
		{Name: "merino-0.1.2-darwin-arm64"},
		{Name: "SHA256SUMS"},
	}
	if idx := MerinoZipAssetMatcher(updater.CheckRequest{}, assets); idx != -1 {
		t.Fatalf("bare binary must not match, got idx=%d name=%s", idx, assets[idx].Name)
	}
}

func TestNormalizeVersion(t *testing.T) {
	if got := NormalizeVersion("v0.1.2"); got != "0.1.2" {
		t.Fatalf("got %q", got)
	}
	if got := NormalizeVersion("dev"); got != "0.0.0-dev" {
		t.Fatalf("got %q", got)
	}
}
