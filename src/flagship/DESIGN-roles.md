# DESIGN-roles.md — Captain/Worker Multi-Agent Coordination

> Architecture design doc. No implementation yet.
> Implement from this document; every CLI command, event type, db column, and
> playbook step named here is the contract.

---

## 1. Problem Statement

One captain (coordinator) agent and N worker agents operate against a shared fs
event store. Each agent must know its role durably — derived from fs state, not
from who prompted it. Workers must record all work as fs events, even when the
user talks to a worker directly (bypassing the captain). The captain must
discover state changes via push signals and periodic heartbeats.

---

## 2. fs Agent Registry

### 2.1 Concept

A new first-class entity: **agent registrations**. An agent registration binds a
name, role, herdr pane, and liveness metadata. Stored in the global registry db
(`~/.fs/flagship.db`) alongside the existing `projects` table — NOT as events in
the event store (agents are infrastructure, not project history).

### 2.2 Schema — new `agents` table

```sql
-- In registry.go's createSchema, add:
CREATE TABLE IF NOT EXISTS agents (
    name        TEXT PRIMARY KEY,      -- unique agent name (e.g. "captain", "impl-auth")
    role        TEXT NOT NULL,         -- "captain" | "worker" | "reviewer"
    pane_id     TEXT NOT NULL,         -- herdr pane ID (e.g. "w1:p1")
    workspace   TEXT NOT NULL DEFAULT '',  -- herdr workspace ID
    project     TEXT NOT NULL DEFAULT '',  -- project name; empty = global agent, matches any --project filter
    status      TEXT NOT NULL DEFAULT 'active',  -- "active" | "idle" | "dead"
    playbook    TEXT NOT NULL DEFAULT '',  -- bound playbook name (e.g. "captain-playbook", "worker-playbook")
    last_seen   TEXT NOT NULL,         -- RFC3339Nano timestamp, updated on every fs call
    created_at  TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_agents_role ON agents(role);
CREATE INDEX IF NOT EXISTS idx_agents_project ON agents(project);
```

This lives in `internal/registry/registry.go` — extend the existing `Registry`
struct. The `agents` table shares the same `~/.fs/flagship.db` file as
`projects`.

### 2.3 CLI surface

```
fs agent register --name <name> --role <captain|worker|reviewer> --pane <pane_id> \
                  [--workspace <ws_id>] [--project <name>] [--playbook <name>]
    # Captain-uniqueness: errors if an active captain already exists for
    # the same project. Succeeds if the existing captain is dead (replaces it).
    # See §10 Q2 (RESOLVED).
fs agent get <name>
fs agent get --self                   # identity discovery (see §2.4)
fs agent list [--role <role>] [--project <project>] [--status <status>]
fs agent heartbeat <name>             # touch last_seen
fs agent update <name> [--status <status>] [--project <project>]
fs agent deregister <name>
```

**Implementation location:** new subcommand group in `cmd/main.go` → `agentCmd()`,
backed by new methods on `registry.Registry`.

**JSON envelope:** same `{ok, data, error}` shape as every other fs command.

### 2.4 Identity discovery — how an agent learns its own role

An agent discovers itself via **environment → registry lookup**:

1. Read `$HERDR_PANE_ID` (always set by herdr for every pane).
2. Run `fs agent get --self` which internally does:
   ```
   SELECT * FROM agents WHERE pane_id = $HERDR_PANE_ID
   ```
3. The returned record contains `role`, `playbook`, `project`, `name`.

If no record exists → the agent is unregistered → it should refuse role-specific
behavior and prompt the user/captain to register it. This is the "durable
identity" contract: the role is in the db, not in the prompt that launched the
agent.

**Implication for the captain-playbook `delegate` step:** after starting the
agent (via `fs resolve distribution-engine.start-agent`), the captain must also
run `fs agent register` for the new worker before prompting it. The worker's first action (from its playbook) is `fs agent get --self` to
confirm identity.

### 2.5 last_seen update

Every `fs` CLI call that opens the registry should opportunistically update
`last_seen` for the calling agent (looked up from `$HERDR_PANE_ID`). This is
a best-effort write — if the agent isn't registered, skip silently.

**Implementation:** in `cmd/main.go`’s **`handler()`** function (which opens the
global store), add the touch after opening the registry:

```go
// In cmd/main.go handler():
func handler() *command.Handler {
    dbPath := globalDBPath()
    h, err := command.NewHandler(dbPath)
    // ... existing setup ...

    // Opportunistic last_seen touch (best-effort, not in command.go's Handler).
    paneID := os.Getenv("HERDR_PANE_ID")
    if paneID != "" {
        reg := openRegistry()
        reg.TouchAgent(paneID)
        reg.Close()
    }
    return h
}
```

The touch lives in `cmd/main.go`’s `handler()`, NOT in `command.go`’s `Handler`.
`command.Handler` is the event-store gatekeeper; agent liveness is a registry
concern, and mixing them would create a dependency from `command` → `registry`.

