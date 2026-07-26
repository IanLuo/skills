---
name: prd
description: Interactively grill the user, one decision at a time, to produce and LOCK a durable PRD, system-design, or architecture doc that stays frozen until re-opened with this same skill. On re-entry, show the user the locked doc and re-grill only what they say changed. The lock writes a link contract — upstream it depends on, referrers that must cite it — stamped at the top of the doc. Use for "write a PRD", "spec the system before building", "lock the requirements", "update the architecture doc", "what's our system design", "re-open the PRD". Do NOT use for batch/auto-surveying a repo into an index (use init-context), for one-shot code research that won't be locked, for visual/product design artifacts (use design-task), or for reviewing whether code matches a doc (use review-task).
metadata:
  audience: personal
  domain: specification
---

# prd

An interactive interrogation skill. You grill the user to lock durable context docs;
their contents survive as frozen inputs until the user re-opens one with this skill. The
defining method is the dialogue — relentless, ranked, one decision at a time — not batch
survey.

Three doc types, one skill. Pick or infer the type, then run the ritual.

| `--type` | question ladder | contract defaults |
|---|---|---|
| `prd` | [references/prd.md](references/prd.md) | upstream none; referrers architecture, system-design, design-system |
| `system-design` | [references/system-design.md](references/system-design.md) | upstream PRD; referrers architecture, design-system |
| `architecture` | [references/architecture.md](references/architecture.md) | upstream PRD + system-design; referrers dev/design tasks |

## Ritual

### 1. Pick or infer the type

Explicit `--type` arg, or infer from the request: "requirements/scope/acceptance" → `prd`,
"components/interfaces/data" → `system-design`, "structure/layers/repo tree" →
architecture. Confirm the inferred type with the user before grilling.

If the doc already has a lock marker (re-entry): show the user the locked doc (marker +
contract + body), say "locked at `<sha>` on `<date>`," and ask what changed. Re-grill
**only** the rungs whose answers the reported changes would alter. If they say "nothing
changed," you're done — stop.

### 2. Grill + draft, one rung at a time

Read the ladder for the type. Ask **one rung at a time**, lead with the load-bearing
decision a fresh agent would get *wrong* without it. Follow up on empty/edge/assumption
gaps — the same edge-case discipline `grill` uses, aimed *upward* to drive a spec out.

The answer-clears gate is [references/judging.md](references/judging.md). Don't advance
to the next rung until the answer is: not recoverable by one command/read, something a
fresh agent would get *wrong* (not slow) without, and its empty/edge cases handled or
parked. As each rung clears, draft that section, show it, get explicit accept — markdown
ready to live in the doc body, concrete, no hedging.

### 3. Lock

When the whole ladder **plus** the contract rung clears, run:

```bash
../prd/scripts/lock.sh <doc> --type <t> \
    --upstream <comma docs> --referrers <comma docs> [--force]
```

`--force` rotates an existing lock to the current sha + date — that's the update path.
Without `--force` an already-locked doc refuses (exit 1) so you can't clobber a frozen
doc by accident. The marker + contract are written by the script, never by hand — the
exact shape is what `review-task` greps.

Then register the doc so consumers find it:

```bash
../task-core/scripts/register-doc add --path <doc> \
    --cue "<type-derived cue>" --task <current goal id>
```

Type→cue defaults: `prd` → "product intent, scope, acceptance criteria, non-goals";
`system-design` → "system design — components, data model, interfaces, failure modes";
`architecture` → "load-bearing structure, layers, boundaries, rationale". The goal id
is the current `CURSOR.md` goal (its `synced` sha or Position) — read it or pass what
the orchestrator is tracking. This is the one place prd writes outside the doc: a single
row in `AGENTS.md`'s Deeper-docs table, so dev-task / design-task / review-task read it.

### 4. Hand off

Tell the user: the doc is locked at `<sha>` and registered in `AGENTS.md`'s Deeper-docs
table, and `review-task` is the skill to check later ("did the code do what this locked
doc asked?"). For architecture/system-design, point at any ADRs the user may want next.

## Delegation

- **No subagents by default.** The core is the interactive loop with the user — delegating
  the grilling defeats it. The orchestrator that loaded this skill owns the dialogue, the
  contract rung, the lock, and the handoff.
- **Subagents only for grounding**, one at a time, scoped to the paths a given question
  touches: "you locked X at `<sha>`; code at Y now does Z — which governs?" A read-and-
  summarize subagent, not a divergent one.

## Rules

- **Lock = frozen input.** Once `lock.sh` writes the marker, treat the doc as a decision
  a fresh agent must obey, not re-litigate. Changing it requires the user re-run this skill
  with `--force` — never hand-edit the body or marker.
- **Locked doc ≠ a log.** Re-grilled sections replace their predecessors in place; the lock
  marker + sha are the only history. Never append a "v2" after a "v1".
- **Contract before body.** The contract rung gates the lock — `upstream` must be `none`
  or name locked docs that exist; `referrers` are confirmed with the user, not guessed.
- **The model is smart.** Add only what it can't derive — concrete frozen decisions, not
  recoverable boilerplate. Prefer pointers to existing docs over restating them.
- **What this skill owns:** the grilling + the lock. **Not:** the index (init-context),
  the implementation (dev-task), the visual artifact (design-task), or the drift-vs-code
  review (review-task).