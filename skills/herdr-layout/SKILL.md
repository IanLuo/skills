---
name: herdr-layout
description: 'Shape the Herdr desktop app window — its spaces, tabs and panes, and the layouts that frame them: list what is open, create, rename, move, select, settle, zoom, split, swap, close. Literal triggers: "herdr app", "the Herdr window", "open a space", "new space", "list my spaces", "which spaces are open", "rename this space", "close the space", "switch layout", "split the terminal", "zoom this pane", "screenshot herdr", "herdr-app", "hc", "list my terminals". Not the old terminal multiplexer: that is the herdr skill. Starting, prompting, reading or waiting on an agent is hc, not this. Requires the app running for ls/status/do; commands works without it.'
metadata:
  audience: personal
  domain: agent-orchestration
compatibility: Requires herdr-app 0.1.x and herdr-core (hc) 0.1.x. `herdr-app commands` is the authority for every argument — read it, never guess. Behaviours below were exercised against herdr-app 0.1.11 / hc 0.1.18.
disable-model-invocation: true
---

# herdr-layout

Herdr is two programs. `herdr-app` **shows** spaces, tabs and panes;
`herdr-core` (`hc`) **runs** their terminals and keeps them alive when the window
closes. This skill arranges what the person sees; starting, prompting, reading and
waiting on an agent is `hc` — see `hc --skill`.

The old terminal multiplexer (`herdr`: workspaces `w7`, tabs `w7:t2`, panes
`w7:p3`) is a different product with its own skill; nothing here drives it.

## Never launch the window to inspect it

Bare `herdr-app` opens or raises the person's window: inspect with `ls`,
`status` and `commands` instead. Never automate their pointer or keyboard, and
never quit, stop or restart their app or core.

## The model

```
space   sp861c4c…        a folder (and machine) with tabs
└── tab tbe22bef…        a screen in that space
    └── pane p19e828…    a rectangle in a tab; it shows one terminal
        └── terminal t3  the program; lives in hc, outlives panes and the window
```

| id | looks like | who owns it |
|---|---|---|
| space | `sp861c4c32859f1680…` | the app |
| tab | `tbe22bef3228016452…` | the app |
| pane | `p19e828798d112eb27…` | the app |
| terminal | `t3`, or a name you gave it | hc |

A **layout** is not content: it is the chrome — sidebar, tab strip, terminal
area — read from `~/.config/herdr-app/layouts/<name>.json`.

## Inspect

```bash
herdr-app ls                 # spaces > tabs > terminals, with pane IDs
herdr-app status             # layout, space, tab, focused pane, zoom, overlays, errors
herdr-app commands           # every command, its arguments, its description
herdr-app shot /tmp/w.png    # the window's own frame, full size, even if covered
```

`ls` and `status` talk to the running window. `commands` works without one.
`shot` needs a window that has drawn a frame; it prints the file's path.

## Changing anything: `herdr-app do`

```bash
herdr-app do space.new --name api --folder /path/to/api
herdr-app do space.select '{"index":1}'
```

`--key value` pairs (JSON for arrays and objects), or one JSON object. It prints
the created ids as JSON, or `{"error": "..."}` and exits 1. An unknown argument
name fails and lists the valid ones — it never falls back to the focused pane.
Read the returned ids; confirm with `ls` or `status`.

## Spaces

```bash
herdr-app do space.new --name api --folder /path/to/api
herdr-app do space.rename --id sp861c4c… --name "api v2"
herdr-app do space.move --id sp861c4c… --index 1
herdr-app do space.select --index 1            # or --id sp861c4c…
herdr-app do space.next                        # space.prev
herdr-app do space.close --id sp861c4c… --confirmed true
```

- New spaces are **shown immediately**, so create one only when asked, and name
  it — an unnamed space is called after where its first terminal lands.
