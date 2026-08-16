#!/usr/bin/env bash
#
# lock.sh — write the specs lock marker + link contract at the top of a doc and freeze it.
#
# Usage:
#   lock.sh <doc> --type <spec|system-design|architecture> \
#           [--upstream <docs>] [--referrers <docs>] [--force] [--date YYYY-MM-DD]
#
#   --upstream   docs this one relied on at lock time ("none" for a root).
#   --referrers  docs/code that must cite this one when they change.
#   --force      rotate an existing lock to the current sha + date. Without it,
#                an already-locked doc exits non-zero.
#   --date       override today's date (deterministic tests).
#
# Exit codes: 0 written/rotated · 1 refuse (already locked, no --force) · 2 usage error.

set -euo pipefail

DOC=""
TYPE=""
UPSTREAM=""
REFERRERS=""
FORCE=0
DATE=""

while [ $# -gt 0 ]; do
  case "$1" in
    --type)       shift; TYPE="${1:-}"; shift ;;
    --upstream)   shift; UPSTREAM="${1:-}"; shift ;;
    --referrers)  shift; REFERRERS="${1:-}"; shift ;;
    --force)      FORCE=1; shift ;;
    --date)       shift; DATE="${1:-}"; shift ;;
    -h|--help)    printf 'Usage: lock.sh <doc> --type <spec|system-design|architecture> [--upstream <docs>] [--referrers <docs>] [--force] [--date YYYY-MM-DD]\n'; exit 0 ;;
    -*)           printf 'lock: unknown option: %s\n' "$1" >&2; exit 2 ;;
    *)
      if [ -z "$DOC" ]; then DOC="$1"
      else printf 'lock: unexpected arg: %s\n' "$1" >&2; exit 2; fi
      shift ;;
  esac
done

[ -n "$DOC" ] || { printf 'lock: <doc> is required\n' >&2; exit 2; }
case "$TYPE" in
  spec|system-design|architecture) ;;
  *) printf 'lock: --type must be spec|system-design|architecture (got "%s")\n' "${TYPE:-}" >&2; exit 2 ;;
esac

SHA="$(git rev-parse --short HEAD 2>/dev/null || true)"
if [ -z "$SHA" ]; then
  printf 'lock: not in a git repo (no HEAD); cannot record lock sha\n' >&2; exit 1
fi
[ -n "$DATE" ] || DATE="$(date +%F)"

if grep -qE '^<!-- specs:locked:[0-9a-f]+ [0-9-]+ type=' "$DOC" 2>/dev/null; then
  if [ "$FORCE" -ne 1 ]; then
    printf 'lock: %s is already locked; pass --force to rotate\n' "$DOC" >&2; exit 1
  fi
fi

# Normalize upstream/referrers: comma-or-pipe separated → comma+space, or "none".
normalize_list() {
  local raw="${1:-}"
  [ -z "$raw" ] && { printf 'none'; return; }
  printf '%s' "$raw" | sed 's/|/,/g' | tr ',' '\n' | sed 's/^[[:space:]]*//;s/[[:space:]]*$//' | grep -v '^$' | paste -sd ', ' -
}

UPSTREAM_NORM="$(normalize_list "$UPSTREAM")"
REFERRERS_NORM="$(normalize_list "$REFERRERS")"
[ -z "$UPSTREAM_NORM" ] && UPSTREAM_NORM="none"
[ -z "$REFERRERS_NORM" ] && REFERRERS_NORM="none"

MARKER="<!-- specs:locked:${SHA} ${DATE} type=${TYPE} -->"
CONTRACT_HEADER="## Link contract"
UPSTREAM_LINE="- **upstream** (this doc relies on): ${UPSTREAM_NORM}"
REFERRERS_LINE="- **referrers** (must cite this when they change): ${REFERRERS_NORM}"

# Read existing body, stripping old marker + contract block.
if [ -f "$DOC" ]; then
  awk -v marker_re='^<!-- specs:locked:' -v contract_hdr="$CONTRACT_HEADER" '
    BEGIN { skip = 0; started = 0 }
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
} > "$DOC"

rm -f "${DOC}.body"

printf 'lock: %s locked @ %s (%s)\n' "$DOC" "$SHA" "$DATE"
printf '  type=%s upstream=%s referrers=%s\n' \
  "$TYPE" "${UPSTREAM_NORM}" "${REFERRERS_NORM}"
