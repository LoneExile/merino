package web

import (
	"fmt"
	"net"
	"strconv"
	"strings"
)

// AccessOrigin is a base URL the phone can open for pairing/login.
type AccessOrigin struct {
	// Kind is "local", "lan", "tailscale", or "public".
	Kind string `json:"kind"`
	// Label is short UI copy, e.g. "This Mac" / "Wi‑Fi".
	Label string `json:"label"`
	// URL is the origin only (scheme://host:port), no path.
	URL string `json:"url"`
	// Hint is one-line guidance under the chip.
	Hint string `json:"hint"`
}

func listenPort(listenAddr string) string {
	port := "8730"
	if h, p, err := net.SplitHostPort(listenAddr); err == nil {
		if p != "" {
			port = p
		}
		_ = h
	} else if strings.HasPrefix(listenAddr, ":") {
		port = strings.TrimPrefix(listenAddr, ":")
	}
	if _, err := strconv.Atoi(port); err != nil {
		port = "8730"
	}
	return port
}

func isTailscaleIP(ip net.IP) bool {
	// Tailscale userspace / CGNAT range 100.64.0.0/10
	if ip4 := ip.To4(); ip4 != nil {
		return ip4[0] == 100 && ip4[1] >= 64 && ip4[1] <= 127
	}
	return false
}

// LocalAccessOrigins returns localhost + primary LAN IPv4 + Tailscale origins
// for the dashboard listen port. Used so first-run pairing works before Cloudflare.
func LocalAccessOrigins(listenAddr string) []AccessOrigin {
	port := listenPort(listenAddr)

	out := []AccessOrigin{
		{
			Kind:  "local",
			Label: "This Mac",
			URL:   fmt.Sprintf("http://127.0.0.1:%s", port),
			Hint:  "Browser on this computer only",
		},
	}

	seen := map[string]bool{"127.0.0.1": true}
	var lan *AccessOrigin
	var ts *AccessOrigin

	ifaces, _ := net.Interfaces()
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		name := strings.ToLower(iface.Name)
		// Skip Apple AWDL / bridge noise. Keep utun — Tailscale lives there on macOS.
		if strings.HasPrefix(name, "awdl") || strings.HasPrefix(name, "llw") ||
			strings.HasPrefix(name, "bridge") {
			continue
		}
		addrs, _ := iface.Addrs()
		for _, a := range addrs {
			ipNet, ok := a.(*net.IPNet)
			if !ok || ipNet.IP == nil {
				continue
			}
			ip := ipNet.IP.To4()
			if ip == nil || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
				continue
			}
			s := ip.String()
			if seen[s] {
				continue
			}
			seen[s] = true

			if isTailscaleIP(ip) {
				if ts == nil {
					o := AccessOrigin{
						Kind:  "tailscale",
						Label: "Tailscale",
						URL:   fmt.Sprintf("http://%s:%s", s, port),
						Hint:  "Phone on the same tailnet — still plain HTTP (no in-page camera)",
					}
					ts = &o
				}
				continue
			}
			if !ip.IsPrivate() {
				continue
			}
			// Skip other 100.x that is not CGNAT tailscale if any
			if lan == nil {
				o := AccessOrigin{
					Kind:  "lan",
					Label: "Wi‑Fi / LAN",
					URL:   fmt.Sprintf("http://%s:%s", s, port),
					Hint:  "Phone on the same network — no Cloudflare needed",
				}
				lan = &o
			}
		}
	}
	if lan != nil {
		out = append(out, *lan)
	}
	if ts != nil {
		out = append(out, *ts)
	}
	return out
}

// PreferLANBase picks the best default QR base: first LAN origin, else Tailscale, else local.
func PreferLANBase(listenAddr string) string {
	origins := LocalAccessOrigins(listenAddr)
	for _, o := range origins {
		if o.Kind == "lan" {
			return o.URL
		}
	}
	for _, o := range origins {
		if o.Kind == "tailscale" {
			return o.URL
		}
	}
	if len(origins) > 0 {
		return origins[0].URL
	}
	return "http://127.0.0.1:8730"
}

// defaultPairBase is the origin a pairing QR should encode.
//
// PublicBaseURL wins whenever it is set, because autodetection answers "which
// of MY interfaces looks most like a LAN address" — and inside a container
// that is the container's own address (172.17.x.x on a default bridge), a URL
// no phone can open. Found on a real Docker host: with publicUrl configured,
// the session payload still advertised the bridge address here while
// Pairing.Mint was already encoding the configured one, so the UI and the
// minted ticket disagreed.
//
// Falling back to the LAN guess keeps the zero-config Mac path unchanged:
// nobody sets publicUrl there, and the guess is right on a laptop.
func (s *Server) defaultPairBase() string {
	if s.cfg.PublicBaseURL != "" {
		return s.cfg.PublicBaseURL
	}
	return PreferLANBase(s.cfg.Addr)
}
