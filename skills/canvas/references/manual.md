# Canvas manual — operating the skill

## Read this when

- You are about to run any canvas command and want the right one first try.
- Something is wrong: a note was ignored, a page will not update.
- You are handing a canvas to another agent, or taking one over.
- N/A: the content contract (sections, anchors, primitives) is in `SKILL.md` and `graphics.md`; the palette/type/component rules are locked in `design-system.md`.

## The model in one paragraph

- The **page is the reply**: the user annotates, the page writes to disk, chat stays near-silent.
- **The daemon is a page server.** It serves the shell, hot-swaps only the sections whose hash
  changed, and stores each annotation in `feedback.json` the moment it is typed. It never edits
  content and it never starts a round.
- **Nothing wakes the agent.** There is no sweep, no park, no coordinator. A round happens when
  the user asks you to look at the canvas, and the batch is whatever `pending` returns. A Send
  is only the signal that the batch is ready; it is not a promise of a reader.
- One writer per topic: **you**. Nothing else writes `content.html`.

## Roles — decide this before you type anything

| who | runs what | never does |
|---|---|---|
| **the session** (the agent the user is talking to) | opens/archives canvases, authors content, runs the round steps, answers in chat | claims a round will start by itself |
| **the daemon** (`canvas.py serve`) | serves the page, stores notes, hot-swaps sections | edits a topic file, starts a round |

- There is no third role. Any command that used to need one — `coordinator start`, `round` — is
  gone; if you find yourself wanting it, you want to run the round steps yourself.

## Topic files

```
.agents/canvas/<topic>/
├── content.html     the session edits this — the section-keyed content
├── index.html       build-canvas.py writes it — shell + chrome, the file:// fallback
├── feedback.json    the daemon writes it — the user's annotations
├── send.json        the daemon writes it — the Send state (ts, count)
├── history.jsonl    the daemon writes it — append-only record of the conversation
└── versions/        canvas.py snapshots content.html in here, so a round can be undone
```

Everything a session produced lives in that one directory, so a topic can be archived, copied or
deleted as a unit. `<topic>` is a slug: lowercase, digits, hyphens (`^[a-z0-9][a-z0-9-]{0,63}$`).

## Command map

| I want to… | command |
|---|---|
| start / stop the page server | `canvas.py start --root .agents/canvas` · `canvas.py stop` |
| open a topic in a browser | `canvas.py open <topic>` (prints the authoritative URL) |
| see every topic and what is waiting | `canvas.py list --root .agents/canvas` (no daemon needed) |
| see the round's batch (section · id · state · comment) | `canvas.py pending <topic>` (never blocks; `--json` for fields) |
| read one section, or the section index | `canvas.py show <topic> [sN …]` (no daemon needed) |
| create a topic | `build-canvas.py <topic> --new --root .agents/canvas` |
| rebuild the shell after editing content | `build-canvas.py <topic>` |
| check the page actually renders | `verify-canvas.py <topic> [--viewport 1280x900]` |
| close the round on the page | `canvas.py say <topic> "<conclusion> — next: <one move>"` |
| mark notes done | `canvas.py ack <topic> --ids id,id` · `--all` |
| escalate a decision I may not make | `canvas.py flag <topic> --ids id --note "why"` |
| simulate the page's Send button | `canvas.py send <topic>` |
| undo a round | `canvas.py versions <topic>` · `canvas.py restore <topic> --to last` |
| end a topic | `canvas.py drop <topic>` (refuses on unresolved notes; `--force` discards them) |
| hand a reviewed conclusion to a worker | `/task-agent start` (worktree, recommended) · `/dev-task` (small in-repo change) |

- **A Send is the batch boundary.** `pending` returns the notes written up to the last Send;
  anything typed after it is held back and listed as `HELD`, because resolving a note the user is
  still writing gets un-done by their next keystroke (an edit re-posts it as unresolved). With no
  Send at all there is nothing to gate against, so every unresolved note is the batch.
- The notes are on disk from the moment they are typed — a Send decides *what is in play*, never
  whether a note is stored.
- Daemon routes, if you need them directly: `/` topic index · `/t/<topic>` page · `/c` content
  · `/v` version+state · `/h` history · `/a` annotations · `/health` · `POST /daemon/stop`.
  There is no dashboard and no `/topics`: `canvas.py list` and `build-canvas.py <topic> --new`
  are the interface.

## Lifecycle

