#!/bin/bash
# scripts/pipeline/gate-boundary.sh — verify architecture boundary rules
# Usage: gate-boundary.sh <version>
# Checks: all 5 vital slots present in architecture.md
set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
source "$SCRIPT_DIR/lib.sh"

ARCH="$ROOT_DIR/docs/architecture.md"

slots=("State Manager" "Memory Engine" "Identity Vault" "Trace Log" "Model Driver")
failed=0

for slot in "${slots[@]}"; do
    if grep -q "$slot" "$ARCH" 2>/dev/null; then
        log "  ✓ $slot — present"
    else
        log "  ✗ $slot — MISSING from architecture.md"
        failed=1
    fi
done

# Check State Manager <4KB rule
if grep -q '<4KB\|< 4KB\|<4 KB' "$ARCH" 2>/dev/null; then
    log "  ✓ State <4KB rule — present"
else
    log "  ✗ State <4KB rule — MISSING"
    failed=1
fi

# Check credentials isolation
if grep -q 'credential\|JIT\|Vault' "$ARCH" 2>/dev/null; then
    log "  ✓ Credentials isolation — present"
else
    log "  ✗ Credentials isolation — MISSING"
    failed=1
fi

exit $failed
