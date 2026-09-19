#!/usr/bin/env bash
#
# check-parallel.sh — assert a batch of dispatch cards is safe to run at once.
#
# The cap's parallel failure mode is two workers in one file. That is invisible
# until the merge conflicts, and by then both branches have moved. A card that
# declares every path it will touch makes the question mechanical: intersect the
# cards' file sets, and refuse the batch if the intersection is not empty.
#
# The declaration is the convention; this script is what makes it load-bearing.
# A card with no "## Files" section is refused rather than assumed disjoint —
# that refusal is the whole point, because without it disjointness goes back to
# being a judgement call the cap gets wrong.
#
# Usage:
#   bin/check-parallel.sh <card.md> [card.md ...]
#
# Exit: 0 disjoint · 1 overlap, or a card with no file list · 2 no cards named.

set -euo pipefail

usage() {
  cat >&2 <<'EOF'
check-parallel.sh — assert a batch of dispatch cards declares disjoint files.

Usage: bin/check-parallel.sh <card.md> [card.md ...]

Each card must carry a "## Files" section listing the paths it will touch, one
per line; a fenced block and trailing # comments are fine. The batch is safe in
parallel only when no path appears in two cards.

With no cards named, this refuses rather than passing vacuously: a check whose
input is missing must not report success.
EOF
}

if [ "$#" -eq 0 ]; then
  usage
  exit 2
fi

tmp="$(mktemp)"
trap 'rm -f "$tmp"' EXIT

# declaredPaths prints one "path<TAB>card" line per declared path, reading the
# card's "## Files" section: fenced blocks and comments are ignored, and a
# trailing annotation after the path is dropped.
declaredPaths() {
  awk -v card="$1" '
    /^## Files[[:space:]]*$/ { inFiles = 1; next }
    inFiles && /^## /       { inFiles = 0 }
    inFiles {
      line = $0
      sub(/^[[:space:]]+/, "", line)
      sub(/[[:space:]]+$/, "", line)
      if (line == "" || line ~ /^```/ || line ~ /^~~~/ || line ~ /^-/) next
      sub(/[[:space:]]*#.*$/, "", line)
      sub(/[[:space:]].*$/, "", line)
      if (line != "") print line "\t" card
    }
  ' "$1"
}

for card in "$@"; do
  if [ ! -f "$card" ]; then
    echo "check-parallel: no such card: $card" >&2
    exit 1
  fi
  before="$(wc -l < "$tmp")"
  declaredPaths "$card" >> "$tmp"
  after="$(wc -l < "$tmp")"
  if [ "$before" -eq "$after" ]; then
    echo "check-parallel: $card declares no files — add a '## Files' section listing every path this card will touch" >&2
    exit 1
  fi
done

# Group the unique (path, card) pairs by path; any path claimed by more than one
# card is an overlap. sort -u collapses a card that names one path twice.
overlaps="$(
  sort -u "$tmp" | awk -F'\t' '
    $1 != prev && prev != "" {
      if (n > 1) print prev "\t" cards
      n = 0; cards = ""
    }
    { prev = $1; n++; cards = (cards == "" ? $2 : cards ", " $2) }
    END { if (n > 1) print prev "\t" cards }
  '
)"

if [ -n "$overlaps" ]; then
  echo "check-parallel: cards overlap — these paths are declared by more than one card:" >&2
  while IFS=$'\t' read -r path cards; do
    printf '  %s  (%s)\n' "$path" "$cards" >&2
  done <<< "$overlaps"
  exit 1
fi

echo "check-parallel: $# cards, no overlapping paths"
