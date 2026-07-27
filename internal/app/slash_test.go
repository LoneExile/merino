package app

import (
	"strings"
	"testing"
)

func TestFilterSlashCommandsPrefix(t *testing.T) {
	hits := FilterSlashCommands("claude", "/hel", "")
	if len(hits) == 0 {
		t.Fatal("expected hits for /hel on claude")
	}
	if !strings.HasPrefix(hits[0].Name, "hel") && hits[0].Name != "help" {
		// first should prefer help
		found := false
		for _, h := range hits {
			if h.Name == "help" {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("help missing from %v", names(hits))
		}
	}
	// Values always start with / or skill:
	for _, h := range hits {
		if !strings.HasPrefix(h.Value, "/") && !strings.HasPrefix(h.Value, "skill:") {
			t.Errorf("bad value %q", h.Value)
		}
	}
}

func TestFilterSlashCommandsOmpSkillsPrefixed(t *testing.T) {
	hits := FilterSlashCommands("omp", "skill:", "")
	// May be empty if no skills on disk in CI — still must not panic.
	for _, h := range hits {
		if !strings.HasPrefix(h.Name, "skill:") {
			t.Errorf("omp skill query returned non-skill %q", h.Name)
		}
	}
}

func TestFilterSlashCommandsEmptyQueryListsBuiltins(t *testing.T) {
	hits := FilterSlashCommands("grok", "/", "")
	if len(hits) == 0 {
		t.Fatal("empty query should list grok builtins")
	}
}

func names(hs []SlashCommand) []string {
	out := make([]string, len(hs))
	for i, h := range hs {
		out[i] = h.Name
	}
	return out
}
