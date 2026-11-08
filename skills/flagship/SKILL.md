---
name: flagship
description: 'Coordinate isolated agent tasks through the flagship (fs) CLI, backed by shared memstore. Use when the user says "add a task", "give this to an agent", "claim a task", "what is fs doing", "what is that agent doing", "what is the status", "is anyone on this", "fs", or "flagship" — or when an agent must find its next task, report progress, or complete a reviewed task. Do NOT use for spawning agents or panes (herdr), writing code (dev-task), writing specs (specs), or research (librarian).'
metadata:
  audience: personal
  domain: agent-orchestration
---

# flagship

- `fs` coordinates **isolated agent tasks**. It records who is on what, keeps one task per agent session, and refuses to mark work complete without an independent review.
- It is a **client of shared memstore**. Every record lives in `scope = "fs:<project>"` inside memstore. fs owns no database, no cache, no event ledger and no cap.
- Five verbs: `add`, `claim`, `report`, `done`, `status`.
- Contracts, both locked: `~/Documents/apps/fs/.agents/specs/fs-rebuild.md` (requirements) and `~/Documents/apps/fs/.agents/specs/fs-rebuild-design.md` (structure, record kinds, the claim protocol). Read the requirements before changing behaviour; read the design before touching keys or the claim path.

## The binary

`fs` resolves from `PATH`. Call it by name; never look for a copy inside this skill folder —
the only thing in `scripts/` is `dispatch-review.sh`, which drives `fs` and `herdr` and is not
a copy of the tool.

```bash
command -v fs     # the install this skill drives
```

`fs` is its **own repository** (`~/Documents/apps/fs`). Build, test and install it there; this
skill only drives it:

```bash
cd ~/Documents/apps/fs
nix build .#default          # ./result/bin/fs
nix profile upgrade fs       # repoint the installed fs at this build
                             # first time only: nix profile install .#default
```

An installed `fs` does not follow the repository: after a change, `nix profile upgrade fs`, or
`PATH` keeps serving the old build — silently, because the command still works. If a fix seems
absent, check which build answered before doubting the fix:

```bash
readlink -f "$(command -v fs)"            # the store path in use
readlink ~/Documents/apps/fs/result       # the build of the current tree
```

If `fs` is not on `PATH`, stop and report it. Do not build a copy, and do not put one in
`scripts/`. There is no `fs version` — to tell two builds apart, compare
`git -C ~/Documents/apps/fs rev-parse --short HEAD` with the commit you installed from.

## The user's loop

```bash
fs add    --project demo --goal "add lease-expiry tests" --check "go test ./internal/coord" --path .
fs status --project demo
```

`add` records intent: goal, project scope, the checks a reviewer must run, and optionally the worktree path. A review task is created with `--review-of <task-id>` and inherits the reviewed task's checks — it must not declare its own. `--path` is resolved when the task is written, so `--path .` records the directory you ran it from, not a dot that means whatever directory the reader stands in. A row written by an older build can still hold `.`: records are append-only, so an upgrade does not repair it — re-add the task.

## The agent's loop

```bash
fs claim  --project demo --session "$FS_SESSION"          # oldest eligible task
fs claim  --project demo --session "$FS_SESSION" --task t-…  # a specific one
fs report --project demo --session "$FS_SESSION" --task t-… --state progress --note "half way"
fs report --project demo --session "$FS_SESSION" --task t-… --state dev-done --note "tests added"
fs add    --project demo --goal "review t-…" --review-of t-…  # the review task, before you stop
```

`add` is not session-gated, so the development session creates its own review task — creating a
task grants no review authority, so independence is untouched. Do it before the session ends: a
dev-done with no review task is work nobody can complete, and the dispatcher has nothing to hand
to a reviewer.

For a review, in a session that is not the developer's:

```bash
fs claim  --project demo --session "$FS_SESSION" --task t-rev…   # the review task, not the dev task
fs report --project demo --session "$FS_SESSION" --task t-rev… --state pass --role review \
          --command "go test ./internal/coord" --exit 0
fs done   --project demo --session "$FS_SESSION" --task t-rev…
```

- `--command` must be the declared check **verbatim**. The gate takes the newest `role=review` run by this session of that exact string, so a near-miss records a run the gate does not count, and completion is refused with "no recorded reviewer run of: …".
- `report` is also the **keepalive**: there is no renew verb. A lease lapses 120 s after the session's newest write, and a lapsed lease reads as `unknown`, never as live or done. The old session's writes are then refused and the task frees itself for a new session at the next round — the task stays incomplete either way.
- `done` is refused unless this reviewer ran **every** declared check and each one passed. A worker cannot complete its own task.

