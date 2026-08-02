package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	qrcode "github.com/skip2/go-qrcode"
)

func TestDialBase(t *testing.T) {
	// A daemon listening on a wildcard is not reachable AT the wildcard.
	// Getting this wrong produces "connect: can't assign requested address"
	// from a CLI running on the same host as the daemon it cannot reach.
	for _, tc := range []struct {
		addr, want string
		wantErr    bool
	}{
		{addr: "0.0.0.0:8730", want: "http://127.0.0.1:8730"},
		{addr: "::]:8730", wantErr: true},
		{addr: ":8730", want: "http://127.0.0.1:8730"},
		{addr: "127.0.0.1:9000", want: "http://127.0.0.1:9000"},
		// A specific interface is left alone: the operator said where.
		{addr: "10.0.0.5:8730", want: "http://10.0.0.5:8730"},
		{addr: "", wantErr: true},
		{addr: "no-port", wantErr: true},
	} {
		got, err := dialBase(tc.addr)
		if tc.wantErr {
			if err == nil {
				t.Errorf("dialBase(%q) = %q, want an error", tc.addr, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("dialBase(%q): %v", tc.addr, err)
			continue
		}
		if got != tc.want {
			t.Errorf("dialBase(%q) = %q, want %q", tc.addr, got, tc.want)
		}
	}
}

// IPv6 wildcard arrives from net.Listener as "[::]:8730" and SplitHostPort
// hands back "::" — kept separate because the bracket form is easy to assume
// and wrong.
func TestDialBaseIPv6Wildcard(t *testing.T) {
	got, err := dialBase("[::]:8730")
	if err != nil {
		t.Fatal(err)
	}
	if got != "http://127.0.0.1:8730" {
		t.Fatalf("got %q, want loopback", got)
	}
}

// The whole point of the command is a QR someone can photograph. Comparing
// the drawn characters back to the library's own module matrix proves the art
// IS the code for that URL, rather than merely that some blocks were printed.
func TestRenderQRDrawsTheCodeForTheURL(t *testing.T) {
	t.Setenv("NO_COLOR", "1") // compare glyphs, not escape sequences
	const link = "http://10.0.0.5:8730/login?token=abcdef0123456789"

	code, err := qrcode.New(link, qrcode.Medium)
	if err != nil {
		t.Fatal(err)
	}
	want := code.Bitmap()

	got := decodeHalfBlocks(t, renderQR(link))

	if len(got) < len(want) {
		t.Fatalf("art has %d module rows, code has %d", len(got), len(want))
	}
	for y := range want {
		for x := range want[y] {
			if got[y][x] != want[y][x] {
				t.Fatalf("module (%d,%d): art has %v, code has %v", x, y, got[y][x], want[y][x])
			}
		}
	}

	// A quiet zone is part of the spec, not decoration: without it a scanner
	// cannot find the symbol against surrounding terminal text.
	for x := range want[0] {
		if want[0][x] {
			t.Fatalf("no quiet zone: module (%d,0) is dark", x)
		}
	}
}

// decodeHalfBlocks reverses ToSmallString(true): each glyph carries two
// vertically stacked modules.
func decodeHalfBlocks(t *testing.T, art string) [][]bool {
	t.Helper()
	var rows [][]bool
	for _, line := range strings.Split(strings.TrimRight(art, "\n"), "\n") {
		var top, bottom []bool
		for _, r := range line {
			switch r {
			case ' ':
				top, bottom = append(top, false), append(bottom, false)
			case '█':
				top, bottom = append(top, true), append(bottom, true)
			case '▀':
				top, bottom = append(top, true), append(bottom, false)
			case '▄':
				top, bottom = append(top, false), append(bottom, true)
			default:
				t.Fatalf("unexpected glyph %q in QR art", r)
			}
		}
		rows = append(rows, top, bottom)
	}
	return rows
}

// Dark modules must be dark. A terminal's palette decides that, not us: the
// same glyphs scan on a light theme and invert on a dark one, so the colours
// are forced per line.
func TestRenderQRForcesPolarity(t *testing.T) {
	t.Setenv("NO_COLOR", "")
	art := renderQR("http://example.test/login?token=x")
	for i, line := range strings.Split(strings.TrimRight(art, "\n"), "\n") {
		if !strings.HasPrefix(line, "\x1b[30;47m") {
			t.Fatalf("line %d does not force black-on-white: %q", i, line)
		}
		if !strings.HasSuffix(line, "\x1b[0m") {
			t.Fatalf("line %d does not reset, so the background bleeds on: %q", i, line)
		}
	}
}

func TestRenderQRHonoursNoColor(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	if strings.Contains(renderQR("http://example.test/x"), "\x1b[") {
		t.Fatal("NO_COLOR set but escape sequences were emitted")
	}
}

// The two refusals an operator will actually hit, and the message each gets.
// A bare status code here means someone reads "403" and starts debugging a
// password they typed correctly.
func TestMintTicketExplainsRefusals(t *testing.T) {
	t.Run("password sign-in disabled", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusForbidden)
		}))
		defer srv.Close()

		_, err := mintTicket(srv.URL, "operator", "pw")
		if err == nil {
			t.Fatal("want an error")
		}
		if !strings.Contains(err.Error(), "passwordLogin") {
			t.Fatalf("error does not name the fix: %v", err)
		}
	})

	t.Run("pairing disabled", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/login" {
				w.WriteHeader(http.StatusSeeOther)
				return
			}
			w.WriteHeader(http.StatusNotFound)
		}))
		defer srv.Close()

		_, err := mintTicket(srv.URL, "operator", "pw")
		if err == nil || !strings.Contains(err.Error(), "pairing disabled") {
			t.Fatalf("got %v, want a pairing-disabled error", err)
		}
	})

	t.Run("success", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/login" {
				http.SetCookie(w, &http.Cookie{Name: "herdr_session", Value: "x"})
				w.WriteHeader(http.StatusSeeOther)
				return
			}
			fmt.Fprint(w, `{"url":"http://h/login?token=t","token":"t","expiresAt":99}`)
		}))
		defer srv.Close()

		got, err := mintTicket(srv.URL, "operator", "pw")
		if err != nil {
			t.Fatal(err)
		}
		if got.URL != "http://h/login?token=t" {
			t.Fatalf("url = %q", got.URL)
		}
	})
}
