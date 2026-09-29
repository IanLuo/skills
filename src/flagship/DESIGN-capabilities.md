# DESIGN-capabilities.md — Capability Registry for Tool-Agnostic Multi-Agent Playbooks

> Architecture design doc. No implementation yet.
> Extends DESIGN-roles.md. Implement from this document; every config key,
> command template, and file path named here is the contract.

---

## 0. Problem Statement

The captain/worker/reviewer playbooks (DESIGN-roles.md) hard-code concrete
tools: `herdr` CLI for pane/agent management, `--kind pi` as the default agent
harness, `CronCreate` (Claude Code-only) for heartbeats, and `herdr agent
prompt` for inter-agent signaling. This couples the coordination design to one
terminal manager (herdr), one agent harness (pi/claude), and one scheduler
(Claude cron).

**Goal:** playbooks reference **abstract capabilities**, bound to concrete tools
via a config file. Swapping herdr→tmux, pi→codex, CronCreate→external-cron
doesn't touch the playbooks or Go code. The Go binary's role is strictly
**mechanism** — `fs resolve` (deterministic capability resolution), event store,
and agent registry. All coordination logic (spawn, prompt, read, signal,
heartbeat) lives in the **playbooks**, expressed as `fs resolve <cap>.<op>` →
concrete command → the LLM runs it.

---

## 1. Inventory of Hard-Coded References

Every concrete-tool reference across playbooks, Go source, design docs, and test
files, grounded to exact locations. The tables below are the authoritative
inventory — no separate headline count is maintained.

### 1.1 Playbooks

| File | Line(s) | Concrete Reference | Abstract Capability |
|---|---|---|---|
| captain-playbook.yaml | 10 | `env: HERDR_ENV=1` | distribution-engine.precondition |
| captain-playbook.yaml | 11 | `binary: herdr on PATH` | distribution-engine.precondition |
| captain-playbook.yaml | 33 | `herdr agent list` | distribution-engine.list-agents |
| captain-playbook.yaml | 34 | `herdr workspace list` | distribution-engine.list-workspaces |
| captain-playbook.yaml | 35 | `herdr pane list --workspace "$HERDR_WORKSPACE_ID"` | distribution-engine.list-panes |
| captain-playbook.yaml | 63 | `herdr pane layout --pane "$HERDR_PANE_ID"` | distribution-engine.pane-layout |
| captain-playbook.yaml | 64 | `herdr pane split --current --direction right --cwd <path> --no-focus` | distribution-engine.spawn-pane |
| captain-playbook.yaml | 65 | `herdr agent start <name> --kind pi --pane <id>` | distribution-engine.start-agent + coding-agent.kind-default |
| captain-playbook.yaml | 66 | `herdr agent prompt <name> "..." --wait --timeout 300000` | distribution-engine.prompt-agent |
| captain-playbook.yaml | 98 | `herdr agent list` | distribution-engine.list-agents |
| captain-playbook.yaml | 99 | `herdr agent get <name>` | distribution-engine.agent-status |
| captain-playbook.yaml | 100 | `herdr agent read <name> --source recent-unwrapped --lines 120` | distribution-engine.read-agent |
| captain-playbook.yaml | 101 | `herdr agent wait <name> --until idle --timeout 300000` | distribution-engine.wait-agent |
| captain-playbook.yaml | 111 | `herdr agent read <name> --source recent-unwrapped --lines 120` | distribution-engine.read-agent |
| captain-playbook.yaml | 133 | `herdr pane close <pane-id>` | distribution-engine.close-pane |
| captain-playbook.yaml | 159 | `herdr pane split --current --direction right --no-focus` | distribution-engine.spawn-pane |
| captain-playbook.yaml | 161 | `herdr agent start <review-name> --kind pi --pane <id>` | distribution-engine.start-agent + coding-agent.kind-default |
| captain-playbook.yaml | 168 | `herdr agent prompt <review-name> "..."` | distribution-engine.prompt-agent |
| captain-playbook.yaml | 187 | `herdr tab create --workspace "$HERDR_WORKSPACE_ID"` | distribution-engine.create-tab |
| captain-playbook.yaml | 197 | `herdr pane split --current --direction right --cwd <path>` (handoff, focused) | distribution-engine.spawn-pane |
| captain-playbook.yaml | 199 | `herdr agent start <name> --kind pi --pane <id>` | distribution-engine.start-agent + coding-agent.kind-default |
| captain-playbook.yaml | 204 | `herdr agent prompt <name> "..."` | distribution-engine.prompt-agent |
| captain-playbook.yaml | 7,21,70,245 | `Default agent kind: pi` / `Default kind: pi` / `Default to pi agent kind` | coding-agent.kind-default |
| captain-playbook.yaml | 242 | `Never run herdr server stop` | distribution-engine.safety |
| reviewer-playbook.yaml | 13 | `env: HERDR_ENV=1` | distribution-engine.precondition |
| reviewer-playbook.yaml | 15 | `binary: herdr on PATH` | distribution-engine.precondition |
| reviewer-playbook.yaml | 94 | `herdr agent prompt <captain-name> "SIGNAL: ..."` | signal.send-to-agent |
| captain-playbook.yaml | 46 | `Each unit becomes one task in fs and one agent in herdr.` | distribution-engine.identity (prose) |
| captain-playbook.yaml | 81 | `herdr agent prompt impl-auth "Implement OAuth2..."` | distribution-engine.prompt-agent (example) |
| captain-playbook.yaml | 83 | `herdr agent prompt wiki-research ...` | distribution-engine.prompt-agent (example) |
| captain-playbook.yaml | 87 | `herdr agent prompt code-research ...` | distribution-engine.prompt-agent (example) |
| captain-playbook.yaml | 91 | `herdr agent prompt api-research ...` | distribution-engine.prompt-agent (example) |
| herdr-procedure.yaml | 10-12 | `HERDR_ENV=1`, `herdr on PATH`, `herdr --skill` | (herdr-specific procedure — NOT abstracted; see §2.5) |
| herdr-procedure.yaml | 17-142 | All `herdr` commands | (herdr-specific procedure — NOT abstracted) |

### 1.2 Go Source