## The two invariants

Both rest on `Put` refusing a duplicate `(origin, origin_id)` inside memstore — one unique key, so one row each.

| row | key | refuses |
|---|---|---|
| `fs.bind` | `session:<sid>` | a session's **second** task, forever |
| `fs.claim` | `claim:<tid>:<round>` | a **second holder** of one task |

Order is **bind, then claim**. A claim that loses a race leaves the session spent and holding nothing — safe, and the fix is a new session, not a retry.

## Session identity

`--session` is a stable unique id supplied by the harness, one per fresh agent context. fs can only enforce that one identity carries one task; it cannot see a reused conversation. Start every task in a new session, and every review in a session different from the developer's.

## Handing a review to a fresh agent

fs will not start the reviewer, and that is not a gap: the session that wrote the code must
not choose the session that reviews it, or the review inherits the author's context. R3 puts
"fs starting or managing agent processes" out of scope on purpose. The **coordinator
dispatches** instead — you, or a loop — and fs still decides: `done` refuses unless the
reviewer really ran each declared check.

```bash
scripts/dispatch-review.sh --project demo --dry-run                  # what it would spawn
scripts/dispatch-review.sh --project demo --commit HEAD              # one round, now
scripts/dispatch-review.sh --project demo --commit HEAD --watch 30   # rounds until stopped
```

- **A round** gives a fresh reviewer to each review that needs one: a review nobody has
  reviewed yet, or one whose last verdict was `fail`/`blocked` and whose developer has reported
  `dev-done` since. A review whose reviewer is still live is never dispatched over.
- **Each reviewer reviews once, then its pane closes.** When fs shows the verdict, the
  dispatcher closes the pane and removes the worktree. The clock is idle time, not total time:
  a reviewer that is still working is never closed, however long its checks run. A reviewer is
  given up on only when herdr reports it idle and fs has seen no write from it for a lease
  (120 s), for `--wait` seconds in a row. Then its pane closes and the task stays open.
- **A `fail` starts no round by itself.** The developer implements the feedback, reports
  `dev-done`, and the next round dispatches a new reviewer. Until then the loop waits.
- `--commit HEAD` is resolved at each round. A fixed sha would give round 2 the code round 1
  reviewed. `--watch` therefore needs `HEAD`.
- It reads `fs status`, mints a fresh session id per reviewer, splits a herdr pane, starts an
  agent and prompts it with the claim, the declared checks, the report command and `done`.
- **Success is the verdict in fs, not the spawn and not the claim.** A `done` closes the review;
  a `fail` is the verdict that starts the wait for a fix. A reviewer that claims, runs the checks,
  then reads past its 120 s lease reopens the task, and the dispatcher reports it as no verdict.
  The prompt tells the reviewer to write a `progress` report at least every 90 s while it still
  has reading to do, and to `done` as soon as it is finished. The reviewer writes its reports with
  `--role review`; without it they are recorded as developer reports. herdr's
  `agent_prompt_stalled` is a hint, not a verdict: it fires when the agent has not visibly started
  within 5 s.
- **Run it from a coordinator, never from the developer's session.** A developer that spawns
  its own reviewer has chosen its judge.
- `--commit` gives each reviewer `git worktree add --detach <commit>`. fs records no commit, so
  AC5's "detached at the reviewed commit" is the dispatcher's job; without it the reviewer
  runs in `--cwd` (default `$PWD`). The worktree is removed when the round ends, so a round
  leaves nothing behind; one a reviewer changed is kept, and named.
- **The task's `project_path` is not used.** A reviewer's directory is `--cwd` or the
  `--commit` worktree, so dispatching from the wrong directory points the checks at the wrong
  tree, and a check that passes there is evidence of nothing. Without `--commit`, the script
  refuses when a task records an absolute `project_path` that differs from the directory it
  would use.
- `MEMSTORE_DB` is passed into the prompt. A spawned pane inherits herdr's environment, not
  this shell's, so without it the reviewer reads `~/.memstore/memory.db`, finds no such task
  and correctly refuses — a silent, confusing failure.
