# merino — local build and run.
#
# Wails' own Taskfile still drives packaging and codegen; this wraps the
# handful of commands you actually type, with the flags that are easy to get
# wrong (which herdr session, whether the web dashboard is exposed, whether
# writes are allowed) spelled out rather than remembered.
#
#   just            list every recipe
#   just dev        hot-reload frontend against the real herd
#   just app        build + launch the menu-bar app
#   export MERINO_PASS=… && just web     # loopback dashboard
#   export MERINO_PASS=… && just tunnel  # TLS tunnel (loopback + --behind-proxy)
#   just gate       everything CI runs

set shell := ["bash", "-euo", "pipefail", "-c"]

# The toolchains live under mise; recipes must not depend on the caller's PATH.
export PATH := justfile_directory() + "/node_modules/.bin:" + env_var('HOME') + "/.local/share/mise/installs/go/1.26.4/bin:" + env_var('HOME') + "/.local/share/mise/installs/node/latest/bin:" + env_var('PATH')

app_bundle := justfile_directory() + "/bin/merino.app"
binary := justfile_directory() + "/bin/merino"

# Default herdr session. Empty means the default socket — your real herd.
# Override for a throwaway: just sock=~/.config/herdr/sessions/test/herdr.sock app
sock := ""

_default:
    @just --list --unsorted

# --- build -----------------------------------------------------------------

# The committed Wails bindings (frontend/bindings) are the source of truth
# for tsc — CI has no Go toolchain to regenerate them, which is exactly why
# they are committed. `wails3 dev` (and a bare `wails3 generate bindings`
# without -ts) regenerates them as .js and DELETES the committed .ts, which
# breaks every tsc build with TS2307 until they are restored. Restore first
# so app/build/typecheck can never hit that state.
ensure-bindings:
    git restore --source=HEAD -- frontend/bindings 2>/dev/null || true

# Frontend bundle only.
frontend: ensure-bindings
    cd frontend && npm run build

# Go binary with the current frontend embedded.
build: frontend
    go build -o {{binary}} ./cmd/merino

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
    #
    # Do NOT use `open` here. After the herdr-tunnel → merino bundle-id rename,
    # Launch Services often returns -600 even when the binary is healthy.
    # Direct exec is reliable and still gets the menu-bar icon (ActivationPolicyAccessory).
    -pkill -f 'merino.app/Contents/MacOS' 2>/dev/null || true
    -pkill -f 'herdr-tunnel.app/Contents/MacOS' 2>/dev/null || true
    -pkill -f '/bin/merino' 2>/dev/null || true
    nohup "{{app_bundle}}/Contents/MacOS/merino" >/tmp/merino-app.log 2>&1 &
    sleep 0.4
    @if pgrep -f 'merino.app/Contents/MacOS/merino' >/dev/null 2>&1 || pgrep -x merino >/dev/null 2>&1; then \
      echo "running — look at the top-right of your menu bar"; \
    else \
      echo "failed to start Merino — see /tmp/merino-app.log"; \
      tail -n 40 /tmp/merino-app.log 2>/dev/null || true; \
      exit 1; \
    fi

# Phone dashboard (loopback by default — safer).
#
# Password comes from env MERINO_PASS so it never appears on argv:
#   export MERINO_PASS='…'
#   just web
#   just web 8730 lex          # port user
#   just web-lan               # bind 0.0.0.0 (LAN; HTTP)
#
# just 1.57 treats key=value as a positional STRING, not a named override.
web port="8730" user="lex": build
    @test -n "${MERINO_PASS:-}" || { echo "set MERINO_PASS in the environment (not argv)"; exit 1; }
    MERINO_USER={{user}} \
    {{ if sock == "" { "" } else { "HERDR_SOCK=" + sock } }} \
    {{binary}} --listen 127.0.0.1:{{port}} --allow-writes --allow-session-switch

# Same as web but reachable on the LAN (cleartext HTTP). Prefer a TLS tunnel.
web-lan port="8730" user="lex": build
    @test -n "${MERINO_PASS:-}" || { echo "set MERINO_PASS in the environment (not argv)"; exit 1; }
    MERINO_USER={{user}} \
    {{ if sock == "" { "" } else { "HERDR_SOCK=" + sock } }} \
    {{binary}} --listen 0.0.0.0:{{port}} --allow-writes --allow-session-switch

