<!-- specs:locked:f91c65f 2026-09-16 type=spec -->

## Link contract
- **upstream** (this doc relies on): none
- **referrers** (must cite this when they change): skills/canvas/SKILL.md, skills/canvas/references/graphics.md, skills/canvas/references/manual.md

# Communication contract — what a canvas is for
Scope: the canvas **page as a medium** — what makes an agent's answer understandable and a
user's note lossless. Does NOT govern the loop (liveness, dispatch, ownership → `architecture.md`),
the chrome (`design-system.md`), or the content primitives (`graphics.md`).

Obeys `../skill-man/references/doc-format.md`: bullets, fixed headers, explicit `N/A`.

## R1 · Problem & who breaks

- **The agent's answer is hard to understand.** A chat reply is a stream the user has to parse,
  hold, and re-read; a long one buries the one live decision in prose.
- **The user's feedback loses information.** Typed into chat, "this is wrong" goes stale on the
  next message and never says *which* claim it was about — so the agent guesses, or asks again.
- **Who breaks:** the user. They re-derive what the agent meant and re-state what they want, and
  the agent answers a paraphrase instead of the objection.
- **Do nothing:** the answer is checked by re-reading chat; feedback degrades to intent.

## R2 · Non-negotiable outcome

- A reader who was not in the conversation opens the page and answers **what changed, what is now
  true, and what is being decided** by scanning it — no prose wall, nothing to ask, chat not opened.
- Falsifiable: a fresh agent is given the page and nothing else, and must produce those three
  answers. It either can or it cannot.
- **Readability is the test, and it outranks every other rule in this document.** Where a rule
  below and the reader's comprehension disagree, comprehension wins. Two shapes do most of the work:
  - **A diagram over prose.** Anything with parts, order, state or flow is a view, not a paragraph.
    A concept explained in a wall of text was not explained.
  - **Short sentences in plain words.** One idea per sentence. No clause stacking, no jargon the
    user has not used, no sentence that has to be read twice. A sentence you must re-read fails,
    however accurate it is.
- **It is checked, not assumed.** `verify-canvas.py` reports every section's prose and view count
  on each run, and warns on a sentence over 45 words and on a section of 200+ words with no view.
  The threshold is a view, not a word count: the same words with a diagram under them are fine.
- **The page is an article, not a chat.** It answers in this order, every round: **abstract** (the
  point in ~3 lines, first, never folded) → **detail** → **evidence** (a view under every claim that
  can carry one) → **key** (one marked takeaway per section). Accurate, concrete, short.
- **The page is about the subject, never about the round.** It reads as one current document on one
  topic — no round numbers, no "changed this round" framing, no section that exists only to report
  process. Rounds are the agent's bookkeeping and live in the Conversation panel and
  `history.jsonl`. A page that has to be read in round order is not a document.
- **Resolution: the user points at the thing, not at the area.** Every claim that can be disputed is
  its own anchored element — a sentence, a table cell, a caption, a bar, a diagram box — so a note
  never has to say "somewhere in this section". A section-level note is a resolution failure, not
  user error. **The target is the smallest real element that can carry an id.**
- **Anchors, never coordinates.** A click resolves through `elementFromPoint` to the smallest
  `[data-anchor]` under it, and the note stores that anchor. A stored `(x, y)` is rejected: it would
  mean something else after any change above it, so the note would silently point at the wrong
  thing — worse than a coarse note, because nothing announces it. Coordinates are a fine *input*,
  a terrible *storage*.
- **Where the granularity comes from.** A shape rendered from data (`data-render`) is anchored per
  row; a table whose *cells* must be pointable is written by hand with `data-anchor` on the cells.
  Finer targets are a habit the author keeps, not a feature a renderer supplies.
- **Grow resolution by putting anchors on smaller real elements**, not by drawing a grid over them.
  A `container` rewrite was rejected: it makes free-form authoring harder for no gain the anchors do
  not already give.
- **Fewer words, more diagram.** Prose carries the abstract, the lead and the takeaway; everything
  else has a view. A section whose answer is a paragraph when a tree, flow or table would carry it
  fails this contract.

## R3 · Scope in / out + primary flow

- **In:** the page is the reply; smallest-view rendering; stable anchors; the run conclusion;
  lossless capture of a note (on disk, keyed to an anchor, kept after resolution); the article shape.
- **A topic is one problem, small and short-lived.** It exists to make *that* problem clear and to
  raise the bandwidth between the user and the agent — a fragment of a conversation, not a record
  of one. Topics are meant to be created and **dropped** repeatedly; a directory that is kept
  forever is the failure, not the default. When the problem changes, that is a new topic, not a
  new section.
- **Out — persistence.** Not an archive, not a second memory, not a place to accumulate. Anything
  worth keeping after the problem is solved has to leave as a document or a task before the topic
  is dropped.
- **Out — non-goals:** the chrome (→ `design-system.md`); liveness and dispatch (→ `architecture.md`);
  implementing a change (→ `/task-agent`, `/dev-task`); being a chat client (→ chat, but terse).