`registry.TouchAgent(paneID)` does:
```sql
UPDATE agents SET last_seen = ? WHERE pane_id = ?
```

This means `last_seen` advances whenever the agent runs *any* `fs` command —
passive liveness without a dedicated heartbeat call.

**SQLITE_BUSY mitigation:** because every `fs` call now writes `last_seen`, even
read-only commands (like `fs status`) produce a write. Under concurrent agents,
this increases `SQLITE_BUSY` risk. **`registry.Open()` must set a `busy_timeout`
PRAGMA** (it currently doesn’t):

```go
// In registry.Open(), after setting WAL mode:
if _, err := db.Exec("PRAGMA busy_timeout = 5000"); err != nil {  // 5 seconds
    db.Close()
    return nil, fmt.Errorf("registry: set busy_timeout: %w", err)
}
```

Without this, concurrent `fs` calls from multiple agents will sporadically fail
with `SQLITE_BUSY` instead of retrying.

---

## 3. Task Assignee

### 3.1 Approach — metadata-changed event

Task assignment is modeled as a `metadata-changed` event with
`field = "assignee"`. This avoids adding a new event type and keeps the event
store schema untouched.

**Payload shape:**
```json
{
  "field": "assignee",
  "old_value": "",
  "new_value": "impl-auth"
}
```

To unassign: `new_value = ""`.

### 3.2 CLI

```
fs task assign <node_id> --to <agent_name> [--project <project>]
fs task unassign <node_id> [--project <project>]
```

These are sugar over `metadata-changed`:
- `fs task assign t-abc123 --to impl-auth` → appends `metadata-changed` with
  `field=assignee, old_value=<current>, new_value=impl-auth`.
- `fs task unassign t-abc123` → appends `metadata-changed` with
  `field=assignee, old_value=<current>, new_value=""`.

**Implementation:** new functions in `internal/command/command.go`:
`TaskAssign(projectID, nodeID, agentName)` and `TaskUnassign(projectID, nodeID)`.
Both call `deriveAssignee` (same pattern as `deriveStatus`/`deriveGoal` — replay
metadata-changed events filtered on the node, find last `field=assignee` event).

### 3.3 Query engine — assignee in derived state

Extend `query.Node` with:
```go
Assignee string `json:"assignee,omitempty"`
```

In `BuildTree`, handle `metadata-changed` events where `field == "assignee"`:
set `node.Assignee = p.NewValue`.

### 3.4 Status output — assignee-aware

`fs status` output includes `assignee` in each `TaskInfo`. Captain reads this to
see:
- **Unassigned pending tasks** → needs distribution.
- **Assigned active tasks** → being worked.
- **Assigned done tasks** → needs review/collection.
- **Assigned blocked tasks** → needs intervention.

Add `--assignee <name>` filter to `fs status` for workers to see only their own
tasks:
```
fs status --project skills --assignee impl-auth
```

**Implementation:** extend `StatusResult`/`TaskInfo` in `command.go`, add filter
logic in `Status()`.

### 3.5 Captain's unassigned-task view

The captain identifies distributable work by:
```
fs status --project <name>
```
Then scanning for tasks where `status == "pending"` AND `assignee == ""`.
No special CLI flag needed — it's a read of the status tree.

---

## 4. Role Playbooks

### 4.1 Captain playbook — existing, to be extended

File: `~/.fs/kb/captain-playbook.yaml`

**Changes from current version:**

Add a new `register-agent` sub-step to the `delegate` step:
```yaml
  - id: delegate
    sequence:
      - check pane layout
      - split a new pane (via fs resolve distribution-engine.spawn-pane)
      - start agent in the new pane (via fs resolve distribution-engine.start-agent)
      - register agent: fs agent register --name <task-name> --role worker \
          --pane <new-pane-id> --workspace $WORKSPACE_ID \
          --project <name> --playbook worker-playbook
      - assign task: fs task assign <task-id> --to <task-name> --project <name>
      - prompt the agent with the task (via fs resolve distribution-engine.prompt-agent)
      # NOTE: the WORKER marks its own task active (worker-playbook §4.2 'work' step),
      # NOT the captain. See §9.1 for the full sequence.
```

Add a new `heartbeat` step (see §5).

### 4.2 Worker playbook — new

File: `~/.fs/kb/worker-playbook.yaml`

