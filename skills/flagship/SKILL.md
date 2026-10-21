---
name: flagship
description: 'Manage projects, tasks, decisions, and knowledge via the flagship (fs) CLI — the persistent memory layer for agent orchestration. Use when the user says "track this project", "create a project", "add a task", "what''s the status", "what tasks are active", "what decisions were made", "record this decision", "resume work on project X", "pick up where I left off", "dispatch this", "fs", "flagship", or any project/task tracking request. Do NOT use for writing code (dev-task), writing specs (specs), spawning agents yourself (herdr), or research (librarian).'
metadata:
  audience: personal
  domain: agent-orchestration
---

# flagship

- `fs` is the coordination layer: one append-only event log, and every node's status derived by replay.
- A **dispatch** is a worker node, a rendered brief, and a herdr delivery. Nothing else.
- A **close** is one evidence line and a pane teardown.
- The requirements contract is [`.agents/specs/fs-contract.md`](../../.agents/specs/fs-contract.md). Read it before changing fs; this page is how to run it.

## Verbs

- `project create|list|get`
- `task add|get|update|edit|merge|block|unblock|knowledge`
- `status` · `unfinished` · `gaps` · `pending` · `query` · `log`
- `dispatch` · `close` · `report` · `tell` · `cap claim|who`
- `reviews` · `script` · `version`

## Build

- `./bin/build-project.sh flagship` — nix builds `packages.flagship` and installs `skills/flagship/scripts/fs`.
- Nothing puts it on `PATH`. Below, `fs` means that binary.
- `fs version` — the revision it was built from, whether that tree was dirty, and the file it runs from. Use it to tell two builds apart.
- No binary, no skill. Run the build first.

## Read before acting

- `fs project list` — every project, most recent activity first. The first command in a fresh session.
- `fs status --project <name>` — the task tree.
- `fs query "<topic>" --project <name>` — decisions and knowledge recorded about it.

Never create a node or change a status without reading first.

## The loop

1. `fs pending` — everything waiting on you, in every project, with what to do about each.
2. `fs dispatch --project P --type T --goal G --deliver` — the worker starts on the brief.
3. The worker writes its node, then reports.
4. `fs close --node <cap node> --decision "<verdict over the evidence line>"`.

## Dispatch

- **dispatch = worker node + brief + delivery.** There is no other machinery.
- Without `--deliver` the cap node and the brief are recorded and printed. Nothing starts, no pane is split, no worker node exists, and the prepared node stays open.
- `--deliver` splits a sibling pane of your own, creates the worker's node in the target project, starts the agent, sends the brief, records the delivery, and marks the cap node `active`.
- The worker's node is created before the agent starts, because the brief has to name it: *note your work on `<project>:<node>`; do not create another*.
- The brief carries the goal, the tree, the worker's node, every worker's standing obligations, the locked docs the tree carries, and the launch config.
- **Launch config, frozen in the binary:** `engine=herdr` · `agent=pi` · `args=--provider magpie --model opencode-go/deepseek-v4.1-flash`. A fresh agent gets this wrong without being told.
- One dispatch is one open unit per project: another dispatch into that project refuses while one is prepared and undelivered, unless `--allow-unresolved` names what it proceeds past.
- `--integrates <member>` — this dispatch merges that member. `--reviews <cap node>` — this dispatch reads that change. `--fixes <cap node>` — this dispatch repairs it; repeatable.
- `--worktree <workspace>` runs in an existing worktree; `--worktree-new` creates one.

## Close

```bash
fs close --node <cap node> --decision "<verdict>"
fs close --node <cap node> --abandoned --reason "<why>"
```

- The cap node is the one `fs dispatch` printed. The worker's node comes from the delivery record; `--worker` is only a check against it.
- It refuses while the worker's node is not `done`, and while HEAD has not moved past the revision the dispatch was prepared at — unmoved HEAD means nothing reached the repository.
- On success it records one evidence line — the sha HEAD stands at, the base it was measured against, and the verdict — then tears down the pane and marks the node done.
- The verdict is where the suite result belongs: the cap is what ran the suite.
- A project that was not a git repository when the dispatch was prepared is reported, not refused; the line says the deliverable was not checked.
- `--abandoned` skips the reads, never the teardown. Delivered or not, the record says it was abandoned, so the drop shows instead of hiding.

## The worker's side

- `fs report PROJECT:NODE --line "ONE LINE" [--blocked|--question|--done]` — the event is written first and always, addressed to the node and not to a cap; the live cap is notified best-effort through herdr.
- The kind decides the interruption: `--blocked`, `--question` and `--done` notify; no kind is silent.
- A failed notification is not a failed report: exit 0, `recorded, not notified`.
- A `--blocked` report also blocks the node, with the line as its reason.
- Keep it a pointer, never the content: under 200 bytes. The node is the record, and the cap reads the node.
- `fs tell PROJECT:NODE "TEXT"` — the cap's reply, routed through that dispatch's delivery record. A message, not a record: the decision lands on the node.