| File | Line | Concrete Reference | Abstract Capability |
|---|---|---|---|
| `orchestrator/orchestrator.go` | 86 | `Kind: "pi"` — hard-coded default agent kind in `RunTask` | coding-agent.kind-default |
| `internal/engine/engine.go` | 8 | `Kind string // "pi", "claude", "codex"` — doc-level enumeration | coding-agent.kind-default |
| `internal/engine/engine.go` | 18 | `Engine string // e.g., "herdr"` — handle field | distribution-engine.identity |
| `internal/engine/engine.go` | 27 | `// V1: herdr.` — doc comment | distribution-engine.identity |
| `internal/engine/herdr/herdr.go` | 24 | `exec.Command("herdr", args...)` — herdr binary | distribution-engine.binary |
| `internal/engine/herdr/herdr.go` | 86 | `Engine: "herdr"` — literal in AgentHandle | distribution-engine.identity |
| `internal/knowledge/knowledge.go` | 21 | ~~`EngineScope string`~~ **REMOVED** — engine_scope superseded by L1 detail-playbook pointer | ~~distribution-engine.scope~~ |

### 1.3 Test Files (will need updating when deprecated code is removed)

| File | References |
|---|---|
| `internal/engine/herdr/herdr_test.go` | `engine.Engine`, `engine.SpawnConfig`, `herdr.Adapter`, herdr command assertions |
| `orchestrator/orchestrator_test.go` | `orchestrator.RunTask` (11 test functions), `engine.Engine` (mock), `engine.SpawnConfig` |
| `internal/knowledge/knowledge_test.go` | ~~`engine_scope: herdr`~~ **REMOVED** from fixtures; replaced with ignore-test |

### 1.4 DESIGN-roles.md

| Section | Concrete Reference | Abstract Capability |
|---|---|---|
| §2.1, §2.2 | `herdr pane`, `herdr workspace` in schema columns | distribution-engine.identity |
| §2.4 | `$HERDR_PANE_ID` for identity discovery | distribution-engine.self-identity |
| §4.1–4.3 | Playbook command examples (NOW use `fs resolve`) | distribution-engine.* |
| §5.1 | Push signal mechanism (NOW `fs resolve signal.send-to-agent`) | signal.send-to-agent |
| §5.2 | Heartbeat timer (NOW `fs resolve scheduler.heartbeat-arm`) | scheduler.heartbeat-arm |
| §6.3 | Stale-agent check (NOW `fs resolve distribution-engine.agent-status`) | distribution-engine.agent-status |
| §9.1-9.6 | Sequence diagrams (NOW use `fs resolve`) | distribution-engine.* + coding-agent.* |

---

## 2. Abstract Capabilities

Four capabilities, each with the operations the playbooks and orchestrator
actually need.

### 2.1 `distribution-engine`

The runtime that manages panes/containers and agent lifecycles.
V1 binding: herdr. Alternatives: tmux+script, Zellij, Docker, a future
"workspace" abstraction.

| Operation | What the playbook needs | Placeholders |
|---|---|---|
| `spawn-pane` | Create a new pane/container for an agent | `{cwd}`, `{direction}`, `{focus}` |
| `start-agent` | Start a coding agent in a pane | `{name}`, `{kind}`, `{pane}`, `{extra_args}` |
| `prompt-agent` | Send a prompt/instruction to a running agent | `{name}`, `{prompt}`, `{wait}`, `{timeout}` |
| `read-agent` | Read agent output | `{name}`, `{lines}` |
| `wait-agent` | Block until agent reaches a state | `{name}`, `{until}`, `{timeout}` |
| `agent-status` | Get agent lifecycle state | `{name}` |
| `list-agents` | List all running agents | — |
| `close-pane` | Close a pane/container | `{pane}` |
| `pane-layout` | Query current pane geometry | `{pane}` |
| `list-panes` | List panes in current workspace | `{workspace}` |
| `list-workspaces` | List workspaces | — |
| `create-tab` | Create a new tab for isolation | `{workspace}` |
| `env-check` | Precondition: is the engine available? | — |

### 2.2 `coding-agent`

The agent harness itself (what kind of agent to spawn).
V1 binding: pi. Alternatives: claude, codex, copilot, aider.

| Operation | What the playbook needs | Placeholders |
|---|---|---|
| `kind-default` | The default `--kind` value for `start-agent` | — (returns a string) |
| `kind-list` | Enumerate available harness kinds | — |

Note: `coding-agent` is **not** a command producer — it's a value provider
consumed by `distribution-engine.start-agent`'s `{kind}` placeholder. The
binding is a single string, not a command template.

### 2.3 `scheduler`

Periodic heartbeat timer for the captain.
V1 binding: Claude's `CronCreate` (Claude Code only). Alternatives:
`ScheduleWakeup`, external cron + `herdr agent prompt`, OS launchd/systemd
timer, a future `fs heartbeat` daemon.

| Operation | What the playbook needs | Placeholders |
|---|---|---|
| `heartbeat-arm` | Set up a recurring self-prompt | `{interval}`, `{prompt}` |
| `heartbeat-cancel` | Cancel the recurring timer | — |

### 2.4 `signal`

Push notifications between agents (worker→captain).
V1 binding: `herdr agent prompt`. Alternatives: fs-event-based polling (no push),
Unix signals, named pipes, a future `fs signal` command.

| Operation | What the playbook needs | Placeholders |
|---|---|---|
| `send-to-agent` | Send a structured message to another agent | `{target}`, `{message}` |

### 2.5 What is NOT abstracted

- **`herdr-procedure.yaml`** — this is the L2 tool-detail playbook for herdr.
  It teaches agents how to use herdr (sequences, gotchas, lifecycle states,
  read-output workarounds). It stays concrete. When the engine changes, a
  different procedure playbook replaces it (e.g., `tmux-procedure.yaml`).
  ~~The `engine_scope` field on playbooks~~ **SUPERSEDED.** The capability↔playbook
  link is solely the L1 `detail-playbook` pointer in `capabilities.yaml`.
  Playbooks no longer carry an engine_scope tag.
- **`fs` commands** — `fs` is the coordination store, not a swappable capability.
  It's the single source of truth and doesn't get abstracted.
- **File/code operations** — out of scope; those are agent-internal.

### 2.6 Three-Layer Separation Model

The playbook/capability system uses a strict three-layer architecture:

