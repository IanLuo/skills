---
name: dev-task
description: Start and run software development tasks with TDD/BDD, coding conduct, implementation, and verification. Use for "new dev task", "implement", "fix bug", "add feature", "refactor", "write tests", "make this code change", or any coding task expected to modify a repo. Do NOT use for visual/product design tasks without code changes or pure research summaries.
metadata:
  audience: personal
  domain: development
---

# dev-task

## Start

1. Read the last line of `SESSION.md` and `AGENTS.md` before editing.
2. Find locked docs on disk: `grep -rl '<!-- specs:locked:\|<!-- design:locked:' *.md`.
   If a needed doc (PRD, system-design, architecture) is absent, flag it and ask before
   proceeding. If `design-system.md` exists, check its first line for
   `<!-- design:locked:` — if absent, flag "design not frozen; run design-task first."
   Don't silently assume scope.
3. If the locked PRD covers more than one deliverable, ask the user which slice this
   task implements. One dev-task = one deliverable.

## Work

- Prefer a failing or characterizing test before implementation when practical.
- Keep edits scoped to the request and existing project style.
- Run the smallest meaningful verification first, then broader tests when shared
  behavior or risk warrants it.
- For multi-file tasks, spawn subagents for parallel implementation.

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

- A dev task is verified only when fresh command output, CI status, or equivalent
  executable check supports the claim.
- If verification cannot run, record the blocker and leave the task unverified.
- Append one line to SESSION.md:
  `<date> · dev-task · <what was done, proof, next step>.`
