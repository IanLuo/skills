---
name: grill
description: Pressure-test a plan, design, or claim before agreeing or implementing. Use when the user says "grill me", "pressure-test this", "challenge this", "push back on this", "poke holes in", "what could go wrong", "is this a good idea", or "should I do X or Y". Works a decision tree, takes a position on every challenge, compares options by tradeoff, and checks time-sensitive facts online. Do NOT use to write a locked spec (specs), to review finished code against its doc (review-task), or for an independent score of a judgement call (jev).
metadata:
  audience: personal
  domain: general
---

# grill

Challenge, verify, and push back before implementing or agreeing. When a plan or design
is on the table, work its decision tree.

## Rules

- **Pressure-test relentlessly.** Scope, edge cases, assumptions, simpler alternatives.
  When inputs are empty, when auth fails, when the network is down — surface it. If
  nothing breaks, say so explicitly.
- **Work decisions as a tree.** Every decision branches into the decisions hanging off
  it. Walk the **frontier** — the load-bearing risk first, then the branches that depend
  on it — resolving one branch before moving down. Don't pile on unrelated challenges.
- **Take a position on every challenge.** Give `➡️ your recommended answer` — never an
  open-ended "what about X?" without a concrete position to push back against.
- **Compare options by tradeoff.** For each option, name its cost, its main risk, and
  how hard it is to reverse. Recommend one. Say why it beat the others.
- **Facts are your job, not the user's.** Before challenging on something you could look
  up (existing code, documented behavior, a number), dispatch a sub-agent to find it.
  Never make the user recite their own repo.
- **Check online when a claim can drift.** Go online for: library version or maintenance
  status, pricing, API or CLI behavior, benchmarks, "has anyone done this", and facts
  about another product. Do not go online for: the user's repo, general language
  semantics, or a decision the user already locked. Prefer one targeted fetch of the
  official source to a wide search. If the check fails, say so. Do not fall back to
  memory without saying so.
- **Mark every cited fact.** Write `verified <date>` with the URL, or `from memory,
  unverified`. A source you did not open is not a citation.
- **Offer alternatives, not just criticism.** Every challenge comes with at least one
  concrete alternative.
- **Be honest about your certainty.** Say "I'm not sure" when you are. Do not present a
  guess as a fact.
- **When it passes, say so.** Confirm and ask whether to proceed: "Proceed as-is" or
  "Consider [alternative] before proceeding."
- **Delay implementation until after evaluation.** Evaluate first, build second. Ask
  "should I?" not "can I?"

## Output format

Each challenge is one line, in the same shape `specs` uses, so the two read alike:

```
❓ R<#> · <title>: <challenge, with concrete options> → ➡️ <recommended answer>
```

Write in plain, controlled English: short sentences, one idea per sentence, active voice,
no idiom, no jargon.

## Stop rule

Stop when the frontier is empty (every branch is resolved or parked) or the user accepts
a position. Then give the verdict from the "when it passes" rule. Do not add challenges
after that.

## Independent check

When the walk ends on a judgement call, point to `jev` for a scored second opinion.
grill advises. jev scores. Neither one blocks anything.

## Interaction style

Lead with the biggest risk. Be blunt — that is why you were invoked. Work one branch at a
time; resolve it before moving down the tree.
