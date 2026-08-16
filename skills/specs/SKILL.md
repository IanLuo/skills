---
name: specs
description: Interactively elicit decisions as a decision tree — frontier rounds, each with a recommended answer — and LOCK them into a durable formal spec, system-design, or architecture doc. The spec is the complete requirements contract (problem, outcome, scope+flow, acceptance criteria, KPIs, NFRs, assumptions, data, rollback, security, verification — IEEE 29148 §9.6 shape). On re-entry, show the locked doc and re-elicit only what changed. The lock stamps a contract (upstream/referrers) at the top. Use for "write a spec", "spec this task before building", "lock the requirements", "what are the NFRs / assumptions / data requirements", "update the architecture doc", "what's our system design", "re-open the spec". Do NOT use for repo indexing (init-context), one-shot research, visual design (design-task), or doc-vs-code review (review-task).
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
- Wait for answers, recompute the frontier (settled answers unblock downstream rungs),
  ask the next round.

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

`--force` rotates an existing lock to the current sha + date. Without it an
already-locked doc refuses so you can't clobber a frozen doc by accident.

### 4. Hand off

Tell the user: the doc is locked at `<sha>`. `review-task` is the skill to check
later ("did the code do what this locked doc asked?"). Consumer skills discover this
doc by grepping `<!-- specs:locked:` on disk. Append to SESSION.md:
`<date> · specs · locked <type> doc at <path>. Next: choose the first implementation task.`

## Rules

- **Lock = frozen input.** Once `lock.sh` writes the marker, the doc is a decision a
  fresh agent must obey, not re-litigate. Changing it requires the user re-run this
  skill with `--force` — never hand-edit the body or marker.
- **Contract before body.** The contract rung gates the lock — `upstream` must be
  `none` or name locked docs that exist; `referrers` are confirmed with the user.
- **Recommended answer on every rung.** Give `➡️ your recommendation` so the user can
  say yes/no — never an open-ended question without a position to push back on.
- **Facts are your job.** Dispatch a sub-agent (`delegate`/Explore) for anything
  lookup-able; never ask the user to recite their own codebase.
- **Tree order, checklist completeness.** The frontier decides *when* a rung is asked;
  the checklist decides *that* all rungs are settled. Neither excuses skipping a rung.
- Add only what a fresh agent can't derive — concrete frozen decisions, not recoverable
  boilerplate. Prefer pointers to existing docs over restating them.
- **What this skill owns:** the elicitation ritual + the lock. **Not:** the index (init-context),
  the implementation (dev-task), the visual artifact (design-task), or the drift-vs-code
  review (review-task). Task-level breakdown is dev-task's job — this skill stops at the
  complete requirements-spec.
