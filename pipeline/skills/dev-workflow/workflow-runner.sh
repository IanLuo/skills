#!/usr/bin/env bash
set -euo pipefail
PLAN="docs/tasks/plan.md"
die() { echo "ERROR: $*" >&2; exit 1; }

plan_line_raw() { sed -n "${1}p" "$PLAN"; }
plan_state() {
    local line; line=$(plan_line_raw "$1")
    if   [[ "$line" =~ ^(\*|-)[[:space:]]*\[x\] ]];   then echo "done";
    elif [[ "$line" =~ ^(\*|-)[[:space:]]*\[⏳\] ]]; then echo "active";
    elif [[ "$line" =~ ^(\*|-)[[:space:]]*\[\ \] ]];  then echo "pending";
    else echo "unknown"; fi
}
plan_description() {
    plan_line_raw "$1" | sed -E 's/^[-*][[:space:]]*\[[^]]*\][[:space:]]*//'
}
plan_task_id() {
    plan_description "$1" | head -c 40
}

set_state() {
    local linenum="$1" state="$2" marker prefix current
    current=$(plan_line_raw "$linenum")
    if [[ "$current" =~ ^(\*|-) ]]; then prefix="${BASH_REMATCH[1]}"; else prefix="-"; fi
    case "$state" in pending) marker="$prefix [ ]";; active) marker="$prefix [⏳]";; done) marker="$prefix [x]";; *) die "bad state";; esac
    sed -i '' "${linenum}s/^[-*][[:space:]]*\[[^]]*\]/${marker}/" "$PLAN"
}
pending_tasks() { grep -n -E '^[-*][[:space:]]*\[\ \]' "$PLAN" || true; }
active_tasks()  { grep -n -E '^[-*][[:space:]]*\[⏳\]' "$PLAN" || true; }

cmd_status() {
    local line_num=1
    while IFS= read -r line; do
        local state
        if   [[ "$line" =~ ^(\*|-)[[:space:]]*\[x\] ]];   then state="done";
        elif [[ "$line" =~ ^(\*|-)[[:space:]]*\[⏳\] ]]; then state="active";
        elif [[ "$line" =~ ^(\*|-)[[:space:]]*\[\ \] ]];  then state="pending";
        else state="unknown"; fi
        if [[ "$state" != "unknown" ]]; then
            echo "${line_num}|${state}|$(echo "$line" | sed -E 's/^[-*][[:space:]]*\[[^]]*\][[:space:]]*//')"
        fi
        ((line_num++))
    done < "$PLAN"
}

cmd_next() {
    local active; active=$(active_tasks)
    if [[ -n "$active" ]]; then
        local aline; aline=$(echo "$active" | head -1 | cut -d: -f1)
        echo "STALE|${aline}|active|$(plan_description "$aline")"
        echo "HINT: run 'recover' or 'fail $aline' or 'done $aline'" >&2
        return 1
    fi
    local pending; pending=$(pending_tasks)
    if [[ -z "$pending" ]]; then echo "DONE"; return 0; fi
    local pline; pline=$(echo "$pending" | head -1 | cut -d: -f1)
    set_state "$pline" "active"
    echo "${pline}|active|$(plan_description "$pline")"
}

cmd_done() {
    local linenum="${1:?}"
    local state; state=$(plan_state "$linenum")
    [[ "$state" == "done" ]] && die "already done"
    set_state "$linenum" "done"
    if ! git diff --cached --quiet; then git commit -m "staged work for line $linenum"; fi
    if ! git diff --quiet; then git add -u && git commit -m "task: source changes for line $linenum"; fi
    git add "$PLAN" && git commit -m "task: mark line $linenum complete [x]" || true
}

cmd_fail() {
    local linenum="${1:?}"
    [[ "$(plan_state "$linenum")" != "active" ]] && die "not active"
    set_state "$linenum" "pending"
    git add "$PLAN" && git commit -m "task: line $linenum reverted to pending" || true
}

cmd_recover() {
    local active; active=$(active_tasks)
    [[ -z "$active" ]] && echo "No stale active tasks found." && return 0
    while IFS= read -r entry; do
        local linenum; linenum=$(echo "$entry" | cut -d: -f1)
        if ! git diff --quiet; then
            echo "RECOVERED: line $linenum — work detected, marking done"
            set_state "$linenum" "done"
        else
            echo "ORPHANED: line $linenum — no work, reverting"
            set_state "$linenum" "pending"
        fi
    done <<< "$active"
    git add "$PLAN" && git commit -m "recover: heal stale [⏳] tasks" || true
}

main() {
    case "${1:-status}" in
        status)  cmd_status ;;
        next)    cmd_next ;;
        done)    cmd_done "${2:-}" ;;
        fail)    cmd_fail "${2:-}" ;;
        recover) cmd_recover ;;
        *)       die "usage: status|next|done N|fail N|recover" ;;
    esac
}
main "$@"