# Serve behind Cloudflare Tunnel (Secure cookies, trust CF-Connecting-IP).
#
#   export MERINO_PASS='…'
#   just tunnel
#
# This host's cloudflared runs in Docker and dials the Mac LAN IP
# (<home-lan-ip>:8730), so we MUST bind 0.0.0.0 — loopback-only yields CF 502
# ("dial tcp <home-lan-ip>:8730: connection refused").
#
# Trade-off: --behind-proxy + LAN bind means a LAN peer could spoof
# X-Forwarded-For / CF-Connecting-IP and dilute the login throttle. Acceptable
# on a trusted home LAN; do not expose :8730 past the firewall.
#
# For a pure loopback origin (cloudflared on the host, not Docker), use:
#   just tunnel-loopback
tunnel port="8730" user="lex": build
    @test -n "${MERINO_PASS:-}" || { echo "set MERINO_PASS in the environment (not argv)"; exit 1; }
    MERINO_USER={{user}} \
    {{ if sock == "" { "" } else { "HERDR_SOCK=" + sock } }} \
    {{binary}} --listen 0.0.0.0:{{port}} --behind-proxy --allow-writes --allow-session-switch

# Loopback-only origin for host-native cloudflared (not the Docker setup).
tunnel-loopback port="8730" user="lex": build
    @test -n "${MERINO_PASS:-}" || { echo "set MERINO_PASS in the environment (not argv)"; exit 1; }
    MERINO_USER={{user}} \
    {{ if sock == "" { "" } else { "HERDR_SOCK=" + sock } }} \
    {{binary}} --listen 127.0.0.1:{{port}} --behind-proxy --allow-writes --allow-session-switch

# Vite hot-reload (frontend only; Go changes need `just app`)
#
# wails3 dev regenerates frontend/bindings as .js and deletes the committed
# .ts (see ensure-bindings). The dev server tolerates it (vite resolves .js),
# but the tree must not be left broken for the next app/build — restore the
# committed bindings when dev exits, interrupted or not.
dev:
    wails3 dev || true
    git restore --source=HEAD -- frontend/bindings 2>/dev/null || true

# Foreground run with debug logs (incl. per-frame tray animation)
debug: build
    MERINO_DEBUG=1 {{ if sock == "" { "" } else { "HERDR_SOCK=" + sock } }} {{binary}}

# Stop every running instance.
stop:
    -pkill -f 'merino' 2>/dev/null || true
    @echo stopped

# --- checks ----------------------------------------------------------------

# Format Go sources
fmt:
    gofmt -w . 2>/dev/null || true

# Go tests under -race
test:
    go test ./... -race -count=1

# TypeScript typecheck
typecheck: ensure-bindings
    cd frontend && npm run typecheck

# Frontend logic checks. This repo has no JS test runner (vite + tsc only), so
# the __checks__ files run as plain scripts under Node's type stripping.
checks:
    #!/usr/bin/env bash
    set -euo pipefail
    for f in frontend/src/__checks__/*.check.ts; do
      echo "--- $f"
      node --experimental-strip-types "$f"
    done

# Everything CI runs. Run this before pushing.
gate: fmt typecheck checks
    @test -z "$(gofmt -l . | grep -v '^build/' || true)" || { echo "gofmt drift:"; gofmt -l . | grep -v '^build/'; exit 1; }
    go vet ./...
    go test ./... -race -count=1
    cd frontend && npm run build
    go build -o {{binary}} ./cmd/merino
    @echo "gate ok"

# --- release ---------------------------------------------------------------

# Version git-cliff is pinned to. Floating @latest would let a template change
# rewrite the whole changelog on an unrelated day.
cliff := "git-cliff@2.13.1"

# Regenerate CHANGELOG.md for every tag, plus the release being prepared.
#
#   just changelog v0.2.1
#
# git-cliff rebuilds the whole file from commit messages, so any hand-written
# upgrade note is silently dropped. That prose exists nowhere else — it is not
# in the history to recover from — so this counts the notes before and after
# and says so loudly rather than leaving it to whoever reads the diff. It has
# already caught one real loss.
changelog tag:
    #!/usr/bin/env bash
    set -euo pipefail
    marker='^> \*\*Upgrade note'
    before=$(grep -c "$marker" CHANGELOG.md 2>/dev/null || true)
    npx --yes {{cliff}} --tag {{tag}} -o CHANGELOG.md
    after=$(grep -c "$marker" CHANGELOG.md 2>/dev/null || true)
    if [ "${after:-0}" -lt "${before:-0}" ]; then
      echo >&2
      echo "WARNING: regeneration dropped $(( before - after )) upgrade note(s)." >&2
      echo "They are not recoverable from commit messages. Restore them with:" >&2
      echo "    git diff CHANGELOG.md" >&2
      echo >&2
    fi
    echo "review the diff, then commit CHANGELOG.md before tagging"

# What the NEXT release would say, without writing anything.
changelog-preview:
    @npx --yes {{cliff}} --unreleased

# The exact text the release workflow will post for a tag.
release-notes tag:
    @scripts/release-notes.sh {{tag}}

# --- assets ----------------------------------------------------------------

# Regenerate the sheep SVGs from the parametric drawing
icons:
    # Rasterising to PNG needs a headless browser; this emits the SVGs only.
    python3 assets/sheep/gen_sheep.py assets/sheep/svg
    @echo "SVGs regenerated — PNGs are rendered from these, see assets/sheep/README.md"