```yaml
name: worker-playbook
type: procedure
trigger: worker
description: >
  Behavior contract for a worker agent. The worker identifies itself from
  fs state, works only on its assigned tasks, records everything to fs,
  and signals the captain on completion or blockage.

preconditions:
  - binary: fs on PATH
  - check: fs resolve --check

steps:
  # ── 0. Identity ──
  - id: identify
    action: discover own role and assignments
    commands:
      - fs agent get --self
    description: >
      First action on every session/wake. Confirms name, role=worker,
      project, and playbook. If unregistered, STOP and wait — the
      captain has not set you up yet.

  # ── 1. Claim ──
  - id: claim
    action: read assigned tasks and begin work
    commands:
      - fs status --project <project> --assignee <self-name>
    description: >
      List only your tasks. Work on the first pending/active task.
      Never pick up tasks assigned to another agent. Never start a
      task that is not assigned to you.

  # ── 2. Work ──
  - id: work
    action: execute the task
    description: >
      Do the concrete work: code, research, test, whatever the goal says.
      Record intermediate decisions and knowledge as you go:
        fs task update <task-id> --status active --project <project>
        fs task knowledge <task-id> --summary "<finding>" --project <project>

  # ── 3. Complete / Block ──
  - id: signal
    action: update fs AND push-signal the captain
    on-done:
      - fs task update <task-id> --status done --decision "<outcome>" --project <project>
      - fs resolve signal.send-to-agent --target <captain-name> --message "SIGNAL: task <task-id> done by <self-name>. Outcome: <short summary>"
    on-blocked:
      - fs task block <task-id> --reason "<reason>" --project <project>
      - fs resolve signal.send-to-agent --target <captain-name> --message "SIGNAL: task <task-id> blocked by <self-name>. Reason: <reason>"
    description: >
      ALWAYS update fs first, THEN signal. The fs update is the source
      of truth; the signal is a performance optimization. If the signal
      fails (captain pane closed, etc.), the captain's heartbeat will
      still pick it up.

  # ── 4. Direct-to-worker discipline ──
  - id: direct-work
    action: handle user requests that bypass the captain
    description: >
      If the user gives you work directly (not via captain assignment):
      1. BEFORE doing anything, create the task in fs:
         fs task add --goal "<what the user asked>" --project <project>
      2. Assign it to yourself:
         fs task assign <new-task-id> --to <self-name> --project <project>
      3. Signal the captain that new work exists:
         fs resolve signal.send-to-agent --target <captain-name> --message "SIGNAL: new task <new-task-id> created directly by user on worker <self-name>"
      4. THEN begin work (step 2 above).
      This ensures the captain's next heartbeat sees the task even if
      the signal is lost.

safety:
  - Always fs agent get --self first — never assume your role
  - Never work on tasks assigned to another agent
  - Never create tasks without recording them in fs first
  - Always signal the captain after status changes
  - If you can't reach the captain, the fs update is sufficient —
    the heartbeat will catch it
```

### 4.3 Reviewer playbook — new

File: `~/.fs/kb/reviewer-playbook.yaml`

The reviewer is a **read-only** role. It receives an artifact path and review
criteria from the captain, reads the artifact thoroughly, records material
findings to fs, and delivers a verdict. It **never modifies the artifact** — no
edits, no code changes, no commits. If fixes are needed, the verdict is
`needs-changes` and the captain dispatches a separate fixer worker.

**Verdict contract:** exactly one of `approve | needs-changes | reject`. The
verdict is recorded as:
```
fs task update <task-id> --status done \
  --decision "VERDICT: <verdict>. <summary>" --project <p>
```
The `VERDICT:` prefix is machine-parsed by the captain.

**Findings:** each material finding is recorded individually:
```
fs task knowledge <task-id> --summary "<finding>" --project <p>
```
Findings cite sections/lines, state the issue, and describe impact.

**Signal:** after updating fs, the reviewer signals:
```
fs resolve signal.send-to-agent --target <captain> --message "SIGNAL: review <task-id> verdict=<verdict> by <self>. <one-line>"
```

**Steps (mirroring worker-playbook structure):**

| Step | Action | Key command |
|---|---|---|
| identify | Confirm role=reviewer | `fs agent get --self` |
| claim | Find assigned review task | `fs status --project <p> --assignee <self>` |
| review | Read artifact against criteria | (read-only — no modifications) |
| findings | Record each material finding | `fs task knowledge <task-id> --summary ...` |
| verdict | Deliver approve/needs-changes/reject | `fs task update <task-id> --status done --decision "VERDICT: ..."` |
| signal | Push-notify the captain | `fs resolve signal.send-to-agent --target <captain> --message "SIGNAL: review ..."` |

**Safety (hard constraints):**
- NEVER modify the artifact — no edits, no code, no commits.
- If fixes are needed → verdict is `needs-changes`; captain dispatches a fixer.
- Always record findings before the verdict.
- Always signal after the fs update.

### 4.4 Role → playbook binding

**Binding is stored in the `agents` table** (`playbook` column, set at
`fs agent register` time). The agent's startup sequence is:

1. `fs agent get --self` → gets `playbook` field.
2. `fs kb get <playbook>` → loads the procedure.
3. Follow the procedure.

**`fs status` auto-loads playbooks by role:** extend the existing
`loadAllPlaybooks()` to annotate which playbook is bound to the calling agent
(based on `$HERDR_PANE_ID` lookup). This is informational — the agent reads it
and follows it.

No code enforces the playbook — it's a behavior contract that the agent's skill
/ system prompt includes. The `captain-playbook`, `worker-playbook`, and
`reviewer-playbook` are loaded by the agent's skill (the herdr/flagship skills)
at session start.

