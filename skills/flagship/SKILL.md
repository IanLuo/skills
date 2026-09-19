---
name: flagship
description: 'Manage projects, tasks, decisions, and knowledge via the flagship (fs) CLI — the persistent memory layer for agent orchestration. Use when the user says "track this project", "create a project", "add a task", "what''s the status", "what tasks are active", "what decisions were made", "record this decision", "resume work on project X", "pick up where I left off", "check prerequisites", "fs", "flagship", or any project/task tracking request. Do NOT use for writing code (dev-task), writing specs (specs), spawning agents (herdr), or research (librarian).'
metadata:
  audience: personal
  domain: agent-orchestration
---

# flagship

Manage projects, tasks, decisions, and knowledge via the `fs` CLI. Every state change is an immutable event — current state is derived by replaying the event log.

## Setup

The `fs` binary lives at `skills/flagship/scripts/fs` after building:

```bash
# Build (from the skills repo root)
nix develop -c bash src/flagship/build.sh
```

The build script compiles the Go source in `src/flagship/` and copies the binary to `skills/flagship/scripts/fs`. Ensure this path is on PATH, or use the full path.

If the binary doesn't exist, the skill can't function — run the build first.

## The rule: always read context first

**Before any work**, discover projects and read state. Never create tasks or make changes blind:

```bash
fs project list                          # discover all managed projects
fs status --project <name>               # current task tree
fs query "<topic>" --project <name>     # relevant decisions/knowledge
fs kb get <playbook-name>               # applicable procedures
```

In a fresh session, `fs project list` is always the first command — it shows all known projects ordered by last activity. Then drill into the relevant project with `fs status`.

## Workflow

1. **Discover** — `fs project list` to find managed projects, then `fs status` for the relevant one
2. **Check prerequisites** — `fs kb get <task-type>-prerequisites` for required docs/steps
3. **Pick or create task** — find an existing pending task or `fs task add`
4. **Mark active** — `fs task update <id> --status active`
5. **Work** — use other skills as appropriate (dev-task, specs, etc.)
6. **Record decisions and knowledge** — along the way, not after
7. **Mark done** — `fs task update <id> --status done --decision "<what was decided>"`

## CLI reference

All commands return JSON to stdout: `{"ok": true, "data": {...}}` or `{"ok": false, "error": "..."}`. Exit codes: 0=success, 1=app error, 2=usage. Logs go to stderr.

### Project

```bash
fs project create [--name NAME] [--root PATH]   # create + register
fs project list                                  # all known projects (ordered by last activity)
fs project get NAME                              # single project lookup
```

`--root` defaults to git repo root or cwd. `--name` derives from directory if omitted. All projects are registered in the global registry at `~/.fs/registry.db`.

### Tasks

```bash
# Add (top-level or subtask)
fs task add --goal "Implement auth" [--parent NODE_ID] [--project ID]

# Update status (pending → active → done | blocked)
fs task update NODE_ID --status STATUS [--decision TEXT] [--project ID]

# Edit goal
fs task edit NODE_ID --goal "New goal text" [--project ID]

# Block / unblock
fs task block NODE_ID --reason "Waiting for API keys" [--project ID]
fs task unblock NODE_ID [--project ID]

# Record knowledge on a task
fs task knowledge NODE_ID --summary "JWT refresh tokens expire after 7 days" [--project ID]
```

### Dispatch and close-out

```bash
# Prepare a brief only — the response carries the herdr line to run
fs dispatch --project P --type T --goal "Implement auth" [--confirm]

# Prepare and deliver: split a pane, start the worker, send the brief, record it
fs dispatch --deliver --project P --type T --goal "Implement auth"

# Close out a dispatch: refuses unless the worker's node is done
fs close --node CAP_NODE --worker PROJECT:NODE --decision "verdict"

# Close a dispatch that was never delivered
fs close --node CAP_NODE --abandoned --reason "why"
```

### Task status

Status is derived by replaying events: a task's status is the `to` of its last
`status-changed` event. There are exactly four.

| status | meaning |
|---|---|
| `pending` | prepared but **not delivered** — unresolved, revisit it |
| `active` | dispatched; a worker is on it |
| `blocked` | waiting on the user |
| `done` | closed, with a closing decision |

Never mark an unfinished task `done`. Abandoning a dispatch means marking it
`done` with a decision that says it was never delivered: the record then shows
the drop instead of hiding it.

### Querying

```bash
# Task tree with statuses, goals, decisions
fs status [--project ID]

# Every task not done, across every scope — reads the store's scopes, not the
# registry, so it works after a registry wipe and includes the implicit cap scope
fs unfinished

# Full-text search across all event payloads (FTS5)
fs query "search term" [--project ID]

# Event log (full history)
fs log [--project ID] [--node NODE_ID] [--type TYPE]
```

### Reading events

`fs log` returns raw event JSON. For reading:

```bash
skills/flagship/scripts/fs-show <project> [node] [event-type]

fs-show skills t-7609aede              # one node's history
fs-show skills t-7609aede status-changed
fs-show cap                            # a whole scope
```

It finds `fs` via `$PATH` or falls back to this skill's `scripts/`, so it works
from any directory.

### Knowledge center (global playbooks)

```bash
fs kb add --name NAME --file PATH     # add YAML playbook
fs kb get NAME                         # read a playbook
fs kb list                             # list all
fs kb edit --name NAME --file PATH     # update
fs kb remove NAME                      # delete
```

Playbooks are YAML at `~/.fs/kb/<name>.yaml`. Each step is `kind: body`, and `fs kb get` returns steps as `{kind, body}` objects:

```yaml
name: dev-task-prerequisites
type: prerequisite
trigger: dev-task
steps:
  - check: test -f AGENTS.md
  - check: grep -rl -e 'specs:locked' -e 'design:locked' --include='*.md' .
  - ask: a locked PRD is present, and it names this one deliverable
  - ask: is the acceptance check for this deliverable stated
```

The kind decides what the step means: `check` is a shell command `fs dispatch` runs at the project root, `ask` is for the cap to confirm, `say` is worker context and not part of the gate. `fs kb add` and `fs kb edit` refuse a step whose kind the playbook type does not allow — `prerequisite`: check, ask; `procedure`: say, check; `routing`: exactly one say step naming the skill. See `references/fs-advanced.md` for the full format.

## Event model rules

These are non-negotiable:

1. **Never write to `~/.fs/store.db` or `~/.fs/registry.db` directly** — only use `fs` commands
2. **Events are immutable** — no undo; correct with compensating events (update status back, edit goal again)
3. **Always record decisions** — when making a choice, use the `--decision` flag on `fs task update`
4. **Always record knowledge** — when learning something useful, use `fs task knowledge`
5. **Update status honestly** — active when starting, done when finished, blocked when stuck

## Event types

| Type | Produced by | Payload |
|---|---|---|
| `project-created` | `project create` | `{name, root_path}` |
| `task-created` | `task add` | `{goal}` |
| `status-changed` | `task update/block/unblock` | `{from, to}` |
| `decision-recorded` | `task update --decision` | `{summary}` |
| `knowledge-added` | `task knowledge` | `{summary}` |
| `task-blocked` | `task block` | `{reason}` |
| `task-unblocked` | `task unblock` | `{}` |
| `metadata-changed` | `task edit` | `{field, old_value, new_value}` |
| `delivery-recorded` | `dispatch --deliver` | `{pane_id, agent, engine}` |

Every event carries: `id` (ULID), `timestamp` (UTC), `project_id`, `node_id`, `parent_node_id`, `commit_sha`.

## Prerequisite checking

Before starting a task type that has a prerequisite playbook:

1. `fs kb get <task-type>-prerequisites`
2. Verify each required doc exists and is locked
3. If missing → `fs task block <id> --reason "Missing <doc>"`

`fs dispatch --project P --type T --goal G` runs that playbook's `check` steps
at the project root. Checks run in order and **stop at the first failure**: the
command exits non-zero, names the failing command and its output, and says what
to do. Fix it, or pass `--confirm` to run every check anyway — the override is
recorded as a decision on the cap's dispatch node.

Dispatch also refuses while an earlier dispatch node is still unresolved
(`pending`/`active`/`blocked`): the cap must resolve it or pass `--confirm`,
which records `user confirmed proceeding with unresolved dispatches: <ids>`.
Only `dispatch <type>: ` nodes gate this way — the cap's own backlog does not.

### Delivering

`fs dispatch --deliver` delivers the brief itself instead of handing the cap a
herdr line: it splits a sibling pane at the project root, starts a `pi` agent
named `dispatch-<type>`, sends the brief, records the pane binding as a
`delivery-recorded` event (`{pane_id, agent, engine}`), appends the decision
`delivered to pane <id>, agent <name>`, and marks the cap node `active`. If
herdr is unavailable the command fails and the node stays `pending` — nothing is
claimed that did not happen.

### Closing out

`fs close` is the gate that makes an omission fail. In order it refuses when the
node is not a dispatch, when no `delivery-recorded` event exists, when
`--worker <project>:<node>` is missing or that node is not `done` (it names the
actual status), or when `--decision` is missing. On success it closes the pane
from the delivery record and marks the cap node `done` with the verdict. A pane
that is already closed is not an error, and herdr being unavailable is a
`warning` in the response rather than a refusal — close-out must not depend on
the dispatcher being up.

`--abandoned --reason <why>` skips the delivery and worker gates and closes with
the decision `abandoned, never delivered: <why>`.

The gate can check that the worker's node exists and is `done`, and that a
verdict was recorded. It **cannot** check that the cap actually read that node;
reading it is still the cap's job.

## Data locations

Everything lives under `~/.fs/` — no project directory is written to:

- `~/.fs/store.db` — event store: every project's events, partitioned by `project_id`
- `~/.fs/registry.db` — global project registry
- `~/.fs/kb/` — knowledge center (global YAML playbooks)

## Project identity

One store holds every project, partitioned by `project_id`. A command targets
`--project NAME` if given, else the registered project whose `root_path` owns the
working directory, else the directory name. So after `fs project create --name demo`,
every command in that repo targets `demo` — and `--project` reaches any registered
project from anywhere. See [references/fs-advanced.md](references/fs-advanced.md).

## Example session

```bash
# Discover projects in a fresh session
fs project list

# Resume work on a project
fs status --project myapp
fs query "auth" --project myapp

# Create and work a task
fs task add --goal "Add OAuth2 flow" --project myapp
# → node_id: t-abc12345

fs task update t-abc12345 --status active --project myapp

# Record what you learned
fs task knowledge t-abc12345 \
  --summary "PKCE required for public clients per RFC 7636" --project myapp

# Finish with a decision
fs task update t-abc12345 --status done \
  --decision "Used authorization code + PKCE, no implicit grant" --project myapp
```

## References

**Read [references/fs-advanced.md](references/fs-advanced.md) when you need:**
- Full event payload schemas
- FTS5 query syntax and ranking
- Playbook YAML format details
- Project registry schema and behavior
- Multi-project workflows
- Store integrity and WAL behavior