| Layer | What | Where | Contains | Does NOT contain |
|---|---|---|---|---|
| **L1 INTERFACE** | Capability→tool binding | `~/.fs/capabilities.yaml` | Tool name + `detail-playbook` pointer per capability. Nothing else. Exception: capabilities with `detail-playbook: ""` keep operations inline as fallback (e.g. scheduler). | Operations, command templates, flags, env-aliases, env-check, preconditions, tool-usage gotchas |
| **L2 IMPLEMENTATION** | Tool detail + command templates | `~/.fs/kb/<tool>-procedure.yaml` (one per tool) | `capability-impl:` section with per-operation command templates, flag expansions, env-aliases, env-check, preconditions. Plus human-readable guidance: sequences, gotchas, patterns. | Coordination behavior, task assignment, delegation flows |
| **L3 WORK DETAIL** | Coordination behavior by role | `~/.fs/kb/{captain,worker,reviewer}-playbook.yaml` | Abstract `fs resolve <cap>.<op>` calls, delegation patterns, fs task management | Concrete tool names (herdr/pi/CronCreate), tool-specific mechanics |

**Links between layers:**

1. **L1 → L2:** Each capability binding declares `detail-playbook: <name>` pointing to
   its L2 playbook (e.g. `distribution-engine.detail-playbook: herdr-procedure`).
2. **L3 → L1:** Role playbooks use `fs resolve <cap>.<op>` to get concrete commands.
3. **L3 → L2 (via L1):** When a role playbook needs tool detail, it queries the
   capability metadata: `fs resolve <cap>` returns `.detail_playbook`, then loads
   it via `fs kb get <name>`.

**Swap test:** Replace herdr with tmux = repoint L1 `detail-playbook` to
`tmux-procedure.yaml` (new L2 with `capability-impl:` section). L3 role
playbooks remain untouched. L1 stays minimal — just the pointer changes.

**Inline fallback:** Capabilities with `detail-playbook: ""` (e.g. scheduler)
keep their operations inline in L1. This avoids creating trivial L2 files for
2-line command mappings. The resolver checks L2 first; if no L2, falls back to
inline operations in L1.

**L3 purity rule:** L3 playbooks must contain zero raw tool references
(herdr/pi/CronCreate/etc.) except inside `fs resolve` calls. Verify with:
`grep -E 'herdr|CronCreate|--kind pi' <playbook> | grep -v 'fs resolve'`

**Discovery mechanism:**
```bash
# L3 playbook needs tool detail for distribution-engine:
fs resolve distribution-engine
# Returns: {"ok":true,"data":{"detail_playbook":"herdr-procedure",...}}
fs kb get herdr-procedure
# Returns the L2 playbook content
```

---

## 3. Config Format & Location

### 3.1 Location

```
~/.fs/capabilities.yaml
```

Same directory as `flagship.db` and `kb/`. Single file, global scope (not
per-project — the distribution engine is infrastructure, not project-specific).

### 3.2 Format — Full Example

**L1 capabilities.yaml (interface-only):**

```yaml
# ~/.fs/capabilities.yaml — INTERFACE / DECLARATION ONLY
# Each capability declares: tool binding + detail-playbook pointer.
# ALL implementation detail lives in the L2 detail-playbook.
# Exception: capabilities with detail-playbook: "" keep operations inline.

version: 1

capabilities:

  distribution-engine:
    name: herdr
    detail-playbook: herdr-procedure

  coding-agent:
    name: pi
    detail-playbook: ""
    kind-default: pi
    kind-list:
      - pi
      - claude
      - codex
      - copilot

  scheduler:
    name: claude-cron
    detail-playbook: ""               # inline fallback — no L2
    operations:
      heartbeat-arm: >-
        CronCreate interval={interval} prompt="{prompt}"
      heartbeat-cancel: >-
        CronCancel

  signal:
    name: herdr-prompt
    detail-playbook: herdr-procedure
```

**L2 herdr-procedure.yaml (implementation — `capability-impl:` section):**

```yaml
name: herdr-procedure
type: procedure
trigger: herdr

capability-impl:

  distribution-engine:
    env-check: 'test "${HERDR_ENV:-}" = 1'
    preconditions:
      - env: HERDR_ENV=1
      - binary: herdr
    env-aliases:
      PANE_ID: HERDR_PANE_ID
      WORKSPACE_ID: HERDR_WORKSPACE_ID
      TAB_ID: HERDR_TAB_ID
      ENGINE_ENV: HERDR_ENV
    flags:
      focus_flag:
        "true": ""
        "false": "--no-focus"
      wait_flag:
        "true": "--wait"
        "false": ""
      timeout_flag:
        "true": "--timeout {timeout}"
        "false": ""
    operations:
      spawn-pane: >-
        herdr pane split --current --direction {direction}
        --cwd {cwd} {focus_flag}
      start-agent: >-
        herdr agent start {name} --kind {kind} --pane {pane}
      prompt-agent: >-
        herdr agent prompt {name} "{prompt}" {wait_flag} {timeout_flag}
      # ... (all 12 distribution-engine operations)

  signal:
    operations:
      send-to-agent: >-
        herdr agent prompt {target} "{message}"

steps:
  # ... human-readable guidance (unchanged)
```

**`detail-playbook` field:** Each capability declares a `detail-playbook`
pointing to its L2 playbook. `fs resolve` follows this pointer to load
templates, flags, and env-aliases from L2. Empty string means no L2 — the
capability keeps operations inline in L1 (fallback for trivial bindings like
scheduler).

**`capability-impl:` section in L2:** Machine-readable block parsed by
`fs resolve`. Keyed by capability name. Each entry holds the implementation
detail that was previously in L1: operations (command templates), flags,
env-aliases, env-check, preconditions. This section coexists with the
human-readable `steps:`/`gotchas:`/`safety:` sections.

### 3.3 Alternative Bindings (for illustration)

**tmux + external cron + pi:**

```yaml
capabilities:
  distribution-engine:
    name: tmux
    env-check: 'test -n "$TMUX"'
    preconditions:
      - env: TMUX
      - binary: tmux
    operations:
      spawn-pane: >-
        tmux split-window -{direction_flag} -c {cwd}
      start-agent: >-
        tmux send-keys -t {pane} '{kind} --skill {skill}' Enter
      prompt-agent: >-
        tmux send-keys -t {pane} '{prompt}' Enter
      # ... etc.

  coding-agent:
    kind-default: codex

  scheduler:
    name: external-cron
    operations:
      heartbeat-arm: >-
        fs-heartbeat-install --interval {interval} --message {prompt}
      heartbeat-cancel: >-
        crontab -l | grep -v flagship-heartbeat | crontab -

  signal:
    name: fs-event
    operations:
      send-to-agent: >-
        fs signal send --to {target} --message "{message}"
```

