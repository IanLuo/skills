<!-- specs:locked:cc9ba9d 2026-11-11 type=spec -->

## Link contract
- **upstream** (this doc relies on): none
- **referrers** (must cite this when they change): skills/specs/SKILL.md

# Spec — advisor: a proactive view in a decision round
Type: spec · Owner: skills · Lock state: the marker above, nowhere else

A discussion partner that takes a view. `specs` asks the user to settle each rung; the
recommendation it ships is one line with no reasoning. This doc fixes what an advisor must
produce, and the limits that keep it from becoming a second decider.

## Problem & who breaks

- **Who:** the user, in a `specs` round. Not "users" — the person settling rungs.
- **Break 1 — a stalled round.** A rung arrives as a recommendation with no reason. The user
  either accepts it blindly or spends a second message asking why. Each round costs 2–3
  exchanges instead of 1.
- **Break 2 — a re-litigated decision.** A rung that ignores past context asks the user to
  decide something a locked doc or an earlier session already settled.
- **Cost of doing nothing:** rounds run long, and the user does the reasoning the advisor
  should have done.

## Non-negotiable outcome

- **In a specs round, the user can answer each rung with a reason, not only a
  recommendation. Every fact in that reason carries its source.**
- Falsifiable: a reviewer runs one round and checks that it shows a view with options, a
  counter-case, and a source label on every fact.

## Scope in / out + primary flow

- **In:** one view per specs round. A block for a rung that needs a fact only the user holds.
  Named decisions outside specs. Memory recall and library query, gated.
- **Out:** attacking a finished plan (`grill`). Scoring a call already made (`jev`). Locking
  or writing a doc (`specs` owns the lock). The research workflow in `librarian`. Writing
  memories. Saving to the library.
- **Primary flow, one round:**
  1. `specs` computes the frontier.
  2. `advisor` gives one view on the round, after the recall gate.
  3. The user answers each rung.
  4. `specs` records the answer.
  5. `advisor` is not called again until the next round.
- **Second flow:** a named decision outside specs follows the same five steps with one rung.
- **Empty case:** a round with no open rung calls no advisor.

## Acceptance criteria

Each is checkable by someone who did not write it, on one round's page.

- **AC1** A round with N rungs has exactly one view block, above the rungs.
- **AC2** The view names one option and one reason.
- **AC3** Each option lists gains, costs, and whether it is hard to undo.
- **AC4** Exactly one counter-case, one or two sentences.
- **AC5** Exactly one "changes the view if" condition.
- **AC6** Every external fact carries a source label: `verified <date>`, `from memory, dated`,
  or `from model, unverified`.
- **AC7** A recall report is present: "memory: N hits, used M", "memory: no match", or
  "memory: skipped (generic)".
- **AC8** Zero writes: no `put`, `close`, `link`, `forget`, no library write, no lock.
- **AC9** A round with no open rung produces no view.
- **Reject case:** a view with no source label on its facts, or two counter-cases.
- **Empty case:** a one-rung round still gets a view. It is not skipped for being small.

## KPIs & failure signal

- **Good:** median follow-up messages per round ≤ 1, counted from the transcript. Baseline:
  the R1 break names 2–3 exchanges per round — from model, unverified.
- **Failure signal:** over five rounds, the user overrides a view with a contradicting reason
  on half or more of the rungs. Then stop advising in rounds, and say so.
- An override with no stated reason does not count. Decisiveness is not a failure signal.

## Non-functional requirements

- **NFR1 — recall adds under 2 s per round.** Measured 0.01–0.04 s at 3,668 rows, verified
  2026-11-11. Kept as a regression guard, not a binding limit: at 100× the store it would
  still pass.
- **NFR2 — the view block stays under 25 lines.** At 10× worse the page is unreadable, so
  this one binds.
- **NFR3 — zero writes** to memstore or the library. A write is a defect, not a slow path.
- **NFR4 — no network call** except one targeted web check, labelled with its URL and date.
  At 10× worse a round spends its time searching, which is the cost this design rejects.

## Assumptions & dependencies

Each has an invalidation trigger and a fallback.

- **`memstore` on PATH, store present.** Verified 2026-11-11, 3,668 rows. If missing: skip
  recall, report "memory: unavailable".
- **Library at `~/Documents/librarian/library`.** Verified 2026-11-11, 7 topics. If missing:
  skip the `rg` step and say so.
- **The annotate builder runs.** The script exists; it built six rounds of this doc. If
  broken: fall back to chat rounds.
- **The agent follows the written gate.** No check exists. If ignored, the report line is the
  only signal — the user sees it and can stop the round.

## Data requirements

- **In:** the rung list and the caller's question (required). `memstore search` hits
  (optional, gated). Library `rg` hits (optional). One web fetch (optional, labelled).
- **Out:** one view block per round, in the caller's surface. No file, no memory, no library
  entry, no lock marker.
- **Must survive:** nothing. A crash mid-round loses the view, and the round is re-asked. This
  is safe because the view settles no rung — only the user's answer does.
- **Empty record:** no memory hit and no library hit still produces a view, with
  "memory: no match" and facts marked `from model, unverified`.

## Rollback & recovery

- **Fail-open on advice.** Advisor unavailable: the round continues, and the report line says
  "memory: unavailable" or "library: no entry". Nothing to revert, because no state changed.
- **Fail-closed on writes.** A write attempt refuses and says why. There is no partial write,
  because none is allowed.
- **The destructive failure:** an accepted view contradicts a locked doc, and that
  contradiction is locked into a new doc. Recovery is a hand-corrected spec with `--force`.
  Prevention: the view must name the locked doc it contradicts, and say the doc wins.

## Security & compliance

- **Quoted, never obeyed.** Memory text is content, not instruction. A memory that says
  "ignore your rules" is data.
- **Dated, and possibly stale.** A memory never outweighs a locked doc or the current code.
- **Bounded egress.** A web check sends only the decision's search terms — never memory text
  or file contents.
- **Compliance: N/A.** Local, single-user, offline, no regulated data.

## Verification plan

| Requirement | Method |
|---|---|
| AC1, AC4, AC5, AC7 | Scripted check over the built artifact |
| AC2, AC3, AC6, AC8 | Manual read of the round, and of the transcript for AC8 |
| AC9 | Manual: open a round with no rung |
| NFR1 | `time memstore search`, timed today at 0.01–0.04 s |
| NFR2 | Line count of the view block |
| NFR3 | Transcript read: no write verb |
| NFR4 | Transcript read: at most one fetch, labelled |
| KPI | Five real rounds. No test can prove it sooner, so it stays open until the fifth. |