- Freshness is **not** the trigger, and the two reports are compared with each other, never with
  the clock. Any observation older than the 120 s lease reads `unknown`, so waiting for a *fresh*
  `dev-done` starves every slow review. Adding the review task is the signal for round 1; after a
  `fail`, the next round is "the developer reported `dev-done` more recently than the reviewer said
  `fail`", which is a comparison of ages and holds however long both sat there.
- The reviewer's own claim keeps its task `claimed` for up to 120 s after it finishes. That is why
  a round 2 waits: `fs status` shows `claimed` while the failed reviewer's lease is live, and the
  dispatcher treats a live claim as somebody else still working.

## Picking up a task whose session died

A dead session takes its context with it, and fs keeps no transcript on purpose. What survives
is the task: the goal, the declared checks, the absolute path, the last reported state, and the
reports in `evidence`. Resume from those, never from a guess about what the old agent was
thinking.

```bash
# 1. is it free?  LEASE: lapsed, AGENT: progress~ (half done) or dev-done~ (finished)
fs status --project P --format table

# 2. take it over — round+1. If the claim is refused, retry with THIS session id:
#    the bind is written before the claim is decided, so a refused id is spent otherwise.
fs claim --project P --session s-take --task T

# 3. where did it stop?
fs status --project P | jq -r '.data.projects[].tasks[] | select(.task=="T") | .evidence[0]'

# 4. run the declared checks before reading the code: the cheapest objective state
# 5. read the work itself: git status, git log, git diff, TASK.md in the worktree

# 6. write the resume point down before editing — this is also the keepalive
fs report --project P --session s-take --task T --state progress --note "…" --digest "…"
```

Then continue. Three ways this goes wrong:

- **Not taking the task over.** A dev task resumed as a *new* task leaves two rows for one
goal and nobody can tell which attempt closed it. Take over the row that exists.
- **Asking the dead session to continue.** Its writes are refused — the claim row names the new
holder now. There is nothing to ask.
- **Starting to work without reporting.** Then the next crash is as expensive as this one, because
`progress` with no note is all the next session gets.

A reviewer does not need this: a review task is normally unheld, so a fresh session claims it at
round 1 (see above). And a reviewer must not receive the developer's conclusions — the contract,
the diff and the check results are the input, not the notes the developer wrote to itself.

## Verbs

```text
fs add    --project P --goal G --check C [--check C]... [--path DIR] [--review-of TID]
fs claim  --project P --session S [--task TID]
fs report --project P --session S --task TID --state S [--role dev|review|runtime]
          [--command C --exit N] [--digest D] [--note TEXT]
fs done   --project P --session S --task TID [--note TEXT]
fs status [--project P] [--session S] [--format json|table]
```

States: `progress`, `dev-done`, `pass`, `fail`, `blocked`. Roles: `dev` (default), `review`, `runtime`.

## Reading `status`

```bash
fs status                        # every project (the user's view)
fs status --project demo         # one project
fs status --session s-1          # what this session's binding authorizes; refused when it holds nothing
fs status --format table         # the same report, laid out for a human
```

`--format table` is for reading, not parsing: same single read, no second query, columns
`TASK KIND STATE HOLDER ROUND LEASE AGENT GOAL`. `LEASE` is the time the holder has left
before it must write again (`lapsed` once it has), and `AGENT` is its last reported state,
with a `~` when that report is older than the lease (`dev-done~`: verdict unknown, and the
state shown is the last report). Projects with no tasks are folded into one trailing line — a
scope keeps its name while any `fs:` row exists, so empty projects say nothing about work.
The header line carries the counts, and an over-long cell is cut with `…`.
Reach for `--format table` when a human asked for status; keep the default when a script
parses the answer.

Per task: `task`, `kind` (`dev`/`review`), `state`, `goal`, `checks`, `session`, `round`,
`lease_expires`, `lease_live`. A lapsed claim keeps its old holder in `session` — expiry is not erasure — so `state: open` is what means unheld, never `session == null`.
`state` is the **hold** state — `open`, `claimed`, `done` — not a
reported state: `claimed` means a live holder, `open` a task nobody holds or whose lease
lapsed, and `done` a completed one. Then two observations:

- `agent` — what the agent last reported (`source: fs.event`, with `age_seconds` and `fresh`).
- `runtime` — what a harness reported with `--role runtime`. `{state: "unknown", source: "none", fresh: false}` means **nobody reported it**. Absence is not health.
- `evidence` — every report the task carries, newest first, capped at 20, whatever role wrote it: `role`, `state`, `session`, `age_seconds`, `note`, `digest`, and for a check run the exact `command` and `exit`. This is the resume record; see below.

