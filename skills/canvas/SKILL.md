---
name: canvas
description: "Canvas exists so the agent's answers are understandable and the user's feedback keeps its meaning: an article-shaped live page on 127.0.0.1 — abstract, then detail, evidence under each claim, one marked key per section — which the user reads and annotates element by element, and which the agent keeps updating until the topic changes. A round is not done until the page is understood on its own. Use ONLY when the user explicitly asks for a canvas — \"/canvas\", \"open a canvas\", \"let's work on this on a canvas\", \"keep it in the canvas\", \"co-edit this with me\" — or explicitly asks to continue on a canvas that already exists under .agents/canvas/. Never auto-trigger. Do NOT use to answer a question with a one-shot page (that is annotate: annotate answers a question, canvas explains a response in depth); nor for a visual answer inside chat (show-me), for charts or dashboards, for writing specs (specs), or for design files (design-task)."
metadata:
  audience: personal
  domain: agent-orchestration
compatibility: Needs python3 3.9+ for the local daemon (binds 127.0.0.1 only, no external network); nothing else. The built .html still opens from file:// without the daemon, with reload-to-update and copy-to-clipboard feedback instead of Send.
---

# canvas

**The purpose: the agent's answer must be understandable, and the user's feedback must not lose
information.** A canvas is **one page the user and the agent edit together, bound to a topic**;
the user reads it and annotates anything on it, and you append graphics-first content to the same
page until the topic changes. Every rule in this skill serves those two failures or is
mechanics for them — when a rule seems to point elsewhere, this is what it is for.

## What a canvas is for

Two failures it removes, in this order:

- **The agent's answer is hard to understand.** Chat prose is a stream the user has to parse
  and re-read. A canvas answers in the smallest view that carries the point — pseudocode, a
  tree, a flowchart, a diff — placed where the question is, with the one live thing marked.
  The page is the reply; chat is only the nudge to look at it.
- **The user's feedback loses information.** "It's wrong" in a chat box goes stale the moment
  the next message lands. On a canvas every element the user might dispute carries a stable
  `data-anchor`, so a note lands on the exact claim it is about, is stored on disk, and
  survives the round that answers it.

**Done is not "the notes were acked."** A round is finished when a reader who was not in the
conversation can open the page and answer *what changed, what is now true, and what is being
decided* by scanning it — no prose required, nothing to ask. A round that answered every note
and explained nothing failed.

The page is the reply. **Delivery is push-free**: the user annotates and presses **Send** on the
page, and the note is on disk the moment it is typed — nobody copies anything into a chat box.
The one thing the page cannot do is start a round, so read `pending` when the user asks you to look.

Below, `$S` is this skill's directory (the parent of `SKILL.md`). Run the commands from
the project root.

## Content contract

**What the page must achieve — understandable answers, lossless notes — is the contract in
[references/communication-contract.md](references/communication-contract.md).** This section is
how to satisfy it. `<topic>.content.html` is a flat list of top-level sections:

**The page is an article, not a chat.** Every canvas answers in this order: **abstract** (the point
in ~3 lines, in the first section, never folded) → **detail** → **evidence** (a view under every
claim that can carry one) → **key** (one marked takeaway per section). Accurate, concrete, short.
That shape is what tells a canvas apart from `/annotate`: annotate **answers a question** and the
artifact is the response; canvas **explains a response** the user has to understand, in depth.

**It is about the subject, never about the round.** The page reads as one current document on one
topic — no round numbers, no “changed this round” in the content. Rounds are your bookkeeping: they
live in the Conversation panel and `history.jsonl`. A page that has to be read in round order is not
an article.

**Resolution.** Every claim the user might dispute is its own `data-anchor` — a sentence, a table
row, a bar, a diagram box — so a note never has to say “somewhere in this section”. Fewer words,
more diagram: prose carries the abstract, the lead and the takeaway; everything else gets a view.

A section is `<section data-section="sN">` with a `data-anchor` on everything the user might
disagree with; `show` prints them and [references/graphics.md](references/graphics.md) has the
markup for each view.

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
- **[graphics.md](references/graphics.md)** — the section skeleton, views, mermaid, measured
  costs, failure modes. Read before rendering content.
- **[manual.md](references/manual.md)** — command map, lifecycle, topic files, symptom→cause→fix
  playbooks. Read when running a command or when something is wrong.
- **[architecture.md](references/architecture.md)** — locked (`specs:locked:`): what the daemon is
  for and what it deliberately is not. Read before changing how it serves or stores.
- **[design-system.md](references/design-system.md)** — locked visuals: palette, type, spacing,
  component states, with contrast evidence. Read before changing any canvas visual.

## Who does what — you run the round

Two roles, and only one of them authors anything:

