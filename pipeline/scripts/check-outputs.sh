#!/bin/bash
# scripts/pipeline/check-outputs.sh — post-flight validation
# Usage: check-outputs.sh <step_id> <version>
# Exit: 0 if all required outputs exist, 1 otherwise
set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
source "$SCRIPT_DIR/lib.sh"

STEP_ID="${1:-}"
VERSION="$(norm_version "${2:-}")"
[ -n "$STEP_ID" ] || fail "Usage: check-outputs.sh <step_id> [version]"

outputs_json=$(get_step_field "$STEP_ID" "outputs")
[ "$outputs_json" = "null" ] && log "No outputs required." && exit 0

failed=0
echo "$outputs_json" | python3 -c "
import json, os, sys
outputs = json.load(sys.stdin)
for out in outputs:
    if out.get('optional'):
        continue
    f = out['file'].replace('{version}', '$VERSION')
    resolved = os.path.join('$ROOT_DIR', f)
    ok = False
    if f.endswith('/'):
        ok = os.path.isdir(resolved) and os.listdir(resolved)
    else:
        ok = os.path.isfile(resolved) and os.path.getsize(resolved) > 0
    if ok:
        print(f'  ✓ {f}')
    else:
        print(f'  ✗ {f} — missing or empty')
        if not os.path.exists(resolved):
            print(f'    (resolved path: {resolved})')
        sys.exit(1)
" || failed=1

exit $failed