### 3.4 Flag Expansion Convention

Placeholders like `{focus_flag}` and `{wait_flag}` are **conditional flags**.
Each capability declares its own `flags:` section mapping flag placeholders to
their true/false (or valued) expansions. The resolver reads these from config —
**nothing is hard-coded in Go**.

**Schema — per-capability `flags:` section:**

```yaml
  distribution-engine:
    name: herdr
    flags:
      focus_flag:
        "true": ""              # focused — no extra flag needed
        "false": "--no-focus"    # default
      wait_flag:
        "true": "--wait"
        "false": ""
      timeout_flag:
        "true": "--timeout {timeout}"
        "false": ""
      direction_flag:            # herdr uses literal direction
        "right": "right"
        "down": "down"
    # ... operations as before
```

**tmux binding — different direction flag:**

```yaml
  distribution-engine:
    name: tmux
    flags:
      focus_flag:
        "true": ""
        "false": "-d"            # tmux: -d = don't switch focus
      direction_flag:            # tmux uses -h/-v, not right/down
        "right": "-h"
        "down": "-v"
    operations:
      spawn-pane: >-
        tmux split-window {direction_flag} -d -c {cwd}
```

**Resolution rules:**
1. Resolver looks up the `flags:` map for the current capability binding.
2. For each `{flag_placeholder}` in the template, finds the key matching the
   caller-supplied value (or the default if not supplied).
3. Substitutes the expansion string.
4. Strips double-spaces and trailing whitespace after all expansions.

This replaces the previous approach where flag semantics were implicit in the
resolver. Resolves FINDING-6.

---

## 4. Resolution Mechanism

### 4.1 Options Compared

| Criterion | (A) fs-as-resolver | (B) agent-reads-config |
|---|---|---|
| **Mechanism** | New `fs resolve <cap>.<op> [--key val ...]` command. Reads `capabilities.yaml`, substitutes placeholders, prints the concrete command to stdout. | Playbooks say "resolve capabilities.yaml yourself, substitute, run the result." |
| **Decoupling** | Real — the playbook never mentions herdr/tmux/pi. `fs resolve` is the abstraction boundary. | Nominal — the playbook says "read config and substitute" but the LLM still sees the concrete command after substitution. |
| **LLM error surface** | Low — LLM runs one command (`fs resolve ...`), gets back one string, runs it. Two steps, both deterministic. | High — LLM must parse YAML, match placeholders, substitute, handle conditionals, strip whitespace. Every step is a hallucination opportunity. |
| **Testability** | `fs resolve` is a Go function — unit-testable, CI-checkable. Missing bindings fail fast with a clear error. | Untestable — the substitution happens inside the LLM's reasoning, invisible to CI. |
| **Go work** | ~150 LOC: YAML parser for capabilities.yaml, placeholder substitution, one new CLI subcommand. | Zero Go work. |
| **Playbook readability** | Playbooks read like pseudocode: `fs resolve distribution-engine.spawn-pane --cwd /proj --direction right` | Playbooks read like metaprogramming: "Look up distribution-engine.spawn-pane in capabilities.yaml, substitute {cwd} with /proj..." |
| **Config validation** | `fs resolve --check` can validate all bindings at deploy time. | No validation until runtime (LLM tries it). |
| **Orchestrator.go** | `orchestrator.go` calls `fs resolve` (or the underlying Go function directly) instead of hard-coding `Kind: "pi"`. Same code path for CLI and library. | `orchestrator.go` would need its own YAML-reading logic — duplicates the config. |

### 4.2 Recommendation: (A) fs-as-resolver

**Rationale:** The captain is an LLM. LLMs are unreliable YAML parsers and
string substituters. Every step we offload from the LLM to deterministic code
reduces error rate. `fs resolve` gives us:

1. **One command in, one command out.** The LLM's job is to know *what* it wants
   (spawn a pane, start an agent) and *what parameters* — not *how* to spell the
   concrete command.
2. **Single source of truth.** Both the Go orchestrator and the LLM-driven
   playbooks resolve through the same config, the same code path.
3. **Fail-fast validation.** `fs resolve --check` at deploy time catches missing
   bindings, unknown capabilities, and malformed templates before any agent runs.
4. ~~**The `engine_scope` field already exists**~~ **SUPERSEDED.** `engine_scope`
   has been removed from `Playbook`. The capability↔playbook link is solely
   the L1 `detail-playbook` pointer in `capabilities.yaml`.

The Go work is modest (~150 LOC) and pays for itself immediately in reliability.

### 4.3 CLI Surface

```
fs resolve <capability>.<operation> [--<key> <value> ...]
```

**Output format:** standard fs JSON envelope (same `{ok, data, error}` shape as
every other fs command). The resolved command string is in `.data.command`.

- **Success** → `{"ok":true, "data":{"command":"<concrete command>"}}` exit 0.
- **Error** → `{"ok":false, "error":{...}}` exit non-zero.

The LLM parses `.data.command` and runs it. This is consistent with all other
`fs` output (resolves FINDING-16/17).

**Examples:**
```bash
# Spawn a pane
fs resolve distribution-engine.spawn-pane --cwd /proj --direction right --focus false
# → {"ok":true,"data":{"command":"herdr pane split --current --direction right --cwd /proj --no-focus"}}

# Start an agent (kind auto-filled from coding-agent.kind-default)
fs resolve distribution-engine.start-agent --name impl-auth --pane w1:p5
# → {"ok":true,"data":{"command":"herdr agent start impl-auth --kind pi --pane w1:p5"}}

# Start an agent with explicit kind override
fs resolve distribution-engine.start-agent --name impl-auth --pane w1:p5 --kind claude
# → {"ok":true,"data":{"command":"herdr agent start impl-auth --kind claude --pane w1:p5"}}

# Get default agent kind (value, not command)
fs resolve coding-agent.kind-default
# → {"ok":true,"data":{"command":"pi"}}

# Arm heartbeat
fs resolve scheduler.heartbeat-arm --interval 25m --prompt "HEARTBEAT: run heartbeat scan"
# → {"ok":true,"data":{"command":"CronCreate interval=25m prompt=\"HEARTBEAT: run heartbeat scan\""}}

# Signal another agent
fs resolve signal.send-to-agent --target captain --message "SIGNAL: task t-abc done"
# → {"ok":true,"data":{"command":"herdr agent prompt captain \"SIGNAL: task t-abc done\""}}

# Validate all bindings
fs resolve --check
# → {"ok":true,"data":{"capabilities":["distribution-engine","coding-agent","scheduler","signal"]}}
```