---

## 5. Liveness Mechanics

### 5.1 Push signals — worker → captain

**Format:** a capability-resolved signal to the captain with a structured prefix:

```
fs resolve signal.send-to-agent --target <captain-name> --message "SIGNAL: <event-type> <task-id> <agent-name> [detail]"
```

**Defined signal types:**
| Signal | Meaning |
|---|---|
| `SIGNAL: task <id> done by <agent>. Outcome: <text>` | Task completed |
| `SIGNAL: task <id> blocked by <agent>. Reason: <text>` | Task blocked |
| `SIGNAL: new task <id> created directly by user on worker <agent>` | Direct-to-worker work registered |
| `SIGNAL: review <id> verdict=<verdict> by <agent>. <text>` | Review verdict delivered |

**The captain name:** the worker discovers it via:
```
fs agent list --role captain --project <project>
```
There should be exactly one captain per project. If none found, the worker skips
the push signal — fs update is sufficient.

**Push is fire-and-forget:** if the captain's pane is gone or busy, the signal
may be dropped. That's acceptable — the heartbeat (§5.2) is the safety net.

### 5.2 Heartbeat — captain periodic scan

**Mechanism: capability-bound periodic timer (see DESIGN-capabilities.md §2.3,
§7).** The captain schedules a recurring self-prompt at session start via:
```
fs resolve scheduler.heartbeat-arm --interval 25m --prompt "HEARTBEAT: run heartbeat scan"
```
The concrete mechanism is bound in `~/.fs/capabilities.yaml`: `CronCreate` for
Claude captains, `ScheduleWakeup` for pi, or an external cron wrapper script.
The playbook never names a concrete scheduler.

Interval is ~20-30 min, off-minute (e.g. 25m, not 30m) per harness scheduling
guidance to avoid contention with round-number timers.

**Where the timer lives:** in the captain's Claude **session memory** — not on
disk, not in fs, not in herdr. Consequences:
- Fires ONLY while the captain session is alive AND idle. If the captain is
  mid-turn when the cron fires, delivery is deferred until the turn ends (correct
  — never interrupt active work).
- Dies when the captain session exits. There is no surviving timer.
- Recurring cron auto-expires after 7 days. The captain re-arms it on session
  start (the `orient` step calls `fs resolve scheduler.heartbeat-arm`
  unconditionally).

**Durability model — three layers:**

| Layer | Path | Covers |
|---|---|---|
| 1. Push signals (`fs resolve signal.send-to-agent`) | Fast path | Worker → captain on task done/blocked/new |
| 2. In-session heartbeat (this timer) | Safety net | Missed signals, stale agents, new unassigned work — **while the captain session lives** |
| 3. Orient-on-restart | Recovery | Captain session dies → user starts a new captain → `orient` step re-scans all fs state + re-arms heartbeat cron |

**Known gap (v1):** if the captain session closes AND no one restarts it, no
heartbeat runs. Workers keep working (fs state is durable), but no coordination
happens. Covering this requires an external watchdog — OS-level cron or herdr
post-close hook that detects a missing captain and alerts the user. Deferred to
v2 (cross-ref §5.3).

```yaml
  # ── Heartbeat (new step, added to captain-playbook) ──
  - id: heartbeat
    action: periodic scan of fs for state changes needing captain action
    trigger: >-
      fs resolve scheduler.heartbeat-arm (~25min interval, re-armed on orient)
    commands:
      - fs status --project <name>
      - fs agent list --project <name>
    description: >
      Fired by the in-session cron timer. On each wake, scan for:
      1. Unassigned pending tasks (assignee="" AND status=pending) → distribute
      2. Done tasks still assigned (status=done) → collect results, unassign
      3. Blocked tasks (status=blocked) → diagnose, reassign or escalate
      4. Stale agents (last_seen > 10min AND status=active) → check health
      5. New tasks created by workers (direct-to-worker) → acknowledge, maybe re-parent
      Idempotent: processing a task the captain already handled is a no-op
      because the task's status/assignee will have already changed.
```

### 5.3 Known gap (v1)

If the captain session closes AND no one restarts it, no heartbeat runs. Workers
keep working (fs state is durable), but no coordination happens. Covering this
requires an external watchdog — OS-level cron or herdr post-close hook that
detects a missing captain and alerts the user. Deferred to v2.

### 5.4 Captain Recovery (consolidated)

> Previously spread across §5.3, §6.3, §10 Q2 nuance, and the orient step.
> This subsection is the single reference for captain recovery.

**Scenario:** captain session dies.

1. **In-session heartbeat dies with it.** The heartbeat timer (CronCreate,
   ScheduleWakeup, etc.) is session-bound — it does not survive the process.
2. **Workers keep working.** They have their assigned tasks in fs. Worker
   signals to the dead captain are lost, but fs state persists.
