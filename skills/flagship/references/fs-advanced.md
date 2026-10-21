# Flagship (fs) — Advanced Reference

- The contract: [`.agents/specs/fs-contract.md`](../../.agents/specs/fs-contract.md). This file is the detail behind it.
- Sections: [Event payload schemas](#event-payload-schemas) · [FTS5 query syntax](#fts5-query-syntax) · [Project registry](#project-registry) · [Multi-project workflows](#multi-project-workflows) · [Store integrity and WAL](#store-integrity-and-wal) · [Common patterns](#common-patterns)

## Event payload schemas

- Every event is a JSON object: a fixed envelope plus a type-specific payload.

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

- `node_id` is null only for `project-created` and `cap-claimed`, which address a project and not a node.

### Payloads by type

| Type | Payload fields |
|---|---|
| `project-created` | `name`, `root_path` |
| `task-created` | `goal`, `kind`, `found_by`, `check`, `severity`, `integrates`, `reviews` — the last two empty for a node that links to nothing |
| `status-changed` | `from`, `to` — `pending`, `active`, `done`, `blocked` |
| `decision-recorded` | `summary` |
| `knowledge-added` | `summary` |
| `task-blocked` | `reason`, `check` |
| `task-unblocked` | empty object |
| `metadata-changed` | `field`, `old_value`, `new_value` |
| `delivery-recorded` | `pane_id`, `tab_id`, `agent`, `engine`, `project`, `node`, `type`, `worktree`, `worktree_path`, `branch` — `branch` only for a worktree this delivery created |
| `report-recorded` | `kind` (`blocked`, `question`, `done`, or absent for a note), `line` — addressed to the worker's own node |
| `cap-claimed` | `pane_id`, `tab_id`, `agent`, `claimed_at`, and on a take-over `taken_over_from`, `taken_over_at`, `reconstructed`. No `node_id` |

### Composite operations

- `fs task block NODE --reason R` → `task-blocked` + `status-changed {from: current, to: blocked}`
- `fs task unblock NODE` → `task-unblocked` + `status-changed {from: blocked, to: pending}`
- `fs task update NODE --status done --decision D` → `status-changed` + `decision-recorded`
- `fs dispatch` → `task-created` (the cap node) + `decision-recorded` (the project the work went to)
- `fs report NODE --blocked --line L` → `report-recorded` + `task-blocked {reason: L, check: ""}` + `status-changed {from: current, to: blocked}`; any other kind is `report-recorded` alone
- `fs task merge` → two `decision-recorded` + two `status-changed`

## FTS5 query syntax

- `fs query` searches event payloads with SQLite FTS5, ranked by BM25.

```bash
fs query "authentication" --project myapp
fs query '"JWT refresh token"' --project myapp    # exact phrase
fs query "auth NOT session" --project myapp       # AND is implicit; OR and NOT explicit
fs query "auth*" --project myapp                  # prefix
```

## Project registry

- The global registry at `~/.fs/registry.db` tracks every known project.

| Column | Type | Description |
|---|---|---|
| `name` | TEXT (unique) | the `--project` identifier |
| `root_path` | TEXT | absolute path to the project root |
| `created_at` | TIMESTAMP | when `fs project create` ran |
| `last_activity` | TIMESTAMP | updated on every project-targeting command |

- `fs project create` registers a project.
- `fs project list` returns every project, most recently active first.
- `fs project get NAME` returns one entry.
- The registry is managed only by `fs`. Never write to it directly.

## Multi-project workflows

- One store, `~/.fs/store.db`, holds every project's events partitioned by `project_id`.
- `--project` selects the partition, so any project is reachable from any directory.

```bash
fs project list
fs status --project frontend
fs status --project backend
fs query "shared API contract" --project backend
```

- With `--project` omitted, the target is the registered project whose `root_path` owns the working directory; an unregistered directory resolves to its own name.
- `fs pending` is the exception: it reads every project, because a dispatch is a dispatch wherever it was written.

## Store integrity and WAL

- **WAL mode** — write-ahead logging for crash safety; transactions commit atomically.
- **Append-only** — events are never modified or deleted; every status rebuilds from replay.
- **Corruption** — a damaged header or schema is refused at open. A full `PRAGMA integrity_check` is deliberately not run per command: it is linear in file size, and the store grows with every project. A damaged data page is reported by SQLite when that page is read.
- **FTS5 self-healing** — a corrupted index rebuilds on the next query.
- **Concurrent writers** — commands wait up to 5s for the write lock (`busy_timeout`) instead of failing with SQLITE_BUSY. Very long write bursts still serialize on SQLite's single writer.
- **Search cost** — FTS5 matches the whole store and then filters by `project_id`, so a common term costs more than its own project's size.

## Common patterns

### Resume a session

```bash
fs pending                                  # anything waiting on you, first
fs status --project myapp                   # the task tree
fs query "authentication decisions" --project myapp
fs log --project myapp --node t-abc12345    # one node's history
```

### Subtask decomposition

```bash
fs task add --goal "Implement auth" --project myapp
fs task add --goal "JWT middleware" --parent t-aaa11111 --project myapp
fs task add --goal "Login endpoint" --parent t-aaa11111 --project myapp
```

### Decision audit trail

```bash
fs task update t-abc --status done \
  --decision "Chose PKCE over implicit grant for public client security" --project myapp
fs log --project myapp --type decision-recorded
fs query "PKCE implicit" --project myapp
```

### Knowledge capture

```bash
fs task knowledge t-abc \
  --summary "modernc.org/sqlite is pure Go — no CGO needed" --project myapp
fs query "SQLite CGO" --project myapp
```
