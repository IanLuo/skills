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
#   bin/check-parallel.sh a.md,b.md
#
# The second form is the one fs dispatch uses: parallel-entry.yaml runs
# this with $FS_CARDS, which fs dispatch sets from --cards, comma-separated.
#
# Exit: 0 disjoint · 1 overlap, or a card with no file list · 2 no cards named.

set -euo pipefail

# Cards are collected before anything is checked, so "named no cards" and "named
# nothing but blanks" are the same refusal. An argument may list several cards,
# comma-separated; surrounding whitespace is dropped.
cards="$(
  printf '%s\n' "$@" | tr ',' '\n' |
    sed -e 's/^[[:space:]]*//' -e 's/[[:space:]]*$//' -e '/^$/d'
)"

if [ -z "$cards" ]; then
  cat >&2 <<'EOF'
check-parallel: no cards were named — pass --cards a.md,b.md to fs dispatch.

This check reads the batch from $FS_CARDS, which fs dispatch sets from --cards.
With no cards there is nothing to intersect, and a check with no input must
refuse rather than pass vacuously.
EOF
  exit 2
fi

count="$(printf '%s\n' "$cards" | wc -l | tr -d ' ')"

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

while IFS= read -r card; do
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
done <<< "$cards"

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

echo "check-parallel: $count cards, no overlapping paths"