3. **User starts a new captain.** There is no automatic captain restart.
4. **New captain orients.** The `orient` step runs:
   ```
   fs project list
   fs status --project <name>
   fs resolve distribution-engine.list-agents
   fs resolve distribution-engine.list-workspaces
   ```
5. **Reconcile stale agents.** For each registered agent with stale `last_seen`:
   ```
   fs resolve distribution-engine.agent-status --name <name>
   ```
   If the distribution engine reports agent not found (pane closed) →
   `fs agent update <name> --status dead`.
6. **Reassign orphaned active tasks.** Any task with `status=active` whose
   assignee is now `dead` → `fs task unassign <task-id>`, making it available
   for redistribution on the next plan/delegate cycle.
7. **Re-register self.** If the old captain registration is dead (§10 Q2),
   `fs agent register --name captain --role captain ...` succeeds (replaces
   the dead registration).
8. **Re-arm heartbeat.**
   ```
   fs resolve scheduler.heartbeat-arm --interval 25m --prompt "HEARTBEAT: run heartbeat scan"
   ```
9. **Resume normal operation.** Unassigned pending tasks → plan → delegate.

---

## 6. Concurrency & Safety

### 6.1 Double-claim prevention — task assignment

**Problem:** two workers (or captain + worker) attempt to assign the same task
simultaneously.

**Solution: optimistic concurrency via event replay.**

`TaskAssign` in `command.go`:
1. Derive current assignee by replaying `metadata-changed` events for the node.
2. If already assigned to a different agent → return error:
   `"task already assigned to <other-agent>"`.
3. If unassigned (or assigned to self) → append the `metadata-changed` event.

**This is NOT truly atomic** — there's a TOCTOU window between the read and the
write. Each `fs` invocation is a **separate OS process** with its own SQLite
connection. SQLite WAL serializes **writes** (single-writer) but does NOT
serialize the read-then-write window across processes: process A reads
"unassigned", process B reads "unassigned", both write an assignment, and the
last writer wins silently.

Practical mitigation:
- In practice, only the captain assigns tasks. Workers don't race each other.
- If two captains exist (a bug), the second assign will fail on the next
  heartbeat when the captain re-derives state and sees the assignee changed.

**Acceptable for v1.** A future v2 could add a compare-and-swap event type, but
the event-sourced model makes this low-risk: you can always replay to detect
inconsistencies.

### 6.2 Captain re-distributing a mid-flight task

**Rule: the captain MUST NOT reassign a task that has `status=active`.**

The heartbeat only considers:
- `status=pending, assignee=""` → assign.
- `status=done` → collect.
- `status=blocked` → diagnose.

An active task with a live agent is untouchable. If the agent is stale
(`last_seen` too old), the captain should:
1. Check agent health: `fs resolve distribution-engine.agent-status --name <name>`.
2. If the agent is dead, `fs agent update <name> --status dead`, then
   `fs task unassign <task-id>` to make it available for reassignment.
3. If the agent is alive but stale, prompt it:
   `fs resolve distribution-engine.prompt-agent --name <name> --prompt "status check"`.

### 6.3 Stale heartbeats

An agent's `last_seen` drifts when it's doing long work without calling `fs`.
The captain should not consider an agent dead based solely on `last_seen`. The
check order:
1. `last_seen` > threshold (10 min) → suspicious.
2. `fs resolve distribution-engine.agent-status --name <name>` → check lifecycle.
3. If engine says `idle` or `working` → agent is fine, just hasn't called fs.
4. If engine says agent is gone / pane closed → mark dead.

For full captain recovery after a crash, see §5.4.

### 6.4 Event store safety

The existing event store guarantees apply:
- **Append-only** — events are immutable. No UPDATE/DELETE on the events table.
- **WAL mode** — crash-safe.
- **Single-writer** — SQLite serializes writes. Concurrent reads are safe.
- **Per-command transactions** — `AppendBatch` is atomic.

Multiple agents calling `fs` concurrently is safe because each call opens its own
connection, and SQLite WAL allows concurrent readers with a single writer. Write
contention is low (agents are IO-bound on LLM calls, not on fs writes).

---

## 7. Compatibility & Migration

### 7.1 Additive changes (non-breaking)

| Change | Location | Why additive |
|---|---|---|
| New `agents` table | `internal/registry/registry.go` `createSchema` | `CREATE TABLE IF NOT EXISTS` — existing db unaffected |
| New `assignee` field on `Node` | `internal/query/query.go` | Derived from existing `metadata-changed` events — no schema change |
| New `assignee` in `TaskInfo` | `internal/command/command.go` | Additional JSON field — old consumers ignore it |
| New CLI commands: `fs agent *`, `fs task assign/unassign` | `cmd/main.go` | New subcommands don't affect existing ones |
| `--assignee` flag on `fs status` | `cmd/main.go` | Optional flag — omitting it gives existing behavior |
| `last_seen` touch in handler | `cmd/main.go` | No-op when agent not registered |
| Worker-playbook KB entry | `~/.fs/kb/worker-playbook.yaml` | New file, doesn't modify existing playbooks |
| Reviewer-playbook KB entry | `~/.fs/kb/reviewer-playbook.yaml` | New file, doesn't modify existing playbooks |

