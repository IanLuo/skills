# herdr — operations reference

Read this when the task needs more than the core sequence in
`SKILL.md`: layout inspection, live-agent driving, read sources, non-agent pane
commands, or one of the pattern recipes. The installed CLI is the authority on
syntax — discover with `herdr --help`, `herdr agent`, `herdr pane`, or
`herdr --skill`. Never run bare `herdr` for discovery (it launches/attaches the
TUI), and never probe a mutating nested command by omitting its arguments
(`herdr workspace create` executes with defaults).

## Targets, IDs, naming

- **Panes** = raw terminals (shells, tests, servers). **Agents** = a recognized
  coding agent occupying a pane. A pane exists whether or not it has an agent.
- IDs: workspace `w1`, tab `w1:t1`, pane `w1:p1`. Closed IDs are never reused;
  `pane move` re-qualifies the pane ID — continue with
  `.result.move_result.pane.pane_id`, never the old value.
- Agent commands accept a **unique live agent name** or the **pane ID** hosting it
  — never terminal IDs, never bare kind labels. Names match `[a-z][a-z0-9_-]{0,31}`.
- Prefer `--current` or your own `$HERDR_PANE_ID`; never assume the focused pane is
  yours. Read IDs from JSON responses (`.result.pane.pane_id`), not sidebar order.

## Lifecycle states

`idle` = ready for input (its tab seen); `done` = idle after unseen background
work; `blocked` = approval/question UI; `working`; `unknown`. `agent prompt
--wait` settles on idle/done/blocked. CLI reads do NOT mark a tab seen; `focus`
does.

## Inspect, wait, interact

- `herdr workspace list` / `herdr tab list --workspace "$HERDR_WORKSPACE_ID"` /
  `herdr pane list --workspace "$HERDR_WORKSPACE_ID"` — map the layout around you.
- `herdr pane current --current` — identify your own pane.
- `herdr agent list` — all agents: names, pane IDs, states.
- `herdr agent get <target>` — current state (add `--json` to parse).
- `herdr agent wait <target> --until blocked --timeout 120000` — wait for a specific
  state (blocked = needs input). Without `--until`, settles on idle/done/blocked.
- `herdr agent send-keys <target> esc` — logical keys for interactive agent UIs.
- On a failed wait or `blocked`: read the transcript (`agent get` / `agent read`)
  BEFORE deciding what input to send.

## Reading output

Read sources: `visible` (rendered viewport) · `recent` (rendered, soft-wrapped) ·
`recent-unwrapped` (wraps joined — prefer for logs/transcripts) · `detection`
(bottom-buffer, used for detection). Use `--format text`; `ansi` only when styling
is evidence. If raising `--lines` reveals no more output, the agent is on the
alternate screen — fallback: ask it to write the full response to a temp file and
reply with only the path, then read that file. Don't request file output in the
initial prompt.

## Ordinary commands in a pane (no agent)

```bash
herdr pane run <pane-id> "just test"
herdr pane wait-output <pane-id> --match "test result" --timeout 120000
herdr pane read <pane-id> --source recent-unwrapped --lines 120
```

`pane run` sends text + Enter atomically. `--match` is a literal substring;
`--regex` for a Rust regex. Omitting `--timeout` waits indefinitely.

## Pattern recipes

### New tab (when the user explicitly asks for a tab/window)

```bash
herdr tab create --cwd "$PWD" --no-focus   # -> .result.tab + .result.root_pane (read them)
herdr agent start <name> --kind claude --pane <root-pane-id> -- --permission-mode auto
herdr agent prompt <name> "<task>" --wait --timeout 120000
herdr agent read <name> --source recent-unwrapped --lines 200 --format text
# temp (a <task> was given): read the result, then close the tab. Persistent (the
# user wants to work in it themselves): leave it. Close only what you created.
```

### Open an agent for the user (no task)

Start it in auto mode and keep it — no `agent prompt`, no close:

```bash
P=$(herdr pane split --current --direction right --cwd "$PWD" --no-focus | jq -r '.result.pane.pane_id')
herdr agent start <name> --kind claude --pane "$P" -- --permission-mode auto
```

### Interactive follow-ups

Keep one agent alive across a chain of prompts, reading between:

```bash
herdr agent prompt worker "Analyze src/auth.ts for security issues." --wait --timeout 120000
herdr agent read worker --source recent-unwrapped --lines 120 --format text
herdr agent prompt worker "Now check whether those issues also exist in src/session.ts." --wait --timeout 120000
herdr agent read worker --source recent-unwrapped --lines 120 --format text
# ... then close
```

### Parallel workers

Split N panes, start N agents, prompt all, wait/read all, close all:

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
