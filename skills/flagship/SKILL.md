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
./bin/build-project.sh flagship
```

That builds `packages.flagship` with nix — Go 1.27, named because `go.mod`
requires >= 1.26.7 and nixpkgs' default `go` is older, with dependencies vendored
in `src/flagship/vendor` so the build is hermetic and offline — and installs the
binary at `skills/flagship/scripts/fs`. Nothing installs it on your `PATH`: either
add that directory yourself or `nix profile install .#flagship` for a
nix-managed entry. In every example below, `fs` means one of those.

If the binary doesn't exist, the skill can't function — run the build first.
Then `fs bootstrap` seeds `~/.fs/kb/` with the playbooks shipped inside the
binary (see Knowledge center below).

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

Exit 2 means the command line itself was wrong: an unknown flag, a flag missing its value, or a stray positional where the subcommand takes none. It is refused, never ignored — a typo'd flag is an error, not a no-op that reports success. Every subcommand answers `--help` / `-h` / `help` with its usage on stderr and exit 0, without touching the store, the registry, or `~/.fs`.

### Project

```bash
fs project create [--name NAME] [--root PATH]   # create + register
fs project list                                  # all known projects (ordered by last activity)
fs project get NAME                              # single project lookup
```

`--root` defaults to git repo root or cwd. `--name` derives from directory if omitted. All projects are registered in the global registry at `~/.fs/registry.db`.

Commands that take a scope (`status`, `query`, `log`, `task *`) resolve it from the cwd's registered project. Run from a directory that is no registered project's root, they refuse with exit 1 rather than inventing a partition named after the directory. Pass `--project <name>` to target any scope explicitly — registered or not, the implicit `cap` scope included — or run `fs project create` to register the directory.

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

# Prepare and deliver: open the worker's own tab, start the worker, send the
# brief, record it
fs dispatch --deliver --project P --type T --goal "Implement auth"

# Close out a dispatch: refuses unless the worker's node is done. The worker's
# node comes from the delivery record; --worker only checks against it.
fs close --node CAP_NODE --decision "verdict"

# Close a dispatch that was never delivered
fs close --node CAP_NODE --abandoned --reason "why"

# What is waiting on you: every open dispatch, classified. Writes nothing
fs pending
```

`--deliver` gives the worker a **tab of its own**, not a sibling pane in yours,
and records `pane_id`, `tab_id`, `project`, and `node`. That layout is what makes
the dispatch readable at all: herdr's `done` means *idle after work nobody has
looked at*, and a pane in your own tab is always seen — so a finished worker in a
sibling pane reads `idle` forever, and the signal you pick up on never fires.

Start every turn with `fs pending`; never wait on one and never poll with a
sleep. It answers "what is waiting on me" in one read: it takes no arguments,
reads the cap scope and every project a delivery names, and writes nothing.

| state | meaning | what to do |
|---|---|---|
| `ready` | the worker's node is done | read the node, then `fs close` |
| `running` | the node is not done and herdr still has the agent | let it work |
| `unseen` | the node is not done but herdr reports the agent `done` | finished unseen — go look |
| `gone` | the node is not done and herdr has no such agent | the worker died; needs the user |
| `unlinked` | no delivery record, or one written before it carried a node | close it with `--abandoned`, or `--worker` for an old record |

herdr is a hint about *when to look*, never the truth about whether the work is
done — a worker whose pane you happened to look at reads `idle`, not `done`, and
is still finished. So a done node is `ready` whatever herdr says. When herdr
cannot be asked at all, the states that need it are reported as `running` with a
`warning`: unreadable is not the same as gone.

`fs close` then closes the recorded pane and tab (an empty tab is the one thing
herdr cannot report on, so it is closed explicitly) and marks the cap node done
with your verdict.

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
# Task tree with statuses, goals, decisions, knowledge
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

`fs status` and `fs query` answer most questions with far less output — reach for
them first. `fs log` is the raw history, one JSON envelope per event, meant for
machines; filter it with `jq`:

```bash
fs log --project skills --node t-7609aede | jq -r '
  .data.events[] | "\(.timestamp)  \(.type)  \(.node_id // "-")\n    \(.payload)"'
