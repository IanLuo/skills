# Flagship (fs) — Advanced Reference

## Table of Contents

- [Event payload schemas](#event-payload-schemas)
- [FTS5 query syntax](#fts5-query-syntax)
- [Playbook YAML format](#playbook-yaml-format)
- [Project registry](#project-registry)
- [Multi-project workflows](#multi-project-workflows)
- [Store integrity and WAL](#store-integrity-and-wal)
- [Common patterns](#common-patterns)

## Event payload schemas

Every event is a JSON object with fixed envelope + type-specific payload:

```json
{
  "id": "01HXYZ...",
  "timestamp": "2025-09-13T12:00:00Z",
  "project_id": "myapp",
  "node_id": "t-abc12345",
  "parent_node_id": "t-parent00",
  "commit_sha": "a1b2c3d",
  "type": "task-created",
  "payload": { "goal": "Implement authentication" }
}
```

### Payloads by type

| Type | Payload fields |
|---|---|
| `project-created` | `name` (string), `root_path` (string) |
| `task-created` | `goal` (string) |
| `status-changed` | `from` (string), `to` (string) — values: `pending`, `active`, `done`, `blocked` |
| `decision-recorded` | `summary` (string) |
| `knowledge-added` | `summary` (string) |
| `task-blocked` | `reason` (string) |
| `task-unblocked` | (empty object) |
| `metadata-changed` | `field` (string), `old_value` (string), `new_value` (string) |
| `delivery-recorded` | `pane_id` (string), `tab_id` (string), `agent` (string), `engine` (string), `project` (string), `node` (string), `type` (string), `worktree` (string), `worktree_path` (string) |
| `plan-recorded` | `plan` (ordered `{name, phase, order}` entries), `tags` (the tag set that selected them) — the dispatch's plan |
| `report-recorded` | `kind` (string — `blocked`, `question`, `done`, or absent for a note), `line` (string). Addressed to the worker's own node |
| `cap-claimed` | `pane_id`, `tab_id`, `agent`, `claimed_at` (RFC3339 UTC), and on a take-over `taken_over_from`, `taken_over_at`, `reconstructed`. Scope-level: **no `node_id`** |

### Composite operations

Some commands produce multiple events atomically:

- `fs task block NODE_ID --reason R` → `task-blocked` + `status-changed {from: current, to: blocked}`
- `fs task unblock NODE_ID` → `task-unblocked` + `status-changed {from: blocked, to: pending}`
- `fs task update NODE_ID --status done --decision D` → `status-changed` + `decision-recorded`
- `fs dispatch` → `task-created` (the cap node) + `plan-recorded` + the trace's plan line
- `fs report NODE --blocked --line L` → `report-recorded` + `task-blocked {reason: L, check: ""}` + `status-changed {from: current, to: blocked}`; a report of any other kind is `report-recorded` alone

## FTS5 query syntax

`fs query` uses SQLite FTS5 for full-text search across all event payloads.

```bash
# Simple term
fs query "authentication" --project myapp

# Phrase (exact match)
fs query '"JWT refresh token"' --project myapp

# Boolean: AND is implicit; OR and NOT explicit
fs query "auth NOT session" --project myapp
fs query "JWT OR OAuth" --project myapp

# Prefix matching
fs query "auth*" --project myapp
```

Results are ranked by BM25 relevance. The LLM uses FTS5 as a candidate retriever, then ranks by semantic meaning.

## Playbook YAML format

A playbook is a protocol for a kind of work. Global playbooks are stored at
`~/.fs/kb/<name>.yaml`; a project's own at `~/.fs/projects/<project>/kb/<name>.yaml`,
and a project playbook with the same name **overrides the fields it declares and
inherits the rest** from the global one for a dispatch into that project.

```yaml
name: code-entry            # required; must equal the file name
phase: entry                # required: cap | entry | work | exit
applies_when: code          # optional, default empty = matches every dispatch.
                            # comma-separated terms, ALL must hold. term = `k` (present) or `k=v`
order: 10                   # optional, default 500; ascending; ties by name
steps:                      # required, non-empty
  - check: test -f AGENTS.md
  - ask: a locked PRD is present, and it names this one deliverable
  - say: read AGENTS.md before editing
```

Each step is `kind: body`, and the closed kind vocabulary is `say | use | check | ask | do | include`:

| Kind | Meaning |
|---|---|
| `check` | A shell command the gate runs: `fs dispatch` with cwd = the project root, `fs close` with cwd = the tree the worker ran in (the `worktree_path` the delivery record kept, or the project root when the record names no worktree). Pass/fail plus the command's output. |
| `ask` | Only the cap can confirm it; reported `?` at dispatch and refused at close until `--confirm`. |
| `do` | A shell command that acts — the declared teardown. `exit` phase only; runs in the project root. |
| `say` | Worker context (or close prose), not part of the gate. Carried in the brief text. |
| `use` | A skill hint for the worker, as `<skill> — <when>`. |
| `include` | Splices another playbook's steps at this position. |

A step line with no recognized kind prefix defaults to `say` (split at the first `:`),
so prose playbooks written before kinds existed still parse. `fs kb get` returns steps
as `{kind, body}` objects.

Which kinds a phase allows is a closed table — anything else is refused at write time:

| phase | runs | allowed kinds |
|---|---|---|
| `cap` | the cap's standing rules, in the session prompt | `say` |
| `entry` | before the worker starts | `check`, `ask`, `say` |
| `work` | the worker's brief | `say`, `use` |
| `exit` | at close | `check`, `ask`, `do`, `say` |

`include` is allowed in every phase — it is spliced away before validation, and
what it splices must itself be legal for the including playbook's phase. A cycle
is refused with a message naming the chain; include depth over 8 is refused too.

The removed fields `type:`, `trigger:` and `engine_scope:` are **refused at
parse** with a message naming the replacement: `trigger: cap` → `phase: cap`;
`engine_scope: herdr` → `applies_when: engine=herdr`; `type: prerequisite|cleanup`
→ the `phase` of the moment plus a matching `applies_when`.

A dispatch's `check` steps see the dispatch's own inputs in the environment, so
a gate can be about *this* dispatch — which cards, which member — and not only
the world: `FS_PROJECT`, `FS_TYPE`, `FS_GOAL`, `FS_CARDS` (the `--cards` value,
comma-separated, empty when none was given, and never unset), `FS_INTEGRATES`
(the `--integrates` value), and `FS_SCRIPTS` (where the binary's own scripts were
written). That is what `batch.yaml` uses:
`check: bash "$FS_SCRIPTS/check-parallel.sh" $FS_CARDS`. `fs close`'s exit steps
get a different, narrower set: `FS_PROJECT`, `FS_TYPE`, `FS_NODE`, `FS_WORKER`,
`FS_CARDS` (empty), `FS_INTEGRATES`, `FS_PANE`, `FS_WORKTREE`,
`FS_WORKTREE_PATH`, `FS_SCRIPTS`.

A shipped playbook is run in every project, so a step it declares may name an
`fs` subcommand, a generic POSIX tool, or `"$FS_SCRIPTS/<name>"` for a script the
binary ships — never `bin/…` or `tests/…`, which resolve in one repo and exit 127
in the rest. `FS_SCRIPTS` is empty when the scripts could not be written, so a
step that needs one must treat empty as missing rather than run a path that is
not there.

### Selection

A playbook is selected when every term of its `applies_when` holds for the
dispatch's tag set — `type=<T>` (from `--type`), `engine=herdr` (derived), and
every `--tag` (`k` or `k=v`, repeatable). Terms match presence and equality only:
no ranges, no negation. An empty `applies_when` matches every dispatch — that is
how the `worker` playbook loads for every dispatch.

### CRUD operations

```bash
# Create
cat > /tmp/playbook.yaml << 'EOF'
name: my-gate
phase: entry
applies_when: code
steps:
  - check: test -f specs/prd.md
  - ask: is the acceptance check stated?
EOF
fs kb add --name my-gate --file /tmp/playbook.yaml

# Read
fs kb get my-gate
# Returns the parsed playbook: steps are {kind, body} objects

# List all, with each playbook's scope and state against the shipped default
fs kb list

# Update
fs kb edit --name my-gate --file /tmp/updated.yaml

# Remove
fs kb remove my-gate

# A project's own playbook: --project writes to ~/.fs/projects/<P>/kb/
fs kb add --name my-gate --file /tmp/playbook.yaml --project myapp
```

### Shipped defaults and drift

Fourteen playbooks ship inside the binary and are seeded into `~/.fs/kb/`:
`cap`, `herdr`, `worker`, `code-entry`, `code-exit`, `plan`, `development`, `tdd`,
`review`, `research`, `parallel-entry`, `parallel-exit`, `integration-entry`, and
`integration-exit`. They live in `src/flagship/defaults/kb/` and are embedded with
`//go:embed`, so the contract is versioned with the code and survives a wipe.

```bash
fs bootstrap                  # create ~/.fs/kb and write every absent playbook
fs kb list                    # each playbook's scope and state against the shipped default
fs kb diff NAME               # shipped default vs the playbook on disk
fs kb reset NAME --yes        # restore the shipped default
```

`bootstrap` is idempotent and never overwrites: each file is reported `created`
(absent, written) or `kept` (present, untouched). A seeded file starts with
the stamp line

```yaml
# fs-default: sha256=<sha256 of the shipped default's body>
```

which is what lets `kb list` separate the two ways a playbook can differ from
the binary:

| state | meaning |
|---|---|
| `default` | unedited, and identical to the shipped default |
| `edited` | its body differs from the default it was seeded from |
| `stale` | the shipped default has moved on since it was seeded |
| `local` | no shipped default with that name — hand-written |

The comparison is `edited` = body hash ≠ stamp, `stale` = stamp ≠ the current
default's hash; both can hold at once and both are reported as flags alongside
the headline `state` (stale wins). A playbook with no stamp is compared to the
shipped default as it is now, so an identical hand-written file reads `default`.
`fs kb list` also reports each playbook's `scope` (`global` or the project name)
and `used_by` (`prompt: …` for a `cap` playbook, `dispatch: …` otherwise, `none`
when nothing selects it).

A new binary never rewrites a playbook its reader depends on: a moved default is
reported `stale` and applied only by `kb reset --yes`. `kb diff` ignores the
stamp line, so a freshly seeded playbook diffs empty.

## Project registry

The global project registry at `~/.fs/registry.db` tracks all known projects.

### Schema

| Column | Type | Description |
|---|---|---|
| `name` | TEXT (unique) | Project name — the `--project` identifier |
| `root_path` | TEXT | Absolute path to the project root |
| `created_at` | TIMESTAMP | When `fs project create` was run |
| `last_activity` | TIMESTAMP | Auto-updated on every project-targeting command |

### Behavior

- **Auto-registration** — `fs project create` adds the project to the registry automatically.
- **Activity tracking** — every command that uses `--project` (status, query, task add, etc.) updates `last_activity` for that project. This keeps the registry sorted by recency.
- **Listing** — `fs project list` returns all registered projects ordered by `last_activity` descending (most recently used first).
- **Lookup** — `fs project get NAME` returns a single project's registry entry (name, root_path, created_at, last_activity).
- **No manual edits** — the registry is managed exclusively by `fs` commands. Never write to `~/.fs/registry.db` directly.

### Commands

```bash
# List all projects (most recently active first)
fs project list
# → [{name, root_path, created_at, last_activity}, ...]

# Single project lookup
fs project get myapp
# → {name: "myapp", root_path: "/path/to/myapp", created_at: "...", last_activity: "..."}
```

## Multi-project workflows

One store, `~/.fs/store.db`, holds every project's events partitioned by
`project_id`. `--project` selects the partition, so any project is reachable from
any directory.

```bash
# Discover projects
fs project list

# Work across projects (no need to cd somewhere first)
fs status --project frontend
fs status --project backend
fs query "shared API contract" --project backend
```

When `--project` is omitted the target is the registered project whose `root_path`
owns the working directory; if the directory is unregistered, its directory name is
used. Registration happens on `fs project create`.

The knowledge center is two-scoped: the global `~/.fs/kb/` and each project's
`~/.fs/projects/<project>/kb/`. A project playbook with the same `name` overrides
the fields it declares and inherits the rest from the global one for a dispatch
into that project; `fs kb add|edit --project P` writes to the project's own KB,
and `fs kb list` reports each playbook's `scope`.
A project with no KB directory behaves exactly as global-only.

## Store integrity and WAL

- **SQLite WAL mode** — write-ahead logging for crash safety. Transactions commit atomically.
- **Append-only** — events are never modified or deleted. Derived state rebuilds from replay.
- **Corruption** — a damaged file header or schema is refused at open. A full
  `PRAGMA integrity_check` is deliberately *not* run per command (it is linear in
  file size, and this one store grows with every project); damage to a data page is
  reported by SQLite when that page is read.
- **FTS5 self-healing** — if the FTS index becomes corrupted, it auto-rebuilds on next query.
- **Concurrent writers** — commands wait up to 5s for the write lock (`busy_timeout`)
  instead of failing with SQLITE_BUSY, so parallel agents can share the store. Very
  long write bursts still serialize on SQLite's single writer.
- **Search scope** — FTS5 matches the whole store and then filters by `project_id`, so a
  query whose terms appear in many projects costs more than its own project's size.

## Common patterns

### Resume a session

```bash
# Full picture
fs status --project myapp

# What was decided about a topic?
fs query "authentication decisions" --project myapp

# History of a specific task
fs log --project myapp --node t-abc12345
```

### Subtask decomposition

```bash
# Parent task
fs task add --goal "Implement auth" --project myapp
# → t-aaa11111

# Subtasks
fs task add --goal "JWT middleware" --parent t-aaa11111 --project myapp
fs task add --goal "Login endpoint" --parent t-aaa11111 --project myapp
fs task add --goal "Refresh token rotation" --parent t-aaa11111 --project myapp
```

### Decision audit trail

```bash
# Record decisions as you go
fs task update t-abc --status done \
  --decision "Chose PKCE over implicit grant for public client security" \
  --project myapp

# Later, find all decisions
fs log --project myapp --type decision-recorded

# Search for specific decision topics
fs query "PKCE implicit" --project myapp
```

### Knowledge capture

```bash
# Record learnings immediately
fs task knowledge t-abc \
  --summary "modernc.org/sqlite is pure Go — no CGO needed, simplifies cross-compilation" \
  --project myapp

# Search later
fs query "SQLite CGO" --project myapp
```

### Entry gate

```bash
# Before starting code work, prepare the dispatch — its entry gate runs the
# code-entry checks (AGENTS.md present; a locked doc grep matches) and asks you
# to confirm a locked PRD names the deliverable and the acceptance check is
# stated. Fix what it refuses, or answer its asks.
fs dispatch --project myapp --type dev-task --goal "..." --tag code

# If something is missing and you cannot fix it now, block the task and name
# what would show the condition is over:
fs task block t-abc --reason "Missing system-design doc" --project myapp
```