`agent` carries two facts, and they differ once a report goes stale: `state` is the verdict
(`unknown` past the lease — expiry is not evidence), `reported` is what that source last said,
with its age. Read `reported` to know what happened, and `state` to know how much to trust it.

A task whose lease has lapsed shows `lease_live: false` and `agent.state: unknown`, with
`agent.reported` still naming the last thing that session said. Neither means done.

### All projects, and who is on them

There is **no `fs agents` verb**, and no agent registry. An agent exists only as the holder of a
task, so it appears as the `session`/`round`/`lease_live`/`agent` block *inside* that task. A
session that never claimed has no row, so it appears nowhere.

```bash
# every project, one per line
fs status | jq -r '.data.projects[].project'

# project → agent → task, one line each
fs status | jq -r '.data.projects[] | .project as $p | .tasks[]
  | select(.session != null)
  | [$p, .session, .task, .state, (.lease_live|tostring), .agent.state] | @tsv'
```

A project appears as soon as its scope holds any `fs:` row. Rows from the retired projection
(kinds `decision`, `knowledge`, `report`, `project`) still name their scopes, so those projects
show with `"tasks": []` and no agents. Empty tasks means no fs work there — not a missing
command. Deleting those rows is the user's call.

## Refusals you will actually see

```text
session s1 is bound to task t-02cf…; it cannot touch t-other
completion refused: no recorded reviewer run of: go test ./internal/coord; the task stays incomplete
completion refused: failed: go test ./internal/coord (exit 1); the task stays incomplete
t-02cf… is development work: completion requires a review task, claimed by a fresh session, that runs the checks — a worker cannot complete its own work
task t-02cf… is held by session s2 until 2026-11-01T12:20:03Z
session s1's lease on task t-02cf… expired at 2026-11-01T12:31:38Z; it may no longer write
a review task runs the task's declared checks: drop --check, or add a task of its own
no eligible task in fs:demo: every task there is held or closed
```

Each is a state decision, not an error to retry blindly. Read `fs status` first.

## Output and exit codes

- Every command prints a JSON envelope on stdout: `{"ok":true,"data":…}` or `{"ok":false,"error":…}`. One exception, on request only: `status --format table` prints text and exits `0`, because a human asked not to be given JSON.
- `0` result · `1` refused (bad input, wrong session, failed gate) · `2` malformed command line (unknown command, unknown flag, no command).
- Help is an answer, not a failure: `fs --help` prints the whole interface and `fs <verb> --help` prints that verb's block, both on stdout with exit `0`.

## Where data lives

- `$MEMSTORE_DB`, else `~/.memstore/memory.db` — every coordination record, in `fs:<project>` scopes. Shared with other services; fs touches only its own scopes.
- `~/.fs/logs/fs.log` — the running log, JSON lines, rotating at 4 MiB keeping three files. Never gates a decision. `-v`/`--verbose` sets DEBUG, `FS_LOG_LEVEL` is the fallback.
- `~/.fs/store.db` is **legacy**: the rebuilt fs never reads or writes it. Leave it alone.

## Rules

1. Never write memstore's SQLite file directly, and never add a second store — the coordination model depends on memstore's own duplicate refusal being the only arbiter.
2. Never claim work without a session id, and never reuse a session id for a second task.
3. A claim is not evidence. Completion needs a reviewer's own run of every declared check.
4. When a refusal surprises you, the answer is in `fs status`, not in a retry.
5. Changing a key, the claim order, or the completion gate needs the design doc re-locked first: the safety argument is the order of the two writes.
6. A declared check must exit 0 on a clean tree, or the task can never close. `gofmt -l . | grep -v ^vendor/` is the trap: the pipeline's status is grep's, and grep exits 1 when it finds nothing. Use `test -z "$(gofmt -l .)"` or a command whose own exit code is the answer.

## References

- `~/Documents/apps/fs/.agents/specs/fs-rebuild.md` — acceptance criteria, KPIs, failure signals.
- `~/Documents/apps/fs/.agents/specs/fs-rebuild-design.md` — record kinds, the claim protocol, layers, and the delete list.
- `~/Documents/apps/fs/AGENTS.md` — build, test, and the invariants a change must not break.
