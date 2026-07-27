package desktop

import "testing"

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
	}
	for _, tc := range cases {
		if got := versionLess(tc.a, tc.b); got != tc.less {
			t.Errorf("versionLess(%q,%q)=%v want %v", tc.a, tc.b, got, tc.less)
		}
	}
}
