# observations — one row per real gate

The experiment `SKILL.md` describes. One row each time Jev was asked at a **real** decision
gate before the answer was given. The protocol is in `SKILL.md`; the log lives here, in the
skill, because that is the unit that travels — a skill writing data into a directory
somewhere else makes the experiment depend on a path nobody else can find.

| date | gate | my answer | jev score | conf | changed my answer? |
|---|---|---|---|---|---|
| 2026-09-19 | prd_locked | no locked PRD exists (grepped: only the skills that teach the marker match) | 0.07 | 0.89 | no |

**Only the last column matters.** If it is still empty after ~10 gates, Jev is well-grounded,
cheap and redundant — stop, and say so in the session summary rather than building anything.
If it changed a call even once, that is the evidence for the integration, and the rubric that
gate used is the shape it should take.

Not logged here: the eight-point spread experiment that established the model is not
degenerate. Those were synthetic states labelled by the same person grading the answers, so
they cannot show that it *helps*. They are in `evidence.md`.
