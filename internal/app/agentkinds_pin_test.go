package app

import (
	"context"
	"slices"
	"testing"
)

// unpin restores autodetection so a pinned list cannot leak into the rest of
// the package's tests.
func unpin(t *testing.T) {
	t.Helper()
	t.Cleanup(func() { pinnedKinds.Store(nil) })
}

func TestPinAgentKindsReplacesAutodetection(t *testing.T) {
	unpin(t)

	if dropped := PinAgentKinds([]string{"omp", "claude"}); len(dropped) != 0 {
		t.Fatalf("supported kinds must not be dropped, got %v", dropped)
	}

	got := AvailableAgentKinds(context.Background())
	if len(got) != 2 {
		t.Fatalf("want exactly the pinned kinds, got %v", got)
	}
	for i, want := range []string{"omp", "claude"} {
		if got[i].Kind != want {
			t.Fatalf("kind %d: got %q, want %q", i, got[i].Kind, want)
		}
	}
	// Order is the operator's, not sorted: the config file is the authority.
	if got[0].Label != "Oh My Pi" {
		t.Fatalf("labels come from supportedKinds, got %q", got[0].Label)
	}
	// Nothing was probed, so claiming a path would be a lie.
	if got[0].Path != "" {
		t.Fatalf("a pinned kind has no discovered path, got %q", got[0].Path)
	}
}

// A pinned list must win even when the autodetect cache is already warm —
// otherwise the setting appears to work only on a cold start.
func TestPinAgentKindsBeatsAWarmCache(t *testing.T) {
	unpin(t)

	_ = AvailableAgentKinds(context.Background()) // warm the cache
	PinAgentKinds([]string{"codex"})

	got := AvailableAgentKinds(context.Background())
	if len(got) != 1 || got[0].Kind != "codex" {
		t.Fatalf("pin must override a warm cache, got %v", got)
	}
}

func TestPinAgentKindsReportsUnknownNames(t *testing.T) {
	unpin(t)

	dropped := PinAgentKinds([]string{"omp", "notanagent", "alsonot"})
	if !slices.Equal(dropped, []string{"notanagent", "alsonot"}) {
		t.Fatalf("unknown kinds must be reported so boot can warn, got %v", dropped)
	}

	got := AvailableAgentKinds(context.Background())
	if len(got) != 1 || got[0].Kind != "omp" {
		t.Fatalf("unknown kinds must be dropped, not passed to herdr, got %v", got)
	}
}

// An empty list is "the operator said nothing", which must leave
// autodetection alone rather than pinning an empty spawn sheet.
func TestPinAgentKindsEmptyIsANoOp(t *testing.T) {
	unpin(t)

	PinAgentKinds(nil)
	if pinnedKinds.Load() != nil {
		t.Fatal("an absent herdr.agents key must not pin anything")
	}
}
