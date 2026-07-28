package web

import (
	"fmt"
	"net"
	"strconv"
	"strings"
)

// AccessOrigin is a base URL the phone can open for pairing/login.
type AccessOrigin struct {
	// Kind is "local", "lan", or "public".
	Kind string `json:"kind"`
	// Label is short UI copy, e.g. "This Mac" / "Wi‑Fi".
	Label string `json:"label"`
	// URL is the origin only (scheme://host:port), no path.
	URL string `json:"url"`
	// Hint is one-line guidance under the chip.
	Hint string `json:"hint"`
}

// LocalAccessOrigins returns localhost + primary LAN IPv4 origins for the
// dashboard listen port. Used so first-run pairing works before Cloudflare.
func LocalAccessOrigins(listenAddr string) []AccessOrigin {
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

	out := []AccessOrigin{
		{
			Kind:  "local",
			Label: "This Mac",
			URL:   fmt.Sprintf("http://127.0.0.1:%s", port),
			Hint:  "Browser on this computer only",
		},
	}

	// Prefer a private IPv4 that is up and not loopback/link-local.
	seen := map[string]bool{"127.0.0.1": true}
	ifaces, _ := net.Interfaces()
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		// Skip awdl/llw/utun noise when possible.
		name := strings.ToLower(iface.Name)
		if strings.HasPrefix(name, "awdl") || strings.HasPrefix(name, "llw") ||
			strings.HasPrefix(name, "utun") || strings.HasPrefix(name, "bridge") {
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
			if !ip.IsPrivate() {
				continue
			}
			s := ip.String()
			if seen[s] {
				continue
			}
			seen[s] = true
			out = append(out, AccessOrigin{
				Kind:  "lan",
				Label: "Wi‑Fi / LAN",
				URL:   fmt.Sprintf("http://%s:%s", s, port),
				Hint:  "Phone on the same network — no Cloudflare needed",
			})
			// One primary LAN IP is enough for the chips; more confuses.
			return out
		}
	}
	return out
}

// PreferLANBase picks the best default QR base: first LAN origin, else local.
func PreferLANBase(listenAddr string) string {
	origins := LocalAccessOrigins(listenAddr)
	for _, o := range origins {
		if o.Kind == "lan" {
			return o.URL
		}
	}
	if len(origins) > 0 {
		return origins[0].URL
	}
	return "http://127.0.0.1:8730"
}
