#!/usr/bin/env bash
#
# dispatch-review.sh — run review rounds: give each review that needs a reviewer to
# a fresh agent, and close that agent once its verdict is in.
#
# Why this lives here and not in fs: fs refuses to start processes, on purpose.
# The session that wrote the code must not choose the session that reviews it, or
# the review can inherit the author's context — the exact failure the review gate
# exists to prevent (spec R2; R3 puts "fs starting or managing agent processes"
# out of scope). So the *coordinator* dispatches, and fs still decides: `done` is
# refused unless the reviewer really ran every declared check, recorded the exact
# command and its exit code, and each one passed.
#
# A round. A review task needs a reviewer when either
#   - no reviewer has reported on it yet (the review task is the ready signal: a
#     coordinator adds it with --review-of when the work is up for review), or
#   - its last reviewer reported fail or blocked, and the developer has reported
#     dev-done since. That is "the feedback was implemented". Until then a new
#     reviewer would only re-read the same code and fail it again.
# A review whose reviewer is still live (claimed) is never dispatched over.
#
# The verdict. A reviewer's verdict is in fs: the task is done, or the reviewer
# wrote fail/blocked under its own session. When it is in, the dispatcher closes the
# reviewer's pane and removes its worktree, so a finished round leaves nothing
# running. A reviewer that gives no verdict within --wait seconds is closed too,
# after its last output is printed to stderr for inspection. Panes do not pile up.
#
# Freshness is not the trigger. fs collapses any observation older than the 120 s
# lease to `unknown`, so the test compares the ages of the two reports with each
# other, never with the clock. Waiting for a fresh dev-done would starve every
# review that waits more than two minutes.
#
# Success is decided by fs, not by the spawn: a round is closed when fs shows the
# verdict. A prompt that was "submitted" into an agent that never claimed, or claimed
# and then let its 120 s lease lapse, is a failed dispatch, and says so.
#
# Usage:
#   dispatch-review.sh --project demo --dry-run                 # what would be dispatched
#   dispatch-review.sh --project demo --commit HEAD             # one round, now
#   dispatch-review.sh --project demo --commit HEAD --watch 30  # rounds, until stopped
#
# Flags:
#   --project P     required — the fs project scope to dispatch in
#   --commit REF    the reviewed commit; HEAD is resolved at each round, and each
#                   reviewer gets `git worktree --detach <commit>`
#   --cwd DIR       where reviewers run when --commit is absent (default: $PWD)
#   --kind K        agent kind for the pane (default: pi; claude | codex also work)
#   --wait SECONDS  how long a reviewer has to give its verdict (default: 180)
#   --watch SECONDS run rounds every SECONDS (needs --commit HEAD; not with --dry-run)
#   --dry-run       print the dispatch, spawn nothing, change nothing
#
# Exit: 0 every dispatched reviewer gave a verdict · 1 refused, or a reviewer gave
# none within --wait. With --watch the exit code is not used: it runs until stopped.

set -euo pipefail

PROJECT=""
COMMIT=""
CWD_DIR="${PWD}"
KIND="pi"
WAIT_S=180
WATCH_S=0
DRY_RUN=0
FAILED=0

die() { printf 'dispatch-review: %s\n' "$*" >&2; exit 1; }

while [ $# -gt 0 ]; do
  case "$1" in
    --project) PROJECT="${2:-}"; shift 2 ;;
    --commit)  COMMIT="${2:-}";  shift 2 ;;
    --cwd)     CWD_DIR="${2:-}"; shift 2 ;;
    --kind)    KIND="${2:-}";    shift 2 ;;
    --wait)    WAIT_S="${2:-}";  shift 2 ;;
    --watch)   WATCH_S="${2:-}"; shift 2 ;;
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
if [ "$WATCH_S" -ne 0 ]; then
  [ "$DRY_RUN" -eq 0 ] || die "--dry-run does not watch"
  [ "$COMMIT" = HEAD ] || die "--watch needs --commit HEAD: a fixed commit would give round 2 the code round 1 reviewed"
fi
CWD_DIR="$(cd "$CWD_DIR" 2>/dev/null && pwd -P)" || die "--cwd is not a directory"

# The reviewer must read the same store the task lives in. A spawned pane inherits
# the herdr server's environment, not this shell's, so MEMSTORE_DB is passed in the
# prompt; without it the reviewer reads ~/.memstore/memory.db, finds no such task,
# and correctly refuses — a silent, confusing failure.
ENVP=""
if [ -n "${MEMSTORE_DB:-}" ]; then ENVP="MEMSTORE_DB=$MEMSTORE_DB "; fi

