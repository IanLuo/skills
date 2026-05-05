#!/bin/bash
# scripts/pipeline/advance.sh — mark step [x] in plan.md header
# Usage: advance.sh <step_id>
set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
source "$SCRIPT_DIR/lib.sh"

STEP_ID="${1:-}"
[ -n "$STEP_ID" ] || fail "Usage: advance.sh <step_id>"
TIMESTAMP="${2:-$(date +%Y-%m-%d\ %H:%M)}"

# Replace [ ] with [x] timestamped in plan.md header for this step
if grep -q "^- \[ \].*${STEP_ID}" "$PLAN_MD"; then
    STEP_LABEL=$(grep "^- \[ \].*${STEP_ID}" "$PLAN_MD" | head -1 | sed 's/^- \[ \]//' | sed "s/→ ${STEP_ID}.*//" | xargs)
    sed -i '' "s#^- \[ \].*${STEP_ID}.*\$#- [x] ${STEP_LABEL}  ${TIMESTAMP}   → ${STEP_ID}#" "$PLAN_MD"
    log "Advanced: $STEP_ID marked [x] at $TIMESTAMP"
else
    log "Step $STEP_ID not found or already [x] in header"
fi
