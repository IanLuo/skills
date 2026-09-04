# Scoring rubric

Read this when running the **Research workflow** — it defines the three scoring
dimensions and the self-scoring protocol. Research agents self-assess; you (the
orchestrator) review and adjust, then make the accept/low-confidence call.

SKILL.md's Research step 2 carries the compressed paste-brief that goes into each
subagent prompt; this file is the full wording for your own review — keep the three
dimensions in sync across both.

## The three dimensions

Every finding is scored on three dimensions, each 1–5. Each research agent self-scores
its own finding; you verify when reviewing.

| dimension | question | 1 | 5 |
|---|---|---|---|
| **source_quality** | Is the origin authoritative/primary? | blog spam, no cite | official docs, peer-reviewed, primary source |
| **claim_specificity** | Concrete vs hand-wavy? | "often helps", vague | exact numbers, names, versions, mechanisms |
| **gap_coverage** | Does it answer the angle it was assigned? | off-topic or shallow | fully addresses the sub-question |

The binding number for a finding is the **floor** — the minimum of its three dimension
scores. A finding must clear *all* dimensions, not just the average. A 5/5/2 is a
refine, not an accept.

## Self-scoring protocol

1. Each research agent includes a one-line self-assessment in its structured output:
   `self: source_quality=N claim_specificity=N gap_coverage=N`, each 1–5.
2. You (the orchestrator) read each finding and verify the self-scores against the
   rubric. Adjust any score that looks inflated or missed. This takes seconds — do not
   spawn scoring subagents.

## Accept / low-confidence

One pass plus at most ONE targeted follow-up — no refinement loop, no re-fan-out (see
SKILL.md Research step 4 for the follow-up trigger and what to hand the agent):

- **floor ≥ accept threshold** → accept. Threshold is 3 for `skim`, 4 for `read`/`study`.
- **floor < threshold after the follow-up** → admit as-is, tagged `confidence: low`.

Do not re-dispatch a finding a second time, and never re-fan-out all angles. The user
can re-run the angle at `study` depth later if they want deeper coverage.

## The meaning of confidence

The entry's `confidence` frontmatter field (you pass it to `write-entry.py`):

- **high** — all findings cleared threshold.
- **medium** — a minority of findings `low-confidence`.
- **low** — a majority `low-confidence`. Honest: the entry is your current best, with
  gaps named in `## Open questions`.

`low` is not a failure — it is a durable record that this angle couldn't be sourced at
the requested depth. The user can re-run that angle at `study` later.
