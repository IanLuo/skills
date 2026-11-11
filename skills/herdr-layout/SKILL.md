---
name: herdr-layout
description: 'Shape the structure of a herdr session — workspaces, tabs, panes, Git worktree workspaces, named persistent sessions: list, create, rename, focus, resize, zoom, move, swap, close. Literal triggers: "create a workspace", "list workspaces", "which workspaces are open", "rename this workspace", "close the workspace", "list tabs", "rename this tab", "focus that tab", "close the tab", "pane layout", "list panes", "resize the pane", "zoom the pane", "move the pane", "swap two panes", "worktree workspace", "open a worktree", "list sessions". Not for starting, prompting, waiting on or reading the agent that occupies a pane — that is the herdr skill. Not for running commands in a shell. Requires herdr running.'
metadata:
  audience: personal
  domain: agent-orchestration
compatibility: Requires herdr talking to a running server. The installed binary is the authority for syntax — run `herdr <group> --help` and `herdr <group> <verb> --help`. Behaviours below were exercised against herdr 0.9.5.
disable-model-invocation: true
---

# herdr-layout

herdr runs agents; this skill builds and shapes the container they run in.
Routing, one line each way: **spawn an agent in a pane** (start, prompt, wait,
read, close the agent) is the [`herdr`](../herdr/SKILL.md) skill; **name, list,
focus, resize or close that workspace, tab or pane** is this one.

## The TUI is not a control surface

Never run bare `herdr` — it launches or attaches to the interactive TUI. The TUI
belongs to the human. Shape structure only through `herdr <group> <verb>`, and
never drive the TUI with keystrokes. Confirm syntax from the binary, not memory:

```bash
herdr --help
herdr workspace --help        # likewise: tab, pane, worktree, session, api
herdr pane resize --help
```

## The model

```
workspace  w7            one project/directory context; holds 1..n tabs
└── tab    w7:t2         a screen the human switches between
    └── pane w7:p3       a terminal; an agent may occupy it
```

- IDs are opaque and stable: `w7`, `w7:t2`, `w7:p3`. Closed tab and pane IDs are
  never reused.
- A pane keeps its ID when it moves inside its workspace; it gets a **new**
  workspace-qualified ID when it moves to another workspace.
- A **worktree workspace** is a workspace whose cwd is a linked Git worktree.
- A **named session** is a whole separate server — its own socket and workspaces.

## Discovery — read before you change

```bash
herdr workspace list                 # .result.workspaces[]: label, focused, tab_count, pane_count, worktree
herdr workspace get w7               # .result.workspace
herdr tab list --workspace w7        # .result.tabs[]
herdr tab get w7:t2                  # .result.tab
herdr pane list --workspace w7       # .result.panes[]: pane_id, tab_id, label, cwd, focused
herdr pane current --current         # .result.pane — or --pane w7:p3
herdr pane get w7:p3                 # .result.pane
herdr pane layout --current          # .result.layout: area, panes[rect], splits[ratio], zoomed
herdr pane process-info --current    # what is actually running in that pane
herdr pane neighbor --pane w7:p3 --direction left   # .result.neighbor_pane_id
herdr pane edges --pane w7:p3        # .result.edges: left/right/up/down booleans
herdr api snapshot                   # .result.snapshot: the whole live state in one document
herdr api schema --json              # the socket API schema (protocol 22 on 0.9.5)
```

`--current` needs to be inside a pane (`HERDR_ENV=1`, `HERDR_PANE_ID` set). From
outside herdr the same commands work with an explicit ID, but `--current` falls
back to the *focused* pane, which may belong to the human — pass the ID instead.

## Create and close a workspace

```bash
WS=$(herdr workspace create --cwd "$PWD" --label scratch --no-focus \
     | jq -r '.result.workspace.workspace_id')
herdr tab list --workspace "$WS"
herdr workspace close "$WS"      # .result.type == "ok"
```

`create` returns `.result.workspace`, `.result.tab` and `.result.root_pane`.
`close` destroys every tab, pane and agent inside — close only what you opened.

## Split, zoom and resize a pane

```bash
P=$(herdr pane split --current --direction right --cwd "$PWD" --no-focus \
    | jq -r '.result.pane.pane_id')
herdr pane zoom "$P" --on        # .result.zoom.zoomed == true
herdr pane zoom "$P" --off
herdr pane resize --pane "$P" --direction right --amount 0.1
herdr pane layout --pane "$P"
herdr pane close "$P"            # .result.type == "ok"
```

