#!/usr/bin/env bash
# fs-stop-hook.sh — Stop hook for Claude Code / pi agent.
# Blocks the agent from stopping if there are active tasks with unrecorded
# decisions/knowledge. Reads hook input from stdin (JSON).
#
# Install in settings.json:
#   "hooks": {
#     "Stop": [{
#       "hooks": [{
#         "type": "command",
#         "command": "skills/flagship/scripts/fs-stop-hook.sh"
#       }]
#     }]
#   }
#
# Behavior:
# - If stop_hook_active is true (already blocked once), let through to avoid loops.
# - Calls `fs status` and checks for warnings.
# - If bare_active_task warnings exist, blocks with a reason listing the task IDs.
# - Otherwise, passes through (exit 0, no output).

set -euo pipefail

# Read stdin into a variable.
INPUT=$(cat)

# If this is already a re-entry (agent was already blocked once by us),
# let it through to avoid infinite loops.
STOP_ACTIVE=$(echo "$INPUT" | python3 -c "
import sys, json
data = json.load(sys.stdin)
print(str(data.get('stop_hook_active', False)).lower())
" 2>/dev/null || echo "false")

if [ "$STOP_ACTIVE" = "true" ]; then
    exit 0
fi

# Check if fs is available.
if ! command -v fs &>/dev/null; then
    exit 0
fi

# Detect project from git root.
PROJECT=$(basename "$(git rev-parse --show-toplevel 2>/dev/null)" 2>/dev/null || echo "")
if [ -z "$PROJECT" ]; then
    exit 0
fi

# Get status and extract warnings.
STATUS=$(fs status --project "$PROJECT" 2>/dev/null || echo '{"ok":false}')

# Parse warnings using python3 (available on macOS).
RESULT=$(echo "$STATUS" | python3 -c "
import sys, json

data = json.load(sys.stdin)
if not data.get('ok'):
    sys.exit(0)

warnings = data.get('data', {}).get('warnings') or []
bare = [w for w in warnings if w['type'] == 'bare_active_task']

if not bare:
    sys.exit(0)

task_ids = []
for w in bare:
    task_ids.extend(w.get('task_ids', []))

reason = 'Active task(s) with no recorded decisions or knowledge: ' + ', '.join(task_ids) + '. Please record what you learned or decided before finishing. Use: fs task knowledge <id> --summary \"...\" or fs task update <id> --decision \"...\"'

print(json.dumps({
    'decision': 'block',
    'reason': reason
}))
" 2>/dev/null || echo "")

if [ -n "$RESULT" ]; then
    echo "$RESULT"
else
    exit 0
fi
