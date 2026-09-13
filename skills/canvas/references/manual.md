# Canvas manual — operating the skill

## Read this when

- You are about to run any canvas command and want the right one first try.
- Something is wrong: no coordinator, a note was ignored, a page will not update.
- You are handing a canvas to another agent, or taking one over.
- N/A: the content contract (sections, anchors, primitives) is in `SKILL.md` and `graphics.md`; the palette/type/component rules are locked in `design-system.md`.

## The model in one paragraph

- The **page is the reply**: the user annotates, the page writes to disk, chat stays near-silent.
- **Delivery is push-free**: notes POST to `feedback.json` the moment they are typed; **Send** is only the signal that the batch is ready.
- **An agent cannot be pushed to** — the daemon sweeps instead. It scans every topic every
  `--sweep` seconds (default 2) and wakes the coordinator when a topic has unread notes and the
  coordinator is idle. Nothing has to be parked for a note to be read; a Send is served by the
  next sweep, not by a listener.
- **One coordinator per root** hears every topic and dispatches each batch to a **disposable
  worker** whose context dies with its pane. One Send wakes it at most once: a successful wake
  consumes the send and counts the attempt, and the sweep skips a coordinator that is mid-round.
- N/A: there is no merge, no lock file, and no conflict resolution — see Invariants.

## Roles — decide this before you type anything

| who | runs what | never does |
|---|---|---|
| **coordinator session** (the one talking to the user) | opens/archives canvases, authors content, `coordinator start/stop`, answers in chat | runs rounds, reads the DOM |
| **coordinator agent** (one per root, in a pane) | `round <topic> --wait` per wake from the daemon | edits content, builds, verifies, polls, implements a decision |
| **round worker** (disposable, one batch) | edits the affected sections, builds, verifies, `say`s, `ack`s | waits for more work, starts another round, touches repo code |

- Deadlock rule: the coordinator session must not run a round. If you are the session the user is typing into, delegate.
- **Claiming the role:** `coordinator start`, and only that. Live coordinator for the root → it reports who it is and starts nothing (relay it; never a second). None → it becomes one (new pane + `.coordinator.json`). Never hand-write the record: it skips that check, and two wakers dispatch two rounds for one note.
- One writer per topic at a time. `content.html` is a single file with no merge; two writers means last-write-wins.

## Command map

| I want to… | command |
|---|---|
| create a topic | `build-canvas.py <topic> --new --root .agents/canvas` |
| start / stop the daemon | `canvas.py start --root .agents/canvas --port 8788` · `canvas.py stop` |
| open a topic in a browser | `canvas.py open <topic>` (prints the authoritative URL) |
| see what is waiting | `canvas.py pending <topic>` (never blocks) |
| get a coordinator — become one, or report the live one | `canvas-worker.py coordinator start --root .agents/canvas` (idempotent; then the sweep does the waking) |
| close the round on the page | `canvas.py say <topic> "<conclusion> — next: <one move>"` |
| mark notes done | `canvas.py ack <topic> --ids id,id` · `--all` |
| escalate a decision I may not make | `canvas.py flag <topic> --ids id --note "why"` |
| rebuild the shell after editing content by hand | `build-canvas.py <topic>` |
| check the page actually renders | `verify-canvas.py <topic> [--viewport 1280x900]` |
| undo a round | `canvas.py versions <topic>` · `canvas.py restore <topic> --to last` |
| start / stop the coordinator | `canvas-worker.py coordinator start\|stop\|status --root .agents/canvas` |
| run one round by hand | `canvas-worker.py round <topic> --root .agents/canvas --wait` |
| hand a reviewed conclusion to a worker | `/task-agent start` (worktree, recommended) · `/dev-task` (small in-repo change) |
| see every topic and what is running | `canvas-worker.py list` · `canvas.py open` the `/dashboard` page |

- The daemon's sweep is the only wake path: `POST /a/<topic>/send` writes state and wakes nobody, so a Send cannot produce two rounds. A successful wake logs `{kind: wake, ok: true, agent}` to `history.jsonl`.
- Daemon routes, if you need them directly: `/t/<topic>` page · `/c` content · `/v` version+state · `/h` history · `/a` annotations · `/topics` · `/dashboard` · `/coordinator/start|stop` · `/daemon/stop`.

## Lifecycle

