package web

import "testing"

func TestSafePasteName(t *testing.T) {
	ok := []string{"paste-1.png", "paste-1785193671255712000.png", "paste-9.webp"}
	for _, n := range ok {
		if !safePasteName(n) {
			t.Errorf("want ok %q", n)
		}
	}
	bad := []string{"", "paste.png", "../paste-1.png", "paste-1.png/x", "paste-abc.png", "xpaste-1.png", "paste-1.exe"}
	for _, n := range bad {
		if safePasteName(n) {
			t.Errorf("want deny %q", n)
		}
	}
}
