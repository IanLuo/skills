#!/usr/bin/env bash
#
# check-deliverable.sh — assert the change reached the repository, and that the
# paths a dispatch's cards declare are not left uncommitted.
#
# The defect this answers: a card asked for "The commit sha" as evidence, the
# worker's node named one that no longer existed, and fs close passed anyway —
# 15 changed files sat uncommitted in the working tree while HEAD had not moved.
# A card is a request, not a gate, and nothing in the mechanism could tell a
# finished deliverable from a transcript that said so.
#
# What is checkable is exactly this: HEAD has moved past the revision the
# project stood at when the dispatch was prepared, and — for the paths a card
# declares — nothing is left dirty and a commit in that range touches them.
# Whether the code WORKS is not checkable here and is not claimed.
#
# It is deliberately not a clean-tree check. The tree is routinely dirty with
# another agent's work in this repo, so a repo-wide check would refuse closes
# that are fine and be switched off within a day. The scope is the card's own
# declaration.
#
# Usage:
#   "$FS_BIN" script check-deliverable.sh <base-rev> [<card.md>[,<card.md>...]]
#
# The base is the revision the project stood at when the dispatch was prepared.
# fs close reads it off the cap node's own task-created event — the store has
# recorded that sha immutably for every dispatch that ever existed — and exports
# it as $FS_BASE. The cards are $FS_CARDS, which the dispatch recorded from
# --cards; a dispatch that named none has none, and the commit half below is
# then the half that runs.
#
# The tree it is asked about is the cwd: fs runs an exit check in the tree the
# dispatch worked in. A card is looked for there first and then in the checkout
# the cards belong to — a batch member works in a worktree that does not carry
# an uncommitted card from the cap's own checkout, and the cap's checkout is
# where the card was written.
#
# Exit: 0 checked and clean · 1 refused, with the reason on stderr · 2 the
# command line was wrong.
#
# Two cases stand down at 0 rather than refusing, and both say so: a tree that
# is not a git repository has no repository a deliverable could have reached,
# and a dispatch whose preparation recorded no revision — a project that was not
# a repository yet, or an unborn HEAD — has nothing to measure a change against.
# Refusing those would turn a missing input into a gate the cap cannot pass,
# which is the failure the review gate's own history is a record of.

set -euo pipefail

if [ "$#" -lt 1 ]; then
  cat >&2 <<'EOF'
check-deliverable: usage: check-deliverable.sh <base-rev> [<card.md>[,<card.md>...]]

The base revision is what the change is measured against, and fs close passes
it from the dispatch's own record as $FS_BASE.
EOF
  exit 2
fi

base="$1"
shift

# Cards are collected the way check-parallel.sh collects them: an argument may
# list several, comma-separated, which is how fs passes $FS_CARDS through one
# word, and surrounding whitespace is dropped.
cards="$(
  printf '%s\n' "$@" | tr ',' '\n' |
    sed -e 's/^[[:space:]]*//' -e 's/[[:space:]]*$//' -e '/^$/d'
)"

if ! git rev-parse --is-inside-work-tree >/dev/null 2>&1; then
  echo "check-deliverable: $(pwd) is not a git repository, so there is no repository for the deliverable to have reached — nothing to check"
  exit 0
fi

if [ -z "$base" ]; then
  echo "check-deliverable: no revision was recorded for when this dispatch was prepared, so there is nothing to measure the change against — nothing to check"
  exit 0
fi

# A base this tree does not know is not a missing input: the record and the tree
# disagree, and that is worth saying rather than passing over. It is what a tree
# that is not the one the dispatch was prepared against looks like.
if ! git rev-parse --verify --quiet "$base^{commit}" >/dev/null; then
  echo "check-deliverable: $base, recorded as the revision the project stood at when this dispatch was prepared, is not a commit in $(pwd) — so what this change is cannot be told from what came before it. Close from the tree the dispatch was prepared against, or re-dispatch" >&2
  exit 1
fi

