# evaluation — the ranked rubric for auditing a skill

Read this in **evaluate** mode. It answers "is this skill any good?" — a different
question from `validate.py`'s "does it satisfy the spec?". Validate first; evaluate after.

## Rank by failure impact

Dimensions are tiered: A breaks the skill outright, D only bites later. Report in tier
order — the ranking *is* the output.

| Tier | Question | Fails → |
|---|---|---|
| **A** | Does it load and fire? | skill is dead weight, or silently sends agents down a wrong path |
| **B** | Does it produce the right behavior? | right trigger, wrong outcome |
| **C** | Is it cheap and unambiguous? | wasted context, misreads |
| **D** | Does it survive change? | silent drift later |

**Protocol.** Any Tier A failure ends the audit: report those, state that B–D are moot
until they clear, and stop. One finding = observation + `file:line` + the concrete fix.
Score each tier pass/fail — never 1–10, that is precision theater. Do not edit while
evaluating; fixes are `revise` mode. Close with the single highest-leverage fix.

## Tier A — loads and fires

| # | Dimension | How to measure |
|---|---|---|
| A1 | **Trigger** | Description constraint checks are `validate.py`'s job. Judgement: ≥3 literal trigger phrases, at least one keyword a user would plausibly type unprompted; a negative trigger naming the sibling it collides with; every mode the body defines is reachable from the description. Test: 3 phrasings that *should* fire, 2 that should not. |
| A2 | **Reference integrity** | `python3 scripts/audit.py <skill>` — 0 broken, 0 ambiguous, 0 orphan resources. Fails hardest because a dead path means the instruction silently never runs. |
| A3 | **Claim accuracy** | Run every fenced command. Each must behave as documented, including exit codes. Paths and facts must be true after any rename or move — this is where stale instructions hide. |

## Tier B — behaves correctly

| # | Dimension | How to measure |
|---|---|---|
| B1 | **Contract completeness** | From the body alone: what must be true before starting, what "done" means, and where to go when it can't proceed. A skill with an entry and no exit is a trap. |
| B2 | **Degrees of freedom matched to fragility** | Every step that *must* be exact has an exact command; every step with many valid approaches stays prose. Fails in both directions: "validate the config" with no command, or a mandatory 30-flag incantation for a choice that doesn't matter. |
| B3 | **Boundary discipline** | A negative scope ("does NOT cover") plus a named sibling for each deferral. A deferral with no destination routes the user nowhere. |

## Tier C — cheap and unambiguous

| # | Dimension | How to measure |
|---|---|---|
| C1 | **Token economy** | `audit.py` reports body size (soft 200 lines, spec warns at 500). Judgement: detail pushed to `references/` behind a "read this when"; no rule restated that a script or another file already owns. Long is fine when it earns it. |
| C2 | **Clarity** | Count hedges ("consider", "may want", "try to") and unresolved either/ors with no recommended default. Each step imperative. An agent should not have to guess. |
| C3 | **Evidence discipline** | Claims that can be checked carry a command or artifact. Where a claim is checkable, a Good / Not-evidence pair. |

## Tier D — survives change

| # | Dimension | How to measure |
|---|---|---|
| D1 | **Single source of truth** | Each rule lives in exactly one place; scripts are referenced, not re-implemented; spec constants are not restated. Cross-check: does a spec change force edits in 2+ files? |
| D2 | **Provenance** | `sync-check.sh` exits 0; `SPEC_REVIEWED` not stale against upstream. |

## What this rubric deliberately does not measure

Style and taste, length by itself, cleverness of design, or how popular the skill is.
Duplication across sibling skills is *not* scripted either — exact-line matching was
tested against this repo and found zero signal, so it stays an agent judgement under D1.

## Automatable vs judged

| Mechanized (`audit.py`) | Judged by you |
|---|---|
| A2 reference integrity, C1 body size | A1, A3, B1, B2, B3, C2, C3, D1, D2 |

The mechanical checks are fast and deterministic — run them first, then spend judgement on
the rest. Never report a judged finding as if a script produced it.
