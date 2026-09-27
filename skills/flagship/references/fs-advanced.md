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
| `plan-recorded` | `plan` (ordered `{name, moments, order}` entries), `tags` (the tag set that selected them), and the resolved realization — `engine`, `agent`, `agent_args` — so which harness ran is reconstructible from the record alone |
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

A playbook is a protocol for a kind of work, and it resolves from one of three
scopes. The **shipped** set rides inside the binary and runs from there, so it is
never installed; a global override lives at `~/.fs/kb/<name>.yaml` and a project's
own at `~/.fs/projects/<project>/kb/<name>.yaml`. A playbook with the same name
**overrides the fields it declares and inherits the rest** from the scope beneath
it — project over global over shipped — for a dispatch into that project.

```yaml
# name is required, and must equal the file's name.
name: my-gate
# applies_when is optional; empty matches every dispatch. Comma-separated terms,
# ALL of which must hold. A term is `k` (present) or `k=v`.
applies_when: code
# order is optional, default 500; ascending, ties by name.
order: 10
# One section per moment: cap | entry | work | exit.
entry:
  - check: test -f AGENTS.md
  - ask: a locked PRD is present, and it names this one deliverable
  - say: read AGENTS.md before editing
```

A comment must own its line. The parser is line-oriented and does not strip a
trailing one, so `name: my-gate  # mine` declares a name of `my-gate  # mine`
and is refused for not matching the file — which is why every comment above sits
on a line of its own.

Each step is `kind: body`, and the closed kind vocabulary is `say | use | check | ask | do | include`:

| Kind | Meaning |
|---|---|
| `check` | A shell command the gate runs: `fs dispatch` with cwd = the project root, `fs close` with cwd = the tree the worker ran in (the `worktree_path` the delivery record kept, or the project root when the record names no worktree). Pass/fail plus the command's output. |
| `ask` | Only the cap can confirm it; reported `?` at dispatch and refused at close until `--confirm`. |
| `do` | A shell command that acts — the declared teardown. The `exit:` section only; runs in the project root. |
| `say` | Worker context (or close prose), not part of the gate. Carried in the brief text. |
| `use` | A skill hint for the worker, as `<skill> — <when>`. |
| `include` | Splices another playbook's steps at this position. |

A step line with no recognized kind prefix defaults to `say` (split at the first `:`),
so prose playbooks written before kinds existed still parse. `fs kb get` returns steps
as `{kind, body}` objects.

Which kinds a section allows is a closed table — anything else is refused at write time:

| section | runs | allowed kinds |
|---|---|---|
| `cap:` | the cap's standing rules, in the session prompt | `say` |
| `entry:` | before the worker starts | `check`, `ask`, `say` |
| `work:` | the worker's brief | `say`, `use` |
| `exit:` | at close | `check`, `ask`, `do`, `say` |

`include` is allowed in every section — it is spliced away before validation, and
what it splices must itself be legal for the including playbook's moment. A cycle
is refused with a message naming the chain; include depth over 8 is refused too.

The removed fields `phase:`, `steps:`, `type:`, `trigger:` and `engine_scope:` are
**refused at parse**, each with a message naming its replacement: `phase: entry` →
the `entry:` section holding the same list; `steps:` → one section per moment;
`trigger: cap` → the `cap:` section; `engine_scope: herdr` → the declaration
`engine: herdr` (the term `applies_when: engine=herdr` is only for matching an
engine, not declaring one); `type: prerequisite|cleanup` → the section of that
moment — `entry:` for a prerequisite, `exit:` for a cleanup — plus an
`applies_when` term naming the work it matches.

