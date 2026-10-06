---
name: specs
description: Interactively elicit decisions as a decision tree — frontier rounds, each with a recommended answer — and LOCK them into a durable formal spec, system-design, or architecture doc. The spec is the complete requirements contract (problem, outcome, scope+flow, acceptance criteria, KPIs, NFRs, assumptions, data, rollback, security, verification — IEEE 29148 §9.6 shape). On re-entry, show the locked doc and re-elicit only what changed. Many tasks at once → batch mode — settle what they share in one pass, then per-task rungs. The lock stamps a contract (upstream/referrers) at the top. Use for "write a spec", "spec this task before building", "lock the requirements", "what are the NFRs / assumptions / data requirements", "update the architecture doc", "what's our system design", "re-open the spec". Do NOT use for repo indexing (init-context), one-shot research, visual design (design-task), or doc-vs-code review (review-task).
metadata:
  audience: personal
  domain: specification
---

# specs

An interactive elicitation skill. The `grill` skill provides the critical-thinking
pressure; this skill structures the answers into locked durable docs. Contents survive
as frozen inputs until the user re-opens one with this skill.

Three doc types, one skill. Pick or infer the type:

| `--type` | question ladder | contract defaults |
|---|---|---|
| `spec` | [references/spec.md](references/spec.md) | upstream none; referrers architecture, system-design, design-system |
| `system-design` | [references/system-design.md](references/system-design.md) | upstream spec; referrers architecture, design-system |
| `architecture` | [references/architecture.md](references/architecture.md) | upstream spec + system-design; referrers dev/design tasks |

## Ritual

### 1. Pick or infer the type

Explicit `--type` arg, or infer from the request: "requirements/scope/acceptance/NFR/
assumptions/data/rollback/security" → `spec`, "components/interfaces/data" →
`system-design`, "structure/layers/repo tree" → architecture. Confirm the inferred
type with the user before starting.

If the doc already has a lock marker (re-entry): show the user the locked doc, say
"locked at `<sha>` on `<date>`," and ask what changed. Re-elicit **only** the rungs
whose answers the reported changes would alter. If nothing changed, stop.

**Many tasks (a batch) → batch mode.** Grep `<!-- specs:locked:` first. Settle the
rungs the tasks share — scope, NFRs, data assumptions — **once**, then elicit each
task's private rungs. If an upstream doc already freezes the shared answers, cite it,
never re-ask. If the batch shares scope/NFRs and no upstream exists, that is the
signal to lock the upstream (architecture / system-design) before the task specs.

### 2. Elicit + draft, as a decision tree

Read the ladder for the type. Its rungs are a **decision tree** — each rung has
prerequisites (the spec ladder maps them explicitly; shorter ladders follow their
order — each rung depends on the ones before it). Work the **frontier** in rounds:

- The **frontier** = every rung whose prerequisites are settled — the ones you can ask
  now without guessing at answers you haven't heard yet.
- **Ask the whole frontier in one round.** Number each rung and give **your recommended
  answer** (`➡️`) so the user can mostly say yes/no — they push back only where they
  disagree. Format:
  `❓ R<#> · <rung title>: <question, with concrete options> → ➡️ <your recommended answer>`
- **Render each round with `annotate`, not chat.** Load the `annotate` skill and build
  the round with `../annotate/scripts/build-artifact.py` — the artifact IS the question.
  One element per rung with a stable `data-anchor`; recommendation included. The user
  annotates the rungs on the page and pastes the feedback JSON; resolve each line back
  to its rung before recomputing the frontier. Later rounds = new artifacts built with
  `--prev`: each annotated rung carried as a `.ev.user` thread paired with your
  `.ev.agent` answer, settled rungs retired explicitly. Plain chat questions are the
  fallback only when annotate cannot run.
