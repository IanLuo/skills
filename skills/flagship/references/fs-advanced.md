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
  - spec                    # each step is a required doc or action
  - system-design
  - architecture
```

### Playbook types

| Type | Purpose |
|---|---|
| `prerequisite` | Docs/conditions that must exist before starting a task type |
| `procedure` | Step-by-step instructions for a workflow |
| `convention` | Coding/naming/formatting rules to follow |
| `routing` | Maps task types to skills/engines |

### CRUD operations

```bash
# Create
cat > /tmp/playbook.yaml << 'EOF'
name: dev-task-prerequisites
type: prerequisite
trigger: dev-task
steps:
  - spec
  - system-design
  - architecture
EOF
fs kb add --name dev-task-prerequisites --file /tmp/playbook.yaml

# Read
fs kb get dev-task-prerequisites
# Returns the YAML content

# List all
fs kb list
# Returns names and types of all playbooks

# Update
fs kb edit --name dev-task-prerequisites --file /tmp/updated.yaml

# Remove
fs kb remove dev-task-prerequisites
```

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