**Special behaviors:**
- `--kind` defaults to `coding-agent.kind-default` if not provided and the
  template contains `{kind}`.
- Unknown capability or operation → error with available alternatives listed.
- Missing required placeholder → error listing which keys are needed.
- `--check` validates the config without resolving any operation.

### 4.4 Resolution Flow

```
Playbook step (abstract)
  │
  ▼
fs resolve <cap>.<op> --key val ...     ← LLM runs this
  │
  ├─ Load L1: ~/.fs/capabilities.yaml
  ├─ Find capability → get tool name + detail-playbook pointer
  ├─ If detail-playbook is set:
  │    ├─ Load L2: ~/.fs/kb/<detail-playbook>.yaml
  │    ├─ Find capability-impl.<cap>.<op> → template + flags + env-aliases
  │  Else (inline fallback):
  │    ├─ Read template from L1 inline operations
  ├─ Substitute {placeholders} with --key values
  ├─ Apply flag expansions from L2's flags: maps (§3.4)
  ├─ Expand env-aliases from L2 (§10)
  ├─ Shell-escape user-controlled values (§7.3)
  ├─ Strip empty segments, normalize whitespace
  │
  ▼
JSON envelope: {ok:true, data:{command:"..."}} exit 0
                                        ← LLM parses .data.command
                                        ← LLM runs the command
```

The two-file resolution: L1 provides the pointer, L2 provides the implementation.
For capabilities without an L2 (detail-playbook: ""), templates are read inline
from L1 (no flags or env-aliases in that case).

The LLM executes two steps: `fs resolve ...` → parses `.data.command` from the
JSON response → runs the resolved command. This keeps the LLM's job to structured
JSON parsing (which it does reliably) rather than raw stdout capture.

---

## 5. Playbook Rewrite — Before/After

### 5.1 Captain Playbook — `delegate` Step

**BEFORE (current, hard-coded):**

```yaml
  - id: delegate
    action: spawn a worker agent for each task
    sequence:
      - check pane layout to pick split direction
      - split a new pane (--no-focus, --cwd to target project)
      - start agent in the new pane
      - prompt the agent with the task
      - mark the fs task active
    commands:
      - herdr pane layout --pane "$HERDR_PANE_ID"
      - herdr pane split --current --direction right --cwd <project-path> --no-focus
      - herdr agent start <task-name> --kind pi --pane <new-pane-id>
      - herdr agent prompt <task-name> "<task instructions>" --wait --timeout 300000
      - fs task update <task-id> --status active --project <name>
```

**AFTER (capability-resolved):**

```yaml
  - id: delegate
    action: spawn a worker agent for each task
    sequence:
      - check pane layout to pick split direction
      - split a new pane (no-focus, cwd to target project)
      - start agent in the new pane
      - register agent in fs (DESIGN-roles §4.1)
      - assign task to agent
      - prompt the agent with the task
      - mark the fs task active
    commands:
      - fs resolve distribution-engine.pane-layout --pane "$PANE_ID"
      - fs resolve distribution-engine.spawn-pane --cwd <project-path> --direction right --focus false
      - fs resolve distribution-engine.start-agent --name <task-name> --pane <new-pane-id>
      - fs agent register --name <task-name> --role worker --pane <new-pane-id> --project <name> --playbook worker-playbook
      - fs task assign <task-id> --to <task-name> --project <name>
      - fs resolve distribution-engine.prompt-agent --name <task-name> --prompt "<task instructions>" --wait true --timeout 300000
      - fs task update <task-id> --status active --project <name>
```

Note: `$PANE_ID` replaces `$HERDR_PANE_ID`. The environment variable name for
"current pane" becomes engine-specific; the resolver maps it. (See §6.1 for
env-var portability.)

### 5.2 Captain Playbook — `orient` Step

**BEFORE:**
```yaml
    commands:
      - fs project list
      - fs status --project <name>
      - herdr agent list
      - herdr workspace list
      - herdr pane list --workspace "$HERDR_WORKSPACE_ID"
```

**AFTER:**
```yaml
    commands:
      - fs project list
      - fs status --project <name>
      - fs resolve distribution-engine.list-agents
      - fs resolve distribution-engine.list-workspaces
      - fs resolve distribution-engine.list-panes --workspace "$WORKSPACE_ID"
```

### 5.3 Captain Playbook — `monitor` Step

**BEFORE:**
```yaml
    commands:
      - herdr agent list
      - herdr agent get <name>
      - herdr agent read <name> --source recent-unwrapped --lines 120
      - herdr agent wait <name> --until idle --timeout 300000
```

**AFTER:**
```yaml
    commands:
      - fs resolve distribution-engine.list-agents
      - fs resolve distribution-engine.agent-status --name <name>
      - fs resolve distribution-engine.read-agent --name <name> --lines 120
      - fs resolve distribution-engine.wait-agent --name <name> --until idle --timeout 300000
```

### 5.4 Signal — Worker/Reviewer → Captain

**BEFORE (reviewer-playbook.yaml:94):**
```yaml
      - herdr agent prompt <captain-name> "SIGNAL: review <task-id> verdict=<verdict> by <self-name>. <one-line summary>"
```

**AFTER:**
```yaml
      - fs resolve signal.send-to-agent --target <captain-name> --message "SIGNAL: review <task-id> verdict=<verdict> by <self-name>. <one-line summary>"
```

### 5.5 Preconditions

**BEFORE:**
```yaml
preconditions:
  - env: HERDR_ENV=1
  - binary: herdr on PATH
  - binary: fs on PATH
```

**AFTER:**
```yaml
preconditions:
  - binary: fs on PATH
  - check: fs resolve --check
```

The `fs resolve --check` command validates that all capabilities have bindings
and that preconditions (env vars, binaries) are met. This replaces per-playbook
hard-coded precondition checks.

---

## 6. Orchestrator.go — SUPERSEDED

> **GOVERNING DECISION (single-path):** The Go binary’s job is strictly
> mechanism: `fs resolve` (deterministic capability resolution) + event store +
> agent registry. The Go binary NEVER spawns, prompts, or reads agents. ALL
> coordination logic lives in the playbooks, expressed as `fs resolve <cap>.<op>`
> → concrete command → the LLM runs it.

### 6.1 What is deprecated

