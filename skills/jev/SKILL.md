---
name: jev
description: Get an independent, scored second opinion about a piece of state from TypeSafe's System One model (Jev), called through cred. Use when a decision gate is being answered on judgement alone and a calibrated second opinion would help — "ask jev", "second opinion", "score this gate", "how confident is that", "is this gate uncertain", "double-check my call", "is this ready to merge", "is this deliverable actually done" — or when recording the result of such a check. Do NOT use it to gate or block anything — it advises and never decides. Do NOT use for research (that is librarian), for exploring a codebase (that is Explore), or as a substitute for a test, a check step, or evidence.
metadata:
  audience: personal
  domain: reasoning
---

# jev

Ask TypeSafe's System One model (`POST https://api.typesafe.ai/v1/systemone`) for an
independent judgement about a state, as a **score with a confidence**. The model reads
the state you hand it and rates it against a rubric you write. It is a second opinion —
the thing you reach for at a gate that is currently being answered on judgement alone.

Its purpose is not to answer better than you. It is to tell you **where the uncertainty
is**, so your attention lands on the gates that are genuinely arguable.

## Working rules

1. **Advisory only — never a gate.** It never blocks, never auto-passes, never discharges
   an obligation. Whatever it says, you still answer, and the answer is still yours.
   Reason from evidence, not from policy: on one question in one request, `noul` said
   P(yes) = 0.21 while `choice` said 0.00 — so these numbers are not a probability safe
   to threshold. See [references/evidence.md](references/evidence.md).
2. **Read `confidence`, not `score`, to decide whether to care.** A three-level rubric
   scores 0–2; a score of 0.89 is ambiguous only if the confidence is low. Measured:
   clear cases came back 0.99–1.00, the genuinely arguable one 0.79. Rule of thumb:
   surface it to the human when **confidence < 0.9**.
3. **One primitive per gate.** `score` for anything graded (it returns a distribution you
   can sanity-check), `noul` for a plain yes/no. Never combine or compare the two — they
   do not share a probability space.
4. **Never let the state do the model's job for it.** Facts stated in the state come back
   at ~0.97: it is reading, not inferring. If the state asserts the conclusion, the
   score is a readback of your own framing.
5. **Write the rubric, or don't ask.** The rubric's wording decides the answer — a
   poorly-partitioned rubric produces a confident answer to a different question. Writing
   rubrics for real gates also exposes gates whose wording is vague; that is a finding
   worth reporting back, not something to paper over.

## Asking

```bash
# a gate with a rubric already written (see references/rubrics.json)
python3 skills/jev/scripts/jev.py gate acceptance_stated --state /tmp/state.md

# any payload you want, verbatim
python3 skills/jev/scripts/jev.py raw --payload /tmp/payload.json

# what rubrics exist
python3 skills/jev/scripts/jev.py gate --list
```

The state is the evidence: what is on disk, what was run, what the tests said. Write it
as facts and numbers, not as a conclusion — rule 4 is about exactly this failure. Pass a
file, not an argument, so quoting can never mangle it.

The token comes from `cred`, so nothing here handles a secret: the script shells out to
`cred run typesafe -- curl …`, and the Keychain approval covers it. If `cred` reports
`vault is LOCKED`, ask the human — see the `credentials` skill.

## Using it at a real gate (start here)

Do **not** wire it into a pipeline yet. Use it by hand and find out whether it earns a
place; the evidence that it helps does not exist until real disagreements are recorded.

At each real gate (`ask` steps in `~/.fs/kb/*-prerequisites.yaml`, and any judgement
call you would otherwise make alone):

1. Ask Jev, with the rubric for that gate.
2. Append one row to `~/.fs/jev/observations.md`:

```markdown
| date | gate | my answer | jev score | conf | changed my answer? |
|---|---|---|---|---|---|
| 2026-09-19 | acceptance_stated | stated | 1.91 | 0.94 | no |
```

3. Re-read the log after ~10 gates. **Only the last column matters.** If it is empty
   every time, Jev is well-grounded, cheap and redundant — stop, and say so. If it
   changed a call even once, that is the evidence for building the integration, and the
   rubric you wrote twice by hand is the shape it should take.

Do not put the log in `fs` yet: mixing the experiment into the thing being tested makes
it impossible to tell which one failed.

## Writing a rubric

`references/rubrics.json` holds one entry per gate: `instructions` (the gate stated as a
question) and `criteria` (ordered levels, worst to best — the model returns a
probability per level and a probability-weighted `score`).

- **Levels must partition.** "Any locked doc that names the deliverable" and "a locked
  PRD" are not the same condition, and a gate whose wording blurs them gets a confident
  answer to an unintended question.
- Two levels minimum, three is usually right: unmet / partly / met.
- Keep them in the model's language, not yours: describe what the level *looks like*,
  not what it is called.

## What has been measured

Eight points across four states, and one primitives-consistency check. The short version:
it discriminated (scores 0.10–2.00, mean 1.15; the ambiguous case landed mid-scale at
0.89), it was grounded on facts, and it agreed with the human every time it had room to
disagree — which is exactly why the experiment above exists. Numbers, method and caveats:
[references/evidence.md](references/evidence.md).

## Layout

- `scripts/jev.py` — the only way to call the API (token via `cred`).
- `references/rubrics.json` — gate → rubric.
- `references/evidence.md` — what was measured, and how.
