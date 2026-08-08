# Scoring rubric

Read this when running the **Research workflow** — it defines the three scoring
dimensions and the self-scoring protocol. Research agents self-assess; you (the
orchestrator) review and adjust, then make the accept/low-confidence call.

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

1. Each research agent includes a self-assessment block in its structured output:
   `source_quality: N`, `claim_specificity: N`, `gap_coverage: N`, each 1–5.
2. You (the orchestrator) read each finding and verify the self-scores against the
   rubric. Adjust any score that looks inflated or missed. This takes seconds — do not
   spawn scoring subagents.

## Accept / low-confidence

One pass, no refinement loop:

- **floor ≥ accept threshold** → accept. Threshold is 3 for `skim`, 4 for `read`/`study`.
- **floor < threshold** → admit as-is, tagged `confidence: low`.

Do not re-dispatch findings that fall below threshold. The user can re-run the angle at
`study` depth later if they want deeper coverage.

## The meaning of confidence

The entry's `confidence` frontmatter field (you pass it to `write-entry.py`):

- **high** — all findings cleared threshold.
- **medium** — a minority of findings `low-confidence`.
- **low** — a majority `low-confidence`. Honest: the entry is your current best, with
  gaps named in `## Open questions`.

`low` is not a failure — it is a durable record that this angle couldn't be sourced at
the requested depth. The user can re-run that angle at `study` later.