- **Write the round to be read, not parsed.** Content in plain, controlled English —
  the house style, about 80% of the way to ASD-STE100: short sentences, one idea each,
  active voice, no idiom, no jargon. A rung is one line of context, one concrete
  question with options, one `➡️` recommendation. Where structure helps, draw it — the
  rung tree with the frontier marked, the contract graph — as inline SVG/CSS only
  (annotate's page must stay self-contained on `file://`). Settled-vs-open is already
  visual: use annotate's `data-status` pills, not prose.
- **Batch: shared rungs once, task rungs per task.** In batch mode the shared rungs are
  their own first round — one annotate page for the batch, not one per task. Lock them
  where they belong (the upstream doc, or the first task's spec) before task rounds
  start. Each task then gets its own page with only its private rungs; a shared rung
  appears as a settled note whose answer cites the frozen doc — re-asking it is a fail.
- Wait for answers, recompute the frontier (settled answers unblock downstream rungs),
  ask the next round — on a new annotate artifact.

**Facts are your job, never the user's.** Before a rung that needs a fact from the
environment (what the codebase already does, existing data, current behavior), dispatch
a sub-agent (`delegate` / Explore) to find it — never make the user recite their repo.
An unresolved fact is an unsettled prerequisite: rungs downstream wait on it, the rest
of the frontier is asked now.

Each answer must clear the gate in [references/judging.md](references/judging.md) — not
recoverable by one command/read, something a fresh agent would get *wrong* (not slow)
without, edge cases handled or parked. As rungs clear, draft their sections, show them,
get explicit accept — concrete, no hedging.

**Completeness is the checklist, not the order.** The tree decides *when* each rung is
asked; it does not excuse any rung. All rungs must be settled before lock — a rung that
doesn't apply is locked as explicit `N/A:`.

### 3. Lock

When the whole ladder **plus** the contract rung clears, run:

```bash
../specs/scripts/lock.sh <doc> --type <t> \
    --upstream <comma docs> --referrers <comma docs> [--force]
```

`--force` rotates an existing lock to the current sha + date, and replaces a lock
marker this script did not write — another regime's (`design:locked:`), or its own
shape without the `type=` it reads. Without it both refuse, so you can't clobber a
frozen doc by accident.

### 4. Hand off

Tell the user: the doc is locked at `<sha>`. `review-task` is the skill to check
later ("did the code do what this locked doc asked?"). Consumer skills discover this
doc by grepping `<!-- specs:locked:` on disk.

## Rules

- **Lock = frozen input.** Once `lock.sh` writes the marker, the doc is a decision a
  fresh agent must obey, not re-litigate. Changing it requires the user re-run this
  skill with `--force` — never hand-edit the body or marker.
- **Contract before body.** The contract rung gates the lock — `upstream` must be
  `none` or name locked docs that exist; `referrers` are confirmed with the user.
- **Recommended answer on every rung.** Give `➡️ your recommendation` so the user can
  say yes/no — never an open-ended question without a position to push back on.
- **Grill on the page.** Every frontier round is an `annotate` artifact, not a chat
  message. Pasted feedback resolves back to its rung: "yes" or silence → settled with
  the recommendation; a correction → re-elicit that rung. An annotated-but-unanswered
  rung is unresolved — it blocks its downstream rungs like any other unsettled
  prerequisite.
- **Grill content is written to be read.** Controlled plain English, short sentences,
  no jargon; diagrams as inline SVG where structure helps; `data-status` pills carry
  the state. A reader gets the rung in one pass, not three.
- **Facts are your job.** Dispatch a sub-agent (`delegate`/Explore) for anything
  lookup-able; never ask the user to recite their own codebase.
- **Tree order, checklist completeness.** The frontier decides *when* a rung is asked;
  the checklist decides *that* all rungs are settled. Neither excuses skipping a rung.
- **Once per batch, not per task.** Shared rungs are asked once and locked where they
  belong; task rungs are asked per task. Re-asking a frozen answer — in another task or
  another session — is the fail this rule exists to prevent.
- Add only what a fresh agent can't derive — concrete frozen decisions, not recoverable
  boilerplate. Prefer pointers to existing docs over restating them.
- **What this skill owns:** the elicitation ritual + the lock. **Not:** the index (init-context),
  the implementation (dev-task), the visual artifact (design-task), or the drift-vs-code
  review (review-task). Task-level breakdown is dev-task's job — this skill stops at the
  complete requirements-spec.
