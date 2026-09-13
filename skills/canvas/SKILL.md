---
name: canvas
description: Co-edit one live page with the user — a canvas on 127.0.0.1 where every element is clickable to annotate, and the agent keeps appending graphics-first content to the same page until the topic changes. Use ONLY when the user explicitly asks for a canvas — "/canvas", "open a canvas", "let's work on this on a canvas", "keep it in the canvas", "co-edit this with me" — or explicitly asks to continue on a canvas that already exists under .agents/canvas/. Never auto-trigger. Do NOT use for a one-shot annotatable page that is not edited across rounds (that is annotate), for a visual answer inside chat (show-me), for charts or dashboards (dataviz), for writing specs (specs), or for design files (design-task).
metadata:
  audience: personal
  domain: agent-orchestration
compatibility: Needs python3 3.9+ for the local daemon (binds 127.0.0.1 only, no external network). The built .html still opens from file:// without the daemon, with reload-to-update and copy-to-clipboard feedback instead of Send.
---

# canvas

A canvas is **one page the user and the agent edit together, bound to a topic**. The user
reads it and annotates anything on it; you append graphics-first content to the same page
until the topic changes.

The page is the reply. **Delivery is push-free**: the user annotates and presses **Send** on the
page; whoever is parked in `canvas.py wait` gets the notes as a tool result. Nobody copies
anything into a chat box, and the user never has to say "go".

Below, `$S` is this skill's directory (the parent of `SKILL.md`). Run the commands from
the project root.

## Who does what — do not park the main session

The feedback loop **blocks** while it waits, and running it costs a lot of context (file
reads, build output, headless DOM dumps). Run it in the session the user is talking to and
you block them for minutes and spend their context on the loop. So:

| role | who | does |
|---|---|---|
| **coordinator** | the session talking to the user | opens/archives canvases, authors content, starts/stops the worker, answers in chat. **Never parks, never runs the loop.** |
| **worker** | a separate agent in a herdr pane | **woken by the daemon** when notes arrive, then per round: reads the notes, edits the sections, builds, verifies, `say`s, `ack`s, ends its turn. Parks in `wait` only as the fallback (no herdr, or inline). |

The user only ever watches the page. The worker's context absorbs the loop, and your
session stays free.

```bash
python3 $S/scripts/canvas-worker.py start  <topic> --root .agents/canvas --kind pi
python3 $S/scripts/canvas-worker.py status <topic> --root .agents/canvas
python3 $S/scripts/canvas-worker.py stop   <topic> --root .agents/canvas          # graceful: round finishes, then the pane closes
python3 $S/scripts/canvas-worker.py stop   <topic> --root .agents/canvas --now    # close the pane now (kills a live round)
python3 $S/scripts/canvas-worker.py ensure <topic> --root .agents/canvas          # start a worker ONLY if none is parked
```

`start` writes `<topic>/WORKER.md` (the task card), splits a pane with herdr, launches the
worker and hands it the card. The card is self-sufficient — the worker has no memory of your
conversation, so put anything it needs to know in the card (or in the content).

**Nobody listening is a state to repair, not wait out.** A worker counts as listening only
while its `wait` call is parked. If it exits — STOP, a crash, a context cut, an ended turn —
the canvas is unowned, and a Send queues with no reader while the page still says "waiting
for the agent to look". `stop` leaves nothing behind (it lets the round finish, bounded by
`--wait`, then closes the pane and clears STOP); `status` prints the recovery command; and
`ensure` is idempotent — parked → no-op, otherwise it clears the leftovers and starts one. If
the page shows nobody listening, `ensure` is the fix, and it is safe to run blind.

**Working inline** is the fallback when there is no herdr session (`HERDR_ENV` unset), or for
a single quick round the user is waiting on. Then you run the loop yourself from the section
below — knowing you are blocking until the next Send.

**One writer at a time.** While a worker is running, it owns `content.html`. If you need to
author content, `stop` the worker first (graceful), make your change, then `start` it again.

## Where topics live, and how other agents find them

