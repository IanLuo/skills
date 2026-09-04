---
name: herdr
description: Control herdr — the terminal workspace manager — to spawn agents (pi, claude, codex, shell) in panes, submit prompts, wait for results, read output, and clean up. Use when the user explicitly mentions herdr, or asks to inspect/control panes, tabs, workspaces, commands, or another agent. Trigger phrases — "spawn an agent", "send to pane", "herdr", "review in another pane", "start a worker", "one-shot agent", "in a pane/tab". Do NOT use for ordinary local bash in the current pane (use the bash tool), or merely because a task could benefit from delegation or parallel work — that's the delegate skill (in-process subagents). Requires HERDR_ENV=1.
metadata:
  audience: personal
  domain: agent-orchestration
compatibility: Requires herdr running (HERDR_ENV=1) and the herdr CLI. The installed binary is the authority for syntax — run `herdr --help` / `herdr agent` / `herdr pane` to discover commands.
---


# herdr

Control herdr — the terminal workspace manager — via its CLI. Spawn agents in
panes, submit prompts, wait for completion, read output, close panes. The agent
choice is a parameter; this skill is the herdr-control layer, agent-agnostic.

## Quick command format

The user can specify location and agent inline:

```
/herdr <location> <agent>: <task>
```

**Location** — where to put the agent:

| token | meaning |
|---|---|
| `new-pane` or `pane` | `herdr pane split` (sibling pane in current tab) |
| `new-tab` or `tab` | `herdr tab create` (new tab in current workspace) |
| *(omitted)* | default to `new-pane` |

**Agent** — which agent kind to start (maps to `--kind`):

| token | `--kind` | launch command | auto-mode flag |
|---|---|---|---|
| `claude` or `claudecode` | `claude` | `claude` | `-- --permission-mode auto` |
| `pi` | `pi` | `pi` | *(none — auto by default)* |
| `codex` | `codex` | `codex` | `-- --full-auto` |
| *(any other)* | as-is | as-is | *(discover with `herdr agent start --help`)* |

**Examples:**

- `/herdr new-pane claude: review the last commit` → split pane, start claude in auto mode, prompt it
- `/herdr tab pi: analyze src/auth.ts` → new tab, start pi, prompt it
- `/herdr claude: fix the failing tests` → default to new-pane, start claude in auto mode
- `/herdr new-tab codex: add input validation` → new tab, start codex in full-auto

When no agent is specified (just `/herdr new-pane: run tests`), use `pane run` for a
plain shell command instead of starting an agent.

## Prerequisites

You must be running inside a herdr session:

```bash
echo $HERDR_ENV        # must be "1"
echo $HERDR_PANE_ID    # must be set
```

If not set, tell the user to launch herdr first.

## The CLI is the authority

The installed binary owns the syntax and evolves between versions (the repo skill
is not guaranteed current). Before driving commands you haven't used this session,
discover them:

```bash
herdr --help           # top-level commands
herdr agent            # agent subcommands: list get read send-keys prompt wait
herdr pane             # pane subcommands: split run wait-output read close
herdr --skill          # herdr's own canonical agent instructions
```

## Primitives and targeting

- **Panes** = raw terminals (shells, tests, servers). **Agents** = the recognized
  coding agent occupying a pane. A pane exists whether or not it contains an agent.
- IDs: workspace `w1`, tab `w1:t1`, pane `w1:p1`. Closed IDs are never reused;
  `pane move` re-qualifies the pane ID.
- Agent commands accept a **unique live agent name** or the **pane ID** hosting it —
  never terminal IDs, never bare kind labels. Names match `[a-z][a-z0-9_-]{0,31}`.
- Prefer `--current` or your own `$HERDR_PANE_ID`; never assume the focused pane is
  yours. Read IDs from JSON responses (`.result.pane.pane_id`), not sidebar order.

## Lifecycle states

`idle` = ready for input (its tab seen); `done` = idle after unseen background work;
`blocked` = approval/question UI; `working`; `unknown`. `agent prompt --wait` settles
on idle/done/blocked. CLI reads do NOT mark a tab seen; `focus` does.

## Core workflow: spawn → prompt → wait → read → close

### 1. Create a location

**Split pane** (sibling, preserve cwd, keep user focus):

```bash
herdr pane split --current --direction right --cwd "$PWD" --no-focus
# -> .result.pane.pane_id
```

**New tab** (separate tab, preserve cwd):

```bash
herdr tab create --cwd "$PWD" --no-focus
# -> .result.tab.pane_id (the tab's initial pane)
```

### 2. Start an agent with auto mode

Pass agent-specific flags after `--` to enable auto/unattended mode:

```bash
# Claude Code — auto mode
herdr agent start <name> --kind claude --pane <pane-id> -- --permission-mode auto

# Pi — auto by default, no extra flags
herdr agent start <name> --kind pi --pane <pane-id>

# Codex — full-auto mode
herdr agent start <name> --kind codex --pane <pane-id> -- --full-auto
```

