---
name: task-core
description: Shared task-state spine for task skills. Use only when another skill such as dev-task, design-task, or a future research-task explicitly tells Codex to call task-core scripts or read task-core protocol. Do NOT invoke directly for ordinary user requests, coding work, design work, research, planning, or generic "new task" language.
metadata:
  audience: personal
  domain: workflow
---

# task-core

This is the non-triggerable shared spine for task-family skills. Domain skills own
the work; this skill owns the invariant state actions.

## Core Actions

Use these scripts by sibling-relative path from a domain skill:

- `../task-core/scripts/create-goal` — initialize or refresh a task goal in `CURSOR.md`.
- `../task-core/scripts/update-progress` — rewrite `CURSOR.md` with claimed and verified progress.
- `../task-core/scripts/update-doc` — update a durable task artifact after meaningful changes.
- `../task-core/scripts/check` — fail-loud drift gate: validate every pointer in the
  cursor exists on disk and the `<!-- synced: <sha> -->` marker is an ancestor of HEAD.
  Run before starting work and before committing a rewritten cursor. Exit zero = clean.

Read [references/protocol.md](references/protocol.md) when writing a domain skill,
debugging the scripts, or deciding what belongs in shared state versus a domain
artifact.

## Rules

- Keep task tracking type-invariant: goal, progress, blockers, verification,
  decisions, and active pointers live in the shared spine.
- Put domain workflow in the domain skill: tests for dev, design tokens for design,
  citations for research, and so on.
- Do not mark work verified without fresh evidence. If evidence is missing, record
  the status as claimed or pending verification.
- Rewrite state in place. Never append a cursor-like log.
- Run the drift gate (`../task-core/scripts/check`) at session start and before
  committing a rewritten cursor. Prose context is not enforceable — the script is.
