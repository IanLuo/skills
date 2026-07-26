#!/usr/bin/env bash
#
# lock.sh — write the prd lock marker + link contract at the top of a doc and freeze it.
#
# Marker line (must stay the first line of the file):
#   <!-- prd:locked:<sha> <YYYY-MM-DD> type=<t> -->
# Under it, a `## Link contract` block with upstream / referrers. review-task greps
# these exact strings — keep the shape stable; the Python helper owns it.
#
# Usage:
#   lock.sh <doc> --type <prd|system-design|architecture> \
#           [--upstream <docs>] [--referrers <docs>] [--force] [--date YYYY-MM-DD]
#
#   --upstream   docs this one relied on at lock time ("none" for a root).
#   --referrers  docs/code that must cite this one when they change.
#   --force      rotate an existing lock to the current sha + date (update flow). Without
#                it, an already-locked doc exits non-zero.
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
    -h|--help)    sed -n '3,22p' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
    -*)           printf 'lock: unknown option: %s\n' "$1" >&2; exit 2 ;;
    *)
      if [ -z "$DOC" ]; then DOC="$1"
      else printf 'lock: unexpected arg: %s\n' "$1" >&2; exit 2; fi
      shift ;;
  esac
done

[ -n "$DOC" ] || { printf 'lock: <doc> is required\n' >&2; exit 2; }
case "$TYPE" in
  prd|system-design|architecture) ;;
  *) printf 'lock: --type must be prd|system-design|architecture (got "%s")\n' "${TYPE:-}" >&2; exit 2 ;;
esac

SHA="$(git rev-parse --short HEAD 2>/dev/null || true)"
if [ -z "$SHA" ]; then
  printf 'lock: not in a git repo (no HEAD); cannot record lock sha\n' >&2; exit 1
fi
[ -n "$DATE" ] || DATE="$(git log -1 --format=%cs HEAD 2>/dev/null || date +%F)"

if grep -qE '^<!-- prd:locked:[0-9a-f]+ [0-9-]+ type=' "$DOC" 2>/dev/null; then
  if [ "$FORCE" -ne 1 ]; then
    printf 'lock: %s is already locked; pass --force to rotate\n' "$DOC" >&2; exit 1
  fi
fi

# Delegate the byte-stable rewrite to Python: parse the existing file (if any), strip the
# old marker + contract, and re-emit marker + contract + preserved body. Keeping this in a
# script, not prose, is what guarantees downstream tools can grep the marker reliably.
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
python3 "$SCRIPT_DIR/_write_locked.py" \
  --doc "$DOC" --sha "$SHA" --date "$DATE" --type "$TYPE" \
  --upstream "${UPSTREAM:-}" --referrers "${REFERRERS:-}"

printf 'lock: %s locked @ %s (%s)\n' "$DOC" "$SHA" "$DATE"
printf '  type=%s upstream=%s referrers=%s\n' \
  "$TYPE" "${UPSTREAM:-none}" "${REFERRERS:-none}"