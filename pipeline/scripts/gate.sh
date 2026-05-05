#!/bin/bash
# scripts/pipeline/gate.sh — route to correct gate script
# Usage: gate.sh <step_id> <gate_type> <version>
set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
source "$SCRIPT_DIR/lib.sh"

STEP_ID="${1:-}"
GATE="${2:-}"
VERSION="$(norm_version "${3:-}")"

[ -n "$STEP_ID" ] || fail "Usage: gate.sh <step_id> <gate_type> [version]"
[ -n "$GATE" ] || fail "Usage: gate.sh <step_id> <gate_type> [version]"

log "Running gate: $GATE for $STEP_ID"

case "$GATE" in
    boundary_check)
        bash "$SCRIPT_DIR/gate-boundary.sh" "$VERSION"
        ;;
    traceability)
        bash "$SCRIPT_DIR/gate-traceability.sh" "$VERSION"
        ;;
    schema_validation)
        bash "$SCRIPT_DIR/gate-schema.sh" "$STEP_ID" "$VERSION"
        ;;
    *)
        log "Gate type '$GATE' not implemented yet — passing (manual check required)"
        ;;
esac
