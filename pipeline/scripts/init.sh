#!/bin/bash
# scripts/pipeline/init.sh — bootstrap pipeline in a new project
# Usage: cd /path/to/project && bash scripts/pipeline/init.sh [version]
set -euo pipefail

VERSION="${1:-1}"
PLAN="docs/tasks/plan.md"
HEADER="## Pipeline Status: v${VERSION}
- [ ] PRD approved             —                  → step-1-prd
- [ ] Architecture updated      —                  → step-2a-architecture
- [ ] UI/UX updated             —                  → step-2b-designer
- [ ] Design reviewed           —                  → step-2-review
- [ ] Plan updated              —                  → step-3-planner
- [ ] Cross-review passed       —                  → step-4-cross-review
- [ ] QA test cases drafted     —                  → step-5-qa-design
- [ ] Development               —                  → step-6-dev
- [ ] QA verified               —                  → step-7-qa-exec"

# Create directories
mkdir -p docs/tasks docs/prd docs/qa docs/pipeline docs/ui-ux/screens docs/ui-ux/assets

# Seed plan.md header (preserve existing if any)
if [ -f "$PLAN" ] && grep -q "Pipeline Status" "$PLAN"; then
    echo "[init] plan.md already has pipeline header — skipping"
else
    if [ -f "$PLAN" ]; then
        # Prepend header to existing plan
        tmp=$(mktemp)
        echo "$HEADER" > "$tmp"
        echo "" >> "$tmp"
        cat "$PLAN" >> "$tmp"
        mv "$tmp" "$PLAN"
    else
        echo "# Action Plan" > "$PLAN"
        echo "" >> "$PLAN"
        echo "$HEADER" >> "$PLAN"
        echo "" >> "$PLAN"
    fi
    echo "[init] $PLAN — pipeline header seeded"
fi

echo "[init] Pipeline v${VERSION} ready."
echo "       Run: /pipeline-orchestrator run"
