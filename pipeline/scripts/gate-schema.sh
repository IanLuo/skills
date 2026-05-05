#!/bin/bash
# scripts/pipeline/gate-schema.sh — validate output files against declared schemas
# Usage: gate-schema.sh <step_id> <version>
set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
source "$SCRIPT_DIR/lib.sh"

STEP_ID="${1:-}"
VERSION="$(norm_version "${2:-}")"
[ -n "$STEP_ID" ] || fail "Usage: gate-schema.sh <step_id> <version>"

outputs_json=$(get_step_field "$STEP_ID" "outputs")

# No outputs to validate → pass
if [ "$outputs_json" = "null" ]; then
    log "  ✓ No outputs declared — nothing to validate"
    exit 0
fi

failed=0
echo "$outputs_json" | python3 -c "
import json, os, sys, re

outputs = json.load(sys.stdin)
version = '$VERSION'
root_dir = '$ROOT_DIR'
schema_dir = '$SCHEMA_DIR'

# Schema → required markdown sections mapping
schema_sections = {
    'prd.schema.json': [
        '# PRD v',
        '## Current State',
        '## Changes Required',
        '## Acceptance Criteria',
    ],
    'test-plan.schema.json': [
        '# Test Plan v',
        '## Traceability Matrix',
    ],
    'qa-report.schema.json': [
        '# QA Report v',
        '## Result',
        '## Test Results',
    ],
    'plan-header.schema.json': [
        '## Pipeline Status: v',
        '- [',
    ],
}

for out in outputs:
    if out.get('optional'):
        continue
    f = out['file'].replace('{version}', version)
    resolved = os.path.join(root_dir, f)
    
    schema_name = out.get('schema', '').split('/')[-1] if out.get('schema') else ''
    
    if not os.path.isfile(resolved):
        print(f'  ✗ {f} — file not found (schema validation skipped)')
        sys.exit(1)
    
    content = open(resolved).read()
    
    if schema_name and schema_name in schema_sections:
        for pattern in schema_sections[schema_name]:
            if pattern not in content:
                print(f'  ✗ {f} — missing required section: \"{pattern}\" (per {schema_name})')
                sys.exit(1)
        print(f'  ✓ {f} — passes {schema_name}')
    else:
        # No schema defined or unknown schema — just check non-empty
        if len(content.strip()) > 0:
            print(f'  ✓ {f} — non-empty (no schema to validate against)')
        else:
            print(f'  ✗ {f} — empty file')
            sys.exit(1)
" || failed=1

exit $failed
