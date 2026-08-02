#!/usr/bin/env bash
#
# Install merinod, the headless Merino daemon.
#
#   curl -fsSL https://raw.githubusercontent.com/LoneExile/merino/main/scripts/install-merinod.sh | bash
#
# Env:
#   MERINOD_BIN_DIR   install location (default: /usr/local/bin, or ~/.local/bin
#                     when that is not writable)
#   MERINOD_VERSION   a tag such as v0.3.0 (default: latest release)
set -euo pipefail

REPO="LoneExile/merino"
BIN_DIR="${MERINOD_BIN_DIR:-}"
VERSION="${MERINOD_VERSION:-}"

die() { printf 'merinod: %s\n' "$*" >&2; exit 1; }
need() { command -v "$1" >/dev/null 2>&1 || die "$1 is required"; }

need curl

# Linux only. A darwin build of merinod pulls in the Wails-backed file in
# internal/app and needs cgo, so there is no static binary to publish — and
# macOS already has the menu bar app, which serves the same dashboard.
case "$(uname -s)" in
  Linux) GOOS=linux ;;
  Darwin)
    die "there is no macOS build of merinod.

On a Mac, install the menu bar app — it serves the same dashboard:
  brew tap LoneExile/merino && brew install --cask merino

To run the daemon on a Mac anyway: go build ./cmd/merinod" ;;
  *) die "unsupported OS: $(uname -s). Build from source: go build ./cmd/merinod" ;;
esac

case "$(uname -m)" in
  x86_64|amd64) GOARCH=amd64 ;;
  aarch64|arm64) GOARCH=arm64 ;;
  *) die "unsupported architecture: $(uname -m). Build from source: go build ./cmd/merinod" ;;
esac

ASSET="merinod-${GOOS}-${GOARCH}"
if [ -n "$VERSION" ]; then
  BASE="https://github.com/${REPO}/releases/download/${VERSION}"
  ASSET="merinod-${VERSION}-${GOOS}-${GOARCH}"
else
  BASE="https://github.com/${REPO}/releases/latest/download"
fi

# Pick a writable destination rather than assuming sudo. A daemon is often
# installed by a user who has no root on the box.
if [ -z "$BIN_DIR" ]; then
  if [ -w /usr/local/bin ]; then
    BIN_DIR=/usr/local/bin
  else
    BIN_DIR="$HOME/.local/bin"
  fi
fi
mkdir -p "$BIN_DIR" || die "cannot create $BIN_DIR"
[ -w "$BIN_DIR" ] || die "$BIN_DIR is not writable. Set MERINOD_BIN_DIR, or re-run with sudo."

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

printf 'Downloading %s…\n' "$ASSET"
curl -fsSL --progress-bar -o "$TMP/merinod" "${BASE}/${ASSET}" \
  || die "download failed: ${BASE}/${ASSET}"

# Verify against the release's own SHA256SUMS. The app installer skips this
# because Gatekeeper checks the bundle; a bare server binary has nothing
# checking it, and this one ends up running as a service.
if curl -fsSL -o "$TMP/SHA256SUMS" "${BASE}/SHA256SUMS" 2>/dev/null; then
  want="$(awk -v a="$ASSET" '$2 == a || $2 == "*"a {print $1}' "$TMP/SHA256SUMS" | head -1)"
  if [ -z "$want" ]; then
    die "$ASSET is not listed in SHA256SUMS — refusing to install"
  fi
  if command -v sha256sum >/dev/null 2>&1; then
    got="$(sha256sum "$TMP/merinod" | awk '{print $1}')"
  elif command -v shasum >/dev/null 2>&1; then
    got="$(shasum -a 256 "$TMP/merinod" | awk '{print $1}')"
  else
    die "need sha256sum or shasum to verify the download"
  fi
  [ "$want" = "$got" ] || die "checksum mismatch: expected $want, got $got"
  printf 'Checksum OK.\n'
else
  die "could not fetch SHA256SUMS — refusing to install unverified"
fi

chmod +x "$TMP/merinod"
mv "$TMP/merinod" "$BIN_DIR/merinod"

printf '\nInstalled %s\n' "$BIN_DIR/merinod"
"$BIN_DIR/merinod" version || true

case ":$PATH:" in
  *":$BIN_DIR:"*) ;;
  *) printf '\n%s is not on your PATH.\n' "$BIN_DIR" ;;
esac

cat <<'NEXT'

Next:
  merinod config init     write a commented config.yml
  merinod                 serve on 0.0.0.0:8730

merinod must run as the user that owns herdr's socket. Running it as a
service, in Docker or on Kubernetes: https://github.com/LoneExile/merino/tree/main/deploy
NEXT
