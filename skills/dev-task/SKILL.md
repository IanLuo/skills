---
name: dev-task
description: Start and run software development tasks with task-core tracking plus TDD/BDD, coding conduct, implementation, verification, and progress updates. Use for triggers like "new dev task", "implement", "fix bug", "add feature", "refactor", "write tests", "make this code change", or any coding task expected to modify a repo. Do NOT use for visual/product design tasks without code changes or pure research summaries.
metadata:
  audience: personal
  domain: development
---

# dev-task

Use this for coding tasks that should produce a verifiable software change.

## Start

1. Read the project cursor or equivalent state file before editing.
2. Create or refresh the task state:

```bash
../task-core/scripts/create-goal --type dev --summary "<task summary>"
```

3. Read [references/tdd-bdd.md](references/tdd-bdd.md) when the task touches
   behavior, regressions, or user-facing workflows.
4. Read [references/triggers.md](references/triggers.md) when deciding what progress
   and evidence to record.

## Work

- Prefer a failing or characterizing test before implementation when practical.
- Keep edits scoped to the request and existing project style.
- Run the smallest meaningful verification first, then broader tests when shared
  behavior or risk warrants it.
- Update progress after each material phase:

```bash
../task-core/scripts/update-progress --type dev --status claimed --summary "<what changed>"
../task-core/scripts/update-progress --type dev --status verified --evidence "<command + result>"
```

## Done

- A dev task is verified only when fresh command output, CI status, or an equivalent
  executable check supports the claim.
- If verification cannot run, record the blocker and leave completion as claimed, not
  verified.