- `--folder` defaults to the focused terminal's folder; `--machine` (`hc machine
  ls`) puts the space on another machine, where that folder must exist.
- `close` asks first unless `--confirmed true`. It ends the space's terminals —
  but check `hc ls` afterwards: in one run three terminals were still `live`
  after the space closed, and needed `hc kill`.

## Tabs

```bash
herdr-app do tab.new --name "fix login" --space sp861c4c… --folder /path
herdr-app do tab.rename --id tbe22bef… --name logs
herdr-app do tab.move --id tbe22bef… --index 1
herdr-app do tab.select --index 2              # or --id tbe22bef…
herdr-app do tab.next                          # tab.prev
herdr-app do tab.settle --id tbe22bef…         # put away; still running
herdr-app do tab.unsettle --id tbe22bef…
herdr-app do tab.close --id tbe22bef… --confirmed true
```

Unnamed tabs are called by their number, and moving or closing renumbers the
rest — take names from a fresh `ls`, not from the previous one. `settle` takes a
tab out of the lists without ending it; showing it, or an agent in it needing
the person, brings it back.

## Panes

```bash
herdr-app do pane.split --direction right --pane p19e828…    # or down
herdr-app do pane.focus --pane p19e828…                      # or --terminal t3
herdr-app do pane.go --direction left                        # focus the neighbour
herdr-app do pane.swap --direction left                      # swap with it
herdr-app do pane.zoom --pane p19e828…                       # toggle: alone / all
herdr-app do pane.close --pane p19e828…                      # ends its terminal
```

`split` defaults to the focused pane and accepts `--argv` (launch a program or
agent there) or `--mod` with `--props` (a mod as the pane). `pane.close` ends the
terminal, so prefer `terminal.park` when the work should survive.

## Terminals

A terminal can exist with no pane — a hidden worker, a sub-agent, a parked one.
`herdr-app ls` is the authority for which terminals have a pane; `hc ls` lists
all of them.

```bash
herdr-app do terminal.open --folder /tmp --place tab --name ex   # or right|down
herdr-app do terminal.park --terminal t3          # take its pane away; keeps running
herdr-app do terminal.keep --terminal t3 --place right          # give it a pane back
herdr-app do terminal.show --terminal t3 --pane p19e828…        # borrow a pane
herdr-app do terminal.show                                      # give it back
herdr-app do terminal.kill --terminal t3                        # end it and sub-agents
```

`park` and `keep` answer `{"pending": true}`: the window applies them, so confirm
with `ls` rather than assuming. `terminal.copy` / `paste` / `paste_image` touch
the person's clipboard and are out of scope here.

## Layouts

```bash
herdr-app do layout.switch --name main      # no name: the next layout
herdr-app do layout.clone --name main --as mine      # personal copy, shown at once
herdr-app do layout.hide --id agents        # layout.show, layout.toggle
herdr-app do layout.resize --id sidebar --px 320      # or --weight
herdr-app do layout.reset --name mine       # old file kept aside
```

Nodes are addressed by their `id` in the JSON (`sidebar`, `spaces`, `agents`,
`work-body`, `main`). Shipped layouts accept only click/look fields — change
structure on a clone. `layout.select` needs a **stack** node and the shipped
`main` has none, so it fails there; `layout.set` replaces a personal tree
wholesale. A broken layout keeps the old one running and reports in `status`.

## The terminals behind the panes

```bash
hc ls                 # live terminals: address, agent, cwd, state, where
hc get t3             # one terminal's state and status
hc label t3 role=review
hc kill t3            # end the program
hc rm t3              # remove a finished record
```

That is the whole of `hc` this skill needs: find a terminal, label it, free it.
`hc run --bg` starts a worker with **no pane** unless it carries a `herdr.space`
label, so a background job never disturbs the window — `terminal.keep` gives it
a pane when the person should see it. Everything else is `hc --skill`.

## Rules

1. **Read `ls` / `status` first**, and use the ids from them. Never guess the
   focused pane or reuse an id from an earlier listing.
2. **Do not change what the person did not ask to see.** New spaces and tabs
   appear at once; splits and zooms reshape their screen.
3. **Never launch bare `herdr-app`**, never automate pointer/keys, never quit,
   stop or restart their app or core.
4. **Close only what you opened**, then check for terminals left running.
5. **Arrangement here; agents in `hc`; the old TUI in `herdr`; subagents in `delegate`.**

## Not covered here

`app.*` (settings, theme, palette, updates, quit), `window.*`, `mod.*`,
`overlay.close`, the clipboard commands, `agent.focus` / `agent.next` /
`agent.prev`, and the mod API — that last one is documented in
`~/.config/herdr-app/AGENTS.md` and its generated `.d.ts` files, which the app
rewrites on start.

## Exercised against herdr-app 0.1.11 / hc 0.1.18

Every command above ran on a live window, in disposable spaces that were closed
and removed afterwards; the original space, tab and layout were restored.

- `layout.select`, `layout.set`, `terminal.copy/paste/paste_image` and the
  `agent.*` show commands were not run — the first fails on the shipped `main`
  (no stack node), the rest were skipped to leave the tree and clipboard alone.
- `space.close` ended some terminals and not others; see the note under **Spaces**.
