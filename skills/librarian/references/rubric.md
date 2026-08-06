# Scoring rubric

Read this when running the **Research workflow** — it defines the three scoring
dimensions and the cross-scoring protocol. You (the orchestrator) make the
accept/refine/stop call; there is no gate script.

## The three dimensions

Every finding is scored on three dimensions, each 1–5. Scoring is *adversarial*: a
finding's own author never scores it; sibling subagents do.

| dimension | question | 1 | 5 |
|---|---|---|---|
| **source quality** | Is the origin authoritative/primary? | blog spam, no cite | official docs, peer-reviewed, primary source |
| **claim specificity** | Concrete vs hand-wavy? | "often helps", vague | exact numbers, names, versions, mechanisms |
| **gap coverage** | Does it answer the angle it was assigned? | off-topic or shallow | fully addresses the sub-question |

The binding number for a finding is the **floor** — the minimum of its three dimension
means (mean each dimension across the ≥2 sibling scorers, then take the min of those
three means). A finding must clear *all* dimensions, not just the average. A 5/5/2 is a
refine, not an accept.

## Cross-scoring protocol

On each round:

1. Have each finding scored by **≥2 sibling subagents** that did *not* author it. Average
   per dimension into a mean.
2. **Decide:** accept (floor ≥ threshold), refine (below threshold, rounds remain),
   or low-confidence (below threshold at the cap).
3. If refined: re-dispatch that angle via the Agent tool with the specific feedback
   ("below 4 on claim_specificity"). Re-score. Loop.

## Stopping rules

Stop at the **first** of:

1. **Threshold met** — every finding's floor ≥ the depth's accept threshold → done.
2. **Plateau** — a full scoring round moved no dimension mean by anything noticeable.
   Stop even if some findings are below threshold. A stuck loop is making no progress.
3. **Hard cap** — the depth's max rounds reached. Any finding still below threshold is
   accepted but tagged `confidence: low`.

You enforce all three. There is no scenario in which the loop runs forever — the cap is
absolute; never extend it "to be sure."

## The meaning of confidence

The entry's `confidence` frontmatter field (you pass it to `write-entry.py`):

- **high** — all findings cleared threshold.
- **medium** — a minority of findings `low-confidence` at the cap.
- **low** — a majority `low-confidence`. Honest: the entry is your current best, with
  gaps named in `## Open questions`.

`low` is not a failure — it is a durable record that this angle couldn't be sourced at
the requested depth in the rounds available. The user can re-run that angle at `study`
later.