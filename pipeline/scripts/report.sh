#!/bin/bash
# scripts/pipeline/report.sh — append event entry to execution report
# Usage: report.sh <step_id> <phase> <result> <detail>
set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
source "$SCRIPT_DIR/lib.sh"

STEP_ID="${1:-}"
PHASE="${2:-}"  # pre-flight | dispatch | post-flight | gate | advance | skip | fail
RESULT="${3:-pass}"
DETAIL="${4:-}"

[ -n "$STEP_ID" ] || fail "Usage: report.sh <step_id> <phase> <result> [detail]"
VERSION="$(norm_version "${5:-}")"
REPORT="$ROOT_DIR/docs/pipeline/report-v${VERSION}.md"

TS=$(date +%H:%M:%S)
STEP_NAME=$(get_step_field "$STEP_ID" "name")
ICON="✓"

case "$RESULT" in
    pass|ok) ICON="✅" ;;
    fail) ICON="❌" ;;
    skip) ICON="⬚" ;;
    await) ICON="⏸" ;;
esac

case "$PHASE" in
    pre-flight)
        echo "" >> "$REPORT"
        echo "${TS} → ${STEP_ID} (${STEP_NAME}): pre-flight ${ICON}" >> "$REPORT"
        ;;
    dispatch)
        echo "${TS} → ${STEP_ID} (${STEP_NAME}): dispatch ${ICON}" >> "$REPORT"
        [ -n "$DETAIL" ] && echo "  ${DETAIL}" >> "$REPORT"
        ;;
    post-flight)
        echo "${TS} → ${STEP_ID} (${STEP_NAME}): post-flight ${ICON}" >> "$REPORT"
        [ -n "$DETAIL" ] && echo "  ${DETAIL}" >> "$REPORT"
        ;;
    gate)
        echo "${TS} → ${STEP_ID} (${STEP_NAME}): gate ${GATE_TYPE:-} ${ICON}" >> "$REPORT"
        [ -n "$DETAIL" ] && echo "  ${DETAIL}" >> "$REPORT"
        ;;
    advance)
        NEXT=$(get_step_field "$STEP_ID" "on_pass")
        if [ "$NEXT" = "null" ]; then NEXT="COMPLETE"; fi
        echo "${TS} → ${STEP_ID} (${STEP_NAME}): advance ${ICON}" >> "$REPORT"
        echo "  ${STEP_ID} → ${NEXT}" >> "$REPORT"
        [ -n "$DETAIL" ] && echo "  ${DETAIL}" >> "$REPORT"
        ;;
    skip)
        NEXT=$(get_step_field "$STEP_ID" "on_pass")
        if [ "$NEXT" = "null" ]; then NEXT="COMPLETE"; fi
        echo "${TS} → ${STEP_ID} (${STEP_NAME}): skip ⬚" >> "$REPORT"
        echo "  Already [x]." >> "$REPORT"
        [ -n "$DETAIL" ] && echo "  ${DETAIL}" >> "$REPORT"
        echo "  ⏭ ${STEP_ID} → ${NEXT}" >> "$REPORT"
        ;;
    fail)
        echo "${TS} → ${STEP_ID} (${STEP_NAME}): ${PHASE} ❌ FAIL" >> "$REPORT"
        [ -n "$DETAIL" ] && echo "  ${DETAIL}" >> "$REPORT"
        ;;
esac