| Symbol | File | Status |
|---|---|---|
| `orchestrator.RunTask()` | `orchestrator/orchestrator.go` | **DEPRECATED.** Was the programmatic dispatch path (compose prompt → engine.Spawn → engine.Read → engine.Close). Superseded by captain-playbook + `fs resolve`. |
| `engine.Engine` interface | `internal/engine/engine.go` | **DEPRECATED.** `Spawn`, `Read`, `Close` were the Go-side distribution abstraction. The playbook path replaces this: the LLM runs the resolved concrete command directly. |
| `herdr.Adapter` | `internal/engine/herdr/herdr.go` | **DEPRECATED.** The sole `engine.Engine` implementation. No longer needed since playbooks call herdr (or tmux, etc.) via resolved commands, not via a Go adapter. |

### 6.2 Caller grep — safe removal impact

Grepping `src/flagship/` for callers of `RunTask`, `engine.Engine`, and the
orchestrator package (excluding test files):

| Caller | File | Impact |
|---|---|---|
| `orchestrator.New(s, kb, eng)` | Only in `orchestrator_test.go` | No production caller in `cmd/main.go` or anywhere else. |
| `engine.Engine` type assertions | Only in `herdr.go` (implements it) and `herdr_test.go` / `orchestrator_test.go` | No production consumer outside the orchestrator package. |
| `import "github.com/flagship-dev/flagship/orchestrator"` | Only in `orchestrator_test.go` | `cmd/main.go` does **not** import the orchestrator package. |
| `import "github.com/flagship-dev/flagship/internal/engine"` | Only in `orchestrator.go` and `herdr/herdr.go` | No other production code imports `engine`. |

**Conclusion:** `orchestrator.RunTask`, `engine.Engine`, and `herdr.Adapter` have
**zero production callers** outside their own packages. `cmd/main.go` never
imported or wired them. They can be removed (or moved behind a build tag) with no
impact on the `fs` binary.

**Test files that will need updating on removal:**
- `orchestrator/orchestrator_test.go` — 11 `RunTask` test functions + mock engine.
- `internal/engine/herdr/herdr_test.go` — `engine.Engine` compile-time check + Spawn/Read/Close tests.

### 6.3 Answering DESIGN-roles FINDING-10

> Does orchestrator.go resolve through the same config, or is it superseded by
> the playbook+herdr model?

**Answer: Superseded.** The captain-playbook + `fs resolve` is the **only**
dispatch mechanism. `RunTask`, `engine.Engine`, and the herdr adapter are
deprecated dead code. The impl plan (§9) includes a step to remove them.

---

## 7. Scheduler Portability

### 7.1 The Problem (DESIGN-roles §5.2)

The heartbeat relies on `CronCreate`, a Claude Code-specific tool:

```
CronCreate  interval=25m  prompt="HEARTBEAT: run heartbeat scan"
```

This doesn't exist in pi, codex, copilot, or any non-Claude harness.

### 7.2 Solution: Capability-Bound Heartbeat

The captain playbook's `orient` step arms the heartbeat via `fs resolve`:

```yaml
  - id: orient
    action: understand current state before planning
    commands:
      - fs project list
      - fs status --project <name>
      - fs resolve distribution-engine.list-agents
      - fs resolve distribution-engine.list-workspaces
      - fs resolve distribution-engine.list-panes --workspace "$WORKSPACE_ID"
      # Arm the heartbeat timer
      - fs resolve scheduler.heartbeat-arm --interval 25m --prompt "HEARTBEAT: run heartbeat scan"
```

### 7.3 Binding Examples — Same Playbook, Different Captains + Shell Safety

**Claude captain (CronCreate):**
```yaml
  scheduler:
    name: claude-cron
    operations:
      heartbeat-arm: >-
        CronCreate interval={interval} prompt="{prompt}"
      heartbeat-cancel: >-
        CronCancel
```

`fs resolve scheduler.heartbeat-arm --interval 25m --prompt "HEARTBEAT: ..."` →
`CronCreate interval=25m prompt="HEARTBEAT: ..."`

**Pi captain (external cron → signal, wrapper-script form):**

> **SHELL SAFETY (resolves FINDING-5):** The `{prompt}` placeholder is
> user-controlled. Templates containing `$()`, backticks, or shell metacharacters
> MUST NOT inline raw user values. The resolver **shell-escapes** all placeholder
> values substituted into any template (using Go’s `shellescape.Quote` or
> equivalent POSIX single-quote wrapping: replace `'` with `'\''`, wrap in `'...'`).
>
> For the external-cron binding specifically, the **recommended form** is a
> wrapper script rather than inline nested quoting:

```yaml
  scheduler:
    name: external-cron
    operations:
      heartbeat-arm: >-
        fs-heartbeat-install --interval {interval} --message {prompt}
      heartbeat-cancel: >-
        crontab -l | grep -v flagship-heartbeat | crontab -
```

Where `fs-heartbeat-install` is a shipped helper script:
```bash
#!/bin/sh
# ~/.fs/bin/fs-heartbeat-install
# Installs a crontab entry that signals the captain on a schedule.
INTERVAL="$1"; shift; shift  # skip --interval
MESSAGE="$1"; shift; shift  # skip --message
SIGNAL_CMD=$(fs resolve signal.send-to-agent --target captain --message "$MESSAGE")
CMD=$(echo "$SIGNAL_CMD" | jq -r '.data.command')
(crontab -l 2>/dev/null | grep -v flagship-heartbeat; echo "*/$INTERVAL * * * * $CMD # flagship-heartbeat") | crontab -
```

This avoids the previous design’s nested `$(fs resolve ...)` inside a YAML
template, which would require double-escaping and was fragile. The resolver still
shell-escapes `{prompt}` in the `--message` position, but the script handles the
crontab manipulation safely.

> **§11 Q2 REVISED:** The earlier leaning ("leave nesting to shell") is revised.
> Nested-resolve bindings receive escaped values, and the recommended pattern for
> external-cron is the wrapper-script form above.

`fs resolve scheduler.heartbeat-arm --interval 25m --prompt "HEARTBEAT: ..."` →
the wrapper script installs a crontab entry that periodically prompts the captain.

**Pi captain (ScheduleWakeup, if available):**
```yaml
  scheduler:
    name: schedule-wakeup
    operations:
      heartbeat-arm: >-
        ScheduleWakeup delay={interval} message="{prompt}"
      heartbeat-cancel: >-
        CancelWakeup
```