- **Out — the sibling boundary:** this is **not `/annotate`**. Both produce an annotatable page, so
  "has a server" and "can be annotated" do not separate them. What separates them is what the user
  is doing: `/annotate` **answers a question** and the artifact *is* the response, produced once;
  `/canvas` **explains a response** the user has to understand, in depth, across as many rounds as
  the problem takes. Depth is the differentiator — annotate delivers the answer, canvas delivers
  the detail behind it.
- **Primary flow:** the agent writes content → the user reads → annotates one element → Send →
  the round answers **in that section** → the conclusion is readable on the page.

## R4 · Acceptance criteria

- Given a finished round, the first section's `run-conclusion` states what is now true and one
  next move — readable without opening Conversation.
- Every element the user might dispute carries a stable `data-anchor`; anchors never change
  between rounds.
- A note is on disk before it is answered, and `ack` greys it rather than deleting it.
- A section that poses a question is rendered in a view (pseudocode / tree / mermaid / diff /
  `.cards`), not only in prose.
- **Everyone under `body *` is pointable at the resolution the claim deserves** — the abstract and
  the lead are sentences; repeated shapes (table rows, cards, bars, flow steps, diagram nodes) each
  carry their own anchor, derived from their own data so it cannot drift.
- `data-status` (`settled` / `new` / `open`) is an optional **fold aid**, never the page's structure:
  the content is ordered by subject, not by round.
- **The shape holds.** The first section opens with a ~3-line abstract and is never folded; every
  claim that can carry a view has one; each section has at most one `.key`; no section is a wall of
  prose. A section that is prose from top to bottom fails its own contract.
- **A note is never invisible.** `pending` reports every unresolved note with the section it lives
  in and its state. An anchor that resolves to no section is reported as an **orphan**, never
  skipped — an element can be deleted (a chrome control removed, a section rewritten) and the note
  written on it must still surface, because nothing else knows it exists.
- **A Send is the batch boundary.** A round works the notes written up to the last Send; notes
  typed after it are listed as **held**, not silently included. Resolving a note the user is still
  writing is worse than waiting — an edit re-posts the note as unresolved, so the round's own
  claim gets undone by the next keystroke. No Send at all means no boundary to apply.
- **Smallest input that must succeed:** one note on one anchor → that section re-rendered, the
  note greyed, the conclusion updated.
- **Smallest that must be refused:** a note whose anchor no longer exists → flagged and kept,
  never silently dropped.

## R5 · KPIs & failure signal

- **Good:** the user answers by *looking*; the follow-up question they would have typed is
  already answered on the page.
- **Off-switch (this whole bet is wrong):** the user re-asks in chat something the page already
  said — or a round `ack`s every note and the section is no clearer than before. That is the
  drift this contract exists to stop.

## R6 · Non-functional requirements

- Anchors are stable: renaming one orphans the user's feedback (a data-loss bug, not a cosmetic).
- `feedback.json` is written by the daemon alone; no agent overwrites it.
- The page still renders from `file://` with no daemon — comprehension degrades to
  reload-and-paste, never to blank.
- `N/A`: latency, throughput, cost — owned by `architecture.md`.

## R7 · Assumptions & dependencies

- **A session is in the loop.** Nothing wakes an agent: notes sit on disk until someone reads
  `pending`. Invalidated → the page becomes a notebook nobody opens. The fix is a nudge in chat,
  never a promise that a round is coming.
- **The user reads the page, not chat.** Invalidated → the round is written to nobody. Fall back:
  say it in chat, and treat the canvas as the record.
- **The daemon owns `feedback.json`.** Invalidated → notes the user has not sent are lost.
- **`graphics.md` supplies the view ladder.** Invalidated → everything degrades to prose, which
  is the failure this contract removes.

## R8 · Data requirements

- **Must exist:** `content.html` sections (`data-section` + stable `data-anchor`),
  `feedback.json` notes, append-only `history.jsonl`.
- **Must survive:** every note until acked; every anchor across rounds; a round's prior
  `content.html` via `versions/`.
- **Empty record:** a canvas with zero notes is normal — it is a page, not a queue.

## R9 · Rollback & recovery

- A bad round → `canvas.py versions` / `restore --to last` restores `content.html`.
- Deleting a section deletes its anchors' notes → the round must say so in chat, not drop it silently.
- **Fail-closed:** an unknown anchor keeps the note and flags it; a note is never discarded by
  the agent's judgment.

## R10 · Security & compliance

- `N/A`: the daemon binds `127.0.0.1`, no auth, no tokens, no regulated data. The trust boundary
  is stated in `architecture.md`.

## R11 · Verification plan

- `verify-canvas.py` renders the real page headlessly and fails on an unrendered diagram, a spec
  error, or a broken shell — automated.
- `build-canvas.py` rejects duplicate anchors, nested sections, malformed spec JSON and unknown
  `data-render` kinds at build time — automated.
- **The scan test (R2) is manual:** hand a fresh agent the rendered page and nothing else; it
  must answer what changed / what is true / what is being decided without opening chat.
- Anchor stability is greppable across two revisions of `content.html`.

Last reviewed: 2026-09-15 · canvas /specs