### 7.2 No breaking changes

- **Events table schema: unchanged.** No new columns, no new event types. The
  `metadata-changed` event type already exists and supports arbitrary fields.
- **Existing CLI commands: unchanged.** All existing `fs project/task/status/
  query/log/kb/migrate` commands continue to work identically.
- **Registry db: additive.** The new `agents` table is created alongside
  `projects` with `IF NOT EXISTS`.
- **Playbooks: additive.** `captain-playbook.yaml` gains new steps but existing
  steps are unchanged. `worker-playbook.yaml` is a new file.

### 7.3 Migration path

1. Deploy new `fs` binary.
2. First run of any `fs` command auto-creates `agents` table (via
   `createSchema`).
3. Captain registers itself: `fs agent register --name captain --role captain
   --pane $HERDR_PANE_ID ...`.
4. Deploy `worker-playbook.yaml` to `~/.fs/kb/`.
5. Existing tasks without assignees continue to work — they show up as
   unassigned, and the captain’s heartbeat distributes them.

### 7.4 Capability abstraction & deprecations

- **New `fs resolve` subcommand** — new subcommand, additive (DESIGN-capabilities.md).
- **New `~/.fs/capabilities.yaml`** — new config file, no existing code reads it.
- **New `internal/capabilities/` package** — new package, no existing imports.
- **Playbooks rewritten to use `fs resolve`** — old playbooks work until
  replaced; agents running old playbooks still call herdr directly.
- **`orchestrator.RunTask` + `engine.Engine` + `herdr.Adapter` deprecated** —
  zero production callers (DESIGN-capabilities.md §6.2). Removal does not break
  the `fs` binary. Test files (`orchestrator_test.go`, `herdr_test.go`) will
  need updating on removal.

---

## 8. Implementation Plan — File-by-File

| Step | File(s) | What |
|---|---|---|
| 1 | `internal/registry/registry.go` | Add `agents` table DDL, `Agent` struct, `RegisterAgent`, `GetAgent`, `GetAgentByPane`, `ListAgents`, `TouchAgent`, `UpdateAgent`, `DeregisterAgent`. **Add `PRAGMA busy_timeout = 5000`** to `Open()` (§2.5). |
| 2 | `internal/registry/registry_test.go` | Tests for all new registry methods |
| 3 | `internal/command/command.go` | Add `TaskAssign`, `TaskUnassign`, `deriveAssignee`; extend `TaskInfo` with `Assignee`; extend `Status` to populate assignee and accept `--assignee` filter |
| 4 | `internal/command/command_test.go` | Tests for assign/unassign, assignee derivation, status filtering |
| 5 | `internal/query/query.go` | Extend `Node` with `Assignee`; handle `field=assignee` in `BuildTree` |
| 6 | `internal/query/query_test.go` | Test assignee derivation from metadata-changed events |
| 7 | `cmd/main.go` | Add `agentCmd` dispatcher, `taskAssign`/`taskUnassign` subcommands, `--assignee` flag on `statusCmd`, `last_seen` touch in `handler()` (§2.5), `resolveCmd` dispatcher for `fs resolve` (DESIGN-capabilities.md §9) |
| 8 | `cmd/main_test.go` | Integration tests for new CLI commands |
| 9 | `~/.fs/kb/worker-playbook.yaml` | Write the worker playbook (using `fs resolve` from the start) |
| 10 | `~/.fs/kb/reviewer-playbook.yaml` | Write the reviewer playbook (read-only review role, `fs resolve` signals) |
| 11 | `~/.fs/kb/captain-playbook.yaml` | Extend with `register-agent`, `assign-task`, `heartbeat`, `review-dispatch` steps; rewrite all herdr commands to `fs resolve` |
| 12 | `~/.fs/capabilities.yaml` | Write default config file (DESIGN-capabilities.md §3.2) |
| 13 | `internal/capabilities/capabilities.go` | New package for capability resolution (DESIGN-capabilities.md §9) |
| 14 | `orchestrator/` + `internal/engine/` | **DEPRECATE/REMOVE** `RunTask`, `engine.Engine`, `herdr.Adapter` — zero production callers (DESIGN-capabilities.md §6.2) |

**Companion doc:** DESIGN-capabilities.md details steps 7 (`resolveCmd`), 12
(`capabilities.yaml`), 13 (`capabilities.go`), and 14 (deprecation). This plan
and that one share the implementation but are maintained in separate docs to keep
each focused.

---

## 9. Sequence Diagrams

### 9.1 Captain delegates a task

