#!/usr/bin/env bash
#
# lock.sh — write the specs lock marker + link contract at the top of a doc and freeze it.
#
# Usage:
#   lock.sh <doc> --type <spec|system-design|architecture> \
#           --upstream <docs> --referrers <docs> [--force] [--date YYYY-MM-DD]
#
#   --upstream   docs this one relied on at lock time ("none" for a root). Required.
#   --referrers  docs/code that must cite this one when they change. Required.
#   --force      rotate an existing lock to the current sha + date, or replace a
#                lock marker this script did not write. Without it, either is a
#                refusal.
#   --date       override today's date (deterministic tests).
#
# Both contract flags are required, and "none" is how a doc says it has none: an
# omitted flag is not an answer, and rendering one as "none" made a contract
# destroyed by a bare --force indistinguishable from a doc that genuinely has no
# upstream or no referrers. For an untracked doc the contract is the only record
# of that graph — there is no git history to recover it from — so a rotation was
# also silent. Every entry must name something that exists.
#
# What the doc already carries is established before anything is written. Its own
# marker is a rotation; any *other* lock marker is one this script did not write
# — another regime's (`design:locked:`), or its own shape without the `type=` it
# reads — and since the write is unconditional, a marker it does not recognise is
# one it would destroy rather than replace. Both are refusals, and --force is the
# way past either.
#
# Nothing is written until every check has passed: a refusal leaves the doc
# byte-identical and leaves nothing beside it.
#
# Exit codes: 0 written/rotated · 1 refuse (already locked, or carrying a lock
#             this script did not write) · 2 usage error (missing or invalid
#             argument, including a <doc> that is not a file).

set -euo pipefail

DOC=""
TYPE=""
UPSTREAM=""
REFERRERS=""
UPSTREAM_SET=0
REFERRERS_SET=0
FORCE=0
DATE=""

while [ $# -gt 0 ]; do
  case "$1" in
    --type)       shift; TYPE="${1:-}"; shift ;;
    --upstream)   shift; UPSTREAM="${1:-}"; UPSTREAM_SET=1; shift ;;
    --referrers)  shift; REFERRERS="${1:-}"; REFERRERS_SET=1; shift ;;
    --force)      FORCE=1; shift ;;
    --date)       shift; DATE="${1:-}"; shift ;;
    -h|--help)    printf 'Usage: lock.sh <doc> --type <spec|system-design|architecture> --upstream <docs> --referrers <docs> [--force] [--date YYYY-MM-DD]\n'; exit 0 ;;
    -*)           printf 'lock: unknown option: %s\n' "$1" >&2; exit 2 ;;
    *)
      if [ -z "$DOC" ]; then DOC="$1"
      else printf 'lock: unexpected arg: %s\n' "$1" >&2; exit 2; fi
      shift ;;
  esac
done

[ -n "$DOC" ] || { printf 'lock: <doc> is required\n' >&2; exit 2; }

# <doc> must name a doc this script can write. A directory used to get as far as
# the redirect at the end — after the body had been staged beside it — and exit
# leaving <dir>.body behind: not a refusal, and the header's "leaves nothing
# beside it" broken by the one input that could never have succeeded.
if [ -e "$DOC" ] && [ ! -f "$DOC" ]; then
  printf 'lock: %s is not a file; <doc> must name a doc\n' "$DOC" >&2; exit 2
fi

case "$TYPE" in
  spec|system-design|architecture) ;;
  *) printf 'lock: --type must be spec|system-design|architecture (got "%s")\n' "${TYPE:-}" >&2; exit 2 ;;
esac

DOC_DIR="$(dirname "$DOC")"

ALREADY_LOCKED=0
RECORDED=""
if [ -f "$DOC" ] && grep -qE '^<!-- specs:locked:[0-9a-f]+ [0-9-]+ type=' "$DOC"; then
  ALREADY_LOCKED=1
  # The contract this doc already carries. Reading it back is the only way a
  # caller can pass it through a rotation unchanged, so it is shown when a
  # rotation is refused for a missing flag.
  RECORDED="$(grep -m2 -E '^- \*\*(upstream|referrers)\*\*' "$DOC" || true)"