1. **Create** — `build-canvas.py <topic> --new`, then write `content.html` (sections, stable anchors).
2. **Open** — `canvas.py start` (once per root) then `canvas.py open <topic>`; say the URL once.
3. **Wait** — the user annotates and Sends. You say nothing. There is nothing to poll and nothing
to park, and the page needs no reopening: it hot-swaps itself.
4. **Round** *(when the user asks)* — `canvas.py pending` → edit only the affected sections →
   `build-canvas.py` **and** `verify-canvas.py` → `say` (a conclusion + one suggested next move,
   mirrored into the page's `run-conclusion` block) → `ack` or `flag` every note. A round
   **communicates** — it never changes repo code and never commits.
5. **Review → handoff** — the user reads the conclusion; you do not act on it before that. Once
   approved, hand the conclusion to a worker with its own checkout: `/task-agent start` (worktree)
   for anything non-trivial, `/dev-task` for a small change. Use `/task-agent end` to merge it back.
   The canvas stays the record; the worker's diff is the implementation.
6. **End** — the problem is clear, so `canvas.py drop <topic>`. It refuses while notes are
   unresolved; anything worth keeping leaves as a document or a task first. Session over →
   `canvas.py stop`.

## History

`history.jsonl` is append-only, and the page renders it behind the **Conversation** button —
clicking a user row scrolls to the element that note was about. Read it directly when you need the
session's shape:

```bash
curl -s http://127.0.0.1:<port>/h/<topic>          # JSON events, oldest first
```

## Ending a topic

A topic is meant to be dropped — that is the normal end of its life, not a destructive exception.

- **Problem solved** → `canvas.py drop <topic>`. It **moves** the topic to `<root>/.dropped/<topic>-<ts>/`
  and prints the path, so a drop is recoverable and `list` shows how many are waiting there.
  `--purge` deletes. Refuses while notes are unresolved; `--force` discards those too.
- **Something is worth keeping** → get it out first: `build-canvas.py <topic> --inline-mermaid`
  and hand over the file, or record the outcome as a task (`/task-agent`, `/dev-task`). Nothing
  survives a drop.
- **Problem changed** → new topic. Do not grow this one.
- **Session over** → `canvas.py stop`. Topics stay on disk until they are dropped; `start` brings
  the pages back.

## Recovery playbooks

| symptom | cause | fix |
|---|---|---|
| **page not updating** | daemon down, or you edited the wrong file | `canvas.py status`; content must be `<root>/<topic>/content.html` |
| **notes typed but not on disk** | the daemon was down while the page was open — the page says so | start the daemon; those notes are local to the tab and must be re-entered |
| **nothing happens after Send** | there is nothing to happen: no process watches for it | `canvas.py pending <topic>` — read it, then run the round |
| **notes pile up unresolved** | no session has read them | ask the user to look, then run the round |
| notes marked **stuck** *(legacy)* | a topic from before the loop was removed; the flag no longer updates | ignore it — read `pending` and resolve or flag every note |
| `verify-canvas.py` says **diagrams not rendered** | `assets/mermaid.min.js` not vendored, or bad mermaid syntax (the error names the anchor) | fetch it with the curl in `SKILL.md`, or fix the diagram |
| verify warns **readability** | a sentence over 45 words, or 200+ words of prose with no view | rewrite it in short sentences, or put a view under the claim — not a threshold to argue with |
| verify warns **viewport not honoured** | headless Chrome will not lay out below ~500px | width-sensitive checks are **unverified**, not passing — do not claim a phone layout works |
| a note cannot be executed here | it is chrome/design, not content | `flag --ids … --note …`, leave it unresolved; a chrome change is a `/design-task` pass on the locked doc |
| content drifted from `design-system.md` | the doc is locked and chrome changed | re-run design-task, bump the rev, re-lock — never hand-edit a lock marker |
| **undo a round** | a round wrote the wrong thing | `canvas.py versions <topic>` then `canvas.py restore <topic> --to last` |

## Invariants you must not break

- **Anchors are load-bearing**: never rename or delete a `data-anchor` — every note is keyed to one.
- **Never edit `feedback.json` or `index.html`** by hand: the daemon owns the first, `build-canvas.py` the second.
- **Never re-emit unchanged sections**: sections are swapped by hash, which is the whole token saving.
- **Never ack a note you did not address**, and never leave one undecided — `ack` what you answered, `flag` what you may not decide.
- **Flag, don't guess**: anything that changes what the canvas is *for*, or touches the chrome, belongs to the user or a locked doc.
- **A canvas communicates; it does not execute**: a round edits `<topic>/content.html` and nothing else — no project code, no commits. Explaining more is the round's work; a recorded code change is implemented by a separate worker (`/task-agent` or `/dev-task`) only after the user reviews the conclusion.
- **Chrome changes are not content changes**: `chrome.css`/`chrome.js` are shared by every canvas and locked in `design-system.md`.
- **Nothing is global**: topics, the daemon and the page are all per-root, and there is no index file. Name roots explicitly (`--root`); a root's own directory is the source of truth.
- **Nothing wakes the agent**: do not park, do not poll, do not promise a round. Read `pending` when the user asks.

## What this skill does not do

- N/A: unattended rounds — nothing starts a round but a session the user is talking to.
- N/A: multi-writer merge — one writer per topic, and it is you.
- N/A: hosted/remote canvases — the daemon binds `127.0.0.1` only.
- N/A: recovery of a deleted topic — snapshots cover `content.html` only.
- N/A: phone-viewport verification (see above).

Last reviewed: 2026-09-15 · canvas skill
