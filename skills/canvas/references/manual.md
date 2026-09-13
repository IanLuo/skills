# Canvas manual — operating the skill

## Read this when

- You are about to run any canvas command and want the right one first try.
- Something is wrong: nothing is listening, a note was ignored, a page will not update.
- You are handing a canvas to another agent, or taking one over.
- N/A: the content contract (sections, anchors, primitives) is in `SKILL.md` and `graphics.md`; the palette/type/component rules are locked in `design-system.md`.

## The model in one paragraph

- The **page is the reply**: the user annotates, the page writes to disk, chat stays near-silent.
- **Delivery is push-free**: notes POST to `feedback.json` the moment they are typed; **Send** is only the signal that the batch is ready.
- **An agent cannot be pushed to** — but the daemon can sweep. It scans every topic every
  `--sweep` seconds (default 2) and wakes the coordinator for any raised Send or unresolved sent
  note, so "nothing is listening" costs one sweep, not an outage. A parked waiter only makes
  delivery faster; it is no longer what correctness rests on.
- **One coordinator per root** parks on `wait --any`, hears every topic, and dispatches each batch to a **disposable worker** whose context dies with its pane.
- N/A: there is no merge, no lock file, and no conflict resolution — see Invariants.

## Roles — decide this before you type anything

| who | runs what | never does |
|---|---|---|
| **coordinator session** (the one talking to the user) | opens/archives canvases, authors content, `coordinator start/stop`, answers in chat | parks, runs rounds, reads the DOM |
| **coordinator agent** (one per root, in a pane) | `wait --any`, then `round <topic> --wait` per batch | edits content, builds, verifies |
| **round worker** (disposable, one batch) | edits the affected sections, builds, verifies, `say`s, `ack`s | waits for more work, starts another round |

- Deadlock rule: the coordinator session must not park. If you are the session the user is typing into, delegate.
- One writer per topic at a time. `content.html` is a single file with no merge; two writers means last-write-wins.

## Command map

| I want to… | command |
|---|---|
| create a topic | `build-canvas.py <topic> --new --root .agents/canvas` |
| start / stop the daemon | `canvas.py start --root .agents/canvas --port 8788` · `canvas.py stop` |
| open a topic in a browser | `canvas.py open <topic>` (prints the authoritative URL) |
| hear every topic at once (the coordinator's park) | `canvas.py wait --any --root .agents/canvas --timeout 300 --quiet` |
| hear one topic only | `canvas.py wait <topic>` (add `--eager` to return on a note without a Send) |
| see what is waiting without blocking | `canvas.py pending <topic>` |
| close the round on the page | `canvas.py say <topic> "<conclusion> — next: <one move>"` |
| mark notes done | `canvas.py ack <topic> --ids id,id` · `--all` |
| escalate a decision I may not make | `canvas.py flag <topic> --ids id --note "why"` |
| rebuild the shell after editing content by hand | `build-canvas.py <topic>` |
| check the page actually renders | `verify-canvas.py <topic> [--viewport 1280x900]` |
| undo a round | `canvas.py versions <topic>` · `canvas.py restore <topic> --to last` |
| start / stop the coordinator | `canvas-worker.py coordinator start\|stop\|status --root .agents/canvas` |
| run one round by hand | `canvas-worker.py round <topic> --root .agents/canvas --wait` |
| see every topic and what is running | `canvas-worker.py list` · `canvas.py open` the `/dashboard` page |
| **legacy, single-topic only** | `canvas-worker.py start\|ensure\|stop <topic>` — do not mix with a coordinator |

- Exit codes from `wait`: `0` batch → work it · `1` idle timeout, normal · `3` stop requested · `4` daemon runs a different revision, re-run it.
- Daemon routes, if you need them directly: `/t/<topic>` page · `/c` content · `/v` version+state · `/h` history · `/a` annotations · `/wait`, `/wait-all` · `/topics` · `/dashboard` · `/coordinator/start|stop` · `/daemon/stop`.

## Lifecycle

1. **Create** — `build-canvas.py <topic> --new`, then write `content.html` (sections, stable anchors).
2. **Open** — `canvas.py start` (once per root) then `canvas.py open <topic>`; say the URL once.
3. **Hand off** — `canvas-worker.py coordinator start --root .agents/canvas`. From here the user annotates and Sends; you stay free.
4. **Work a batch** — the coordinator dispatches `round`; the worker edits only the affected sections, runs `build-canvas.py` **and** `verify-canvas.py`, then closes the round with `say` (a conclusion + one suggested next move, mirrored into the page's `run-conclusion` block) + `ack`.
5. **End** — topic changed → new slug, the old directory freezes (still reopenable). Session over → `coordinator stop`, then `canvas.py stop`.

## Recovery playbooks

| symptom | cause | fix |
|---|---|---|
| page says **agent away**, or notes pile up unread | nobody is parked — the sweep should already be waking an existing coordinator | `canvas-worker.py coordinator status` → `coordinator start`. The sweep can wake a coordinator, but it cannot conjure one where no pane exists |
| `wait` returns **exit 4** | the daemon runs an older `canvas.py` than the file on disk | restart the daemon; a parked waiter keeps running the code it started with |
| the coordinator **parks once, then dies** | its `wait` was wrapped in a shell `while` inside ONE tool call; the harness aborts long calls (~1150s) and an aborted call ends the turn | one `wait` **per turn**, `--timeout 300`, loop by running it again — never a shell loop |
| status pill shows **⚠ N sent note(s) not handled** | a send was consumed and the waiter died before resolving it | park a coordinator; the `unread` re-trigger hands that batch over again |
| notes marked **stuck** | two rounds failed to ack or flag them | decide them yourself, or `flag` them so they stop re-triggering |
| **two listeners** on one topic → duplicate rounds | a per-topic watcher running alongside the coordinator | one coordinator per root; stop the legacy watcher |
| an **orphan pane** `canvas-<topic>-rNNNN` | `stop --now` killed a watcher mid-round and orphaned the worker it spawned | find it with `herdr agent list`, close its pane |
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
- **Chrome changes are not content changes**: `chrome.css`/`chrome.js` are shared by every canvas and locked in `design-system.md`.
- **Nothing is global**: topics, the daemon, the coordinator record and the stop flag are all
  per-root, and there is no index file. Name roots explicitly (`--root`); a root's own directory
  is the source of truth.
- **Keep the loop out of the user's session**: blocking waits belong to a pane agent, not the session the user is typing into.

## What this skill does not do

- N/A: multi-writer merge, locking, or conflict resolution — serialise your writers instead.
- N/A: hosted/remote canvases — the daemon binds `127.0.0.1` only.
- N/A: recovery of a deleted topic — snapshots cover `content.html` only.
- N/A: phone-viewport verification (see above).

Last reviewed: 2026-09-12 · canvas skill
