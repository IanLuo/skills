---
name: analyst-workflow
description: Structured loop for requirements analysis. Reads current project state, analyzes gaps, drafts a version PRD, and validates it against the project schema. Used by pipeline Step 1 or standalone for PRD creation.
---

# Analyst Workflow

## Overview
Produces `docs/prd/v{N}.md` from user requirements and current project state. Follow this strict loop — no step may be skipped.

## Prerequisites
- You receive all input files inlined in your prompt. Do NOT read files yourself.
- You receive the target version number.
- You receive the user's requirements.

## Loop

### Step 1: Read Current State
Read the inlined context files:
- `docs/architecture.md` — current system boundaries and rules
- `docs/detailed-components.md` — current interfaces and schemas
- `docs/ui-ux/design-system.md` — current design tokens
- `docs/tasks/plan.md` — current task progress and pipeline status
- `docs/qa/` — existing test coverage

Extract:
- What exists (completed features, active components)
- What's in progress (any `[⏳]` tasks)
- What's planned but not started (any `[ ]` tasks)

### Step 2: Analyze Gap
Map the user's requirements against current state:
- What already exists and needs no change
- What needs modification
- What needs to be created new
- What's ambiguous (mark as `[NEEDS CLARIFICATION]`)

Group findings by affected artifact:
- `architecture` — changes to system design
- `ui_ux` — changes to visual design
- `plan` — new tasks required

### Step 3: Draft PRD
Write `docs/prd/v{N}.md` in this exact format:

```markdown
# PRD v{N}: {title}

## Source
{where the requirement came from}

## Current State
- {what exists}
- ...

## Changes Required
### Architecture
- {change}
### UI/UX
- {change}
### Plan
- {change}

## Acceptance Criteria
- [ ] {verifiable criterion}
- [ ] ...

## Dependencies
- {optional — blocking PRDs or external systems}
```

Rules:
- Every `## Changes Required` item must cite a section from current state ("Currently architecture.md §3.1 defines...")
- Every acceptance criterion must be verifiable (pass/fail, not subjective)
- Do NOT write implementation details — those belong in the plan
- If something can't be determined from current docs, mark `[NEEDS CLARIFICATION]`

### Step 4: Validate
- Re-read the written file. Confirm format is correct.
- Check every `[NEEDS CLARIFICATION]` — are there more than 2? If so, stop and flag for user before proceeding.
- Verify the version number in the title matches the filename.
- Confirm all acceptance criteria use `- [ ]` checklist format.

### Step 5: Done
Report completion with: the PRD path, number of acceptance criteria, and any `[NEEDS CLARIFICATION]` items.
