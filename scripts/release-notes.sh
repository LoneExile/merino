#!/usr/bin/env bash
# Extract one release's section from CHANGELOG.md.
#
#   scripts/release-notes.sh v0.2.0
#
# CHANGELOG.md is the single source of truth for release notes. The workflow
# posts whatever this prints as the GitHub release body, so what you read in
# the repo and what you read on the release page cannot drift — and a
# hand-written upgrade note in a section ships with it.
#
# Exits non-zero when the section is missing, so a release cannot quietly ship
# with empty notes because someone forgot to run `just changelog`.
#
# POSIX/BSD tools only. This runs on a macOS runner, which has no `tac`, no
# `sed -i ''` compatibility with GNU, and no coreutils unless someone installs
# them — the first version used `tac` and died with exit 127 mid-release.
set -euo pipefail

TAG="${1:?usage: release-notes.sh vX.Y.Z}"
VERSION="${TAG#v}"
FILE="${2:-CHANGELOG.md}"

test -f "$FILE" || { echo "no $FILE" >&2; exit 1; }

# From the heading for this version to the next `## `, with leading and
# trailing blank lines removed but internal ones kept. Buffering blanks and
# flushing them only when real content follows does that in one pass.
BODY=$(
  awk -v want="## ${VERSION} " '
    index($0, want) == 1 { grab = 1; next }
    grab && /^## / { exit }
    grab {
      if ($0 ~ /^[[:space:]]*$/) { if (started) pending = pending "\n"; next }
      if (started) printf "%s", pending
      pending = ""
      started = 1
      print
    }
  ' "$FILE"
)

# Pre-releases are not special here. `just changelog v0.3.0-rc.2` writes a
# section for the candidate like any other tag, and cutting the stable
# release afterwards regenerates the file and removes it again — cliff.toml
# ignores hyphenated tags, so those commits come back under the stable
# version rather than being stranded in a candidate's section.
#
# An earlier version of this fell back to the pending "## Unreleased" block
# when a candidate had no section. That block is a snapshot: it goes stale
# the moment anything lands after it was written, and it did — a candidate
# cut that way would have published notes missing the two most recent
# changes, silently and plausibly. Wrong notes are the same failure as empty
# ones, which is what this whole script exists to prevent. So: no fallback.
if [ -z "$BODY" ]; then
  echo "no section for ${VERSION} in ${FILE} — run: just changelog ${TAG}" >&2
  exit 1
fi

printf '%s\n' "$BODY"
