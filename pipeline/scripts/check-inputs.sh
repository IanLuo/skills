#!/bin/bash
# scripts/pipeline/check-inputs.sh — pre-flight validation
# Usage: check-inputs.sh <step_id> <version>
# Exit: 0 if all inputs exist, 1 otherwise
set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
source "$SCRIPT_DIR/lib.sh"

STEP_ID="${1:-}"
VERSION="$(norm_version "${2:-}")"
[ -n "$STEP_ID" ] || fail "Usage: check-inputs.sh <step_id> [version]"

inputs_json=$(get_step_field "$STEP_ID" "inputs")
[ "$inputs_json" = "null" ] && exit 0  # no inputs required

failed=0
echo "$inputs_json" | python3 -c "
import json, sys, os
inputs = json.load(sys.stdin)
for inp in inputs:
    f = inp['file'].replace('{version}', '$VERSION')
    resolved = os.path.join('$ROOT_DIR', f)
    if f.endswith('/'):
        ok = os.path.isdir(resolved) and os.listdir(resolved)
    else:
        ok = os.path.isfile(resolved) and os.path.getsize(resolved) > 0
    print(f'  {\"✓\" if ok else \"✗\"} {f}')
    if not ok:
        sys.exit(1)
" || failed=1

exit $failed
