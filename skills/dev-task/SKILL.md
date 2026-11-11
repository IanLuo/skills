---
name: dev-task
description: Start and run software development tasks with TDD/BDD, the house code-quality bar, implementation, and verification. Plans against the locked spec and works that plan without re-asking settled decisions. Use for "new dev task", "implement", "implement the spec", "work the plan", "fix bug", "add feature", "refactor", "write tests", "make this code change", "clean up this code", "improve the structure", "remove duplication", or any coding task expected to modify a repo. Do NOT use for visual/product design tasks without code changes or pure research summaries.
metadata:
  audience: personal
  domain: development
---

# dev-task

## Start

1. Read `AGENTS.md` before editing.
2. Find locked docs on disk:
   `grep -rl --include='*.md' '<!-- specs:locked:\|<!-- design:locked:' .`
   Read the exit code, not the silence: 0 = docs listed below; 1 = nothing matched (BSD
   grep is also silent for a path that doesn't exist, so confirm you're at the repo root);
   2 = the command failed (bad pattern or unreadable dir) — fix it. Only then conclude
   "no locked docs". If `design-system.md` exists, check its first line for
   `<!-- design:locked:` — if absent, flag "design not frozen; run design-task first."
3. **The locked spec is the decision record. Never ask a question it already answers.**
   Scope, outcome, acceptance criteria, NFRs and non-goals are settled — read them as the
   answer. Ask only when one of these holds:
   - no locked spec covers the request,
   - the spec is silent on a decision you must make, or
   - two independent deliverables exist and the request names neither.
   In the first two cases, state the smallest reversible assumption, put it in the plan,
   and proceed. Ask the user only when the assumption is hard to undo.
4. **Scope from the spec, not from a question.** Take the deliverable the request names.
   If it names none, take the smallest slice that completes at least one acceptance
   criterion end to end, say so in the plan, and continue.

## Plan

Write the plan before the first code edit. It is a coverage table of the locked spec, not
a feature list:

| spec id | what changes | files | proof |
|---|---|---|---|
| AC1 | … | … | `<command>` or inspection |
| NFR2 | … | … | … |

- One row per acceptance criterion and per kept NFR. A rung marked `N/A:` in the spec gets
  a row saying so, or no row — never a silent omission.
- Show the table, then work it. No approval round: the spec settled the decisions, so the
  plan is a reading of it. The user stops you if the reading is wrong.
- An acceptance criterion that cannot be implemented as written is a spec problem: mark
  the row `blocked`, name the reason, and route it to `specs`. Do not substitute a
  different requirement and carry on.
- Re-plan when a row uncovers a dependency you did not see. Update the table; do not reopen
  the spec.

## Work

- Work the plan row by row, top to bottom. Each row: smallest failing test → implement →
  run the row's proof.
- Do not pause for approval between rows. A row is authorized by the spec row it cites.
- If a row turns out to need a decision the spec does not contain, stop that row only, mark
  it `blocked`, and continue with the rows that are not blocked.
- Keep edits scoped to the request and existing project style.
- Multi-file or noisy work: delegate rather than fan out inline — `delegate` for an
  in-process subagent, `task-agent` for a standalone task in its own worktree.

### Code conduct

Read `references/code-quality.md` before writing — it is the bar the diff is
reviewed against. Baseline while writing:

- Build vs. buy first: a general function — parser, retry loop, date/time handling, HTTP
  client, arg parser — is imported from a stable, maintained library or run as an existing
  tool, not written from scratch. Hand-rolling it is a finding (check 13).
- Smallest structure that works: no speculative config, flags, or single-implementation
  abstraction.
- One reason to change per unit; name units for intent; handle the failure paths you add.
- Remove your own orphans; leave pre-existing dead code alone.
- Refactor as its own behavior-preserving step (tests green before and after), never mixed
  into a feature diff.
- Can't fix a design problem within scope? Flag it — don't build on top of it.

### TDD/BDD loop

1. Characterize current behavior with a focused test or command.
2. Write the smallest failing test when the expected behavior is clear.
3. Implement the narrowest change that makes the test pass.
4. Run the focused check again.
5. Run broader verification when shared code, integrations, or public behavior changed.

For user-visible behavior: **Given** the relevant state, **When** the user does X,
**Then** Y changes.

Skip a new failing test only when: mechanical rename/doc-only, no viable test harness,
existing failing test already captures it, or exploratory/prototype work. When skipping,
record why.

### Verification evidence

Good: `pytest tests/test_auth.py -q` passes. `npm test` fails on a named unrelated test.
CI check green for commit `<sha>`. Manual repro no longer reproduces with exact steps.

Not evidence: "Looks right." "I inspected the code." "Should work."

## Done

- **The plan is the gate.** Walk the table. Every row has either fresh evidence from its
  own `proof` column, or a stated `blocked` reason. An uncovered row is a failure, not an
  omission — say "AC4 not implemented" rather than not mentioning it.
- A dev task is verified only when fresh command output, CI status, or equivalent
  executable check supports the claim.
- Passing verification is not the whole gate: re-read your own diff against
  `references/code-quality.md` and fix or record what it flags. Verification
  proves it works; the rubric proves it's buildable-on.
- If the project is coordinated through `fs` (`fs status` answers), record the work as a
  task, and add the review task before you stop: `fs add --review-of <task>` inherits the
  declared checks. You never review your own work. You do start the reviewer, though: run
  `fs dispatch --project <p> --task <review>` once you have reported `dev-done`. fs mints a
  fresh session for it and writes the prompt, so you are not choosing your judge — you are
  only choosing when the round starts. The reviewer gets the contract and the checks, never
  your notes. `fs reap --project <p>` then closes the agents whose round is over. Details:
  `../flagship/SKILL.md`.
- If a review comes back `fail`, the feedback is in that review task's `evidence` (`--note`).
  Implement it, report `dev-done` again, and the next review round starts from that report.
- If verification cannot run, record the blocker and leave the task unverified.
