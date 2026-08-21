#!/usr/bin/env bash
#
# update.sh — search the authorized source (anthropics/skills) for skill-related
# updates since the pinned ref, and report them. This is skill-man's `update` mode.
#
# Reports:
#   1. Spec drift — is validate.py's pin behind upstream HEAD?
#   2. Official skills added / removed in anthropics/skills since the pin.
#   3. A re-sync recommendation (never auto-applies — you review first).
#
# Usage: bash update.sh
# Exit: 0 in sync (or offline) · 1 updates available · 2 pin refs disagree

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)"
SKILL_DIR="$(cd "$SCRIPT_DIR/.." && pwd -P)"
UPSTREAM_FILE="$SKILL_DIR/.upstream"
VALIDATE_PY="$SKILL_DIR/scripts/validate.py"
REMOTE="https://api.github.com/repos/anthropics/skills"

log() { printf '[skill-update] %s\n' "$*"; }

# ── Pinned ref ─────────────────────────────────────────────────────────────
PINED_SHA="$(grep -vE '^\s*#|^\s*$' "$UPSTREAM_FILE" 2>/dev/null | head -1 | tr -d '[:space:]')"
[ -z "$PINED_SHA" ] && { log "✖ no pin in $UPSTREAM_FILE"; exit 1; }

# ── Upstream HEAD ──────────────────────────────────────────────────────────
HEAD_SHA="$(git ls-remote https://github.com/anthropics/skills main 2>/dev/null | awk '{print $1}')"
[ -z "$HEAD_SHA" ] && { log "⚠ offline — cannot reach anthropics/skills"; exit 0; }

if [ "$PINED_SHA" = "$HEAD_SHA" ]; then
  log "✓ in sync — pin + upstream HEAD both at ${PINED_SHA:0:7}"
  exit 0
fi

log "⚠ updates available: pinned ${PINED_SHA:0:7}, upstream HEAD ${HEAD_SHA:0:7}"
UPDATES=0

# ── 1. Spec drift (quick_validate.py) ──────────────────────────────────────
NEW_V="$(curl -fsSL "https://raw.githubusercontent.com/anthropics/skills/${HEAD_SHA}/skills/skill-creator/scripts/quick_validate.py" 2>/dev/null || true)"
CUR_V="$(cat "$VALIDATE_PY" 2>/dev/null || true)"
if [ -n "$NEW_V" ] && [ "$NEW_V" != "$CUR_V" ]; then
  log "  • spec (quick_validate.py) CHANGED upstream — diff it against validate.py"
  UPDATES=$((UPDATES + 1))
fi

# ── 2. Skill inventory (skills/*/SKILL.md at pin vs HEAD) ──────────────────
list_skills() {
  curl -fsSL "${REMOTE}/git/trees/$1?recursive=1" 2>/dev/null \
    | grep -o '"path":"skills/[^"]*/SKILL\.md"' \
    | sed 's/"path":"//;s/"$//' | sort || true
}
PIN_SKILLS="$(list_skills "$PINED_SHA")"
HEAD_SKILLS="$(list_skills "$HEAD_SHA")"
if [ -n "$HEAD_SKILLS" ]; then
  ADDED="$(comm -13 <(printf '%s\n' "$PIN_SKILLS") <(printf '%s\n' "$HEAD_SKILLS"))"
  REMOVED="$(comm -23 <(printf '%s\n' "$PIN_SKILLS") <(printf '%s\n' "$HEAD_SKILLS"))"
  if [ -n "$ADDED" ]; then log "  • new upstream skills:"; printf '%s\n' "$ADDED" | sed 's/^/      /'; UPDATES=$((UPDATES + 1)); fi
  if [ -n "$REMOVED" ]; then log "  • removed upstream skills:"; printf '%s\n' "$REMOVED" | sed 's/^/      /'; UPDATES=$((UPDATES + 1)); fi
fi

log "→ ${UPDATES} skill-related update(s) available. Review, then re-sync the spec as needed:"
log "    compare quick_validate.py upstream against validate.py, bump SPEC_PINNED_REF +"
log "    .upstream, re-run tests/run.sh. Never auto-apply."
exit 1