`DEFAULT_ROOT` is the **relative** path `.agents/canvas`, so a topic lives with the project it
documents: `<project>/.agents/canvas/<topic>/`. That choice keeps a topic one archivable,
copyable, deletable unit — and costs discoverability, because nothing then knows other roots
exist. Two things fix that cheaply:

- **Nothing is global.** Roots are named explicitly (`--root`); there is no index file
  and no shared state. A root's own directory is the only source of truth, so two
  projects can never see or break each other. `canvas-worker.py list --root A --root B`
  shows several; with no `--root` it shows just the one here.
gone. `canvas.py stop` marks the root's daemon gone but **keeps the root** — its topics are
  still on disk and still worth finding. `canvas-worker.py list` with no `--root` reads it, so
  one command shows every project's canvases; the dashboard shows the other roots too.
- **Files are the contract.** `say`, `ack`, `pending`, `flag`, `versions`, `restore` are pure
  file operations — **no daemon needed** — and `wait` falls back to watching files. So any
  agent that knows the path can take a topic over cold, with no setup and no shared memory.
  `.agents/canvas/` is gitignored: these files persist on this disk, they do not travel with
  the repo.

**Liveness is a file, not a memory.** A parked waiter heartbeats to `<topic>/parked.json`
while it waits, and readers treat a heartbeat older than `PARKED_TTL` (45s) as absent. So "is
anyone listening" survives a daemon restart, is readable by another agent, and expires by
itself when the waiter is killed. `/v` reports both: `listening` (waiters connected to this
daemon right now) and `parked` (the durable heartbeat).

## Files (root: `.agents/canvas/`, one DIRECTORY per topic)

```
.agents/canvas/<topic>/
├── content.html     the worker and coordinator edit this — the section-keyed content
├── index.html       build-canvas.py writes it — shell + chrome, the file:// fallback
├── feedback.json    the daemon writes it — the user's annotations
├── history.jsonl    the daemon writes it — append-only record of the conversation
├── WORKER.md        canvas-worker.py writes it — the worker's task card
├── worker.json      canvas-worker.py writes it — pane id, agent name, kind
└── STOP             written by `canvas-worker.py stop` to end the worker's loop; cleared when the pane closes
```

Everything a session produced lives in that one directory, so a topic can be archived,
copied, or deleted as a unit. `<topic>` is a slug: lowercase, digits, hyphens
(`^[a-z0-9][a-z0-9-]{0,63}$`).

## Open a canvas

```bash
python3 $S/scripts/build-canvas.py <topic> --new        # first time: creates content + shell
python3 $S/scripts/canvas.py start --root .agents/canvas --open <topic>
python3 $S/scripts/canvas.py open <topic>               # reopen an existing one
python3 $S/scripts/canvas.py stop                       # when the session ends
```

`start` prints the URL and opens the browser. The port is picked at start time (7391 by
default, next free one if taken — `start`/`open` always print the authoritative URL, so
trust those and never guess a port). Say the URL to the user once, then stop mentioning
it — the tab stays open.

Add mermaid to the repo once to get rendered diagrams:

```bash
curl -sL -o $S/assets/mermaid.min.js https://cdn.jsdelivr.net/npm/mermaid@11/dist/mermaid.min.js
```

## The dashboard — what is running, and how to stop it