```
User → Captain: "implement auth"
Captain:
  1. fs task add --goal "implement auth" --project myapp        → t-abc123
  2. fs resolve distribution-engine.spawn-pane --direction right --cwd /proj --focus false
     → herdr pane split --current --direction right --cwd /proj --no-focus  → w1:p5
  3. fs resolve distribution-engine.start-agent --name impl-auth --pane w1:p5
     → herdr agent start impl-auth --kind pi --pane w1:p5
  4. fs agent register --name impl-auth --role worker --pane w1:p5 \
       --project myapp --playbook worker-playbook
  5. fs task assign t-abc123 --to impl-auth --project myapp
  6. fs resolve distribution-engine.prompt-agent --name impl-auth \
       --prompt "<task instructions>" --wait true --timeout 300000
     → herdr agent prompt impl-auth "<task instructions>" --wait --timeout 300000

Worker (impl-auth, on first wake via worker-playbook §4.2 'work' step):
  7. fs task update t-abc123 --status active --project myapp
```

**Note:** the **worker** marks its task active (step 7), not the captain. This
is consistent with worker-playbook §4.2 `work` step and avoids a double-active
race where both captain and worker attempt the status transition.

### 9.2 Worker completes a task

```
Worker (impl-auth):
  1. fs agent get --self                                        → confirms role=worker
  2. fs status --project myapp --assignee impl-auth             → sees t-abc123 active
  3. (does the work, records knowledge)
  4. fs task update t-abc123 --status done --decision "OAuth2 implemented" --project myapp
  5. fs resolve signal.send-to-agent --target captain --message "SIGNAL: task t-abc123 done by impl-auth. Outcome: OAuth2 implemented"

Captain (wakes from signal):
  1. fs status --project myapp                                  → sees t-abc123 done
  2. fs resolve distribution-engine.read-agent --name impl-auth --lines 120
  3. fs task knowledge t-abc123 --summary "<collected summary>"
  4. fs task unassign t-abc123 --project myapp
  5. (optionally: fs resolve distribution-engine.close-pane --pane w1:p5, fs agent deregister impl-auth)
```

### 9.3 User talks directly to a worker

```
User → Worker (impl-auth): "also add rate limiting"
Worker:
  1. fs task add --goal "add rate limiting" --project myapp     → t-def456
  2. fs task assign t-def456 --to impl-auth --project myapp
  3. fs resolve signal.send-to-agent --target captain --message "SIGNAL: new task t-def456 created directly by user on worker impl-auth"
  4. fs task update t-def456 --status active --project myapp
  5. (does the work)
  6. fs task update t-def456 --status done --decision "rate limiting added" --project myapp
  7. fs resolve signal.send-to-agent --target captain --message "SIGNAL: task t-def456 done by impl-auth. Outcome: rate limiting added"

Captain (heartbeat or signal):
  1. fs status --project myapp → sees t-def456 done, assigned to impl-auth
  2. Collects result, re-parents under auth parent task if appropriate
```

### 9.4 Captain recovery (cross-ref: §5.4)

See §5.4 for the consolidated captain recovery sequence diagram.

### 9.5 Captain dispatches a review

```
Captain needs artifact reviewed (e.g., design doc, implementation, diff):

  Captain:
    1. fs task add --goal "Review <artifact-path> against <criteria>" \
         --parent <parent-id> --project myapp                         → t-rev789
    2. fs resolve distribution-engine.spawn-pane --direction right --cwd /proj --focus false
       → w1:p7
    3. fs resolve distribution-engine.start-agent --name review-design --pane w1:p7
    4. fs agent register --name review-design --role reviewer --pane w1:p7 \
         --project myapp --playbook reviewer-playbook
    5. fs task assign t-rev789 --to review-design --project myapp
    6. fs resolve distribution-engine.prompt-agent --name review-design \
         --prompt "Review <artifact-path>. Criteria: <acceptance criteria>. \
         Record findings via fs task knowledge. Deliver verdict \
         (approve | needs-changes | reject) via fs task update. \
         Signal me when done."

  Reviewer (review-design):
    1. fs agent get --self                                             → confirms role=reviewer
    2. fs status --project myapp --assignee review-design              → sees t-rev789 active
    3. (reads artifact thoroughly, checks against criteria)
    4. fs task knowledge t-rev789 --summary "[§3.2] Gap: no error handling for..." --project myapp
    5. fs task knowledge t-rev789 --summary "[§5.1] Risk: stale timeout may..." --project myapp
    6. fs task update t-rev789 --status done \
         --decision "VERDICT: needs-changes. 2 material findings — gap in §3.2, risk in §5.1" \
         --project myapp
    7. fs resolve signal.send-to-agent --target captain \
         --message "SIGNAL: review t-rev789 verdict=needs-changes by review-design. 2 findings."

  Captain (wakes from signal):
    1. fs status --project myapp                                       → sees t-rev789 done
    2. fs query --project myapp --node t-rev789 --type knowledge       → reads findings
    3. Verdict is needs-changes → dispatch a FIXER worker:
       fs task add --goal "Fix findings from review t-rev789: <details>" \
         --parent <parent-id> --project myapp                          → t-fix012
       (normal delegate flow: split pane, register worker, assign, prompt)
    4. Verdict is approve → proceed with next phase
    5. Verdict is reject → escalate to user, re-plan
    6. Clean up reviewer:
       fs resolve distribution-engine.close-pane --pane w1:p7
       fs agent deregister review-design
```

