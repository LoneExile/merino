package app

import (
	"strings"
	"testing"
)

func TestFilterIncludesClaudeBtw(t *testing.T) {
	for _, kind := range []string{"claude", "Claude", "Claude Code", "CLAUDE"} {
		hits := FilterSlashCommands(kind, "btw", "")
		found := false
		for _, h := range hits {
			if h.Name == "btw" {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("kind %q: btw missing (hits=%d)", kind, len(hits))
			for i, h := range hits {
				if i < 8 {
					t.Logf("  %s (%s)", h.Name, h.Source)
				}
			}
		}
	}
}

func TestFilterIncludesOmpBtw(t *testing.T) {
	hits := FilterSlashCommands("omp", "bt", "")
	found := false
	for _, h := range hits {
		if h.Name == "btw" {
			found = true
			// Should rank as builtin, near the top.
			break
		}
	}
	if !found {
		t.Fatalf("omp btw missing; got %v", names(hits))
	}
	// Prefix "btw" exact
	hits = FilterSlashCommands("omp", "btw", "")
	if len(hits) == 0 || hits[0].Name != "btw" {
		t.Fatalf("exact btw: got %v", names(hits))
	}
	if !strings.HasPrefix(hits[0].Value, "/btw") {
		t.Fatalf("value %q", hits[0].Value)
	}
}

func TestNormalizeAgentKind(t *testing.T) {
	cases := map[string]string{
		"Claude Code": "claude",
		"claude":      "claude",
		"omp":         "omp",
		"pi":          "pi",
		"Grok 4":      "grok",
		"":            "",
	}
	for in, want := range cases {
		if got := normalizeAgentKind(in); got != want {
			t.Errorf("normalizeAgentKind(%q)=%q want %q", in, got, want)
		}
	}
}
