---
name: prd
description: Interactively elicit decisions, one rung at a time, and LOCK them into a durable PRD, system-design, or architecture doc. On re-entry, show the locked doc and re-elicit only what the user says changed. The lock stamps a contract — upstream it depends on, referrers that must cite it — at the top of the doc. Use for "write a PRD", "spec the system before building", "lock the requirements", "update the architecture doc", "what's our system design", "re-open the PRD". Do NOT use for batch/auto-surveying a repo into an index (use init-context), for one-shot code research, for visual/product design artifacts (use design-task), or for reviewing whether code matches a doc (use review-task).
metadata:
  audience: personal
  domain: specification
---

# prd

An interactive elicitation skill. The `grill` skill provides the critical-thinking
pressure; this skill structures the answers into locked durable docs. Contents survive
as frozen inputs until the user re-opens one with this skill.

Three doc types, one skill. Pick or infer the type:

| `--type` | question ladder | contract defaults |
|---|---|---|
| `prd` | [references/prd.md](references/prd.md) | upstream none; referrers architecture, system-design, design-system |
| `system-design` | [references/system-design.md](references/system-design.md) | upstream PRD; referrers architecture, design-system |
| `architecture` | [references/architecture.md](references/architecture.md) | upstream PRD + system-design; referrers dev/design tasks |

## Ritual

### 1. Pick or infer the type

Explicit `--type` arg, or infer from the request: "requirements/scope/acceptance" → `prd`,
"components/interfaces/data" → `system-design`, "structure/layers/repo tree" →
architecture. Confirm the inferred type with the user before starting.

If the doc already has a lock marker (re-entry): show the user the locked doc, say
"locked at `<sha>` on `<date>`," and ask what changed. Re-elicit **only** the rungs
whose answers the reported changes would alter. If nothing changed, stop.

### 2. Elicit + draft, one rung at a time

Read the ladder for the type. Ask **one rung at a time**, lead with the load-bearing
decision a fresh agent would get *wrong* without it. Apply `grill`'s edge-case discipline
(pressure-test empty inputs, failure modes, unstated assumptions).

The answer-clears gate is [references/judging.md](references/judging.md). Don't advance
until the answer is: not recoverable by one command/read, something a fresh agent would
get *wrong* (not slow) without, and its empty/edge cases handled or parked. As each rung
clears, draft that section, show it, get explicit accept — concrete, no hedging.

### 3. Lock

When the whole ladder **plus** the contract rung clears, run:

```bash
../prd/scripts/lock.sh <doc> --type <t> \
    --upstream <comma docs> --referrers <comma docs> [--force]
```

`--force` rotates an existing lock to the current sha + date. Without it an
already-locked doc refuses so you can't clobber a frozen doc by accident.

### 4. Hand off

Tell the user: the doc is locked at `<sha>`. `review-task` is the skill to check
later ("did the code do what this locked doc asked?"). Consumer skills discover this
doc by grepping `<!-- prd:locked:` on disk. Append to SESSION.md:
`<date> · prd · locked <type> doc at <path>. Next: choose the first implementation task.`

## Rules

- **Lock = frozen input.** Once `lock.sh` writes the marker, the doc is a decision a
  fresh agent must obey, not re-litigate. Changing it requires the user re-run this
  skill with `--force` — never hand-edit the body or marker.
- **Contract before body.** The contract rung gates the lock — `upstream` must be
  `none` or name locked docs that exist; `referrers` are confirmed with the user.
- Add only what a fresh agent can't derive — concrete frozen decisions, not recoverable
  boilerplate. Prefer pointers to existing docs over restating them.
- **What this skill owns:** the elicitation ritual + the lock. **Not:** the index (init-context),
  the implementation (dev-task), the visual artifact (design-task), or the drift-vs-code
  review (review-task).