Kinds: `pi`, `claude`, `codex`, `gemini`, … (run `herdr agent start --help` for the
full list). Returns only once the agent is detected and ready for input.

### 3. Submit work

```bash
herdr agent prompt <target> "<task>" --wait --timeout 120000
```

`prompt` atomically submits text + Enter (honors bracketed paste). From a
non-working state it must observe a lifecycle change within 5s or returns
`agent_prompt_stalled`. `--wait` is enough for normal work — don't restate the
defaults with `--until`.

### 4. Read the result

```bash
herdr agent read <target> --source recent-unwrapped --lines 200 --format text
```

### 5. Close what you created (only panes you created)

```bash
herdr pane close <pane-id>
```

## Inspect, wait, interact

- `herdr agent list` — all agents: names, pane IDs, states.
- `herdr agent get <target>` — current state (add `--json` to parse).
- `herdr agent wait <target> --until blocked --timeout 120000` — wait for a specific
  state (blocked = needs input). Without `--until`, settles on idle/done/blocked.
- `herdr agent send-keys <target> esc` — logical keys for interactive agent UIs.
- On a failed wait or `blocked`: read the transcript (`agent get` / `agent read`)
  BEFORE deciding what input to send.

## Ordinary commands in a pane (no agent)

```bash
herdr pane run <pane-id> "just test"
herdr pane wait-output <pane-id> --match "test result" --timeout 120000
herdr pane read <pane-id> --source recent-unwrapped --lines 120
```

`pane run` sends text + Enter atomically. `--match` is a literal substring;
`--regex` for a Rust regex. Omitting `--timeout` waits indefinitely.

## Read sources

`visible` (rendered viewport) · `recent` (rendered, soft-wrapped) ·
`recent-unwrapped` (wraps joined — prefer for logs/transcripts) · `detection`
(bottom-buffer, used for detection). Use `--format text`; `ansi` only when styling
is evidence. If raising `--lines` reveals no more output, the agent is on the
alternate screen — fallback: ask it to write the full response to a temp file and
reply with only the path, then read that file. Don't request file output in the
initial prompt.

## Patterns

### One-shot (spawn → prompt → wait → read → close)

```bash
P=$(herdr pane split --current --direction right --cwd "$PWD" --no-focus | jq -r '.result.pane.pane_id')
herdr agent start reviewer --kind claude --pane "$P" -- --permission-mode auto
herdr agent prompt reviewer "Review the last commit for bugs. Report only actionable findings." --wait --timeout 120000
herdr agent read reviewer --source recent-unwrapped --lines 120 --format text
herdr pane close "$P"
```

### Interactive (spawn → prompt → wait → read → follow up → … → close)

Keep an agent alive across follow-ups based on earlier output:

```bash
herdr agent prompt worker "Analyze src/auth.ts for security issues." --wait --timeout 120000
herdr agent read worker --source recent-unwrapped --lines 120 --format text
herdr agent prompt worker "Now check whether those issues also exist in src/session.ts." --wait --timeout 120000
herdr agent read worker --source recent-unwrapped --lines 120 --format text
# ... then close
```

### Parallel workers (split N → start N → prompt all → wait all → read all → close all)

Run several agents simultaneously, then gather:

```bash
for i in 1 2 3; do
  P=$(herdr pane split --current --direction down --cwd "$PWD" --no-focus | jq -r '.result.pane.pane_id')
  herdr agent start worker-$i --kind pi --pane "$P"
done
herdr agent prompt worker-1 "Add error handling to orders.ts" --wait --timeout 120000
herdr agent prompt worker-2 "Add error handling to products.ts" --wait --timeout 120000
herdr agent prompt worker-3 "Add error handling to users.ts" --wait --timeout 120000
herdr agent read worker-1 --source recent-unwrapped --lines 120 --format text
herdr agent read worker-2 --source recent-unwrapped --lines 120 --format text
herdr agent read worker-3 --source recent-unwrapped --lines 120 --format text
```

## Rules

1. **Default `--no-focus`.** Don't yank the user's view to another pane unless they
   explicitly ask.
2. **Target explicitly.** `--current`, an explicit pane ID, or a unique agent name.
   Never rely on another client's focused pane.
3. **Parse IDs from JSON responses**, never from sidebar order or examples.
4. **Close only what you created.** Don't close panes/agents/workspaces the user or
   another session owns. Never `herdr server stop` from an active session. Never kill
   the main herdr process — for experiments use an isolated `herdr --session <name>`.
5. **herdr = external agents/panes.** For in-process subagents use the `delegate`
   skill; for wide deterministic sweeps use the Workflow tool — both are out of scope
   here.
6. **CLI errors:** server errors are JSON on stderr (exit 1); syntax errors exit 2.
7. **Auto mode by default.** When starting an agent for a task, always pass the
   auto-mode flag (`-- --permission-mode auto` for claude, `-- --full-auto` for codex).
   The user is delegating work — an agent that blocks on every permission prompt
   defeats the purpose. Only omit auto mode if the user explicitly asks for manual.