**Note:** the reviewer’s task is marked active by the reviewer itself (step 2,
after claiming), not by the captain. Consistent with the worker-owns-active
pattern (§9.1).

### 9.6 Heartbeat catches missed signal

```
Worker completes t-abc123, signals captain, but captain pane was busy:
  - Signal dropped (push is fire-and-forget)
  - Worker continues — fs state is updated regardless

Captain finishes its current action, runs heartbeat:
  1. fs status --project myapp
  2. Sees t-abc123 status=done, assignee=impl-auth → collects result
  3. Normal collection flow (same as §9.2 steps 2-5)
```

---

## 10. Open Questions

1. **Captain identity at startup.** The captain playbook's `orient` step should
   include `fs agent register --name captain --role captain ...` if not already
   registered. Should this be idempotent (re-register updates pane_id if the
   captain restarts in a new pane)? **Proposed answer: yes — `fs agent register`
   uses `INSERT OR REPLACE` semantics for the same name, updating `pane_id` and
   `last_seen` but preserving `role`.**

2. **Multiple captains.** ~~Open question~~ **RESOLVED.** Single captain only.
   `fs agent register --role captain` **errors** if an active captain already
   exists for the same project. First-live-captain wins.

   **Nuance — "first wins" means first *live* captain.** If the existing
   registered captain is confirmed dead — `last_seen` exceeds the stale
   threshold AND `fs resolve distribution-engine.agent-status` returns not-found
   (the §6.3 stale-detection mechanism) — then a new `fs agent register --role captain`
   **succeeds**: the dead registration is replaced and the new captain takes
   over. Rationale: a hard first-wins-forever rule would brick all coordination
   if the captain crashes, since no successor could ever register.

   **Implementation:** `RegisterAgent` checks
   `SELECT name, last_seen FROM agents WHERE role='captain' AND project=? AND status!='dead'`.
   If a row exists, return error `"active captain '<name>' already registered
   for project '<project>'; deregister it first or wait for stale-detection"`.
   The captain's own `orient` step handles the dead-captain path: detect stale
   → `fs agent update <old> --status dead` → then re-register self.
   See §5.4 (Captain Recovery) for the full sequence.

3. **Agent lifecycle on pane close.** When `herdr pane close` is called (or the
   user closes a pane), should herdr notify fs to mark the agent dead? This would
   require a herdr hook (post-close callback). **Proposed answer: defer to v2.
   For v1, the captain's stale-agent check handles this via
   `fs resolve distribution-engine.agent-status` → agent not found → mark dead.**

4. **Task re-parenting for direct-to-worker tasks.** When the captain sees a task
   created by a worker (no parent), should it auto-parent under the worker's
   original task? Or leave flat? **Proposed answer: captain decides — this is
   orchestration judgment, not automation.**

5. **Signal format.** The `SIGNAL:` prefix is a convention, not enforced by fs.
   Should the captain skill parse it strictly, or should it be natural language
   that the captain interprets? **Proposed answer: strict prefix for reliable
   parsing. The captain skill includes instructions to parse `SIGNAL:` lines as
   structured events.**

6. **Worker-playbook enforcement.** Nothing prevents a worker from ignoring its
   playbook. The enforcement is behavioral (the agent's system prompt includes
   the playbook). Should fs add a "task started without assignment" warning?
   **Proposed answer: yes — `fs task update --status active` should warn (stderr,
   not fail) if the caller is a registered worker and the task is not assigned to
   them.**

---

## 11. References

| What | Path |
|---|---|
| Capabilities design (companion doc) | `src/flagship/DESIGN-capabilities.md` |
| Capability config (to be created) | `~/.fs/capabilities.yaml` |
| Event store | `src/flagship/internal/store/store.go` |
| Registry (projects) | `src/flagship/internal/registry/registry.go` |
| Command handler | `src/flagship/internal/command/command.go` |
| Query engine | `src/flagship/internal/query/query.go` |
| Node ID generation | `src/flagship/internal/command/nodeid.go` |
| Knowledge center | `src/flagship/internal/knowledge/knowledge.go` |
| Engine interface (DEPRECATED) | `src/flagship/internal/engine/engine.go` |
| Herdr adapter (DEPRECATED) | `src/flagship/internal/engine/herdr/herdr.go` |
| Orchestrator (DEPRECATED) | `src/flagship/orchestrator/orchestrator.go` |
| CLI entry point | `src/flagship/cmd/main.go` |
| Captain playbook | `~/.fs/kb/captain-playbook.yaml` |
| Reviewer playbook | `~/.fs/kb/reviewer-playbook.yaml` |
| Herdr skill | `~/.pi/agent/skills/herdr/SKILL.md` |
| Global store DB | `~/.fs/flagship.db` |
