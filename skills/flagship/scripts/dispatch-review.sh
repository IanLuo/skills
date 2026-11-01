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
# A task is dispatched when it is a review task, open, held by nobody, and its
# development task exists, was claimed by someone, and is not already closed.
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
# polls the review task until a session holds it. A prompt that was "submitted"
# into an agent that never claimed is a failed dispatch, and says so.
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
#   --wait SECONDS  how long to wait for each reviewer to CLAIM (default: 180)
#   --dry-run       print the dispatch, spawn nothing, change nothing
#
# Exit: 0 every candidate claimed · 1 refused, or a spawned reviewer never claimed

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

# The candidates, straight from fs: no cache, no second store, one read.
candidates="$(fs status --project "$PROJECT" 2>/dev/null | jq -r '
  [.data.projects[].tasks[]] as $t
  | ($t | map(select(.kind == "dev" and .state != "done" and (.session != null)))) as $dev
  | $t[]
  | select(.kind == "review" and .state == "open" and (.session == null))
  | . as $r
  | ($dev[] | select(.task == $r.parent_dev)) as $d
  | [$r.task, $d.task, ($r.checks | join(" ; ")), $d.agent.state] | @tsv')" \
  || die "fs status failed for project $PROJECT"

if [ -z "$candidates" ]; then
  printf 'nothing to dispatch in %s: no unheld review task whose development task is open and was claimed\n' "$PROJECT"
  exit 0
fi

# claimed polls fs until the task is held by a session, and returns that session.
claimed() { # claimed <task>
  local task="$1" i=0 sid=""
  while [ "$i" -lt "$WAIT_S" ]; do
    sid="$(fs status --project "$PROJECT" 2>/dev/null \
      | jq -r --arg t "$task" '.data.projects[].tasks[] | select(.task == $t) | .session // empty')"
    [ -n "$sid" ] && { printf '%s' "$sid"; return 0; }
    sleep 2; i=$((i + 2))
  done
  return 1
}

rc=0
while IFS=$'\t' read -r task dev checks dstate; do
  [ -n "$task" ] || continue
  sid="rev-${task#t-}-$(date -u +%Y%m%dT%H%M%SZ)"
  name="review-${task#t-}"
  dir="$CWD_DIR"
  [ -n "$COMMIT" ] && dir="$CWD_DIR/.worktrees/$name"

  prompt="You are the independent reviewer for fs task $task in project $PROJECT. You did not write this code and you do not have the author's context; do not trust the task text. In order: (1) ${ENVP}$FS claim --project $PROJECT --session $sid --task $task  (2) run every declared check yourself, exactly as declared: $checks — after each run, record it: ${ENVP}$FS report --project $PROJECT --session $sid --task $task --state pass --role review --command \"<the exact command you ran>\" --exit <the real exit code>, using --state fail when the exit was non-zero. (3) Only if every check passed: ${ENVP}$FS done --project $PROJECT --session $sid --task $task . Never report a check you did not run, and never repeat a result from the task or from another session. If a check fails, report it, say why, and stop: the task stays incomplete."

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

  if got="$(claimed "$task")"; then
    printf 'dispatched %s → %s in pane %s: claimed as %s\n' "$task" "$name" "$pane" "$got"
  else
    printf 'FAILED %s: %s was started in pane %s but never claimed the task within %ss\n' \
      "$task" "$name" "$pane" "$WAIT_S" >&2
    printf '  inspect: herdr agent read %s --lines 40\n' "$name" >&2
    printf '  the task is still open and unheld; nothing was written\n' >&2
    rc=1
  fi
done <<< "$candidates"
exit "$rc"