- `split` takes `--direction right|down`, `--ratio`, `--cwd`, `--env K=V` and
  `--focus|--no-focus`. Name the target: a pane ID, or `--current` for your own pane.
- Zoom is per tab, not per split: `--on`, `--off`, `--toggle` (the default when
  no flag is given). Zooming focuses the pane.
- `resize --amount` is a fraction of the split: `0.1` moved a `0.5` ratio to `0.6`.

## Rename, focus, close — one verb per level

| verb | workspace | tab | pane |
|---|---|---|---|
| list / get | `workspace list` · `get w7` | `tab list --workspace w7` · `get w7:t2` | `pane list --workspace w7` · `get w7:p3` |
| rename | `workspace rename w7 NAME` | `tab rename w7:t2 NAME` | `pane rename w7:p3 NAME` (`--clear` drops it) |
| focus | `workspace focus w7` | `tab focus w7:t2` | `pane focus --direction left` |
| close | `workspace close w7` | `tab close w7:t2` | `pane close w7:p3` |

Rename takes the label as trailing words — `workspace rename w7 my project`.
Focus moves the human's view: do it when asked, and put it back afterwards.

## Move and swap panes

```bash
herdr pane move w7:p3 --tab w7:t1 --split right    # into another tab
herdr pane move w7:p3 --new-tab --label logs       # into a new tab
herdr pane move w7:p3 --new-workspace --label split-out
herdr pane swap --source-pane w7:p3 --target-pane w7:p4
herdr pane swap --pane w7:p3 --direction left      # with its neighbour
```

`.result.move_result` carries the new `.pane.pane_id`, `.previous_pane_id`, and
`.closed_tab_id` when the emptied source tab was removed. `--tab-label` belongs
to `--new-workspace` only; `--new-tab` takes `--label`. Swaps report
`.result.swap.changed`.

## Worktree workspaces

```bash
herdr worktree list --cwd ~/code/proj        # .result.worktrees[]: path, branch, open_workspace_id
herdr worktree create --cwd ~/code/proj --branch feat-x --base main \
    --path ~/code/proj-feat-x --label feat-x --no-focus
herdr worktree open --cwd ~/code/proj --path ~/code/proj-feat-x --label feat-x --no-focus
herdr worktree remove --workspace w1P        # deletes the checkout, closes that workspace
```

- `create` opens the worktree as a workspace **and** opens (or reuses) one for
  the repo root; `.result.workspace.worktree` describes the checkout.
- `open` attaches an existing checkout that has no open workspace
  (`.result.type == "worktree_opened"`).
- `remove` takes a **workspace id**, not a path; it deletes the checkout, drops
  the Git registration and closes only that worktree workspace.

## Named sessions

A session is a separate server: its own socket, workspaces and agents.

```bash
herdr session list            # text table: name, status, directory, socket
herdr session list --json     # .sessions[]: name, running, session_dir, socket_path
herdr --session work          # attach (TUI) — creates the session if absent
herdr session stop work --json
herdr session delete work --json   # only after it is stopped
```

`--session <name>` with a subcommand does not create anything; attaching does.

## Rules

1. **`--no-focus` by default.** Never yank the human's view; restore focus if you move it.
2. **IDs from JSON, never from order.** A gone pane answers with an `error` key;
   check that key, not the exit status — exit codes differ between commands.
3. **Only touch what you created.** Do not close the human's workspaces, tabs,
   panes or sessions, and never stop the server.
4. **Clean up.** Close the workspace or pane you opened, remove temp worktrees.
5. **Structure here; agents in `herdr`; in-process subagents in `delegate`.**

## Out of scope

`--machine` and `--remote` (other hosts), `config`, `channel`, `update`,
`integration`, and `notification show` are not covered here.

## Exercised against herdr 0.9.5

Every command above ran against a live server in a disposable workspace. Three
with a caveat:

- `workspace report-metadata w7 --source X --token k=v --ttl-ms N` — ran and
  wrote the token (readable as `.result.workspace.tokens` in `workspace get`);
  it prints no JSON on success. Display-only metadata.
- `pane input --right-click herdr|pane` — ran; `.result.type == "ok"`.
- `session attach/stop/delete` — only the no-such-session paths ran (stop
  returns `session_stop_failed`, delete returns `deleted: true`). Stopping a
  live session would end the human's work, so it was not exercised.
