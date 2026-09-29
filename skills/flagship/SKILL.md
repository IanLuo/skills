---
name: flagship
description: 'Manage projects, tasks, decisions, and knowledge via the flagship (fs) CLI — the persistent memory layer for agent orchestration. Use when the user says "track this project", "create a project", "add a task", "what''s the status", "what tasks are active", "what decisions were made", "record this decision", "resume work on project X", "pick up where I left off", "check prerequisites", "what should I work on", "what''s left to do", "mark this done", "update the task", "project status", "show tasks", "list projects", "fs", "flagship", or any project/task/decision tracking request. ALSO load this skill at session start when `fs project list` shows a managed project matching the current repo — read state before working. Do NOT use for writing code (dev-task), writing specs (specs), spawning agents (herdr), or research (librarian).'
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

After building, bootstrap the global config (three steps):

```bash
# 1. Build
nix develop -c bash src/flagship/build.sh

# 2. Bootstrap ~/.fs/capabilities.yaml from the built-in skeleton
fs init

# 3. Link the knowledge center (playbooks are versioned in-repo)
ln -s "$(pwd)/skills/flagship/kb" ~/.fs/kb
```

`fs resolve` requires **both** artifacts:
- **L1** — `~/.fs/capabilities.yaml` (tool bindings; materialized by `fs init`)
- **L2** — `~/.fs/kb/<detail-playbook>.yaml` (operations, flags, env-aliases; provided by the kb symlink)

`fs init` alone is half a bootstrap — without the kb, `fs resolve` for capabilities that reference a detail-playbook (e.g. `distribution-engine.*`) will fail with "L2 detail playbook not found".

If `~/.fs/kb` already exists as a real directory, leave it — `fs kb add` manages it directly.

If the binary doesn't exist, the skill can't function — run the build first.

## The rule: always read context first

**Before any work**, discover projects and read state. Never create tasks or make changes blind:

```bash
fs project list                          # discover all managed projects
fs status --project <name>               # task tree + all playbooks
fs query "<topic>" --project <name>     # relevant decisions/knowledge
```

In a fresh session, `fs project list` is always the first command — it shows all known projects ordered by last activity. Then drill into the relevant project with `fs status`, which also returns all playbooks.

## Playbook loading

Playbooks are **auto-loaded with `fs status`** — the response includes a `playbooks`
array containing every playbook from the knowledge center, with their type, trigger,
and steps. No separate `fs kb match` call is needed.

After reading the status response, apply the playbooks:

- **Prerequisite** playbooks (`type: prerequisite`) gate the task — verify each step
  exists before proceeding; block the task if anything is missing
- **Procedure** playbooks (`type: procedure`) define the workflow — follow steps in order
- **Convention** playbooks (`type: convention`) define rules — apply them throughout

Match the `trigger` field to the task type you're about to start (e.g. `dev-task`,
`deploy`, `coordinator`). This is not optional. The playbooks are the codified
workflow. Skipping them means flying blind.

For manual lookup, `fs kb match <trigger>` is still available.

## Workflow

1. **Discover** — `fs project list` to find managed projects, then `fs status` for the relevant one (playbooks are included automatically)
2. **Apply playbooks** — match the `trigger` field to your task type; follow prerequisite → procedure → convention order
3. **Pick or create task** — find an existing pending task or `fs task add`
4. **Mark active** — `fs task update <id> --status active`
5. **Work** — use other skills as appropriate (dev-task, specs, etc.)
6. **Record decisions and knowledge** — along the way, not after
7. **Mark done** — `fs task update <id> --status done --decision "<what was decided>"`

## CLI reference

All commands return JSON to stdout: `{"ok": true, "data": {...}}` or `{"ok": false, "error": "..."}`. Exit codes: 0=success, 1=app error, 2=usage. Logs go to stderr.

### Init

```bash
fs init   # Bootstrap ~/.fs/ — writes capabilities.yaml from skeleton if missing
```

Idempotent. Never overwrites existing files. Output: `{ok, data:{created:[...], existed:[...]}}`.

### Project

```bash
fs project create [--name NAME] [--root PATH]   # create + register
fs project list                                  # all known projects (ordered by last activity)
fs project get NAME                              # single project lookup
```

`--root` defaults to git repo root or cwd. `--name` derives from directory if omitted. All projects are registered in `~/.fs/flagship.db`.

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

### Querying

```bash
# Task tree with statuses, goals, decisions
fs status [--project ID]

# Full-text search across all event payloads (FTS5)
fs query "search term" [--project ID]

# Event log (full history)
fs log [--project ID] [--node NODE_ID] [--type TYPE]
```

### Knowledge center (global playbooks)

```bash
fs kb add --name NAME --file PATH     # add YAML playbook
fs kb get NAME                         # read a playbook
fs kb list                             # list all
fs kb match TRIGGER                    # find playbooks by trigger
fs kb edit --name NAME --file PATH     # update
fs kb remove NAME                      # delete
```

Playbooks are YAML at `skills/flagship/kb/<name>.yaml` (versioned in-repo).
`~/.fs/kb` is a symlink to `skills/flagship/kb/`, so `fs kb add/edit` writes
land in the repo — commit playbook changes like code. Format:

```yaml
name: dev-task-prerequisites
type: prerequisite
trigger: dev-task
steps:
  - spec
  - system-design
  - architecture
```

## Event model rules

These are non-negotiable:

1. **Never write to `~/.fs/flagship.db` directly** — only use `fs` commands
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

Every event carries: `id` (ULID), `timestamp` (UTC), `project_id`, `node_id`, `parent_node_id`, `commit_sha`.

## Prerequisite checking

Before starting a task type that has a prerequisite playbook:

1. `fs kb get <task-type>-prerequisites`
2. Verify each required doc exists and is locked
3. If missing → `fs task block <id> --reason "Missing <doc>"`

## Data locations

```
~/.fs/flagship.db              # Single database: project registry + event store
~/.fs/kb → skills/flagship/kb/  # Symlink — playbooks versioned in-repo
skills/flagship/kb/*.yaml       # Canonical playbook copies (git-tracked)
```

All projects share one database. Cross-project search works out of the box.

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