1. **Create** — `build-canvas.py <topic> --new`, then write `content.html` (sections, stable anchors).
2. **Open** — `canvas.py start` (once per root) then `canvas.py open <topic>`; say the URL once.
3. **Hand off** — `canvas-worker.py coordinator start --root .agents/canvas`. From here the user annotates and Sends; you stay free.
4. **Work a batch** — the coordinator dispatches `round`; the worker edits only the affected sections, runs `build-canvas.py` **and** `verify-canvas.py`, then closes the round with `say` (a conclusion + one suggested next move, mirrored into the page's `run-conclusion` block) + `ack`. A round **records a decision** — it never changes repo code and never commits.
5. **Review → handoff** — the user reads the conclusion; you do not act on it before that. Once approved, hand the conclusion to a worker with its own checkout: `/task-agent start` (worktree) for anything non-trivial, `/dev-task` for a small change. Use `/task-agent end` to merge it back. The canvas stays the decision record; the worker's diff is the implementation.
6. **End** — topic changed → new slug, the old directory freezes (still reopenable). Session over → `coordinator stop`, then `canvas.py stop`.

## Recovery playbooks

| symptom | cause | fix |
|---|---|---|
| page says **no coordinator**, or notes pile up unread | the sweep has nobody to wake | `canvas-worker.py coordinator status` → `coordinator start` (idempotent — reports a live one instead of starting a second). The sweep can wake a coordinator, but it cannot conjure one where no pane exists |
| the page says **no sweep** | the daemon was started with `--sweep 0`, or no daemon is running | `canvas.py status`; restart with the default sweep |
| the coordinator is woken but **does not dispatch** | it is mid-round, or `.COORDINATOR_STOP` exists | `coordinator status`; clear STOP, or wait out the round in flight |
| status pill shows **⚠ N sent note(s) not handled** | the wake failed (no herdr, no coordinator), so the send is still unread | `coordinator start`; the next sweep hands that batch over |
| notes marked **stuck** | two rounds failed to ack or flag them | decide them yourself, or `flag` them so they stop re-triggering |
| **duplicate rounds** for one note | two coordinators on one root | one coordinator per root (`coordinator status`); `start` refuses to make a second |
| an **orphan pane** `canvas-<topic>-rNNNN` | a dispatcher was killed mid-round, so its pane outlived it | find it with `herdr agent list`, close its pane |
| **page not updating** | daemon down, or you edited the wrong file | `canvas.py status`; content must be `<root>/<topic>/content.html` |
| `verify-canvas.py` says **diagrams not rendered** | `assets/mermaid.min.js` not vendored, or bad mermaid syntax (the error names the anchor) | fetch it with the curl in `SKILL.md`, or fix the diagram |
| verify warns **viewport not honoured** | headless Chrome will not lay out below ~500px | width-sensitive checks are **unverified**, not passing — do not claim a phone layout works |
| a worker cannot execute the note | it is chrome/design, not content | `flag --ids … --note …`, leave it unresolved; a design change is a `/design-task` pass on the locked doc |
| content drifted from `design-system.md` | the doc is locked and chrome changed | re-run design-task, bump the rev, re-lock — never hand-edit a lock marker |

## Invariants you must not break

- **Anchors are load-bearing**: never rename or delete a `data-anchor` — every note is keyed to one.
- **Never edit `feedback.json` or `index.html`** by hand: the daemon owns the first, `build-canvas.py` the second.
- **Never re-emit unchanged sections**: sections are swapped by hash, which is the whole token saving.
- **Never ack a note you did not address**, and never leave one undecided — undecided notes are retried, then stuck.
- **Flag, don't guess**: anything that changes what the canvas is *for*, or touches the chrome, belongs to the user or a locked doc.
- **A canvas decides; a worker acts**: a round edits `<topic>/content.html` and nothing else — no project code, no commits. A recorded code change is implemented by a separate worker (`/task-agent` or `/dev-task`) only after the user reviews the conclusion.
- **Chrome changes are not content changes**: `chrome.css`/`chrome.js` are shared by every canvas and locked in `design-system.md`.
- **Nothing is global**: topics, the daemon, the coordinator record and the stop flag are all
  per-root, and there is no index file. Name roots explicitly (`--root`); a root's own directory
  is the source of truth.
- **Keep the loop out of the user's session**: rounds belong to a pane agent, not the session the user is typing into. Nothing needs to be parked for a Send to be read.

## What this skill does not do

- N/A: multi-writer merge, locking, or conflict resolution — serialise your writers instead.
- N/A: hosted/remote canvases — the daemon binds `127.0.0.1` only.
- N/A: recovery of a deleted topic — snapshots cover `content.html` only.
- N/A: phone-viewport verification (see above).

Last reviewed: 2026-09-13 · canvas skill
