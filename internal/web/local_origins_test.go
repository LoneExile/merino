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

// Found on a real Docker host, not in a unit test: with publicUrl set, the
// session payload advertised the container's bridge address (172.17.0.5) as
// the pairing base while Pairing.Mint was already encoding the configured
// URL. The two disagreed, and the one the UI showed was unreachable from any
// phone. deploy/ tells operators to set publicUrl precisely to avoid that, so
// the key has to actually reach this field.
func TestDefaultPairBasePrefersPublicBaseURL(t *testing.T) {
	configured := &Server{cfg: Config{Addr: "0.0.0.0:8730", PublicBaseURL: "https://merino.example"}}
	if got := configured.defaultPairBase(); got != "https://merino.example" {
		t.Fatalf("publicUrl must win over the interface guess, got %q", got)
	}

	// Unset keeps the zero-config Mac path: nobody sets publicUrl on a
	// laptop and the LAN guess is right there.
	guessing := &Server{cfg: Config{Addr: "0.0.0.0:8730"}}
	if got := guessing.defaultPairBase(); got != PreferLANBase("0.0.0.0:8730") {
		t.Fatalf("without publicUrl it must fall back to the LAN guess, got %q", got)
	}
}
