---
name: planner-workflow
description: Structured loop for task planning. Reads PRD, architecture, and UI/UX, derives a sequenced task list, and appends to plan.md. Used by pipeline Step 3 or standalone.
---

# Planner Workflow

## Overview
Appends a new task phase to `docs/tasks/plan.md` and updates the pipeline status header. Follow this strict loop — no step may be skipped.

## Prerequisites
- You receive all input files inlined in your prompt. Do NOT read files yourself.
- You receive the PRD, architecture, detailed-components, and UI/UX docs.
- The output file (`docs/tasks/plan.md`) is inlined. YOU append to it — never delete existing content.
- You receive the current version number.

## Loop

### Step 1: Read All Specs
Read every inlined input:
- `docs/prd/v{N}.md` — acceptance criteria and required changes
- `docs/architecture.md` — component boundaries
- `docs/detailed-components.md` — interfaces to implement/modify
- `docs/ui-ux/` — screens and components to build

Extract all work items from each doc.

### Step 2: Derive Tasks
For each PRD acceptance criterion, derive one or more concrete tasks. A task is:
- A specific, testable action (e.g., "Add `theme` field to Zustand store")
- Mapped to a component from detailed-components.md
- Mapped to a PRD acceptance criterion

For each UI/UX component or screen, add a task for implementation.

### Step 3: Order by Dependency
Resolve the dependency graph:
- A task that depends on another's output must come after it
- Infrastructure tasks come before feature tasks
- Components that depend on shared state must come after that state exists
- Integration tests come after all dependent features

Flag any circular dependency — do not create tasks for it, report it.

### Step 4: Write Phase
Append to `docs/tasks/plan.md`. Preserve ALL existing content. Add at the end:

```markdown
## Phase {N}: {Phase Title} (v{version})
* [ ] {N}.1 {Task description}
* [ ] {N}.2 {Task description}
...
```

Rules:
- Each task starts with `* [ ]`
- Phase number is the next integer after the last existing phase
- Each task description is one line, concise, testable
- Bug tasks from QA use `[BUG]` marker
- Deprecated tasks use `[~]` marker

### Step 5: Update Pipeline Header
Update the `## Pipeline Status` header at the top of `plan.md`:
- Set `version` to the current version
- Mark all prior pipeline steps `[x]` with timestamps (read these from the existing header — do NOT change completed steps)
- Mark `Plan updated` as `[x]` with current timestamp
- Mark all subsequent steps as `[ ]`

Example updated header:
```markdown
## Pipeline Status: v3
- [x] PRD approved             2026-05-05 14:20  → step-1-prd
- [x] Architecture updated      2026-05-05 14:30  → step-2a-architecture
- [x] UI/UX updated             2026-05-05 14:31  → step-2b-designer
- [x] Design reviewed           2026-05-05 14:32  → step-2-review
- [x] Plan updated              2026-05-05 14:45  → step-3-planner
- [ ] Cross-review passed                           → step-4-cross-review
- [ ] QA test cases drafted                         → step-5-qa-design
- [ ] Development                                    → step-6-dev
- [ ] QA verified                                    → step-7-qa-exec
```

### Step 6: Validate
- Re-read the updated `plan.md`. Confirm existing phases are intact.
- Confirm the new phase has sequential task IDs.
- Confirm every task is testable (not vague).
- Confirm the pipeline header shows correct state.
- Confirm the version number is correct everywhere.

### Step 7: Done
Report: plan.md updated, new phase number, task count, first task ID.
