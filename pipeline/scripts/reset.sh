#!/bin/bash
# scripts/pipeline/reset.sh — reset pipeline header for a fresh run
# Usage: reset.sh [version]
set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
source "$SCRIPT_DIR/lib.sh"

VERSION="$(norm_version "${1:-}")"
PLAN="$ROOT_DIR/docs/tasks/plan.md"

# Replace all [x] with [ ] in the pipeline header
sed -i '' '/^## Pipeline Status:/,/^$/{ s/^- \[x\]/- [ ]/; s/20[0-9][0-9]-[0-9][0-9]-[0-9][0-9] [0-9][0-9]:[0-9][0-9]/ —/; s/   → step-/                  → step-/; }' "$PLAN_MD"

# Clean up malformed lines
sed -i '' 's/^- \[ \]  —                  → /- [ ] /' "$PLAN_MD"

rm -f "$ROOT_DIR/docs/pipeline/report-v${VERSION}.md"
log "Pipeline v${VERSION} header reset. All steps → [ ]"
log "Report cleared: docs/pipeline/report-v${VERSION}.md"
