# Re-grill ladder — a locked doc meets a session

Read this when a locked doc has a session behind it. The lock froze a set of decisions;
the session produced evidence about them. Re-grill finds the decisions the evidence
changed and re-asks **only those**. It is not a second full elicitation and not a drift
review — `review-task` asks "did the code do what the doc said?", this asks "was the doc
right?".

## When it fires

- Explicit: `--regrill <doc>`, "regrill the spec", "the session changed the spec", "what
  did the session disprove".
- Never automatic. A doc with no session behind it has nothing to grill.

## Gate — before the first question

- **Marker vs HEAD.** `git log --oneline <lock-sha>..HEAD`. Nothing since the lock → say
  so and stop. A re-grill with no evidence invents doubt, which is worse than silence.
- **Chain on disk.** The doc's upstream and referrers must still exist. A contract that
  moved invalidates the comparison, and `lock.sh` will refuse the rotation anyway.
- **Already recorded.** If `## Session corrections` already carries this session's
  findings, stop — nothing new to ask.

## Evidence pack — facts first

1. The diff: `git diff <lock-sha>..HEAD --stat`, then the files the doc names.
2. The session's failures: checks that did not pass, errors that changed a plan.
3. One line from the user: **"what surprised you?"** Only they have it; it is not in any
   diff. Ask for one line, not an interview.

## Verdict — per decision

A **decision is one locked section**. The rung headers are the units; do not invent
smaller ones.

| verdict | when | what happens |
|---|---|---|
| `holds` | the evidence fits | no question; the section stays verbatim |
| `strained` | not disproved, but doubtful or incomplete | asked, with the doubt named |
| `broken` | the evidence contradicts it | asked, with the evidence quoted |

Silence is not doubt. A decision the session never touched `holds` — do not manufacture a
question to fill the page.

## The round

One annotate page, built with `../annotate/scripts/build-artifact.py`, using the rung
markup (`.rung` / `data-rung` / `.answers[data-for]`).

- One row per `strained` or `broken` decision. Order by blast radius — the decision that
  changes the most downstream first.
- Each row carries three lines, each its own anchor: the **locked answer**, **what the
  session showed**, and the **proposal** with `➡️`.
- Buttons: `Accept →` (take the proposal) · `Keep locked` (the evidence is not enough) ·
  `Option B` · `Other…`.
- `holds` decisions collapse to one line at the bottom, so the reader sees what survived
  as well as what moved.

`data-status="changed"` on a rewritten line; `data-status="new"` on a decision the lock
never had.

## Rotate or supersede — decide, do not drift

- **Rotate in place** (`lock.sh --force`) when the outcome holds and fewer than half the
  decisions change. Same type, same upstream, same referrers.
- **Supersede** — a new doc with a back-reference — when the outcome, the scope, or the
  contract changed. Keep the old doc locked; it records what was decided then.

The contract rung is re-elicited only if the session changed the contract itself (a new
referrer, a superseded upstream). Otherwise reuse the recorded one.

## Record — the part that outlives the session

Append one line per changed decision under `## Session corrections`, newest first, capped
at 10:

```
- <YYYY-MM-DD> · <section> · was: <old> · now: <new> · evidence: <the observation>
```

Written by this skill, never by hand. A correction with no evidence line is a preference
change, not a session finding — say so rather than filing it as one.

## Checklist before the lock rotates

- [ ] every section has a verdict (`holds` / `strained` / `broken`)
- [ ] every `strained`/`broken` decision was asked, not assumed
- [ ] every changed decision has a correction line with its evidence
- [ ] no section was dropped — a decision that no longer applies is `N/A: <why>`
- [ ] the round's pasted answers resolved back to their anchors