# The first half: something was committed. HEAD unmoved is the defect above,
# stated as what it is — the work is on disk and none of it is in the history.
if [ "$(git rev-list --count "$base"..HEAD)" -eq 0 ]; then
  changed="$(git status --porcelain | wc -l | tr -d ' ')"
  echo "check-deliverable: no commit carries this work — $(pwd) stands at $(git rev-parse --short HEAD) and has nothing past $(git rev-parse --short "$base"), the revision the project stood at when this dispatch was prepared, while $changed path(s) in the tree are changed or untracked. Commit the work, then close again" >&2
  exit 1
fi

# The checkout the cards belong to. `--git-common-dir` is the repository's own
# answer: from a worktree it is the main checkout's .git, and from the main
# checkout itself it is the relative ".git", which the cd below resolves against
# the cwd. Nothing here derives a path by picking a directory apart by hand.
common="$(git rev-parse --git-common-dir)"
checkout="$(cd "$(dirname "$common")" && pwd)"

# declaredPaths prints the paths a card declares, read out of its "## Files"
# section. This is check-parallel.sh's convention and the same parser, because a
# card whose declaration one reader accepts and the other drops would be a
# declaration that means two things. The two must stay in step: fenced blocks
# and comments are ignored, and a trailing annotation after the path is dropped.
declaredPaths() {
  awk '
    /^## Files[[:space:]]*$/ { inFiles = 1; next }
    inFiles && /^## /       { inFiles = 0 }
    inFiles {
      line = $0
      sub(/^[[:space:]]+/, "", line)
      sub(/[[:space:]]+$/, "", line)
      if (line == "" || line ~ /^```/ || line ~ /^~~~/ || line ~ /^-/) next
      sub(/[[:space:]]*#.*$/, "", line)
      sub(/[[:space:]].*$/, "", line)
      if (line != "") print line
    }
  ' "$1"
}

checked=0
while IFS= read -r card; do
  [ -n "$card" ] || continue

  # The tree first, then the checkout the cards belong to: a batch member's
  # worktree forks from a commit that may predate the card's own file, and an
  # uncommitted card never reaches a worktree at all.
  file="$card"
  [ -f "$file" ] || file="$checkout/$card"
  if [ ! -f "$file" ]; then
    echo "check-deliverable: the dispatch named the card $card, and it is in neither $(pwd) nor $checkout — the paths it declares cannot be read, so what the deliverable was cannot be told" >&2
    exit 1
  fi

  paths="$(declaredPaths "$file")"
  if [ -z "$paths" ]; then
    echo "check-deliverable: the card $card declares no files — add a '## Files' section listing every path this card will touch" >&2
    exit 1
  fi

  npaths=0
  touched=0
  dirty=""
  while IFS= read -r p; do
    [ -n "$p" ] || continue
    npaths=$((npaths + 1))

    # The status is read first: a path can be touched by a commit and left dirty
    # afterwards, and that is the state the card's reader is misled by.
    st="$(git status --porcelain -- "$p")"
    [ -z "$st" ] || dirty="$dirty
  $st"

    if [ -n "$(git log --format=%H "$base"..HEAD -- "$p")" ]; then
      touched=$((touched + 1))
    fi
  done <<< "$paths"

  if [ -n "$dirty" ]; then
    echo "check-deliverable: the card $card declares paths this change leaves uncommitted:$dirty" >&2
    exit 1
  fi

  # The card declares what the deliverable is, so a commit that touches none of
  # its paths is a change that did not deliver it — however many commits it has.
  if [ "$touched" -eq 0 ]; then
    echo "check-deliverable: the card $card declares $npaths path(s), and no commit past $(git rev-parse --short "$base") touches any of them — the change commits, but nothing on the paths this card asked for" >&2
    exit 1
  fi

  checked=$((checked + 1))
done <<< "$cards"

echo "check-deliverable: $(git rev-list --count "$base"..HEAD) commit(s) past $(git rev-parse --short "$base"), nothing uncommitted among the $checked card(s) declared paths"