**Pi captain (no scheduler — polling only):**
```yaml
  scheduler:
    name: none
    operations:
      heartbeat-arm: >-
        echo "No scheduler available — relying on orient-on-restart only"
      heartbeat-cancel: >-
        echo "No scheduler to cancel"
```

### 7.4 Answering DESIGN-roles FINDING-17

> How does the heartbeat become capability-bound so a pi captain can use
> external-cron and a claude captain uses CronCreate, same playbook?

**Answer:** The playbook says `fs resolve scheduler.heartbeat-arm --interval 25m
--prompt "..."`. The config file binds `scheduler.heartbeat-arm` to whatever
mechanism is available. The playbook never mentions CronCreate, ScheduleWakeup,
or crontab.

---

## 8. Compatibility & Migration

### 8.1 Additive — Nothing Breaks

| Change | Location | Why additive |
|---|---|---|
| New `~/.fs/capabilities.yaml` file | Filesystem | New file — nothing reads it until `fs resolve` is called |
| New `fs resolve` subcommand | `cmd/main.go` | New subcommand — doesn't affect existing commands |
| New `internal/capabilities/` package | Go source | New package — no existing code imports it |
| Updated playbooks (YAML) | `~/.fs/kb/*.yaml` | Playbooks are behavioral contracts, not code. The old playbooks work until replaced. |

### 8.2 Migration Path

1. **Deploy new `fs` binary** with `fs resolve` and `fs init` subcommands.
2. **`fs init`** — materializes `~/.fs/capabilities.yaml` from the embedded
   skeleton (herdr+pi+claude-cron bindings — the current behavior, now explicit).
3. **Link the kb:** `ln -s <skills-repo>/skills/flagship/kb ~/.fs/kb` —
   L2 detail playbooks live in-repo; the symlink makes them available to
   `fs resolve`. Without this, capabilities that reference a `detail-playbook`
   (e.g. `distribution-engine.*`) fail with “L2 detail playbook not found”.
4. **Verify:** `fs resolve --check` → all green.
5. **Update playbooks** one at a time. Each update replaces `herdr` literals
   with `fs resolve` calls. Old and new playbooks can coexist during migration
   (agents running old playbooks still work; they just use herdr directly).
6. **Deprecate/remove orchestrator.go + engine package** (zero production
   callers — §6.2). Test files need updating.
7. **Done.** To switch engines, edit `capabilities.yaml` only.

### 8.3 No New DB Table

The capability registry is a **config file**, not a database table. It's read
at resolution time, not stored in `flagship.db`. Rationale: capabilities are
infrastructure config (like `~/.gitconfig`), not event history. They change
rarely and manually.

### 8.4 Fallback Behavior

If `~/.fs/capabilities.yaml` doesn't exist, `fs resolve` returns an error:

```json
{"ok": false, "error": "capabilities config not found at ~/.fs/capabilities.yaml — run 'fs resolve --init' to create a default"}
```

