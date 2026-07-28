package web

import "testing"

func TestPreferLANBaseHasPort(t *testing.T) {
	b := PreferLANBase("0.0.0.0:8730")
	if b == "" || b == "http://:8730" {
		t.Fatalf("bad base %q", b)
	}
	if PreferLANBase("127.0.0.1:9000") != "http://127.0.0.1:9000" && PreferLANBase("127.0.0.1:9000") == "" {
		t.Fatal("empty")
	}
	origins := LocalAccessOrigins("0.0.0.0:8730")
	if len(origins) < 1 || origins[0].Kind != "local" {
		t.Fatalf("%+v", origins)
	}
}
