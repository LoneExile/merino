# herdr-tunnel — local build and run.
#
# Wails' own Taskfile still drives packaging and codegen; this wraps the
# handful of commands you actually type, with the flags that are easy to get
# wrong (which herdr session, whether the web dashboard is exposed, whether
# writes are allowed) spelled out rather than remembered.
#
#   just            list every recipe
#   just dev        hot-reload frontend against the real herd
#   just app        build + launch the menu-bar app
#   export HERDR_TUNNEL_PASS=… && just web     # loopback dashboard
#   export HERDR_TUNNEL_PASS=… && just tunnel  # TLS tunnel (loopback + --behind-proxy)
#   just gate       everything CI runs

set shell := ["bash", "-euo", "pipefail", "-c"]

# The toolchains live under mise; recipes must not depend on the caller's PATH.
export PATH := justfile_directory() + "/node_modules/.bin:" + env_var('HOME') + "/.local/share/mise/installs/go/1.26.4/bin:" + env_var('HOME') + "/.local/share/mise/installs/node/latest/bin:" + env_var('PATH')

app_bundle := justfile_directory() + "/bin/herdr-tunnel.app"
binary := justfile_directory() + "/bin/herdr-tunnel"

# Default herdr session. Empty means the default socket — your real herd.
# Override for a throwaway: just sock=~/.config/herdr/sessions/test/herdr.sock app
sock := ""

_default:
    @just --list --unsorted

# --- build -----------------------------------------------------------------

# Frontend bundle only.
frontend:
    cd frontend && npm run build

# Go binary with the current frontend embedded.
build: frontend
    go build -o {{binary}} .

# Build the macOS .app bundle
package: frontend
    # Required for the menu bar: the bare binary runs, but only a bundle gets
    # the icon, the no-dock behaviour and a stable identity.
    wails3 package

# --- run -------------------------------------------------------------------

# Build and launch the menu-bar app (your real herd)
app: package
    # Rebuilds every time on purpose: a stale bundle that silently predates
    # your changes is the most confusing state this project has.
    -pkill -f 'herdr-tunnel.app/Contents/MacOS' 2>/dev/null || true
    open {{app_bundle}}
    @echo "running — look at the top-right of your menu bar"

# Phone dashboard (loopback by default — safer).
#
# Password comes from env HERDR_TUNNEL_PASS so it never appears on argv:
#   export HERDR_TUNNEL_PASS='…'
#   just web
#   just web 8730 lex          # port user
#   just web-lan               # bind 0.0.0.0 (LAN; HTTP)
#
# just 1.57 treats key=value as a positional STRING, not a named override.
web port="8730" user="lex": build
    @test -n "${HERDR_TUNNEL_PASS:-}" || { echo "set HERDR_TUNNEL_PASS in the environment (not argv)"; exit 1; }
    HERDR_TUNNEL_USER={{user}} \
    {{ if sock == "" { "" } else { "HERDR_SOCK=" + sock } }} \
    {{binary}} --listen 127.0.0.1:{{port}} --allow-writes --allow-session-switch

# Same as web but reachable on the LAN (cleartext HTTP). Prefer a TLS tunnel.
web-lan port="8730" user="lex": build
    @test -n "${HERDR_TUNNEL_PASS:-}" || { echo "set HERDR_TUNNEL_PASS in the environment (not argv)"; exit 1; }
    HERDR_TUNNEL_USER={{user}} \
    {{ if sock == "" { "" } else { "HERDR_SOCK=" + sock } }} \
    {{binary}} --listen 0.0.0.0:{{port}} --allow-writes --allow-session-switch

# Serve behind Cloudflare Tunnel (Secure cookies, trust CF-Connecting-IP).
#
#   export HERDR_TUNNEL_PASS='…'
#   just tunnel
#
# This host's cloudflared runs in Docker and dials the Mac LAN IP
# (10.0.10.122:8730), so we MUST bind 0.0.0.0 — loopback-only yields CF 502
# ("dial tcp 10.0.10.122:8730: connection refused").
#
# Trade-off: --behind-proxy + LAN bind means a LAN peer could spoof
# X-Forwarded-For / CF-Connecting-IP and dilute the login throttle. Acceptable
# on a trusted home LAN; do not expose :8730 past the firewall.
#
# For a pure loopback origin (cloudflared on the host, not Docker), use:
#   just tunnel-loopback
tunnel port="8730" user="lex": build
    @test -n "${HERDR_TUNNEL_PASS:-}" || { echo "set HERDR_TUNNEL_PASS in the environment (not argv)"; exit 1; }
    HERDR_TUNNEL_USER={{user}} \
    {{ if sock == "" { "" } else { "HERDR_SOCK=" + sock } }} \
    {{binary}} --listen 0.0.0.0:{{port}} --behind-proxy --allow-writes --allow-session-switch

# Loopback-only origin for host-native cloudflared (not the Docker setup).
tunnel-loopback port="8730" user="lex": build
    @test -n "${HERDR_TUNNEL_PASS:-}" || { echo "set HERDR_TUNNEL_PASS in the environment (not argv)"; exit 1; }
    HERDR_TUNNEL_USER={{user}} \
    {{ if sock == "" { "" } else { "HERDR_SOCK=" + sock } }} \
    {{binary}} --listen 127.0.0.1:{{port}} --behind-proxy --allow-writes --allow-session-switch

# Vite hot-reload (frontend only; Go changes need `just app`)
dev:
    wails3 dev

# Foreground run with debug logs (incl. per-frame tray animation)
debug: build
    HERDR_TUNNEL_DEBUG=1 {{ if sock == "" { "" } else { "HERDR_SOCK=" + sock } }} {{binary}}

# Stop every running instance.
stop:
    -pkill -f 'herdr-tunnel' 2>/dev/null || true
    @echo stopped

# --- checks ----------------------------------------------------------------

# Format Go sources
fmt:
    gofmt -w . 2>/dev/null || true

# Go tests under -race
test:
    go test ./... -race -count=1

# TypeScript typecheck
typecheck:
    cd frontend && npm run typecheck

# Everything CI runs. Run this before pushing.
gate: fmt typecheck
    @test -z "$(gofmt -l . | grep -v '^build/' || true)" || { echo "gofmt drift:"; gofmt -l . | grep -v '^build/'; exit 1; }
    go vet ./...
    go test ./... -race -count=1
    cd frontend && npm run build
    go build -o {{binary}} .
    @echo "gate ok"

# --- assets ----------------------------------------------------------------

# Regenerate the sheep SVGs from the parametric drawing
icons:
    # Rasterising to PNG needs a headless browser; this emits the SVGs only.
    python3 assets/sheep/gen_sheep.py assets/sheep/svg
    @echo "SVGs regenerated — PNGs are rendered from these, see assets/sheep/README.md"