## The cap's address

- `fs cap claim [--reconstructed "WHAT YOU READ"]` — the pane in `$HERDR_PANE_ID` becomes the address herdr routes a report to, recorded with its tab, agent and time.
- Refused while a live claim holds it; a take-over of a dead one requires `--reconstructed`.
- `fs cap who` — the address and whether herdr still has it: exit 0 live, 1 not. `none` and `dead` do not read alike.

## What a worker's pane is refused

- A pane fs holds a delivery record for is refused coordinating acts: a write into the cap's own project `cap`, `dispatch`, `close`, `tell`.
- `fs report` from that same pane succeeds.
- It is a discipline guard, not a security boundary: `HERDR_PANE_ID` is an environment variable any process can unset.

## `fs pending` states

| status | meaning | what to do |
|---|---|---|
| `ready` | the worker's node is done | read the node, then `fs close` |
| `blocked` | the agent is parked on a prompt or a modal | go look; never answer an approval for it |
| `running` | working or idle | let it work |
| `unknown` | herdr could not be asked at all | read the node; go look before acting |
| `unseen` | the node is open but herdr reports the agent `done` | finished unseen — go look |
| `gone` | herdr has no such agent | the worker died; needs the user |
| `unlinked` | no delivery record for the node | `--abandoned`, or `--worker` for an old record |

herdr is a hint about when to look, never the truth about whether the work is done.

## Task status

There are four, and status is derived: the `to` of a node's last `status-changed` event.

- `pending` — prepared but not delivered; revisit it.
- `active` — dispatched; a worker is on it.
- `blocked` — waiting on the user.
- `done` — closed, with a closing decision.

Never mark unfinished work `done`.

## Event model rules

1. Never write `~/.fs/store.db` or `~/.fs/registry.db` directly — only through `fs`.
2. Events are immutable. Correct with compensating events, never by editing one.
3. Record decisions as you make them: `--decision` on `task update`.
4. Record knowledge as you learn it: `fs task knowledge`.
5. Update status honestly: `active` when starting, `done` when finished, `blocked` when stuck.

## Querying and history

```bash
fs unfinished                            # every task not done, all projects
fs gaps                                  # the backlog: open before done, heaviest first
fs query "auth" --project myapp          # FTS5 search over event payloads
fs log --project myapp --node t-abc12345 # one node's raw history
```

```bash
fs log --project skills --node t-7609aede | jq -r '
  .data.events[] | "\(.timestamp)  \(.type)  \(.node_id // "-")\n    \(.payload)"'
```

## Output and exit codes

- Every command returns JSON: `{"ok": true, "data": …}` or `{"ok": false, "error": "…"}`. The one exception is `fs script`, which exits with the script's own status.
- Exit 0 success, 1 app error, 2 usage.
- Exit 2 means the command line was wrong: an unknown flag, a flag missing its value, or a stray positional. It is refused, never ignored.
- Every subcommand answers `--help` / `-h` / `help` on stderr with exit 0, without touching the store.
- `fs --help` lists every command and flag.

## Data locations

- `~/.fs/store.db` — every project's events, partitioned by `project_id`.
- `~/.fs/registry.db` — the project registry.
- `~/.fs/logs/fs.log` — the running log: JSON lines, rotating at 4 MiB keeping three files.
- No project directory is written to.

## Project identity

- One store holds every project, partitioned by `project_id`.
- A command targets `--project NAME` if given, else the registered project whose `root_path` owns the working directory, else the directory name.
- `fs project create` registers a project; `--project` reaches any registered project from anywhere.

## The running log

- Every invocation appends JSON lines to `~/.fs/logs/fs.log`: `time`, `level`, `msg`, and the command's own fields.
- The same JSON goes to stderr, teed to the file.
- `-v` / `--verbose` sets DEBUG; `FS_LOG_LEVEL=debug|info|warn|error` is the fallback, the flag winning. An unparseable level is refused, naming the accepted values.
- It never gates: a write failure is at most a WARN and never fails the command.

## References

- **The contract this skill implements:** [`.agents/specs/fs-contract.md`](../../.agents/specs/fs-contract.md) — the outcome, the verbs, the log's shape, the launch config, and what was deliberately left out.
- [references/fs-advanced.md](references/fs-advanced.md) — full event payload schemas, FTS5 query syntax, the registry, and the store's integrity and WAL behaviour.
