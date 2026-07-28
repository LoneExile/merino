#!/usr/bin/env bash
# Uninstall Merino (app bundle + local state).
# Does not touch herdr itself or other apps.
set -euo pipefail

DRY=0
KEEP_STATE=0
for arg in "$@"; do
  case "$arg" in
    --dry-run) DRY=1 ;;
    --keep-state) KEEP_STATE=1 ;;
    -h|--help)
      cat <<'EOF'
Usage: uninstall.sh [--dry-run] [--keep-state]

  Quit Merino, remove app bundles, and (unless --keep-state) delete
  logs, caches, paired-device state, and preferences.

  --dry-run      print paths only
  --keep-state   leave ~/Library/Logs/merino and related data
EOF
      exit 0
      ;;
    *)
      echo "unknown flag: $arg (try --help)" >&2
      exit 2
      ;;
  esac
done

run() {
  if [[ "$DRY" -eq 1 ]]; then
    printf '  would: %s\n' "$*"
  else
    "$@"
  fi
}

echo "→ Quitting Merino…"
if [[ "$DRY" -eq 0 ]]; then
  osascript -e 'quit app "Merino"' 2>/dev/null || true
  osascript -e 'quit app "merino"' 2>/dev/null || true
  # Legacy bundle name from pre-rename builds.
  osascript -e 'quit app "herdr-tunnel"' 2>/dev/null || true
  pkill -f 'Merino\.app/Contents/MacOS' 2>/dev/null || true
  pkill -f 'merino\.app/Contents/MacOS' 2>/dev/null || true
  pkill -f 'herdr-tunnel\.app/Contents/MacOS' 2>/dev/null || true
  sleep 0.3
else
  echo "  would: quit Merino processes"
fi

echo "→ Removing app bundles…"
APP_CANDIDATES=(
  "/Applications/Merino.app"
  "/Applications/merino.app"
  "/Applications/herdr-tunnel.app"
  "${HOME}/Applications/Merino.app"
  "${HOME}/Applications/merino.app"
  "${HOME}/Applications/herdr-tunnel.app"
)
# Dev / source builds often leave a bundle next to the checkout.
if [[ -n "${MERINO_APP_DIR:-}" ]]; then
  APP_CANDIDATES+=("${MERINO_APP_DIR}/Merino.app" "${MERINO_APP_DIR}/merino.app")
fi

REMOVED_APP=0
for p in "${APP_CANDIDATES[@]}"; do
  if [[ -e "$p" ]]; then
    echo "  $p"
    run rm -rf "$p"
    REMOVED_APP=1
  fi
done

# Homebrew cask (best-effort; ignore if not installed).
if command -v brew >/dev/null 2>&1; then
  if brew list --cask merino >/dev/null 2>&1; then
    echo "→ brew uninstall --cask merino"
    if [[ "$DRY" -eq 1 ]]; then
      echo "  would: brew uninstall --cask merino"
    else
      brew uninstall --cask merino 2>/dev/null || true
    fi
    REMOVED_APP=1
  fi
fi

if [[ "$REMOVED_APP" -eq 0 ]]; then
  echo "  (no known app bundle found — already gone or custom path)"
fi

if [[ "$KEEP_STATE" -eq 0 ]]; then
  echo "→ Removing local state…"
  STATE_PATHS=(
    "${HOME}/Library/Logs/merino"
    "${HOME}/Library/Caches/merino"
    # Legacy name before rebrand.
    "${HOME}/Library/Logs/herdr-tunnel"
    "${HOME}/Library/Caches/herdr-tunnel"
    "${HOME}/Library/Preferences/dev.apinant.merino.plist"
    "${HOME}/Library/Preferences/dev.apinant.herdr-tunnel.plist"
    "${HOME}/Library/Preferences/herdr-tunnel.plist"
    "${HOME}/Library/Saved Application State/dev.apinant.merino.savedState"
    "${HOME}/Library/Saved Application State/dev.apinant.herdr-tunnel.savedState"
  )
  for p in "${STATE_PATHS[@]}"; do
    if [[ -e "$p" ]]; then
      echo "  $p"
      run rm -rf "$p"
    fi
  done
else
  echo "→ Keeping state (--keep-state)"
fi

# Drop Launch Services / Spotlight residue for the bundle id (best-effort).
if [[ "$DRY" -eq 0 ]]; then
  /System/Library/Frameworks/CoreServices.framework/Frameworks/LaunchServices.framework/Support/lsregister \
    -u /Applications/Merino.app 2>/dev/null || true
  /System/Library/Frameworks/CoreServices.framework/Frameworks/LaunchServices.framework/Support/lsregister \
    -u /Applications/merino.app 2>/dev/null || true
fi

echo
if [[ "$DRY" -eq 1 ]]; then
  echo "Dry run only — nothing was deleted."
else
  echo "Merino uninstalled."
  echo "  herdr itself was not touched."
  echo "  If Spotlight still shows Merino: wait a moment or log out/in."
fi
