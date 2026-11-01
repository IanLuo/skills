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

`add` records intent: goal, project scope, the checks a reviewer must run, and optionally the worktree path. A review task is created with `--review-of <task-id>` and inherits the reviewed task's checks — it must not declare its own. `--path` is resolved when the task is written, so `--path .` records the directory you ran it from, not a dot that means whatever directory the reader stands in.

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
scripts/dispatch-review.sh --project demo --dry-run                          # what it would spawn
scripts/dispatch-review.sh --project demo --commit "$(git rev-parse HEAD)"    # real run
```

- It reads `fs status`, takes the review tasks that are open and held by nobody, mints a
  fresh session id per task, splits a herdr pane, starts an agent and prompts it with the
  claim, the declared checks, the report command and `done`.
- **Success is the claim, not the spawn.** After prompting it polls fs until a session holds
  the task, prints that session, and exits `1` if none does — a prompt typed into an agent
  that never acted is a failed dispatch. herdr's own `agent_prompt_stalled` is a hint, not a
  verdict: it fires when the agent has not visibly started within 5 s.
- **Run it from a coordinator, never from the developer's session.** A developer that spawns
  its own reviewer has chosen its judge.
- `--commit` gives each reviewer `git worktree add --detach <sha>`. fs records no commit, so
  AC5's "detached at the reviewed commit" is the dispatcher's job; without it the reviewer
  runs in `--cwd`.
- `MEMSTORE_DB` is passed into the prompt. A spawned pane inherits herdr's environment, not
  this shell's, so without it the reviewer reads `~/.memstore/memory.db`, finds no such task
  and correctly refuses — a silent, confusing failure.
- The parent's last report is **not** the trigger. Any observation older than the 120 s lease
  reads `unknown`, so waiting for a fresh `dev-done` starves every slow review. Adding the
  review task is the ready signal; a reviewer that finds unfinished work reports `fail`.

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
fs status --session s-1          # what this session's binding authorizes
fs status --format table         # the same report, laid out for a human
```

`--format table` is for reading, not parsing: same single read, no second query, columns
`TASK KIND STATE HOLDER ROUND LEASE AGENT GOAL`. `LEASE` is the time the holder has left
before it must write again (`lapsed` once it has), and `AGENT` is its last reported state,
with a `~` when that report is older than the lease (`dev-done~`: verdict unknown, and the
state shown is the last report). Projects with no tasks are folded into one trailing line — a
scope keeps its name while any `fs:` row exists, so empty projects say nothing about work.
Reach for `--format table` when a human asked for status; keep the default when a script
parses the answer.

Per task: `task`, `kind` (`dev`/`review`), `state`, `goal`, `checks`, `session`, `round`, `lease_expires`, `lease_live`, and two observations:

- `agent` — what the agent last reported (`source: fs.event`, with `age_seconds` and `fresh`).
- `runtime` — what a harness reported with `--role runtime`. `{state: "unknown", source: "none", fresh: false}` means **nobody reported it**. Absence is not health.

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

## References

- `~/Documents/apps/fs/.agents/specs/fs-rebuild.md` — acceptance criteria, KPIs, failure signals.
- `~/Documents/apps/fs/.agents/specs/fs-rebuild-design.md` — record kinds, the claim protocol, layers, and the delete list.
- `~/Documents/apps/fs/AGENTS.md` — build, test, and the invariants a change must not break.
