#!/usr/bin/env bash
#
# dispatch-review.sh — hand open review tasks to fresh reviewer agents.
#
# Why this lives here and not in fs: fs refuses to start processes, on purpose.
# The session that wrote the code must not choose the session that reviews it, or
# the review can inherit the author's context — the exact failure the review gate
# exists to prevent (spec R2; R3 puts "fs starting or managing agent processes"
# out of scope). So the *coordinator* dispatches, and fs still decides: `done` is
# refused unless the reviewer really ran every declared check, recorded the exact
# command and its exit code, and each one passed.
#
# A task is dispatched when it is a review task and open, and its development task
# exists, was claimed by someone, and is not already closed. Held-ness is not the
# test: a lapsed claim leaves the old holder in `session`, and that task is the one
# that most needs a fresh reviewer.
#
# The parent's last report is NOT the trigger. fs collapses any observation older
# than the 120 s lease to `unknown` — correctly, since a stale heartbeat is not
# evidence — so waiting for "parent reported dev-done" would silently starve every
# review that waits more than two minutes. Creating the review task is the ready
# signal (a coordinator adds it with `--review-of` when the work is up for review),
# and a reviewer that finds unfinished work reports `fail`: the gate is the arbiter,
# and a refused review is cheap and honest.
#
# Success is decided by fs, not by the spawn: after prompting, the dispatcher
# polls the review task until it is `done`. A prompt that was "submitted" into an
# agent that never claimed, or claimed and then let its 120 s lease lapse, is a
# failed dispatch, and says so.
#
# Usage:
#   dispatch-review.sh --project demo --dry-run          # show what it would spawn
#   dispatch-review.sh --project demo --commit <sha>     # real run, detached worktree
#
# Flags:
#   --project P     required — the fs project scope to dispatch in
#   --commit SHA    the reviewed commit; each reviewer gets `git worktree --detach SHA`
#   --cwd DIR       where reviewers run when --commit is absent (default: $PWD)
#   --kind K        agent kind for the pane (default: pi; claude | codex also work)
#   --wait SECONDS  how long to wait for each reviewer to CLOSE its task (default: 180)
#   --dry-run       print the dispatch, spawn nothing, change nothing
#
# Exit: 0 every candidate closed · 1 refused, or a spawned reviewer did not close its task

set -euo pipefail

PROJECT=""
COMMIT=""
CWD_DIR="${PWD}"
KIND="pi"
WAIT_S=180
DRY_RUN=0

die() { printf 'dispatch-review: %s\n' "$*" >&2; exit 1; }

while [ $# -gt 0 ]; do
  case "$1" in
    --project) PROJECT="${2:-}"; shift 2 ;;
    --commit)  COMMIT="${2:-}";  shift 2 ;;
    --cwd)     CWD_DIR="${2:-}"; shift 2 ;;
    --kind)    KIND="${2:-}";    shift 2 ;;
    --wait)    WAIT_S="${2:-}";  shift 2 ;;
    --dry-run) DRY_RUN=1; shift ;;
    -h|--help) sed -n '2,/^set -euo/p' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
    *) die "unknown flag: $1" ;;
  esac
done

[ -n "$PROJECT" ] || die "--project is required"
FS="$(command -v fs)" || die "fs is not on PATH"
if [ "$DRY_RUN" -eq 0 ] && [ "${HERDR_ENV:-}" != "1" ]; then
  die "not inside herdr (HERDR_ENV != 1); a human can read the same state with: fs status --project $PROJECT --format table"
fi

# The reviewer must read the same store the task lives in. A spawned pane inherits
# the herdr server's environment, not this shell's, so MEMSTORE_DB is passed in the
# prompt; without it the reviewer reads ~/.memstore/memory.db, finds no such task,
# and correctly refuses — a silent, confusing failure.
ENVP=""
if [ -n "${MEMSTORE_DB:-}" ]; then ENVP="MEMSTORE_DB=$MEMSTORE_DB "; fi

