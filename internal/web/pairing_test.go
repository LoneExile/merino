package web

import (
	"strings"
	"testing"
	"time"
)

func TestPairingMintAndConsumeOnce(t *testing.T) {
	p := NewPairing("https://herdr-tunnel.example")
	ticket, err := p.Mint()
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	if !strings.HasPrefix(ticket.URL, "https://herdr-tunnel.example/login?token=") {
		t.Fatalf("url = %q", ticket.URL)
	}
	if !strings.HasPrefix(ticket.QRPNG, "data:image/png;base64,") {
		t.Fatalf("qr missing data url prefix")
	}
	if ticket.Token == "" {
		t.Fatal("empty token")
	}
	if ticket.ExpiresAt <= time.Now().Unix() {
		t.Fatalf("expiresAt %d not in the future", ticket.ExpiresAt)
	}
	if !p.Consume(ticket.Token) {
		t.Fatal("first consume should succeed")
	}
	if p.Consume(ticket.Token) {
		t.Fatal("second consume must fail (single-use)")
	}
}

func TestPairingExpiredTokenRejected(t *testing.T) {
	p := NewPairing("")
	p.ttl = time.Millisecond
	ticket, err := p.Mint()
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(5 * time.Millisecond)
	if p.Consume(ticket.Token) {
		t.Fatal("expired token must not redeem")
	}
}

func TestPairingUnknownTokenRejected(t *testing.T) {
	p := NewPairing("")
	if p.Consume("nope") {
		t.Fatal("unknown token must not redeem")
	}
}