| role | who | does |
|---|---|---|
| **the session** | the agent the user is talking to — you | opens/archives canvases, authors content, runs the round steps below, answers in chat |
| **the daemon** | `canvas.py serve` | serves the page, stores every note on disk the moment it is typed, hot-swaps changed sections. **Never edits content.** |

There is no coordinator and no worker: **the round runs in your session, when the user asks.**
Nothing wakes you, so there is no delivery step but the user saying "check the canvas". A round
costs *your* context, which is why `pending` names the sections and `show` reads only those.

**One writer at a time is just a rule about you.** Nothing else writes `content.html`, so do not
run a second session on one topic, or hand it to a subagent while a round is in flight.

## Where topics live

A topic is `<project>/.agents/canvas/<topic>/` — `DEFAULT_ROOT` is the **relative** `.agents/canvas`,
so it lives with the project it documents and is one archivable, copyable, deletable unit. Roots
are named explicitly (`--root`); there is no index file and no shared state, so two projects cannot
see or break each other (two roots means two daemons and two ports). `.agents/canvas/` is gitignored.

**Files are the contract.** `say`, `ack`, `pending`, `flag`, `versions`, `restore` need no daemon,
so any agent that knows the path can take a topic over cold. The daemon is a page server and
nothing more — why is locked in [references/architecture.md](references/architecture.md).

## Open a canvas

```bash
python3 $S/scripts/build-canvas.py <topic> --new        # first time: creates content + shell
python3 $S/scripts/canvas.py start --root .agents/canvas
python3 $S/scripts/canvas.py open <topic>               # opens a browser tab — ONCE per topic
python3 $S/scripts/canvas.py stop                       # when the session ends
```

`start` opens nothing; it prints the origin, and the port is picked once and kept across restarts.
`open` is the only command that opens a tab, and one per topic is enough: the page hot-swaps its
own sections, so it never needs reopening or reloading — **never re-open after a restart or on a
round**. Show the URL to the user once. Rendered diagrams need mermaid vendored once:
`curl -sL -o $S/assets/mermaid.min.js https://cdn.jsdelivr.net/npm/mermaid@11/dist/mermaid.min.js`

## Seeing what is waiting

`python3 $S/scripts/canvas.py list --root .agents/canvas` — every topic, and notes pending /
unsent / flagged. The daemon's origin serves a plain topic index and nothing else. Both read only
files, so neither changes on a restart and neither has a "working" state.

## The round loop

One round is the unit of work. **Nothing starts one but you** — a round begins when the user
asks, or when you see notes waiting and say what you are doing.

1. **Read the batch.** One line per note — section, id, state, comment — which is everything
   the round needs and nothing else:
   ```bash
   python3 $S/scripts/canvas.py pending <topic> --root .agents/canvas
   ```
   ```
   s1     claim-a     c1·important           the ordering is reversed in step 3
   s1     claim-b     c2·critical FLAGGED    needs a product decision
   s3     cmp.its-shape  c5·important         this row is wrong
   --- 3 in this batch · 1 flagged (yours to decide) · 0 orphan
   --- sections to touch: s1, s3
   ```
   - **The `sN` is where the note lives** — read and edit that section only. Anchors that a
     `data-render` table or a mermaid diagram builds in the browser resolve to their container, so
     they map too.
   - **`FLAGGED`** needs a decision you must not make — leave it, and `flag` it if it is not
     flagged yet (step 6).
   - **`ORPHAN`** means the anchor is in no section: the element it was written on is gone (a
     chrome control that was removed, say). Say so in the conclusion and `flag` it — never let a
     note disappear without a word.
   - **`HELD`** lines are notes typed after the last Send. They are *not* in this batch: the user
     is still writing them, and resolving one now gets un-done by their next keystroke. Leave
     them and say how many are waiting.
   - Nothing listed at all → say so and stop.