# The candidates, straight from fs: no cache, no second store, one read. A review
# qualifies when it is `open`, which is also what a lapsed claim reads: the old
# holder stays in `session` (expiry is not erasure), so `session == null` would
# skip exactly the task that most needs a fresh reviewer.
candidates="$(fs status --project "$PROJECT" 2>/dev/null | jq -r '
  [.data.projects[].tasks[]] as $t
  | ($t | map(select(.kind == "dev" and .state != "done" and (.session != null)))) as $dev
  | $t[]
  | select(.kind == "review" and .state == "open")
  | . as $r
  | ($dev[] | select(.task == $r.parent_dev)) as $d
  | [$r.task, $d.task, ($r.checks | join(" ; ")), $d.agent.state, ($r.project_path // "")] | @tsv')" \
  || die "fs status failed for project $PROJECT"

if [ -z "$candidates" ]; then
  printf 'nothing to dispatch in %s: no open review task whose development task is open and was claimed\n' "$PROJECT"
  exit 0
fi

# The reviewer's directory is --cwd or the --commit worktree; fs's recorded
# project_path is not used. Without --commit those must agree: sending a reviewer
# to the wrong tree runs its declared checks against the wrong code, and a check
# that passes there is evidence of nothing. Compare physically, so /tmp and
# /private/tmp are one directory, and refuse before printing or spawning anything.
if [ -z "$COMMIT" ]; then
  here="$(cd "$CWD_DIR" 2>/dev/null && pwd -P)" || here=""
  if [ -n "$here" ]; then
    while IFS=$'\t' read -r ctid cpath; do
      [ -n "$ctid" ] || continue
      [ "${cpath#/}" != "$cpath" ] || continue           # relative: nothing to compare
      there="$(cd "$cpath" 2>/dev/null && pwd -P)" || continue   # gone: cannot verify
      [ "$there" = "$here" ] || die "$ctid records $cpath, but reviewers would run in $CWD_DIR; pass --cwd $cpath (or --commit), because a check run in the wrong tree proves nothing"
    done <<< "$(printf '%s\n' "$candidates" | awk -F'\t' 'NF >= 5 { printf "%s\t%s\n", $1, $5 }')"
  fi
fi

# closed polls fs until the task is closed by a passing review, and says so. The
# claim is not success: a reviewer that claims, runs the checks, and then reads
# past its 120 s lease reopens the task, and "claimed" would have called that a
# finished dispatch.
closed() { # closed <task>
  local task="$1" i=0 st=""
  while [ "$i" -lt "$WAIT_S" ]; do
    st="$(fs status --project "$PROJECT" 2>/dev/null \
      | jq -r --arg t "$task" '.data.projects[].tasks[] | select(.task == $t) | .state')"
    [ "$st" = "done" ] && return 0
    sleep 2; i=$((i + 2))
  done
  printf ' (state: %s)' "${st:-unknown}" >&2
  return 1
}

rc=0
while IFS=$'\t' read -r task dev checks dstate tpath; do
  [ -n "$task" ] || continue
  stamp="$(date -u +%Y%m%dT%H%M%SZ)"
  sid="rev-${task#t-}-$stamp"
  # The name carries a short stamp: a re-dispatch of the same task must not collide
  # with the pane a lapsed reviewer left behind, and herdr caps a name at 32
  # characters — "review-" + 16 hex + "-" + 6 leaves two to spare.
  name="review-${task#t-}-$(date -u +%H%M%S)"
  dir="$CWD_DIR"
  [ -n "$COMMIT" ] && dir="$CWD_DIR/.worktrees/$name"

  prompt="You are the independent reviewer for fs task $task in project $PROJECT. You did not write this code and you do not have the author's context; do not trust the task text. In order: (1) ${ENVP}$FS claim --project $PROJECT --session $sid --task $task  (2) run every declared check yourself, exactly as declared: $checks — after each run, record it: ${ENVP}$FS report --project $PROJECT --session $sid --task $task --state pass --role review --command \"<the exact command you ran>\" --exit <the real exit code>, using --state fail when the exit was non-zero. (3) Your lease lapses 120 s after your newest write, and a lapsed lease cannot be renewed: if you still need to read anything after the checks, write ${ENVP}$FS report --project $PROJECT --session $sid --task $task --state progress --note "reviewing" at least every 90 s, then (4) only if every check passed: ${ENVP}$FS done --project $PROJECT --session $sid --task $task . Never report a check you did not run, and never repeat a result from the task or from another session. If a check fails, report it, say why, and stop: the task stays incomplete."

  if [ "$DRY_RUN" -eq 1 ]; then
    printf '\n[dry run] %s (reviews %s; parent last reported: %s)\n' "$task" "$dev" "$dstate"
    printf '  session: %s\n' "$sid"
    printf '  dir:     %s%s\n' "$dir" "${COMMIT:+ (git worktree add --detach $dir $COMMIT)}"
    printf '  pane:    herdr pane split --current --direction down --cwd "%s" --no-focus\n' "$dir"
    printf '  start:   herdr agent start %s --kind %s --pane <pane>\n' "$name" "$KIND"
    printf '  prompt:  %s\n' "$prompt"
    continue
  fi

  [ -n "$COMMIT" ] && { git worktree add --detach "$dir" "$COMMIT" >/dev/null || die "cannot create $dir at $COMMIT"; }
  pane="$(herdr pane split --current --direction down --cwd "$dir" --no-focus | jq -r '.result.pane.pane_id')" \
    || die "cannot split a pane for $task"
  [ -n "$pane" ] && [ "$pane" != "null" ] || die "herdr returned no pane for $task"
  herdr agent start "$name" --kind "$KIND" --pane "$pane" >/dev/null || die "cannot start $KIND in $pane"

  # agent_prompt_stalled means the prompt was accepted but the agent did not start
  # working within herdr's 5 s window. That is a hint, not a verdict: the claim in
  # fs below is the verdict.
  herdr agent prompt "$name" "$prompt" --wait --timeout 30000 >/dev/null 2>&1 || true

  if closed "$task"; then
    printf 'closed %s → %s in pane %s: reviewed and completed\n' "$task" "$name" "$pane"
  else
    printf 'FAILED %s: %s was started in pane %s but did not close the task within %ss\n' \
      "$task" "$name" "$pane" "$WAIT_S" >&2
    printf '  inspect: herdr agent read %s --lines 40\n' "$name" >&2
    printf '  the task stays incomplete; a lapsed lease cannot be renewed, so re-dispatch for a fresh reviewer\n' >&2
    rc=1
  fi
done <<< "$candidates"
exit "$rc"
