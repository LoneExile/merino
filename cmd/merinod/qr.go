package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/LoneExile/merino/internal/serve"
	qrcode "github.com/skip2/go-qrcode"
	"github.com/spf13/cobra"
)

// `merinod qr` asks the RUNNING daemon for a pairing ticket and draws it in
// the terminal.
//
// It is a client, not a minter, and that is forced rather than chosen: a
// ticket lives in the serving process's memory until it is redeemed, so a
// second process cannot create one. A subcommand that wrote a token file
// would report success and pair nothing — which is exactly why the deploy
// guide used to say a CLI was impossible. It is possible as a client.
//
// Consequences worth stating, because each is a support question:
//
//   - the daemon must be running, and reachable on its own listen address
//   - password sign-in must be on, because this signs in like any operator
//     and deliberately introduces no second way in. A local token file
//     would be a parallel trust boundary guarding the same privilege.
//   - the QR encodes publicUrl, so a wrong one yields a QR that scans
//     perfectly and lands nowhere
func qrCmd() *cobra.Command {
	var raw bool
	cmd := &cobra.Command{
		Use:   "qr",
		Short: "Mint a pairing QR from the running daemon and print it here",
		Long: "qr signs in to the running merinod over its listen address, asks it\n" +
			"for a one-shot pairing ticket, and draws the QR in this terminal.\n\n" +
			"The daemon must already be running: pairing tickets live in its\n" +
			"memory, so nothing else can mint one. Password sign-in must be\n" +
			"enabled (access.passwordLogin), because this authenticates the same\n" +
			"way a browser does.",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runQR(cmd, serveFlags, raw)
		},
	}
	cmd.Flags().BoolVar(&raw, "url", false,
		"print only the pairing URL, for piping into another QR renderer or a message")
	return cmd
}

func runQR(cmd *cobra.Command, f *flagSet, urlOnly bool) error {
	boot, err := prepare(cmd, f)
	if err != nil {
		return err
	}
	user, pass, err := serve.Credentials(boot)
	if err != nil {
		return err
	}

	base, err := dialBase(boot.Options.Addr)
	if err != nil {
		return err
	}

	ticket, err := mintTicket(base, user, pass)
	if err != nil {
		return err
	}

	out := cmd.OutOrStdout()
	if urlOnly {
		fmt.Fprintln(out, ticket.URL)
		return nil
	}

	fmt.Fprint(out, renderQR(ticket.URL))
	fmt.Fprintf(out, "\n%s\n", ticket.URL)
	if ticket.ExpiresAt > 0 {
		left := time.Until(time.Unix(ticket.ExpiresAt, 0)).Round(time.Second)
		fmt.Fprintf(out, "one-shot · expires in %s\n", left)
	}
	// The most common failure is a QR that scans and then cannot connect,
	// because publicUrl still points at a container's own bridge address.
	// Naming the host here turns that into something the operator can see
	// before they walk across the room with a phone.
	if u, perr := url.Parse(ticket.URL); perr == nil {
		fmt.Fprintf(out, "the phone must be able to reach %s\n", u.Host)
	}
	return nil
}

// dialBase turns a listen address into something dialable. A daemon listening
// on 0.0.0.0 or [::] is not reachable AT those addresses; loopback is the one
// interface a local CLI can always count on.
func dialBase(addr string) (string, error) {
	if addr == "" {
		return "", errors.New("no listen address resolved; is config.yml readable?")
	}
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return "", fmt.Errorf("listen address %q: %w", addr, err)
	}
	switch host {
	case "", "0.0.0.0", "::", "[::]":
		host = "127.0.0.1"
	}
	return "http://" + net.JoinHostPort(host, port), nil
}

func mintTicket(base, user, pass string) (*pairingTicket, error) {
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, err
	}
	// No redirect following: a successful sign-in answers 303 and the cookie
	// is what matters, not the page it points at.
	client := &http.Client{
		Jar:     jar,
		Timeout: 10 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	form := url.Values{"username": {user}, "password": {pass}}
	resp, err := client.Post(base+"/login", "application/x-www-form-urlencoded",
		strings.NewReader(form.Encode()))
	if err != nil {
		return nil, fmt.Errorf("no daemon answering at %s: %w\n"+
			"qr asks the running daemon to mint; start it first", base, err)
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusForbidden, resp.StatusCode == http.StatusUnauthorized:
		return nil, fmt.Errorf("sign-in refused by %s.\n"+
			"Password sign-in is off by default. Enable it:\n"+
			"  access:\n    passwordLogin: true\n"+
			"and set auth.user / auth.passwordFile, then restart the daemon", base)
	case resp.StatusCode >= 400:
		return nil, fmt.Errorf("sign-in failed: %s", resp.Status)
	}

	mint, err := client.Post(base+"/api/pairing/mint", "application/json", bytes.NewReader([]byte(`{}`)))
	if err != nil {
		return nil, err
	}
	defer mint.Body.Close()
	if mint.StatusCode == http.StatusNotFound {
		return nil, errors.New("this daemon has pairing disabled")
	}
	if mint.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("mint failed: %s", mint.Status)
	}
	var t pairingTicket
	if err := json.NewDecoder(mint.Body).Decode(&t); err != nil {
		return nil, fmt.Errorf("mint response: %w", err)
	}
	if t.URL == "" {
		return nil, errors.New("mint returned no url")
	}
	return &t, nil
}

type pairingTicket struct {
	URL       string `json:"url"`
	Token     string `json:"token"`
	ExpiresAt int64  `json:"expiresAt"`
}

// renderQR draws the code with half-block characters, two modules per cell,
// so a version-3 code fits an 80-column terminal.
//
// Colour is set explicitly rather than inherited. A QR is only scannable if
// dark modules are actually darker than light ones, and a terminal's own
// palette decides the opposite half the time: the same glyphs that scan on a
// light theme are inverted on a dark one. Forcing black-on-white per line
// makes the output independent of the theme it lands in.
//
// NO_COLOR is honoured because it is a standard, and because a pipe into a
// file or a log has no use for escape sequences. The result then depends on
// the reader's background, which is stated in the accompanying text rather
// than silently hoped for.
func renderQR(content string) string {
	// Medium recovery: the code stays small enough for a terminal while
	// tolerating the smudge a photographed screen introduces.
	code, err := qrcode.New(content, qrcode.Medium)
	if err != nil {
		return ""
	}
	// inverseColor=true puts the block glyph on DARK modules, which is the
	// orientation the colours below assume.
	art := code.ToSmallString(true)
	if os.Getenv("NO_COLOR") != "" {
		return art
	}
	var b strings.Builder
	for _, line := range strings.Split(strings.TrimRight(art, "\n"), "\n") {
		// 30 = black ink, 47 = white paper, reset at end of every line so a
		// resized or wrapped terminal cannot bleed the background onward.
		b.WriteString("\x1b[30;47m")
		b.WriteString(line)
		b.WriteString("\x1b[0m\n")
	}
	return b.String()
}
