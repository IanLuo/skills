---
name: herdr
description: Drive herdr panes and agents from the terminal. Use when you need to spawn another agent (pi, claude, codex, shell command) in a herdr pane, send it prompts, wait for results, read output, or clean up panes — all programmatically without the user switching panes. Triggers include "spawn an agent", "send to pane", "herdr", "parallel agents", "delegate to another agent", "review in another pane", "start a worker", "one-shot agent". Do NOT use for ordinary local bash commands inside the current pane — use the bash tool directly for those.
metadata:
  audience: personal
  domain: agent-orchestration
compatibility: Requires herdr running and HERDR_ENV=1
---

# herdr

Control herdr — the terminal workspace manager — via its CLI. You can spawn agents in
new panes, send prompts, wait for completion, read output, and close panes when done.
The agent choice is a parameter; this skill is the herdr-control layer, completely
agent-agnostic.

## Prerequisites

You must be running inside a herdr session. Verify:

```bash
echo $HERDR_ENV   # must be "1"
echo $HERDR_PANE_ID  # must be set
```

If not set, tell the user to launch herdr first.

## CLI Reference

### `herdr agent start <name> -- <command...>`

Spawn a new agent in a herdr pane. `<name>` is a unique, short, descriptive label
you'll use in every subsequent command (`pi-review`, `claude-impl`, `gpt-scout`).

```bash
# One-shot pi review (background, no focus)
herdr agent start pi-review --split right --no-focus -- pi -p "Review the last commit for bugs"

# Spawn an agent focused (user explicitly asked to see it)
herdr agent start claude-worker --split down --focus -- claude -p "Refactor auth.ts"

# Target a specific workspace/tab with a working directory
herdr agent start worker-1 --cwd /path/to/project --tab 0 --split right --no-focus -- pi -p "Add error handling to users.ts"
```

Key flags:
- `--split right|down` — vertical or horizontal split
- `--focus|--no-focus` — default is focus; prefer `--no-focus` unless user asks to switch
- `--cwd PATH` — working directory
- `--tab ID` — target a specific tab
- `--workspace ID` — target a specific workspace
- `--env KEY=VALUE` — inject environment variables
- `--` — **required** separator between herdr flags and the agent command

### `herdr agent send <target> <text>`

Send literal text to a running agent. `<target>` is the agent name, pane ID, or
terminal ID. This sends raw text — it does NOT press Enter.

```bash
herdr agent send pi-review "Now also check for SQL injection vulnerabilities"
```

### `herdr agent read <target>`

Read an agent's terminal output.

```bash
# Visible screen content (default)
herdr agent read pi-review

# Scrollback, unwrapped, plain text — best for parsing
herdr agent read pi-review --source recent-unwrapped --lines 200

# Plain text, no ANSI codes
herdr agent read pi-review --format text --source recent --lines 500
```

Options:
- `--source visible|recent|recent-unwrapped` — prefer `recent` or `recent-unwrapped` for complete output
- `--lines N` — max lines
- `--format text|ansi` — prefer `text` to avoid ANSI noise

### `herdr agent wait <target> --status <state> [--timeout MS]`

Block until an agent reaches a given state or times out (ms).

```bash
# Wait for agent to finish its turn (60s timeout)
herdr agent wait pi-review --status idle --timeout 60000

# Wait for agent to start working (10s timeout)
herdr agent wait pi-review --status working --timeout 10000

# Wait for blocked state (needs user input)
herdr agent wait pi-review --status blocked --timeout 30000
```

States: `idle`, `working`, `blocked`, `unknown`. Omit `--timeout` to wait
indefinitely (dangerous — always set a timeout).

### `herdr agent list`

List all running agents with names, pane IDs, and states.

```bash
herdr agent list
```

Use before spawning to avoid duplicate agents. Reuse an idle existing agent instead of
creating a new one.

### `herdr agent explain <target> [--json]`

Detailed agent info: state, session, model, pane ID.

```bash
herdr agent explain pi-review --json | jq -r '.pane_id'
```

### `herdr pane close <pane_id>`

Close a pane and terminate its process. Get `<pane_id>` from `herdr agent list` or
`herdr agent explain --json`.