# pending prints one TSV row per review that needs a reviewer this round:
# task, developer task, checks, recorded path, last reviewer verdict. It reads fs
# only: no cache, no second store, one read.
pending() {
  fs status --project "$PROJECT" 2>/dev/null | jq -r '
    [.data.projects[].tasks[]] as $t
    | ($t | map(select(.kind == "dev" and .state != "done" and (.session != null)))) as $dev
    | $t[]
    | select(.kind == "review" and .state == "open")
    | . as $r
    | ($dev[] | select(.task == $r.parent_dev)) as $d
    | ([$r.evidence[]? | select(.role == "review")][0]) as $last
    | ([$r.evidence[]? | select(.role == "review" and (.state == "fail" or .state == "blocked"))][0]) as $rv
    | ([$d.evidence[]? | select(.role == "dev")][0]) as $dv
    | select($rv == null
        or ($dv != null and $dv.state == "dev-done" and $dv.age_seconds < $rv.age_seconds))
    | [$r.task, $d.task, ($r.checks | join(" ; ")), ($r.project_path // "-"), ($last.state // "none")]
    | @tsv'
}

# await polls fs until the reviewer under <session> gives its verdict. It prints
# done (the task closed), or fail (the reviewer wrote fail or blocked), and returns 1
# when neither arrives within --wait. The claim is not a verdict: a reviewer that
# claims, runs the checks, and reads past its 120 s lease reopens the task.
await() { # await <task> <session>
  local task="$1" sid="$2" i=0 out=""
  while [ "$i" -lt "$WAIT_S" ]; do
    out="$(fs status --project "$PROJECT" 2>/dev/null | jq -r --arg t "$task" --arg s "$sid" '
      .data.projects[].tasks[] | select(.task == $t)
      | if .state == "done" then "done"
        elif any(.evidence[]?; .session == $s and .role == "review" and (.state == "fail" or .state == "blocked")) then "fail"
        else empty end')" || true
    if [ -n "$out" ]; then printf '%s' "$out"; return 0; fi
    sleep 2; i=$((i + 2))
  done
  return 1
}

# retire closes a reviewer's pane and removes its worktree. The worktree is removed
# without --force: a reviewer that left changes in it keeps them, and says so.
retire() { # retire <pane> <dir> <commit>
  local pane="$1" dir="$2" commit="$3"
  herdr pane close "$pane" >/dev/null 2>&1 || true
  if [ -n "$commit" ] && ! git -C "$CWD_DIR" worktree remove "$dir" >/dev/null 2>&1; then
    printf 'kept %s: it has changes a reviewer left; remove it by hand\n' "$dir" >&2
  fi
  return 0
}

dispatch_one() { # dispatch_one <task> <dev> <checks> <commit> <last verdict>
  local task="$1" dev="$2" checks="$3" commit="$4" last="$5"
  local stamp sid name dir pane verdict prompt
  stamp="$(date -u +%Y%m%dT%H%M%SZ)"
  sid="rev-${task#t-}-$stamp"
  # The name carries a short stamp: a re-dispatch of the same task must not collide
  # with a pane still open from an earlier round, and herdr caps a name at 32
  # characters — "review-" + 16 hex + "-" + 6 leaves two to spare.
  name="review-${task#t-}-$(date -u +%H%M%S)"
  dir="$CWD_DIR"
  if [ -n "$commit" ]; then dir="$CWD_DIR/.worktrees/$name"; fi

  prompt="You are the independent reviewer for fs task $task in project $PROJECT. You did not write this code and you do not have the author's context; do not trust the task text. You review once, then stop. In order: (1) ${ENVP}$FS claim --project $PROJECT --session $sid --task $task  (2) run every declared check yourself, exactly as declared: $checks — after each run, record it: ${ENVP}$FS report --project $PROJECT --session $sid --task $task --role review --state pass --command \"<the exact command you ran>\" --exit <the real exit code>, using --state fail when the exit was non-zero. (3) If you still have to read anything after the checks, write ${ENVP}$FS report --project $PROJECT --session $sid --task $task --role review --state progress --note \"reviewing\" at least every 90 s; your lease lapses 120 s after your newest write and cannot be renewed. (4) If every check passed: ${ENVP}$FS done --project $PROJECT --session $sid --task $task, then stop. (5) If a check failed, or you could not run one: report --role review with --state fail (or blocked) and a --note saying what is wrong, then stop. Do not fix the code and do not run done: your verdict is the feedback, the developer implements it, and a new reviewer is dispatched. Never report a check you did not run, and never repeat a result from the task or from another session."

  if [ "$DRY_RUN" -eq 1 ]; then
    printf '\n[dry run] %s (reviews %s; last reviewer verdict: %s)\n' "$task" "$dev" "$last"
    printf '  session: %s\n' "$sid"
    printf '  dir:     %s%s\n' "$dir" "${commit:+ (git worktree add --detach $dir $commit)}"
    printf '  pane:    herdr pane split --current --direction down --cwd "%s" --no-focus\n' "$dir"
    printf '  start:   herdr agent start %s --kind %s --pane <pane>\n' "$name" "$KIND"
    printf '  prompt:  %s\n' "$prompt"
    return 0
  fi

  if [ -n "$commit" ]; then
    git -C "$CWD_DIR" worktree add --detach "$dir" "$commit" >/dev/null || die "cannot create $dir at $commit"
  fi
  pane="$(herdr pane split --current --direction down --cwd "$dir" --no-focus | jq -r '.result.pane.pane_id')" \
    || die "cannot split a pane for $task"
  [ -n "$pane" ] && [ "$pane" != "null" ] || die "herdr returned no pane for $task"
  herdr agent start "$name" --kind "$KIND" --pane "$pane" >/dev/null || die "cannot start $KIND in $pane"

  # agent_prompt_stalled means the prompt was accepted but the agent did not start
  # working within herdr's 5 s window. That is a hint, not a verdict: the verdict in
  # fs below is the verdict.
  herdr agent prompt "$name" "$prompt" --wait --timeout 30000 >/dev/null 2>&1 || true

  if verdict="$(await "$task" "$sid")"; then
    retire "$pane" "$dir" "$commit"
    if [ "$verdict" = done ]; then
      printf 'closed %s → %s in pane %s: reviewed and completed\n' "$task" "$name" "$pane"
    else
      printf 'reviewed %s → %s in pane %s: failed, feedback recorded, pane closed. The next round starts when %s reports dev-done\n' \
        "$task" "$name" "$pane" "$dev"
    fi
  else
    { herdr agent read "$name" --lines 40 2>/dev/null || true; } >&2
    retire "$pane" "$dir" "$commit"
    printf 'FAILED %s: %s gave no verdict within %ss; pane closed, the task stays open for the next round\n' \
      "$task" "$name" "$WAIT_S" >&2
    FAILED=1
  fi
  return 0
}

pass() { # one round over every review that needs a reviewer
  local candidates commit="" here task dev checks path last
  candidates="$(pending)" || die "fs status failed for project $PROJECT"
  if [ -z "$candidates" ]; then
    printf 'nothing to dispatch in %s: no review is waiting for a reviewer\n' "$PROJECT"
    return 0
  fi

  if [ -n "$COMMIT" ]; then
    if [ "$COMMIT" = HEAD ]; then
      commit="$(git -C "$CWD_DIR" rev-parse --verify --quiet 'HEAD^{commit}')" || die "$CWD_DIR has no HEAD commit"
    else
      commit="$COMMIT"
    fi
  fi

  # The reviewer's directory is --cwd or the --commit worktree; fs's recorded
  # project_path is not used. Without --commit those must agree: sending a reviewer
  # to the wrong tree runs its declared checks against the wrong code, and a check
  # that passes there is evidence of nothing. Compare physically, so /tmp and
  # /private/tmp are one directory, and refuse before spawning anything.
  if [ -z "$commit" ]; then
    here="$(cd "$CWD_DIR" 2>/dev/null && pwd -P)" || here=""
    if [ -n "$here" ]; then
      while IFS=$'\t' read -r -u 3 ctid cpath; do
        [ -n "$ctid" ] || continue
        [ "${cpath#/}" != "$cpath" ] || continue                   # relative or "-": nothing to compare
        there="$(cd "$cpath" 2>/dev/null && pwd -P)" || continue   # gone: cannot verify
        [ "$there" = "$here" ] || die "$ctid records $cpath, but reviewers would run in $CWD_DIR; pass --cwd $cpath (or --commit HEAD), because a check run in the wrong tree proves nothing"
      done 3< <(printf '%s\n' "$candidates" | awk -F'\t' 'NF >= 5 { printf "%s\t%s\n", $1, $4 }')
    fi
  fi

  # fd 3, not stdin: herdr and jq run inside the loop and must not eat the list.
  while IFS=$'\t' read -r -u 3 task dev checks path last; do
    [ -n "$task" ] || continue
    dispatch_one "$task" "$dev" "$checks" "$commit" "$last"
  done 3<<< "$candidates"
  return 0
}

if [ "$WATCH_S" -eq 0 ]; then
  pass
  exit "$FAILED"
fi
while :; do
  printf '%s round\n' "$(date -u +%H:%M:%S)"
  pass || true
  sleep "$WATCH_S"
done
