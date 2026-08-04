package app

import (
	"regexp"
	"testing"
)

// herdrAgentName is herdr's own rule, quoted from the error it returns:
// "agent name must start with a lowercase letter and contain only lowercase
// letters, digits, '-' or '_' (1-32 characters)". Verified live against
// herdr 0.8.0 (protocol 19) — see herdr.TestLiveAgentStartRejectsInvalidName
// — which rejects anything else with invalid_agent_name AFTER the tab has
// been created.
var herdrAgentName = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,31}$`)

func TestAgentNameFromAlwaysSatisfiesHerdr(t *testing.T) {
	labels := []string{
		"",            // no label: falls back to the kind
		"scratch",     // already valid
		"Scratch Pad", // the reported break: capital + space
		"  padded  ",  // trimmed by the web layer, defensive here
		"2nd try",     // must not start with a digit
		"___",         // separators only
		"-----",       // nothing but dashes
		"!!!",         // nothing usable at all
		"ünïcødé",     // non-ASCII letters are not in herdr's class
		"a very long label that runs well past thirty-two characters",
		"trailing-",           // trailing separator after truncation
		"MiXeD_Case-99",       // digits and underscores survive
		"emoji 🐑 sheep",       // multi-byte runes
		"tabs\tand\nnewlines", // control characters
	}

	for _, label := range labels {
		got := agentNameFrom(label, "omp")
		if !herdrAgentName.MatchString(got) {
			t.Errorf("agentNameFrom(%q) = %q, which herdr rejects", label, got)
		}
	}
}

func TestAgentNameFromPreservesIntent(t *testing.T) {
	for _, tc := range []struct{ label, want string }{
		{"", "omp"},    // empty falls back to the kind
		{"!!!", "omp"}, // so does a label with nothing usable
		{"Scratch Pad", "scratch-pad"},
		{"MiXeD_Case-99", "mixed_case-99"},
		{"2nd try", "nd-try"}, // leading digits dropped, rest kept
	} {
		if got := agentNameFrom(tc.label, "omp"); got != tc.want {
			t.Errorf("agentNameFrom(%q) = %q, want %q", tc.label, got, tc.want)
		}
	}
}

// The kind is the fallback, so every kind in the table must itself be a legal
// agent name — otherwise an unlabelled spawn of that kind fails at herdr.
func TestEverySupportedKindIsALegalAgentName(t *testing.T) {
	for _, k := range supportedKinds {
		if !herdrAgentName.MatchString(k.kind) {
			t.Errorf("kind %q is not a legal herdr agent name", k.kind)
		}
	}
}

func TestSupportedKindRejectsUnknown(t *testing.T) {
	if _, ok := supportedKind("definitely-not-a-kind"); ok {
		t.Fatal("unknown kind accepted")
	}
	if got, ok := supportedKind("omp"); !ok || got != "omp" {
		t.Fatalf("supportedKind(omp) = %q, %v", got, ok)
	}
}
