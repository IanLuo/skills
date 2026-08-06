#!/usr/bin/env bash
#
# init-library.sh — bootstrap the librarian knowledge library.
#
# Creates ~/Documents/librarian/library/ with an empty index.md if it does not
# exist. Idempotent: re-running on an existing library is a no-op. Pass
# --root <dir> to target a different library root (used for testing).
#
# Usage:
#   init-library.sh [--root <dir>]
#
# Exit codes:
#   0  library exists and is valid (created or already present)
#   1  filesystem error

set -euo pipefail

ROOT="${LIBRARY_ROOT:-$HOME/Documents/librarian}"

while [ $# -gt 0 ]; do
  case "$1" in
    --root) shift; ROOT="${1:-}"; shift ;;
    -h|--help)
      sed -n '3,15p' "$0" | sed 's/^# \{0,1\}//'
      exit 0
      ;;
    *) printf 'init-library: unknown argument: %s\n' "$1" >&2; exit 2 ;;
  esac
done

[ -n "$ROOT" ] || { printf 'init-library: --root requires a path\n' >&2; exit 2; }

LIBRARY="$ROOT/library"

mkdir -p "$LIBRARY"

# Write index.md only if absent or empty.
if [ ! -s "$LIBRARY/index.md" ]; then
  cat >"$LIBRARY/index.md" <<'INDEX'
# Librarian library index

Categories and entries are appended here by `write-entry.py`. Query with
`query.py`. Do not hand-edit unless reorganizing — the paths below are the
source of truth for `query.py`.

## Categories

<!-- categories appended below -->

## Index

<!-- index rows appended below -->
INDEX
  printf 'init-library: created %s/index.md\n' "$LIBRARY"
else
  printf 'init-library: %s/index.md already exists\n' "$LIBRARY"
fi