2. **Read what they actually meant** — and **only the sections in play**, because the whole file
   is the expensive thing to read:
   ```bash
   python3 $S/scripts/canvas.py show <topic> s3 s7 --root .agents/canvas   # just those sections
   python3 $S/scripts/canvas.py show <topic> --root .agents/canvas         # or the index
   ```
   Then edit those sections of `.agents/canvas/<topic>/content.html`. Fix notes landing on the
   same section together, and bring in the views from
   [references/graphics.md](references/graphics.md) — the smallest view that makes the point.
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
   python3 $S/scripts/canvas.py say <topic> "the section now shows the real data flow — next: the failure path when the daemon is down"
   ```
   Every round ends with **what is now true** plus **one suggested next move**; "§3 rewritten"
   is a progress note, not a conclusion. Write the same line into the page's conclusion block
   (`data-anchor="run-conclusion"`, in the first section) so it is visible on the topic page
   without opening Conversation, and keep the chat message to that same one line — the page is
   the reply, chat is the nudge.

   **Then read the sections you changed back as the user would.** If the fix is a sentence of
   prose where a pseudocode block, a tree or a diagram would carry the point, replace it — that
   is the job, not an extra.
6. **Escalate instead of inventing.** A note that needs a decision you may not make gets a flag,
   not a guess — leave it unresolved so the user still sees it:
   ```bash
   python3 $S/scripts/canvas.py flag <topic> --ids <id> --note "<what you need decided>"
   python3 $S/scripts/canvas.py say <topic> "QUESTION: <what you need decided>"
   ```
   **Every note must end acked or flagged.** Never guess a product, scope or design decision, and
   never edit around the chrome — `chrome.css`/`chrome.js` are shared by every canvas and locked
   in [references/design-system.md](references/design-system.md).

## A canvas communicates; it does not execute

The page is the **medium, not the actor**. A round's job is to make something understood and to
record it, so a round edits `<topic>/content.html` and nothing else. That is a boundary on the
*repository*, never on the page — explaining more, adding the view that makes a section land, is
the work. Editing the project is not.

- **In a round the only file that changes is `<topic>/content.html`** (plus the shell
  `build-canvas.py` regenerates) and the topic's own history. No project code, no skill files,
  no other topic, and **no `git commit`**.
- **A note that asks for a code change is recorded as a decision, not implemented.** Write what
  should change, why, and the acceptance test — then `ack` it. The change itself belongs to the
  handoff, not the round.
- **Implementation starts only after the user reviews the conclusion.** Hand it to a worker with
  its own checkout: `/task-agent start` for a worktree-backed task (the path for anything
  non-trivial), or `/dev-task` for a small in-repo change. The conclusion is the task card:
  problem, decision, acceptance criteria, and what must not change.
- **Never let a round "just fix it".** A round that edits the repo and commits while the page
  still says *proposed* shows the user a fait accompli instead of a decision to review.

## The record

`history.jsonl` is append-only: every note, resolve, delete and content change, plus your `say`
lines. **Rounds are derived, not stored** — a round is the events since your previous `say` — so
nothing is renumbered and a lost write cannot corrupt the past. `build-canvas.py` refuses
duplicate anchors, nested sections, malformed spec JSON and unknown `data-render` kinds; fix
what it reports. Detail in [references/manual.md](references/manual.md).

## Rules

- **Never ask the user to paste their notes into chat.** That is the friction this skill
  exists to remove. Every note is on disk the moment it is typed — read it with
  `canvas.py pending <topic>`.
- **Nothing wakes you: never claim a round is coming.** The page stores a note but cannot start
  a round, so a Send is only a signal. Read `pending` when the user asks, and do not tell them
  work is in progress because a Send happened.
- Keep chat near-silent. The page is the reply; a paragraph in chat duplicating it is paid
  for twice.
- Never rewrite the whole canvas when a section changed. Re-emitting unchanged sections is
  the single most expensive mistake in this workflow.
- Never renumber or rename anchors, and do not nest sections.
- **A canvas communicates; it does not execute.** A round edits `<topic>/content.html` and
  nothing else — no project code, no commits. Explaining more is the work; editing the repo is
  not. A code change it records is a reviewed handoff (`/task-agent` or `/dev-task`).
- Resolve notes with `ack` once addressed — resolved notes stay visible and greyed, so the
  user can see you did not drop them.
- **A round is not done until the page is understood.** Answering a note by explaining less —
  a terse "fixed", a claim with no view under it — is a failed round. The smallest view that
  makes the point beats a paragraph, and a paragraph beats silence.
- **Keep the article shape.** The abstract goes first and stays unfolded; one `.key` per section;
  a wall of prose is a failure of the round, not a style choice.
- **Order by subject, not by round.** Do not number rounds in the content or write “changed this
  round” into a section — that is what the Conversation panel is for. The page must read correctly
  to someone who never saw an earlier version of it.
- **A topic is one problem, and it is temporary.** It exists to make that problem clear and to
  raise the bandwidth between you and the user — a fragment of a conversation, not a record of one.
  When the problem changes, start a new topic. When it is clear, `canvas.py drop <topic>` — that is
  the ordinary ending, and a topic kept just in case is the failure. Anything worth keeping leaves
  first, as a document or a task committed with `/task-agent` or `/dev-task`; nothing survives a drop.
- The daemon binds `127.0.0.1` only and serves from `.agents/canvas/`; it is not a general
  web server. Do not point it at a repo the user has not asked you to expose.
- Never overwrite `.agents/canvas/<topic>.feedback.json` — the daemon owns it, and clobbering
  it destroys notes the user has not sent you yet.
