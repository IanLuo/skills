#!/bin/bash
# scripts/pipeline/gate-traceability.sh — verify every test case maps to plan + PRD
# Usage: gate-traceability.sh <version>
set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
source "$SCRIPT_DIR/lib.sh"

VERSION="$(norm_version "${1:-}")"
TEST_PLAN="$ROOT_DIR/docs/qa/test-plan-v${VERSION}.md"

[ -f "$TEST_PLAN" ] || fail "Test plan not found: $TEST_PLAN"

# Count test cases
tc_count=$(grep -c '^## TC-' "$TEST_PLAN" 2>/dev/null || echo 0)

# Count Plan Task refs
pt_count=$(grep -c '\*\*Plan Task:\*\*' "$TEST_PLAN" 2>/dev/null || echo 0)

# Count PRD Criterion refs
prd_count=$(grep -c '\*\*PRD Criterion:\*\*' "$TEST_PLAN" 2>/dev/null || echo 0)

log "  Test cases: $tc_count"
log "  Plan Task refs: $pt_count"
log "  PRD Criterion refs: $prd_count"

failed=0
if [ "$tc_count" -eq 0 ]; then
    log "  ✗ No test cases found"
    failed=1
fi
if [ "$pt_count" -lt "$tc_count" ]; then
    log "  ✗ Missing Plan Task refs: $pt_count < $tc_count"
    failed=1
else
    log "  ✓ All $tc_count tests have Plan Task refs"
fi
if [ "$prd_count" -lt "$tc_count" ]; then
    log "  ✗ Missing PRD Criterion refs: $prd_count < $tc_count"
    failed=1
else
    log "  ✓ All $tc_count tests have PRD Criterion refs"
fi

exit $failed
