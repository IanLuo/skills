---
name: herdr
description: 'Open and control herdr tabs/panes — the terminal workspace manager — to run claude (or pi, codex, gemini, …) in a separate VISIBLE terminal: create a tab, split a pane, submit a prompt, wait, read output, close. Trigger on explicit "tab"/"pane"/"window" language — literal triggers: "open a new tab", "new tab", "another tab", "carry over / hand off work to a new tab", "spawn an agent in a pane", "run claude in a tab", "auto claude", "open claude code", "review/continue in another pane", "herdr", "not a background tab". A request for a real interactive terminal is herdr''s job, NOT the in-process Agent tool. Do NOT use for ordinary bash in the current pane (use the bash tool), or merely because work could benefit from delegation with no visible terminal — that is the delegate skill / Agent tool. Requires HERDR_ENV=1.'
metadata:
  audience: personal
  domain: agent-orchestration
compatibility: Requires herdr running (HERDR_ENV=1) and the herdr CLI. The installed binary is the authority for syntax — run `herdr --help` / `herdr agent` / `herdr pane` to discover commands.
disable-model-invocation: true
---

# herdr

Drive the herdr terminal workspace manager: spawn a supported coding agent — `pi`,
`claude`, `codex`, `gemini`, `opencode`, `aider`, … — in a **new visible pane or
tab**, hand it a task, wait, read its output, and close. You are the coordinator;
the agent kind is a parameter you read from the user's request.

## Prerequisites

You must be running inside a herdr session:

```bash
echo $HERDR_ENV          # must be "1"
echo $HERDR_WORKSPACE_ID # w1
echo $HERDR_TAB_ID       # w1:t1
echo $HERDR_PANE_ID      # w1:p1 — your pane
```

If unset, tell the user to launch herdr first.

## The one-shot shorthand — `/herdr <kind>: [pane|tab] <task> [keep]`

Read the request's leading tokens as parameters; don't match a fixed list:

| token | meaning |
|---|---|
| `<kind>` | FIRST token = the agent kind to spawn. Strip a trailing `:` (`pi:` ≡ `pi`). Any kind the CLI supports — run `herdr agent` for the list. |
| `[pane\|tab]` | Where to run it. **Default `pane`** (new sibling pane). `tab` opens a new tab instead. |
| `<task>` | Everything after, minus a trailing `keep`. |
| `[keep]` | Trailing `keep` / "keep open" / "interactive" leaves the container open for follow-ups; otherwise the run is **temp** — you close it after reading. |

**A leading kind token ALWAYS means spawn a NEW pane/tab.** Never resolve `pi:` /
`codex:` / … to an existing idle agent of that kind — the token names a kind, not a
target. Reuse only when the user names a specific agent or pane.

Default expansion (`pi: <task>` → new sibling pane):

```bash
P=$(herdr pane split --current --direction right --cwd "$PWD" --no-focus | jq -r '.result.pane.pane_id')
herdr agent start pi1 --kind pi --pane "$P"
herdr agent prompt pi1 "<task>" --wait --timeout 120000
herdr agent read pi1 --source recent-unwrapped --lines 200 --format text
herdr pane close "$P"
```

`claude` is the one special kind: it defaults to `--permission-mode auto` at launch
(`manual` when the user says to go carefully) — see **Claude** below.

## The canonical sequence

Split → start → prompt → read → close. Every command returns JSON — read the new
IDs from it, never guess.

1. **Split** — default sibling pane, your cwd, no focus:

   ```bash
   herdr pane split --current --direction right --cwd "$PWD" --no-focus
   # new pane id → .result.pane.pane_id
   ```

   Direction: honor the request; otherwise split a wide pane right, a narrow/tall
   pane down. Avoid repeated same-direction splits. For a `tab` run:
   `herdr tab create --cwd "$PWD" --no-focus` → `.result.tab` + `.result.root_pane`
   (start the agent in the root pane).

2. **Start the agent** — needs an existing pane at an interactive shell prompt
   (`agent start` never creates layout):

   ```bash
   herdr agent start <name> --kind <kind> --pane <pane-id> [-- <kind-specific args>]
   ```

   Returns once the agent is detected and ready. Blocks during startup → returns
   `agent_not_ready` but keeps the name usable — wait for `idle` before prompting.
   Startup timeout is 30s by default. Add kind args after `--` (`claude` needs
   `--permission-mode auto`; others may take `--model`/`--cwd` — run
   `herdr agent start --help`).

3. **Prompt**:

   ```bash
   herdr agent prompt <target> "<task>" --wait --timeout 120000
   ```

   Atomically submits text + Enter. From a non-working state it must see a
   lifecycle change within 5s or returns `agent_prompt_stalled`. Prompting an
   agent already at an approval dialog returns `agent_blocked` BEFORE any input —
   inspect the UI and ask the user before answering it.

4. **Read the result**:

   ```bash
   herdr agent read <target> --source recent-unwrapped --lines 200 --format text
   ```

5. **Close what you created** (temp runs):

   ```bash
   herdr pane close <pane-id>
   ```

## Claude — the auto-mode special case

`<kind>=claude` follows the same grammar, with launch defaults pinned. A bare
`claude` can sit in manual mode and ask before every action, and in-session mode
toggling is unreliable in a background pane — fix the mode at launch:

```bash
herdr agent start <name> --kind claude --pane <pane-id> -- --permission-mode auto
```

| You say | You get |
|---|---|
| `claude <task>` / `auto claude <task>` | sibling pane · claude **auto** · runs `<task>` · closes when done (temp) |
| `claude tab <task>` | new tab · claude auto · runs `<task>` · closes when done (temp) |
| `… keep` / `… keep open` / `… interactive` | same, but keeps open for follow-ups |
| `claude, careful` / `claude, ask me first` | claude manual (no auto) |
| `open claude` (no task — work in it yourself) | auto, keep open, no prompt, no close |

`auto` is Claude Code's classifier mode — it still stops on genuinely risky
things, so a `blocked` state mid-run is the classifier doing its job, not "auto
failed to enable": read what it asks and tell the user before answering.

## Operating rules

1. **Default `--no-focus`.** Don't yank the user's view to another pane unless they
   explicitly ask.
2. **Target explicitly.** `--current`, an explicit pane ID, or a unique agent name.
   Never rely on another client's focused pane.
3. **Parse IDs from JSON responses**, never from sidebar order or examples.
4. **Close only what you created.** Don't close panes/agents/workspaces the user or
   another session owns. Never `herdr server stop` from an active session; never
   kill the main herdr process — for experiments use `herdr --session <name>`.
5. **herdr = external agents/panes.** In-process subagents → `delegate`; wide
   deterministic sweeps → the Workflow tool.

## Reference

Operational detail lives in [references/herdr-ops.md](references/herdr-ops.md) —
read it when you need:
- mapping the layout / inspecting & driving a live agent (`list`/`get`/`wait`/
  `send-keys`, lifecycle states),
- `agent read` sources and the alternate-screen fallback,
- ordinary (non-agent) pane commands — `run`/`wait-output`/`read`,
- pattern recipes: new-tab, interactive follow-ups, parallel workers, open-an-
  agent-for-the-user.

The installed CLI is the authority on syntax — `herdr --help`, `herdr agent`,
`herdr pane`, and `herdr --skill` discover the current commands. Never run bare
`herdr` for discovery (it launches the TUI).
