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
#   just web PASS   phone dashboard on the LAN
#   just tunnel PASS  behind a TLS tunnel
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

# Phone dashboard on the LAN.
#
# just 1.57 treats `key=value` as a positional STRING, not a named override
# (`just tunnel pass=secret` sets user="pass=secret" and leaves pass empty).
# Password is therefore the first argument:
#
#   just web herd-test-2026
#   just web herd-test-2026 lex 8730
web pass user="lex" port="8730": build
    # Foreground, so you get logs. The server itself requires the credentials;
    # it refuses to expose agents without a login.
    @test -n "{{pass}}" || { echo "usage: just web <password>  [user] [port]"; exit 1; }
    HERDR_TUNNEL_USER={{user}} HERDR_TUNNEL_PASS={{pass}} \
    {{ if sock == "" { "" } else { "HERDR_SOCK=" + sock } }} \
    {{binary}} --listen 0.0.0.0:{{port}} --allow-writes --allow-session-switch

# Serve behind a TLS tunnel (Secure cookies, trust CF-Connecting-IP).
#
#   just tunnel herd-test-2026
#
# Never use --behind-proxy while the port is ALSO reachable directly on the
# LAN: the login throttle then keys on a header the caller controls.
tunnel pass user="lex" port="8730": build
    @test -n "{{pass}}" || { echo "usage: just tunnel <password>  [user] [port]"; exit 1; }
    HERDR_TUNNEL_USER={{user}} HERDR_TUNNEL_PASS={{pass}} \
    {{ if sock == "" { "" } else { "HERDR_SOCK=" + sock } }} \
    {{binary}} --listen 0.0.0.0:{{port}} --behind-proxy --allow-writes

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
