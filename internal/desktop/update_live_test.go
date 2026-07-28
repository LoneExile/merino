//go:build live

package desktop

import (
	"context"
	"testing"
	"time"
)

// go test -tags=live ./internal/desktop/ -run TestLiveCheck -count=1
func TestLiveCheck(t *testing.T) {
	u := &Updater{Repo: "LoneExile/merino", Current: "0.1.1"}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	info, err := u.Check(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("%+v", info)
	if !info.Newer {
		t.Fatalf("expected newer than 0.1.1, got latest=%s", info.Latest)
	}
	if info.AssetName == "" {
		t.Fatal("expected macos-arm64.zip asset name")
	}
}