A dispatch's `check` steps see the dispatch's own inputs in the environment, so
a gate can be about *this* dispatch — which cards, which member — and not only
the world: `FS_PROJECT`, `FS_TYPE`, `FS_GOAL`, `FS_CARDS` (the `--cards` value,
comma-separated, empty when none was given, and never unset), `FS_INTEGRATES`
(the `--integrates` value), and `FS_BIN` (the running binary's own path). That is
what `batch.yaml` uses:
`check: "$FS_BIN" script check-parallel.sh $FS_CARDS`. `fs close`'s exit steps
get a different set: `FS_PROJECT`, `FS_TYPE`, `FS_NODE`, `FS_WORKER`,
`FS_CARDS` (empty), `FS_INTEGRATES`, `FS_PANE`, `FS_WORKTREE`,
`FS_WORKTREE_PATH`, `FS_BRANCH` (the branch the delivery created its worktree
on, empty when this delivery created none), `FS_BATCH`, `FS_WITHOUT_REVIEW`,
`FS_BIN`.

A shipped playbook is run in every project, so a step it declares may name an
`fs` subcommand, a generic POSIX tool, or a script the binary ships — through
`"$FS_BIN"` either way: `"$FS_BIN" <sub>` and `"$FS_BIN" script <name> [args…]`.
Never `bin/…` or `tests/…`, which resolve in one repo and exit 127 in the rest,
and never a bare `fs`, since the binary is installed into `~/.fs` and not into
PATH. The scripts are read out of the binary rather than written anywhere, so
there is no directory to address and nothing to go stale; `FS_BIN` is empty when
the path cannot be read, which a step must treat as missing rather than run.

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
applies_when: code
entry:
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

The shipped set rides inside the binary and runs from there — nothing is
installed. The names are not restated here, because they are a fact the code owns
and a list in prose is one nobody checks: `fs kb list` prints every playbook with
the scope it resolves from, and `tests/playbooks/test_playbooks.py` is where the
set is measured, so a name added to or dropped from it fails there. They are read
out of `src/flagship/defaults/kb/` with `//go:embed`, so the contract is
versioned with the code and survives a wipe.

```bash
fs kb list                    # each playbook's scope and state against the shipped default
fs kb diff NAME               # shipped default vs the copy on disk
fs kb reset NAME --yes        # write the shipped default to disk, to edit
```

`~/.fs/kb/` is for overrides only, and a fresh home has no such directory: the
shipped set resolves out of the binary. `kb reset --yes` is the one command that
writes a default to disk — how you ask for a copy to edit, and how an edited copy
goes back. It creates the scope's directory, so it works where nothing is there
yet. A written copy starts with the stamp line

```yaml
# fs-default: sha256=<sha256 of the shipped default's body>
```

which is what lets `kb list` separate the two ways a playbook can differ from
the binary:

| state | meaning |
|---|---|
| `shipped` | no file on disk; the binary's default runs |
| `default` | a copy identical to the shipped default — transparent |
| `edited` | a copy whose body differs from the stamp it carries |
| `stale` | the copy was stamped from an older default |
| `local` | no shipped default with that name — hand-written |

The comparison is `edited` = body hash ≠ stamp, `stale` = stamp ≠ the current
default's hash; both can hold at once and both are reported as flags alongside
the headline `state` (stale wins). A playbook with no stamp is compared to the
shipped default as it is now, so an identical hand-written file reads `default`.
`fs kb list` also reports each playbook's `scope` (`shipped`, `global` or the
project name) and `used_by` (`prompt: …` for a `cap` playbook, `dispatch: …`
otherwise, `none` when nothing selects it).

Only an **override** participates: a copy that restates the default is
transparent and the shipped default below it resolves, so a copy can never pin
the binary and a `git pull` that changes a playbook is live with no operator
step. `edited` is the one predicate behind both resolution and reporting, so
`kb list` and a dispatch cannot disagree. A copy that has fallen behind is
reported `stale`, and it runs only if it is also edited. `kb diff` reads the copy
rather than what resolves, so a stale copy shows its drift, and it ignores the
stamp line, so a copy that restates the default diffs empty.

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

The knowledge center is three-scoped: the shipped set in the binary, the global
`~/.fs/kb/`, and each project's `~/.fs/projects/<project>/kb/`. A playbook with
the same `name` overrides the fields it declares and inherits the rest from the
scope beneath it — project over global over shipped — for a dispatch into that
project; `fs kb add|edit --project P` writes to the project's own KB, and
`fs kb list` reports each playbook's `scope`.
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
# `code` playbook's entry checks (AGENTS.md present; a locked doc grep matches)
# and asks you to confirm a locked PRD names the deliverable and the acceptance
# check is stated. Fix what it refuses, or answer its asks.
fs dispatch --project myapp --type dev-task --goal "..." --tag code

# If something is missing and you cannot fix it now, block the task and name
# what would show the condition is over:
fs task block t-abc --reason "Missing system-design doc" --project myapp
```
