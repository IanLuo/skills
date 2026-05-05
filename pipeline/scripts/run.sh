#!/bin/bash
# scripts/pipeline/run.sh — main pipeline orchestrator entry
# Usage: run.sh [step|run] [version]
# This script handles VALIDATION. The orchestrator skill handles DISPATCH.
set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
source "$SCRIPT_DIR/lib.sh"

MODE="${1:-step}"
VERSION="$(norm_version "${2:-}")"

log "Pipeline v${VERSION} — mode: ${MODE}"
log "Plan: $PLAN_MD"

# Step 0: Reset — clear header and report for a fresh run
bash "$SCRIPT_DIR/reset.sh" "$VERSION"

# Initialize report — always start fresh
REPORT="$ROOT_DIR/docs/pipeline/report-v${VERSION}.md"
cat > "$REPORT" <<'REPORT_HEADER'
# Pipeline Execution Report vVERSION_PLACEHOLDER
**Started:** STARTED_PLACEHOLDER | **Mode:** MODE_PLACEHOLDER

## Initial State

## Event Timeline
REPORT_HEADER

# Replace placeholders
STARTED=$(date +%Y-%m-%d\ %H:%M)
sed -i '' "s/VERSION_PLACEHOLDER/${VERSION}/" "$REPORT"
sed -i '' "s/STARTED_PLACEHOLDER/${STARTED}/" "$REPORT"
sed -i '' "s/MODE_PLACEHOLDER/${MODE}/" "$REPORT"

# Append initial state from plan.md header
grep '^- \[' "$PLAN_MD" | head -9 | sed 's/^/| /' >> "$REPORT"
echo "" >> "$REPORT"

# Main loop
STEP_ID=$(get_next_step)

while [ -n "$STEP_ID" ]; do
    STEP_NAME=$(get_step_field "$STEP_ID" "name")
    log "=== $STEP_ID: $STEP_NAME ==="

    # PRE-FLIGHT
    echo "[pipeline] Pre-flight: $STEP_ID"
    bash "$SCRIPT_DIR/check-inputs.sh" "$STEP_ID" "$VERSION" && {
        bash "$SCRIPT_DIR/report.sh" "$STEP_ID" "pre-flight" "pass" "" "$VERSION"
    } || {
        bash "$SCRIPT_DIR/report.sh" "$STEP_ID" "fail" "fail" "Pre-flight: missing inputs" "$VERSION"
        fail "Pre-flight failed for $STEP_ID"
    }

    # SIGNAL: orchestrator needs to dispatch here
    # Write prompt file for orchestrator skill to read
    PROMPT_FILE="/tmp/pipeline-prompt-${STEP_ID}.txt"
    log "Signal: ready for dispatch. Prompt → $PROMPT_FILE"
    log "ACTION_REQUIRED: dispatch sub-agent for $STEP_ID then re-run post-flight"
    echo "DISPATCH:$STEP_ID:$VERSION" > "$PROMPT_FILE"

    # In step mode, exit here and let orchestrator dispatch
    [ "$MODE" = "step" ] && exit 0

    # POST-FLIGHT (won't reach here in step mode — orchestrator calls separately)
    bash "$SCRIPT_DIR/check-outputs.sh" "$STEP_ID" "$VERSION" && {
        bash "$SCRIPT_DIR/report.sh" "$STEP_ID" "post-flight" "pass" "" "$VERSION"
    } || {
        bash "$SCRIPT_DIR/report.sh" "$STEP_ID" "fail" "fail" "Post-flight: missing/invalid outputs" "$VERSION"
        fail "Post-flight failed for $STEP_ID"
        exit 1
    }

    # GATES
    GATES_JSON=$(get_step_field "$STEP_ID" "gates")
    echo "$GATES_JSON" | python3 -c "
import json, sys
gates = json.load(sys.stdin)
for g in gates:
    print(g['type'])
" | while read -r GATE_TYPE; do
        [ -z "$GATE_TYPE" ] && continue
        bash "$SCRIPT_DIR/gate.sh" "$STEP_ID" "$GATE_TYPE" "$VERSION" && {
            bash "$SCRIPT_DIR/report.sh" "$STEP_ID" "gate" "pass" "" "$VERSION"
        } || {
            bash "$SCRIPT_DIR/report.sh" "$STEP_ID" "fail" "fail" "Gate $GATE_TYPE failed" "$VERSION"
            fail "Gate '$GATE_TYPE' failed for $STEP_ID"
        }
    done

    # ADVANCE
    bash "$SCRIPT_DIR/advance.sh" "$STEP_ID" "$(date +%Y-%m-%d\ %H:%M)"
    bash "$SCRIPT_DIR/report.sh" "$STEP_ID" "advance" "pass" "" "$VERSION"

    # NEXT
    STEP_ID=$(get_next_step)
    [ "$MODE" = "step" ] && break
done

log "Pipeline v${VERSION} — DONE."
exit 0
