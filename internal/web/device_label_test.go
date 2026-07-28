package web

import "testing"

func TestFriendlyDeviceName(t *testing.T) {
	cases := map[string]string{
		"Mozilla/5.0 (Linux; Android 10; K) AppleWebKit/537.36":  "Android phone",
		"Mozilla/5.0 (iPhone; CPU iPhone OS 17_0 like Mac OS X)": "iPhone",
		"": "Phone",
	}
	for in, want := range cases {
		if got := friendlyDeviceName(in); got != want {
			t.Fatalf("%q → %q want %q", in, got, want)
		}
	}
}