```

### Knowledge center (global playbooks)

```bash
fs kb add --name NAME --file PATH     # add YAML playbook
fs kb get NAME                         # read a playbook
fs kb list                             # list playbooks and their state
fs kb prompt                           # the cap's standing rules, as plain text
fs kb edit --name NAME --file PATH     # update
fs kb remove NAME                      # delete
```

`fs kb prompt` is how a playbook reaches the agent that has to follow it: it
concatenates every `procedure` playbook whose trigger is `cap`, in name order,
each under a header naming the playbook and its state and stamp. Plain text on
stdout and nothing else, so it pipes straight into a prompt:

```bash
pi --append-system-prompt "$(fs kb prompt)"
```

No blank lines, deliberately: line breaks would split it into separate blocks.
The headers are what make a stale prompt detectable — they say which playbooks
went in and where each came from.

Three playbooks ship **inside the binary** — `cap`, `dev-task-prerequisites`,
and `herdr` — and are seeded into `~/.fs/kb/`:

```bash
fs bootstrap                           # seed ~/.fs/kb from the binary's playbooks
fs kb diff NAME                        # shipped default vs the playbook on disk
fs kb reset NAME --yes                 # restore the shipped default
```

`bootstrap` is idempotent and never overwrites: an absent playbook is reported
`created`, an existing one `kept` and left byte-for-byte alone. Each seeded file
carries `# fs-default: sha256=<hash of the default body>`, which is what lets
`kb list` tell a user's edit from a change to the shipped default:

| state | meaning |
|---|---|
| `default` | unedited, and identical to the shipped default |
| `edited` | differs from the default it was seeded from |
| `stale` | the shipped default has moved on since it was seeded |
| `local` | no shipped default with that name — hand-written |

A new binary never rewrites a playbook the cap is reading: a moved default shows
as `stale` and is applied only by `kb reset`. Playbooks the binary does not ship
are written by hand or with `kb add`.

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
| `delivery-recorded` | `dispatch --deliver` | `{pane_id, tab_id, agent, engine, project, node}` |

Every event carries: `id` (ULID), `timestamp` (UTC), `project_id`, `node_id`, `parent_node_id`, `commit_sha`. `commit_sha` is the git HEAD of the project the event is about — the resolved project's registered `root_path` (`status`/`query`/`log`/`task *`), the dispatched-to project (`dispatch`), the worker's project (`close`), or the root being created (`project create`); the cwd's HEAD only when no project is referenced (`unfinished`), and null when that is not a git repo.

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
herdr line: it opens a tab of the worker's own at the project root, creates the
worker's node in the target project, starts a `pi` agent named
`dispatch-<type>-<node>` in the tab's root pane, sends the brief, records the
binding as a `delivery-recorded` event (`{pane_id, tab_id, agent, engine,
project, node}`), appends the decision `delivered to pane <id> in tab <id>,
agent <name>`, and marks the cap node `active`. The name carries the worker's
node, so two dispatches of one type running at once are two agents — one name in
two panes is what made `herdr agent get <name>` ambiguous.

The worker's node carries the cap node's goal — one text for both ends of the
link — and is created **before** the agent starts and the brief is sent, because
the brief has to name it and the agent is named after it: *note your work on
`<project>:<node>` — that node already exists and is yours; do not create
another.* A worker that invents its own node breaks the
link, so the brief says so outright. That ordering is the only one that works, so
a failure after the node exists marks it `done` with the reason instead of
leaving it outstanding. If herdr is unavailable nothing is created at all: the
command fails, the cap node stays `pending`, and the target project is untouched
— nothing is claimed that did not happen.

### Closing out

`fs close` is the gate that makes an omission fail. In order it refuses when the
node is not a dispatch, when no `delivery-recorded` event exists, when the
worker's node is missing or not `done` (it names the actual status), or when
`--decision` is missing. The worker's node comes from the **delivery record**, so
the cap does not name it: `--worker <project>:<node>` is optional and, when given,
must match the record. A mismatch is refused, naming the recorded node — with two
dispatches into one project, that is the difference between closing the right
work and closing something else. A record written before the link existed carries
no node; closing one of those refuses and asks for `--worker`, the only way such a
record can be closed.

On success it closes the pane from the delivery record, returns the worker node
it rested on, and marks the cap node `done` with the verdict. A pane that is
already closed is not an error, and herdr being unavailable is a `warning` in the
response rather than a refusal — close-out must not depend on the dispatcher
being up.

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
