#!/bin/bash
# scripts/pipeline/lib.sh — shared functions for all pipeline scripts
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
ROOT_DIR="$(cd "$SCRIPT_DIR/../.." && pwd)"
PIPELINE_JSON="$SCRIPT_DIR/pipeline.json"
SCHEMA_DIR="$SCRIPT_DIR/schemas"
PLAN_MD="$ROOT_DIR/docs/tasks/plan.md"
REPORT_MD="$ROOT_DIR/docs/pipeline/report"

PLAN_MD="${PLAN_MD:-$ROOT_DIR/docs/tasks/plan.md}"

log() { echo "[pipeline] $*" >&2; }
fail() { echo "[pipeline] FAIL: $*" >&2; exit 1; }

# Read version from plan.md header, strip 'v' prefix
get_version() {
    grep -m1 '^## Pipeline Status: v' "$PLAN_MD" | sed 's/.*v\([0-9]*\).*/\1/'
}

# Normalize version: strip 'v' prefix if present, default to get_version
norm_version() {
    local v="${1:-}"
    [ -z "$v" ] && v=$(get_version)
    echo "${v#v}"
}

# Get first [ ] step from plan.md header
get_next_step() {
    grep '^- \[ \]' "$PLAN_MD" | head -1 | grep -o 'step-[a-z0-9-]*' || echo ""
}

# Get step definition from pipeline.json (basic jq-free grep)
get_step_field() {
    local step_id="$1" field="$2"
    python3 -c "
import json, sys
with open('$PIPELINE_JSON') as f:
    data = json.load(f)
for s in data['steps']:
    if s['id'] == '$step_id':
        val = s.get('$field')
        if isinstance(val, list):
            print(json.dumps(val))
        elif val is None:
            print('null')
        else:
            print(val)
        break
" 2>/dev/null
}

# Substitute {version} in a path
resolve_path() {
    local path="$1" version="$2"
    echo "$path" | sed "s/{version}/$version/g"
}

# Check file exists and non-empty
file_ok() {
    [ -f "$1" ] && [ -s "$1" ]
}

# Check directory exists and non-empty
dir_ok() {
    [ -d "$1" ] && [ "$(ls -A "$1" 2>/dev/null)" ]
}

# Get file status for report
file_status() {
    local file="$1" step_output="$2"
    if [ ! -f "$file" ]; then
        echo "MISSING"
    elif [ "$step_output" = "pre" ]; then
        echo "OK ($(du -h "$file" | cut -f1))"
    else
        echo "OK ($(du -h "$file" | cut -f1))"
    fi
}
