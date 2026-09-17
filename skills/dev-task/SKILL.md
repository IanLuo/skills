---
name: dev-task
description: Start and run software development tasks with TDD/BDD, the house code-quality bar, implementation, and verification. Use for "new dev task", "implement", "fix bug", "add feature", "refactor", "write tests", "make this code change", "clean up this code", "improve the structure", "remove duplication", or any coding task expected to modify a repo. Do NOT use for visual/product design tasks without code changes or pure research summaries.
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
   "no locked docs". If a needed doc (PRD, system-design, architecture) is absent, flag it
   and ask before proceeding. If `design-system.md` exists, check its first line for
   `<!-- design:locked:` — if absent, flag "design not frozen; run design-task first."
   Don't silently assume scope.
3. If the locked PRD covers more than one deliverable, ask the user which slice this
   task implements. One dev-task = one deliverable.

## Work

- Keep edits scoped to the request and existing project style.
- Multi-file or noisy work: delegate rather than fan out inline — `delegate` for an
  in-process subagent, `task-agent` for a standalone task in its own worktree.

### Code conduct

Read `references/code-quality.md` before writing — it is the bar the diff is
reviewed against. Baseline while writing:

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

- A dev task is verified only when fresh command output, CI status, or equivalent
  executable check supports the claim.
- Passing verification is not the whole gate: re-read your own diff against
  `references/code-quality.md` and fix or record what it flags. Verification
  proves it works; the rubric proves it's buildable-on.
- If verification cannot run, record the blocker and leave the task unverified.