```bash
herdr pane close <pane_id>
```

### `herdr agent focus <target>`

Switch the user's view to an agent's pane. Use only when the user explicitly asks.

```bash
herdr agent focus pi-review
```

## Workflow Patterns

### Pattern 1: One-shot review (spawn → wait → read → close)

Fire-and-forget: spawn an agent, wait for it to finish, read the result, clean up.

```bash
# 1. Spawn in background
herdr agent start reviewer --split right --no-focus -- pi -p "Review the last commit for bugs. Be thorough."

# 2. Wait for completion (2 min timeout)
herdr agent wait reviewer --status idle --timeout 120000

# 3. Read the result
herdr agent read reviewer --source recent --lines 300 --format text

# 4. Report findings to user, then clean up
herdr pane close $(herdr agent explain reviewer --json | jq -r '.pane_id')
```

### Pattern 2: Interactive delegation (spawn → send → wait → read → send more)

Keep an agent alive across multiple prompts, sending follow-ups based on earlier
results.

```bash
# 1. Spawn without -p (stays open for interactive use)
herdr agent start worker --split right --no-focus -- pi --model gpt-4o

# 2. Send first task, wait for idle, read
herdr agent send worker "Analyze src/auth.ts for security issues."
herdr agent wait worker --status idle --timeout 60000
herdr agent read worker --source recent --lines 200

# 3. Follow up based on results
herdr agent send worker "Now check if any of those issues also exist in src/session.ts."
herdr agent wait worker --status idle --timeout 60000
herdr agent read worker --source recent --lines 200

# 4. Clean up when done
herdr pane close $(herdr agent explain worker --json | jq -r '.pane_id')
```

### Pattern 3: Parallel workers (spawn N → wait all → collect)

Run multiple agents simultaneously, then gather all results.

```bash
# Spawn all workers in parallel
herdr agent start worker-1 --split right --no-focus -- pi -p "Add error handling to users.ts"
herdr agent start worker-2 --split right --no-focus -- pi -p "Add error handling to orders.ts"
herdr agent start worker-3 --split right --no-focus -- pi -p "Add error handling to products.ts"

# Wait for all (poll with list, then wait individually)
herdr agent list
herdr agent wait worker-1 --status idle --timeout 120000
herdr agent wait worker-2 --status idle --timeout 120000
herdr agent wait worker-3 --status idle --timeout 120000

# Read all results
herdr agent read worker-1 --source recent --lines 200 --format text
herdr agent read worker-2 --source recent --lines 200 --format text
herdr agent read worker-3 --source recent --lines 200 --format text

# Clean up
herdr pane close $(herdr agent explain worker-1 --json | jq -r '.pane_id')
herdr pane close $(herdr agent explain worker-2 --json | jq -r '.pane_id')
herdr pane close $(herdr agent explain worker-3 --json | jq -r '.pane_id')
```

## Rules

1. **Default to `--no-focus`.** Don't yank the user's view to another pane unless they
   explicitly ask.

2. **Always clean up one-shot agents.** Close with `herdr pane close`. For long-running
   agents, ask the user whether to keep or close.

3. **Wait, then read.** Always `herdr agent wait --status idle` before reading.
   Reading too early returns incomplete output.

4. **Use `--source recent` or `recent-unwrapped`** when reading results. `visible`
   only shows the on-screen portion.

5. **Use `--format text`** when parsing output. ANSI escape codes will confuse you.

6. **Short, descriptive names.** `pi-review`, `claude-impl`, `gpt-scout`. You'll type
   these names in every subsequent command.

7. **Check `herdr agent list` before spawning.** Don't create duplicates. Reuse idle
   agents.

8. **Prefer one-shot mode (`-p`).** For fire-and-forget tasks, use `pi -p "..."` so
   the agent exits when done instead of sitting at an interactive prompt.

9. **Handle timeouts.** If `herdr agent wait` times out, run `herdr agent explain` to
   see the actual state. The agent might be `blocked` on a question — read its output
   to diagnose.

10. **Own the errors.** If an agent fails, read its output, diagnose, and either retry
    with a better prompt or report the failure. Don't silently move on.
