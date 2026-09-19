# evidence — what was measured, and how

Everything here came from real calls to `POST /v1/systemone` with `model: "jev-latest"`
(answered by `jev-1.13.0`), through `cred run`. It is the basis for the working rules in
`SKILL.md`; it is not a general claim about the model.

## The API, from its reference

- **noul** — "A yes/no question. Returns the probability the answer is yes." A scalar
  0 (no) → 1 (yes). Optional `criteria.yes` / `criteria.no` define the two ends.
- **choice** — `criteria` maps option → rubric line; returns the argmax, the full
  probability distribution (sums to 1) and a `confidence`.
- **score** — `criteria` is an ordered array of ≥2 levels; returns a probability-weighted
  value that **can land between levels**, the per-level distribution, a `legend`, and a
  `confidence`.
- One `state` per request; `questions` is a map you key. 401 / 422 / 429 / 529 on failure
  (429 and 529 want backoff, not immediate retry).
- Cost on these probes: 541–696 input tokens, 55–100 output, per request.

## The primitives do not share a probability space

One question — *"Is this change ready to merge as it stands?"* — asked twice in a single
request, same state:

```
noul    0.21                        → P(yes) = 21%
choice  no, confidence 0.99         → probabilities {yes: 0.00, no: 1.00}
```

`choice` assigns **zero** mass where `noul` assigns a fifth. So the numbers are not a
shared probability: you cannot compare them, average them, or treat either as calibrated.
This is the measurement behind the "advisory, never a gate" rule — and it is why a
threshold on these numbers is not a threshold on anything real.

Two byte-identical `noul` questions under different keys returned **exactly** 0.23 each,
so at this granularity the answers are reproducible even though they are not consistent
with a sibling primitive.

## It discriminates, and it does not collapse to one signal

Four states × the two real gates from `~/.fs/kb/dev-task-prerequisites.yaml`, as `score`
questions with three levels (0 unmet → 2 met):

| state | `prd_locked` | `acceptance_stated` |
|---|---|---|
| well-formed (locked PRD, explicit criteria) | 2.00 · P(2)=1.00 | 2.00 · P(2)=1.00 |
| nothing (no docs at all) | 0.14 · P(0)=0.93 | 0.19 · P(0)=0.82 |
| vague (locked design doc, "should feel snappier") | 1.97 · P(2)=0.98 | 0.89 · P(1)=0.89 |
| this session (work defined in conversation) | 0.10 · P(0)=0.95 | 1.91 · P(2)=0.94 |

Spread: min 0.10, max 2.00, mean 1.15, seven distinct values in eight points — so it is
not saturated, and the one genuinely arguable case (the vague acceptance criterion) landed
**mid-scale** rather than being forced to an end. That mid-scale case is the whole value
proposition: it is where a human should look.

The strongest row is the last. Two gates on one state **diverged** (0.10 vs 1.91). A model
keying off one coarse signal — "this repo has no docs, so everything fails" — would have
returned low on both. It did not, so the answers track the individual gate.

**Caveats, which are why the skill prescribes an experiment rather than an integration:**
the states were written by the same person grading the answers, so this can only show that
the model is not degenerate — it cannot show it reads fine distinctions. It agreed with
that person on every point where disagreement was possible.

## Facts in the state come back near 0.97

Both factual questions in that run — one of them a count ("were more than one fragility
found and fixed?", state says *two*) — returned **0.97**, correct. Reading a stated fact
and reporting it is not the same as inferring one, and the ceiling is where that shows:
0.97 for a fact the state asserts. Do not read a high score as agreement with your
conclusion if your state already contains it.

## What is not measured

- **Whether it ever disagrees usefully with a human on a real gate.** No ground truth
  exists for real gates, so this can only be learned by recording disagreements and
  outcomes over time — the `references/observations.md` protocol in `SKILL.md`.
- **Whether confidence < 0.9 is the right cut.** It fits these eight points (clear cases
  0.99–1.00, the arguable one 0.79) and nothing more.
- **Anything about a model other than `jev-1.13.0`.** Version changes are worth noticing;
  the response carries the version.
