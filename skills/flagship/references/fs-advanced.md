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
| `delivery-recorded` | `pane_id` (string), `agent` (string), `engine` (string) |

### Composite operations

Some commands produce multiple events atomically:

- `fs task block NODE_ID --reason R` → `task-blocked` + `status-changed {from: current, to: blocked}`
- `fs task unblock NODE_ID` → `task-unblocked` + `status-changed {from: blocked, to: pending}`
- `fs task update NODE_ID --status done --decision D` → `status-changed` + `decision-recorded`

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

Playbooks are YAML files stored at `~/.fs/kb/<name>.yaml`.

```yaml
name: dev-task-prerequisites
type: prerequisite          # prerequisite | procedure | convention | routing
trigger: dev-task           # which task type or skill triggers this
engine_scope: herdr         # optional — scope to a specific engine
steps:
  - check: test -f AGENTS.md       # kind: body — the kind declares what the step means
  - ask: is the acceptance check stated?
  - say: read AGENTS.md before editing
```

Each step is `kind: body`:

| Kind | Meaning |
|---|---|
| `check` | A shell command `fs dispatch` runs with cwd = the project root. Pass/fail plus the command's output. |
| `ask` | Only the cap can confirm it; dispatch reports `?`. |
| `say` | Worker context, not part of the gate. Carried in the brief text. |

A step line with no recognized kind prefix defaults to `say` (split at the first `:`),
so prose playbooks written before kinds existed still parse. `fs kb get` returns steps
as `{kind, body}` objects.

### Playbook types

| Type | Purpose | Allowed step kinds |
|---|---|---|
| `prerequisite` | Docs/conditions that must exist before starting a task type | `check`, `ask` |
| `procedure` | Step-by-step instructions for a workflow | `say`, `check` |
| `convention` | Coding/naming/formatting rules to follow | — |
| `routing` | Maps task types to skills/engines | exactly one `say` step whose body is the skill name |

`fs kb add` and `fs kb edit` enforce the vocabulary on the write path: an invalid
playbook is refused with an error naming the offending step and the allowed kinds.
`fs kb get` reads whatever is on disk, valid or not.

### CRUD operations

```bash
# Create
cat > /tmp/playbook.yaml << 'EOF'
name: dev-task-prerequisites
type: prerequisite
trigger: dev-task
steps:
  - check: test -f specs/prd.md
  - ask: is the acceptance check stated?
EOF
fs kb add --name dev-task-prerequisites --file /tmp/playbook.yaml

# Read
fs kb get dev-task-prerequisites
# Returns the parsed playbook: steps are {kind, body} objects

# List all, with each playbook's state against the shipped default
fs kb list

# Update
fs kb edit --name dev-task-prerequisites --file /tmp/updated.yaml

# Remove
fs kb remove dev-task-prerequisites
```

### Shipped defaults and drift

Two playbooks ship inside the binary and are seeded into `~/.fs/kb/`: `cap` and
`dev-task-prerequisites` (every other task-type playbook is written by hand).
They live in `src/flagship/defaults/kb/` and are embedded with `//go:embed`, so
the contract is versioned with the code and survives a wipe.

```bash
fs bootstrap                  # create ~/.fs/kb and write every absent playbook
fs kb list                    # each playbook's state against the shipped default
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

The knowledge center (`~/.fs/kb/`) is global — playbooks apply across all projects.

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

### Prerequisite gate

```bash
# Before starting dev work
prereqs=$(fs kb get dev-task-prerequisites)
# Check each step exists (e.g., grep for locked spec markers)
# If missing:
fs task block t-abc --reason "Missing system-design doc" --project myapp
```