`fs resolve --init` writes a default config with herdr+pi+claude-cron bindings
(matching today's hard-coded behavior).

---

## 9. Implementation Plan — File-by-File

| Step | File(s) | What | LOC est. |
|---|---|---|---|
| 1 | `internal/capabilities/capabilities.go` | `Config` struct, `Load(path)`, `Resolve(cap, op, params)`, `Check()`. **Import `gopkg.in/yaml.v3`** — the hand-rolled `parsePlaybook` scanner in `knowledge.go` handles flat key-value YAML but cannot parse the nested `flags:` maps, `env-aliases:` maps, and multi-level `operations:` required here. Full YAML lib is justified. Placeholder substitution + flag expansion (§3.4) + shell-escaping (§7.3) + env-alias resolution (§10). | ~250 |
| 2 | `internal/capabilities/capabilities_test.go` | Unit tests: load, resolve all operations, missing key errors, flag expansion, shell-escaping of user values, env-alias, `--check`, fallback for missing config. | ~200 |
| 3 | `cmd/main.go` | New `resolveCmd` dispatcher. Parses `<cap>.<op>`, collects `--key val` pairs, calls `capabilities.Resolve()`, prints JSON envelope result. `--check` calls `capabilities.Check()`. `--init` writes default config. | ~80 |
| 4 | `cmd/main_test.go` | Integration tests for `fs resolve` (JSON envelope output, error cases). | ~60 |
| 5 | `orchestrator/` + `internal/engine/` | **DEPRECATE/REMOVE** `orchestrator.RunTask`, `engine.Engine` interface, `herdr.Adapter`. Add deprecation notice or remove entirely (zero production callers — see §6.2). If removing: delete `orchestrator/orchestrator.go`, `internal/engine/engine.go`, `internal/engine/herdr/herdr.go` and their test files. | ~−400 |
| 6 | `~/.fs/capabilities.yaml` | Default config file (§3.2 contents, including `flags:` sections). | ~60 lines YAML |
| 7 | `~/.fs/kb/captain-playbook.yaml` | Rewrite commands to use `fs resolve` (§5.1–5.5). | ~edit |
| 8 | `~/.fs/kb/reviewer-playbook.yaml` | Rewrite signal command (§5.4). | ~edit |
| 9 | `~/.fs/kb/worker-playbook.yaml` | (New file from DESIGN-roles) — write with `fs resolve` from the start. | ~new |

**YAML library decision: import `gopkg.in/yaml.v3`.** The `knowledge.go`
`parsePlaybook` scanner is a line-by-line key-value parser that cannot handle
nested maps (`flags:`, `env-aliases:`, `operations:` containing multi-line
templates). Attempting to extend it for capabilities.yaml would be fragile and
would re-implement YAML poorly. `gopkg.in/yaml.v3` is the standard Go YAML
library, zero transitive dependencies, and is already the implicit contract
(playbook files are YAML). This is the right call.

**Total Go work:** ~590 LOC (250 implementation + 260 tests + 80 CLI). Realistic
for the nested YAML parsing, flag expansion, shell-escaping, and env-alias
resolution. The previous estimate of ~400 was underweight (it assumed a minimal
parser and no shell-escaping). The deletion of ~400 LOC of deprecated
orchestrator/engine code is a net reduction.

**No schema changes.** No new DB tables. No event format changes.

### 9.1 Dependency Order

```
Step 1 (capabilities pkg) → Step 2 (tests)
                           → Step 3 (CLI) → Step 4 (CLI tests)
Step 5 (deprecate orchestrator+engine) — independent, contingent on
    confirming zero production callers (§6.2 grep: confirmed)
Step 6 (config file) — independent, can be written anytime
Steps 7-9 (playbooks) — depend on Step 3 (CLI must exist)
```

---

## 10. Environment Variable Portability

The playbooks use herdr-specific env vars: `$HERDR_PANE_ID`,
`$HERDR_WORKSPACE_ID`, `$HERDR_TAB_ID`, `$HERDR_ENV`.

### 10.1 Approach: Canonical Aliases (playbook readability only)

The capability config declares canonical env-var names and the engine maps them:

```yaml
  distribution-engine:
    name: herdr
    env-aliases:
      PANE_ID: HERDR_PANE_ID
      WORKSPACE_ID: HERDR_WORKSPACE_ID
      TAB_ID: HERDR_TAB_ID
      ENGINE_ENV: HERDR_ENV
```

`fs resolve` auto-expands `$PANE_ID` → the value of `$HERDR_PANE_ID` in the
current environment. Playbooks use `$PANE_ID`; the config maps it to the
engine-specific var.

A tmux binding would map:
```yaml
    env-aliases:
      PANE_ID: TMUX_PANE
      WORKSPACE_ID: TMUX_SESSION
      ENGINE_ENV: TMUX
```

### 10.2 Breaking the circular dependency (resolves FINDING-15)

**Critical constraint:** Go identity discovery (`fs agent get --self`, per
DESIGN-roles §2.4) reads the **raw engine env var** (`$HERDR_PANE_ID`, or
`$TMUX_PANE` for tmux) **directly in Go** — NOT via `capabilities.yaml` or the
env-alias map. The lookup in `cmd/main.go` is:

```go
paneID := os.Getenv("HERDR_PANE_ID")  // direct read, no config dependency
if paneID == "" {
    paneID = os.Getenv("TMUX_PANE")   // fallback chain, hard-coded in Go
}
```

This is intentional: identity discovery is a **bootstrap operation** that runs
before (or at the same time as) loading `capabilities.yaml`. If identity
discovery depended on the config, and config resolution depended on knowing which
engine is active (which requires identity), we'd have a circular dependency.

The `$PANE_ID` canonical alias exists for **playbook readability only** — so
playbook authors write `$PANE_ID` instead of `$HERDR_PANE_ID`. The `fs resolve`
command expands it via the env-aliases map. But Go code that needs identity
(like `TouchAgent` and `fs agent get --self`) reads the raw var directly.

### 10.3 Fallback

If the canonical var is used but no alias mapping exists, `fs resolve` checks
the literal env var name. This means `$HERDR_PANE_ID` still works even under the
abstraction — backward-compatible.

---

## 11. Open Questions

1. ~~**Should `fs resolve` shell-out the result or just print it?**~~ **RESOLVED
   (§4.3).** `fs resolve` returns a standard JSON envelope:
   `{"ok":true,"data":{"command":"..."}}`. The LLM parses `.data.command` and
   runs it. `--exec` deferred to v2.

2. ~~**Nested resolution.**~~ **REVISED (§7.3).** The earlier leaning ("leave
   nesting to shell") is revised. The resolver shell-escapes all placeholder
   values substituted into templates. For external-cron, the recommended pattern
   is a wrapper-script form (`fs-heartbeat-install`) rather than inline nested
   `$(fs resolve ...)` quoting. See §7.3 for the full design.

3. **Config hot-reload.** The config is read on every `fs resolve` call (it's a
   small YAML file). No caching, no daemon. If someone edits the file mid-
   session, the next resolve picks up the change. Is this acceptable?
   **Leaning: yes — the file is tiny and changes are rare.**

4. **Per-project overrides.** ~~The current design is one global config. Should a
   project be able to override capabilities (e.g., project X uses claude agents
   while the global default is pi)?~~ **DEFERRED.** Global-only for now.
   `fs init` bootstraps `~/.fs/capabilities.yaml` from the embedded skeleton.
   Per-project config (`<project-root>/.fs/capabilities.yaml` with merge
   semantics) deferred to v2.

5. ~~**`engine_scope` activation.**~~ **SUPERSEDED.** `engine_scope` removed
   entirely from `Playbook` struct and playbook YAML. The capability↔playbook
   link is solely the L1 `detail-playbook` pointer in `capabilities.yaml`.
   `loadAllPlaybooks()` returns all playbooks without engine filtering.

6. ~~**Worker-playbook env var dependency.**~~ **CONFIRMED.** Write the worker
   playbook with `fs resolve` calls from the start (impl plan Step 9). Don’t
   create it with hard-coded herdr and immediately rewrite.

---

## 11.5 Skeleton + `fs init` Bootstrap (DECIDED)

The default capabilities config is maintained as an annotated YAML file
(`internal/capabilities/skeleton.yaml`) and embedded into the binary via
`//go:embed`. This is the **single source of truth** — no runtime repo
dependency, no separate default-config generator.

**`fs init`** is the bootstrap command:
- Ensures `~/.fs/` exists.
- Writes `~/.fs/capabilities.yaml` from the embedded skeleton **only if the file
  does not exist** (never overwrites).
- `flagship.db` auto-creates via existing `Open` paths (no action needed).
- Output: `{"ok":true, "data":{"created":[...], "existed":[...]}}`.
- Idempotent: re-running reports "existed" for files already present.

`fs resolve --init` is retained for backward compatibility but `fs init` is the
primary bootstrap entry point.

---

## 12. References

| What | Path |
|---|---|
| This design doc | `src/flagship/DESIGN-capabilities.md` |
| Roles design (extended by this doc) | `src/flagship/DESIGN-roles.md` |
| Engine interface (DEPRECATED §6) | `src/flagship/internal/engine/engine.go` |
| Herdr adapter (DEPRECATED §6) | `src/flagship/internal/engine/herdr/herdr.go` |
| Orchestrator (DEPRECATED §6) | `src/flagship/orchestrator/orchestrator.go` |
| Knowledge center (EngineScope field) | `src/flagship/internal/knowledge/knowledge.go` |
| CLI entry point | `src/flagship/cmd/main.go` |
| Captain playbook | `~/.fs/kb/captain-playbook.yaml` |
| Reviewer playbook | `~/.fs/kb/reviewer-playbook.yaml` |
| Herdr procedure | `~/.fs/kb/herdr-procedure.yaml` |
| Capability config (to be created) | `~/.fs/capabilities.yaml` |
| Skeleton (embedded source of truth) | `src/flagship/internal/capabilities/skeleton.yaml` |
