#!/usr/bin/env bash
# Install Merino (menu bar + phone dashboard for herdr) on macOS arm64.
# Downloads the latest GitHub Release zip, installs to /Applications, and
# clears the quarantine bit so Gatekeeper does not block first launch.
# Builds are ad-hoc signed until Apple Developer ID + notarization lands.
set -euo pipefail

REPO="${MERINO_REPO:-LoneExile/merino}"
APP_DIR="${MERINO_APP_DIR:-/Applications}"
APP_NAME="Merino.app"
# Bundle inside the zip is merino.app (APFS case-insensitive packaging).
ZIP_APP_NAME="merino.app"

if [[ "$(uname -s)" != "Darwin" ]]; then
  echo "error: Merino installer supports macOS only" >&2
  exit 1
fi

ARCH="$(uname -m)"
case "$ARCH" in
  arm64) ARCH_LABEL=arm64 ;;
  x86_64)
    echo "error: release builds are arm64-only right now (Apple Silicon)." >&2
    echo "       Build from source: https://github.com/${REPO}#build-from-source" >&2
    exit 1
    ;;
  *)
    echo "error: unsupported arch: $ARCH" >&2
    exit 1
    ;;
esac

need() { command -v "$1" >/dev/null 2>&1 || { echo "error: need $1" >&2; exit 1; }; }
need curl
need ditto
need xattr

TMP="$(mktemp -d "${TMPDIR:-/tmp}/merino-install.XXXXXX")"
cleanup() { rm -rf "$TMP"; }
trap cleanup EXIT

echo "→ Resolving latest Merino release…"
API="https://api.github.com/repos/${REPO}/releases/latest"
JSON="$(curl -fsSL -H 'Accept: application/vnd.github+json' "$API")"
TAG="$(printf '%s' "$JSON" | sed -n 's/.*"tag_name":[[:space:]]*"\([^"]*\)".*/\1/p' | head -1)"
if [[ -z "$TAG" ]]; then
  echo "error: could not parse latest tag from GitHub API" >&2
  exit 1
fi
VERSION="${TAG#v}"
ASSET="Merino-${VERSION}-macos-${ARCH_LABEL}.zip"
URL="https://github.com/${REPO}/releases/download/${TAG}/${ASSET}"

echo "→ Downloading ${ASSET} (${TAG})…"
curl -fsSL --progress-bar -o "$TMP/$ASSET" "$URL"

echo "→ Unpacking…"
ditto -x -k "$TMP/$ASSET" "$TMP/out"
if [[ ! -d "$TMP/out/$ZIP_APP_NAME" ]]; then
  FOUND="$(find "$TMP/out" -maxdepth 2 -name '*.app' -type d | head -1 || true)"
  if [[ -z "$FOUND" ]]; then
    echo "error: no .app found in archive" >&2
    ls -laR "$TMP/out" >&2 || true
    exit 1
  fi
  ZIP_APP_NAME="$(basename "$FOUND")"
  SRC="$FOUND"
else
  SRC="$TMP/out/$ZIP_APP_NAME"
fi

DEST="${APP_DIR}/${APP_NAME}"
echo "→ Installing to ${DEST}…"
if [[ -e "$DEST" ]]; then
  rm -rf "$DEST"
fi
# Copy then land as Merino.app even when the zip has merino.app.
ditto "$SRC" "$DEST"

echo "→ Clearing quarantine (unsigned / ad-hoc build)…"
xattr -dr com.apple.quarantine "$DEST" 2>/dev/null || true
xattr -cr "$DEST" 2>/dev/null || true

echo "→ Done. Launching Merino…"
open "$DEST" || true
echo
echo "  App:     $DEST"
echo "  Version: $TAG"
echo "  Look for the sheep in the menu bar."
echo
echo "  If macOS still blocks it: System Settings → Privacy & Security → Open Anyway"
echo "  Or: right-click Merino → Open (once)."
