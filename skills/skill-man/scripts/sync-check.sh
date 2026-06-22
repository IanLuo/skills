#!/usr/bin/env bash
#
# sync-check.sh — report whether skill-man's spec is in sync with upstream
# anthropics/skills.
#
# Compares the pinned SHA in .upstream (and SPEC_PINNED_REF in validate.py)
# against the current main HEAD of github.com/anthropics/skills.
#
# Usage: bash sync-check.sh
# Exit: 0 if in sync (or offline), 1 if behind, 2 if the two pinned refs disagree.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)"
SKILL_DIR="$(cd "$SCRIPT_DIR/.." && pwd -P)"
UPSTREAM_FILE="$SKILL_DIR/.upstream"
VALIDATE_PY="$SKILL_DIR/scripts/validate.py"
REMOTE="https://github.com/anthropics/skills"

log() { printf '[sync-check] %s\n' "$*"; }

# ── Read the pinned SHA from .upstream ────────────────────────────────────
if [ ! -f "$UPSTREAM_FILE" ]; then
  log "✖ .upstream file not found at $UPSTREAM_FILE"
  exit 1
fi
# First non-comment, non-blank line.
PINED_SHA="$(grep -vE '^\s*#|^\s*$' "$UPSTREAM_FILE" | head -1 | tr -d '[:space:]')"
if [ -z "$PINED_SHA" ]; then
  log "✖ .upstream contains no SHA"
  exit 1
fi

# ── Cross-check validate.py carries the same pin ──────────────────────────
PIN_IN_VALIDATE="$(grep -oE 'SPEC_PINNED_REF = "[0-9a-f]{40}"' "$VALIDATE_PY" | grep -oE '[0-9a-f]{40}' || true)"
if [ -z "$PIN_IN_VALIDATE" ]; then
  log "⚠ could not find SPEC_PINNED_REF in validate.py — please add it"
elif [ "$PIN_IN_VALIDATE" != "$PINED_SHA" ]; then
  log "✖ pinned refs disagree:"
  log "    .upstream:      $PINED_SHA"
  log "    validate.py:    $PIN_IN_VALIDATE"
  log "  Update them together when syncing."
  exit 2
fi

# ── Fetch current upstream HEAD ───────────────────────────────────────────
if ! command -v git >/dev/null 2>&1; then
  log "git not found — cannot check upstream. (pinned SHA: ${PINED_SHA:0:7})"
  exit 0
fi

log "Fetching current HEAD of $REMOTE main..."
HEAD_SHA="$(git ls-remote "$REMOTE" main 2>/dev/null | awk '{print $1}')"
if [ -z "$HEAD_SHA" ]; then
  log "⚠ could not reach upstream (offline?). Pinned SHA: ${PINED_SHA:0:7}"
  exit 0
fi

if [ "$PINED_SHA" = "$HEAD_SHA" ]; then
  log "✓ in sync — pinned and upstream HEAD both at ${PINED_SHA:0:7}"
  exit 0
fi

log "⚠ BEHIND — spec was synced from ${PINED_SHA:0:7}, upstream HEAD is now ${HEAD_SHA:0:7}"
log "  Re-sync: compare skills/skill-creator/scripts/quick_validate.py upstream against"
log "  validate.py, update SPEC_PINNED_REF + .upstream, re-run tests/run.sh."
exit 1