The daemon serves `dashboard.html` (this skill's folder, static, no build step) at `/`, and
every canvas page links to it — `dashboard` sits in the bottom-right controls row,
beside Conversation and Send (hidden when the page is opened from `file://`, where `/dashboard`
does not resolve). It lists every topic with the state that matters — is anyone listening, how
many notes are pending / unsent / queued-unread, rounds, and Send → reply latency — and every
button runs one of the skill's own CLIs and prints its real output. The CLI equivalent is on
the page, because the page is never the only way.

```bash
open http://127.0.0.1:<port>/                                   # the daemon prints the port
python3 $S/scripts/canvas-worker.py list --root .agents/canvas  # the same view in a terminal
```

Actions: **rebuild** a canvas, **start worker** (`canvas-worker.py ensure`), **stop worker**
(`stop --wait 5`), **create** a topic, **stop the daemon**. Two states mean "fix me": *no
worker* with notes waiting, and *unread send* — those are exactly when the user's notes are
sitting unread.

It only claims what it can prove: listener count, the STOP flag and the worker record. It has
**no "working" state** on purpose — a consumed send means notes were collected, not that a
round is in flight; the daemon cannot see the agent, so it does not pretend to. It also warns
when the running daemon is an older revision of `canvas.py` than the file on disk.

## Content contract

`<topic>.content.html` is a flat list of top-level sections:

```html
<section data-section="s1">
  <p class="eyebrow">topic · round 2</p>
  <h1 data-anchor="title">Where we are</h1>
</section>
<section data-section="s2">
  <h2 data-anchor="s2-title">The choice</h2>
  <pre class="mermaid" data-anchor="fig-1">flowchart LR
  A[Option A] --> C{Pick}
  B[Option B] --> C</pre>
</section>
```

- **Sections are the update unit.** The daemon hashes each one and hot-swaps only the
  changed ones within ~1s. Touch only what changed — that is the whole token saving.
- **Every element the user might disagree with gets a stable `data-anchor`.** Anchors never
  change between rounds: the user's annotations are keyed by them.
- **The first section carries the run conclusion** — a `data-anchor="run-conclusion"` element
  rewritten at the end of every round with one conclusion line and one `next:` move. So the
  round's outcome sits on the page itself; the Conversation panel is the record, not the only
  place to read it.
- **Pick the smallest view that makes the point** (show-me's ladder, in
  [references/graphics.md](references/graphics.md)): pseudocode for logic, a call/component
  tree for structure, a file tree for layout, a shape-matched diff for what changed, mermaid
  for flow — and `.compare`/`.cards` for options **last**, not first. Prose is the last resort.
  For repeated shapes you may write data instead of markup (`data-render`) — but that is a
  correctness win, not a token win; do not go hunting for token savings in markup.
- Read **[references/architecture.md](references/architecture.md)** before changing how delivery,
  liveness or ownership works. It is locked (`specs:locked:`) and records why the one server, the
  2 s sweep, the lease and the wake target are the way they are — with the rejected alternatives.
- Read **[references/manual.md](references/manual.md)** when running any canvas command, when
  something is wrong (nothing listening, a note ignored, a page not updating), or when handing a
  canvas to another agent. It has the command map, the lifecycle, and symptom→cause→fix playbooks.
- Read **[references/design-system.md](references/design-system.md)** before changing any canvas
  visual: the palette (3 schemes), type scale, spacing, radii and component states are locked
  there, with computed contrast evidence. Changing a token means re-running the contrast check.
- Read **[references/graphics.md](references/graphics.md)** when rendering content, choosing
  a diagram, when a block does not show up, or when the user questions a cost claim. It has
  the primitives, the mermaid picker, the measured token costs, and the failure modes.

## The round loop

One round is the unit of work. **Who holds the wait** is the only thing that changes between
modes: the daemon holds it and prompts the worker (default), or the agent blocks in `wait`
itself (fallback: no herdr, or working inline from the main session).

**The push.** When a Send lands, the daemon prompts the topic's recorded worker via
`herdr agent prompt` — unless somebody is already parked, in which case the parked waiter
picks the notes up by itself. Either way the outcome is logged to `history.jsonl` as a `wake`
event ("agent woken" / "nobody was woken — no worker recorded"), so a silent failure is
visible on the page. Design note: a parked waiter is a **blocking shell inside an agent
turn**, so any interruption kills it — that is what the wake path exists to replace, and why
`ensure` exists to repair it.

This is the loop the **worker** runs (and the one you run when working inline).

1. **Park (fallback), or be woken.** Parked:
   ```bash
   python3 $S/scripts/canvas.py wait <topic> --timeout 600
   ```
   Run it as the LAST thing in your turn so the click lands while you are listening. The page
   shows the user an honest `agent listening` / `agent away` indicator, driven by whether
   this call is actually parked.

   - A Send is **sticky**: if it arrives while you are away, your next `wait` returns
     immediately rather than losing it. A consumed Send is never delivered twice.
   - `--eager` returns as soon as a note is written, without waiting for Send. Use it only
     when the user asks for immediate reaction; it splits their batches.
   - `pending` returns whatever is unresolved without blocking — for when the user tells you
     in chat that they annotated.
   - **Exit codes matter:** `3` = a stop was requested, exit the loop; `4` = the daemon is
     running a different revision of this script, just run `wait` again to pick it up.
   - A parked `wait` ignores later edits to this script — it runs the code it started with.
     That is why the revision check exists; never leave a stale waiter parked.
2. Edit **only** the affected sections of `.agents/canvas/<topic>/content.html`.
3. Refresh the fallback shell and mark the notes you addressed:
   ```bash
   python3 $S/scripts/build-canvas.py <topic>
   python3 $S/scripts/canvas.py ack <topic> --ids <id,id>     # or --all
   ```
4. **Verify before you claim it works.** This renders the real page in headless Chrome and
   checks the DOM — diagrams actually drawn, specs rendered, no render error, inlined JS
   intact:
   ```bash
   python3 $S/scripts/verify-canvas.py <topic> --root .agents/canvas
   ```
   Non-zero exit means do not report success. Needs the daemon running and Chrome
   (`--chrome PATH` if it is somewhere unusual). A canvas can look perfect in source and be
   broken in the browser — three separate bugs shipped that way.
5. Close the round with a conclusion and the next move:
   ```bash
   python3 $S/scripts/canvas.py say <topic> "queue is sound, liveness is the hole — next: R1 wake target"
   ```
   Every round ends with **what is now true** plus **one suggested next move**; "§3 rewritten"
   is a progress note, not a conclusion. Write the same line into the page's conclusion block
   (`data-anchor="run-conclusion"`, in the first section) so it is visible on the topic page
   without opening Conversation, and keep the chat message to that same one line — the page is
   the reply, chat is the nudge.

## History

`<topic>/history.jsonl` is append-only. The daemon logs every note, resolve, delete and
content change by itself; `say` adds your line. **Rounds are derived, not stored** — a round
is the events since your previous `say` — so nothing is renumbered and a lost write cannot
corrupt the past. The page renders it behind the **Conversation** button (which also holds the
requirement composer), and clicking a user
row scrolls to the element that note was about.

Read it directly when you need the session's shape:

```bash
curl -s http://127.0.0.1:<port>/h/<topic>          # JSON events, oldest first
```

`build-canvas.py` refuses to build content with duplicate anchors, nested sections,
malformed spec JSON, or an unknown `data-render` kind. Fix what it reports rather than
shipping a broken page.

## Ending a topic

- **Topic changed** → start a new canvas with a new slug. The old one freezes; its whole
directory stays, and `canvas.py open <old-topic>` reopens it.
- **User wants the file** → rebuild with diagrams embedded, hand them the path, stop:
  ```bash
  python3 $S/scripts/build-canvas.py <topic> --inline-mermaid
  python3 $S/scripts/canvas.py stop
  ```
- **Session over** → stop the worker, then the daemon:
  ```bash
  python3 $S/scripts/canvas-worker.py stop <topic> --root .agents/canvas   # round finishes, pane closes
  python3 $S/scripts/canvas.py stop
  ```
  Nothing is left running, and the pane is closed.

## Rules

- **Never ask the user to paste their notes into chat.** That is the friction this skill
  exists to remove. If you are not parked in `wait`, their Send is stored and waiting —
  just read it.
- Keep chat near-silent. The page is the reply; a paragraph in chat duplicating it is paid
  for twice.
- Never rewrite the whole canvas when a section changed. Re-emitting unchanged sections is
  the single most expensive mistake in this workflow.
- Never renumber or rename anchors, and do not nest sections.
- Resolve notes with `ack` once addressed — resolved notes stay visible and greyed, so the
  user can see you did not drop them.
- The daemon binds `127.0.0.1` only and serves from `.agents/canvas/`; it is not a general
  web server. Do not point it at a repo the user has not asked you to expose.
- Never overwrite `.agents/canvas/<topic>.feedback.json` — the daemon owns it, and clobbering
  it destroys notes the user has not sent you yet.