fi

# A lock this script did not write. Its own shape is read above, so anything
# else that is a lock marker is either another regime's or its own shape missing
# the `type=` it needs to read — and the write below is unconditional, so a
# marker it does not recognise is one it would overwrite rather than rotate.
# Anchored at the start of a line because that is where a marker is a marker. A
# fenced code block is skipped: a doc that quotes the shape in prose, as an
# example, is not carrying a lock.
MARKER_SHAPE='^<!-- [A-Za-z][A-Za-z0-9_-]*:locked:'
FOREIGN=""
FOREIGN_N=""
if [ "$ALREADY_LOCKED" -eq 0 ] && [ -f "$DOC" ]; then
  FOREIGN_MATCH="$(
    awk -v shape="$MARKER_SHAPE" '
      /^```/ { fenced = !fenced; next }
      !fenced && $0 ~ shape { printf "%d:%s\n", NR, $0; exit }
    ' "$DOC"
  )"
  if [ -n "$FOREIGN_MATCH" ]; then
    FOREIGN_N="${FOREIGN_MATCH%%:*}"
    FOREIGN="${FOREIGN_MATCH#*:}"
  fi
fi

MISSING=""
[ "$UPSTREAM_SET" -eq 1 ] || MISSING="--upstream"
[ "$REFERRERS_SET" -eq 1 ] || MISSING="${MISSING:+$MISSING and }--referrers"

if [ -n "$MISSING" ]; then
  printf 'lock: %s is required; pass "none" to say there is genuinely none\n' "$MISSING" >&2
  if [ -n "$RECORDED" ]; then
    printf 'lock: %s records — pass these back to rotate without changing them:\n' "$DOC" >&2
    printf '%s\n' "$RECORDED" | sed 's/^/  /' >&2
  fi
  exit 2
fi

# One entry per line, from a comma-or-pipe separated value.
contract_entries() {
  printf '%s' "$1" | sed 's/|/,/g' | tr ',' '\n' |
    sed 's/^[[:space:]]*//;s/[[:space:]]*$//' | grep -v '^$' || true
}

# A contract value names docs that exist, or is the whole list "none". A sha, a
# path that is not here, or a bare word that matches no doc is refused by name:
# each of those renders as a plausible-looking contract line and none of them is
# one. Resolved against the caller's cwd or against the doc's own directory,
# which is where the specs skill writes a basename like `playbook-system.md`.
check_contract() {  # $1 flag, $2 value
  local flag="$1" raw="$2" entries count entry
  entries="$(contract_entries "$raw")"
  if [ -z "$entries" ]; then
    printf 'lock: %s was given no value; use "%s none" to say there is none\n' "$flag" "$flag" >&2
    return 1
  fi
  count="$(printf '%s\n' "$entries" | wc -l | tr -d ' ')"
  while IFS= read -r entry; do
    if [ "$entry" = none ]; then
      if [ "$count" -gt 1 ]; then
        printf 'lock: %s lists "none" beside other entries; "none" is the whole list\n' "$flag" >&2
        return 1
      fi
      continue
    fi
    if [ -e "$entry" ] || [ -e "$DOC_DIR/$entry" ]; then
      continue
    fi
    printf 'lock: %s got "%s", which names no doc that exists here\n' "$flag" "$entry" >&2
    return 1
  done <<< "$entries"
}

check_contract --upstream "$UPSTREAM" || exit 2
check_contract --referrers "$REFERRERS" || exit 2

if [ "$ALREADY_LOCKED" -eq 1 ] && [ "$FORCE" -ne 1 ]; then
  printf 'lock: %s is already locked; pass --force to rotate\n' "$DOC" >&2; exit 1
fi

if [ -n "$FOREIGN" ] && [ "$FORCE" -ne 1 ]; then
  printf 'lock: %s carries a lock this script did not write:\n' "$DOC" >&2
  printf '  %s\n' "$FOREIGN" >&2
  printf 'lock: pass --force to replace it\n' >&2
  exit 1
fi

SHA="$(git rev-parse --short HEAD 2>/dev/null || true)"
if [ -z "$SHA" ]; then
  printf 'lock: not in a git repo (no HEAD); cannot record lock sha\n' >&2; exit 1
fi
[ -n "$DATE" ] || DATE="$(date +%F)"

# Normalize upstream/referrers: comma-or-pipe separated → comma+space.
# Both values are validated above, so neither can be empty here.
normalize_list() {
  local raw="${1:-}"
  [ -z "$raw" ] && { printf 'none'; return; }
  printf '%s' "$raw" | sed 's/|/,/g' | tr ',' '\n' | sed 's/^[[:space:]]*//;s/[[:space:]]*$//' | grep -v '^$' | paste -sd, - | sed 's/,/, /g'
}

UPSTREAM_NORM="$(normalize_list "$UPSTREAM")"
REFERRERS_NORM="$(normalize_list "$REFERRERS")"

MARKER="<!-- specs:locked:${SHA} ${DATE} type=${TYPE} -->"
CONTRACT_HEADER="## Link contract"
UPSTREAM_LINE="- **upstream** (this doc relies on): ${UPSTREAM_NORM}"
REFERRERS_LINE="- **referrers** (must cite this when they change): ${REFERRERS_NORM}"

# The staged body must not outlive the run on any path, not only the ones this
# script plans for: a doc that cannot be written (a read-only file) fails at the
# redirect below with the stage already on disk. Armed here rather than at the
# top of the script, because until this point the run has written nothing — every
# refusal above exits without staging, and a <doc>.body on disk at that moment
# belongs to someone else. Armed earlier, the trap was a delete of whatever
# carried that name, including on the one exit path that staged nothing at all.
trap 'rm -f "${DOC}.body"' EXIT

# Read existing body, stripping old marker + contract block. The marker stripped
# is any lock marker, not only this script's own shape: --force is the way past
# the foreign-marker refusal above, and a doc that was told its lock would be
# replaced must not come out carrying both the one it had and the new one. The
# foreign marker is stripped on the line detection read it on (FOREIGN_N), not a
# fixed line — so a doc that merely quotes the shape elsewhere, as a fenced
# example, keeps it.
if [ -f "$DOC" ]; then
  awk -v marker_re="$MARKER_SHAPE" -v contract_hdr="$CONTRACT_HEADER" -v foreign_n="${FOREIGN_N:-0}" '
    BEGIN { skip = 0; started = 0 }
    foreign_n > 0 && NR == foreign_n { next }
    NR == 1 && $0 ~ marker_re { next }
    NR == 2 && $0 == "" { next }
    !started {
      if ($0 == contract_hdr) { skip = 1; next }
      if (skip && $0 ~ /^- \*\*/) { next }
      if (skip && $0 == "") { skip = 0; next }
      if (skip) { next }
      started = 1
    }
    { print }
  ' "$DOC" | sed '/./,$!d' > "${DOC}.body"
else
  touch "${DOC}.body"
fi

# Write marker + contract + body.
{
  printf '%s\n\n' "$MARKER"
  printf '%s\n' "$CONTRACT_HEADER"
  printf '%s\n' "$UPSTREAM_LINE"
  printf '%s\n' "$REFERRERS_LINE"
  if [ -s "${DOC}.body" ]; then
    printf '\n'
    cat "${DOC}.body"
  fi
} > "$DOC" || { printf 'lock: cannot write %s\n' "$DOC" >&2; exit 1; }

rm -f "${DOC}.body"

printf 'lock: %s locked @ %s (%s)\n' "$DOC" "$SHA" "$DATE"
printf '  type=%s upstream=%s referrers=%s\n' \
  "$TYPE" "${UPSTREAM_NORM}" "${REFERRERS_NORM}"
