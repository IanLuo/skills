---
name: qa-design-workflow
description: Structured loop for QA test design. Reads PRD, architecture, UI/UX, and plan, drafts test cases, writes test stubs, and passes traceability gate. Used by pipeline Step 5 or standalone.
---

# QA Design Workflow

## Overview
Produces `docs/qa/test-plan-v{N}.md` and optionally `tests/functional/*.test.ts` stubs. Follow this strict loop — no step may be skipped.

## Prerequisites
- You receive all input files inlined in your prompt. Do NOT read files yourself.
- You receive the PRD, architecture, detailed-components, UI/UX, and plan.md.

## Loop

### Step 1: Read Acceptance Criteria
Parse the PRD. Extract the `## Acceptance Criteria` checklist. These are your test targets. Every test must validate at least one criterion.

### Step 2: Read Plan Tasks
Parse the inlined `docs/tasks/plan.md`. Extract the new tasks (the latest phase). These are the implementation units you're testing against.

### Step 3: Read Architecture & UI/UX
From architecture: identify state boundaries, API contracts, and security constraints.
From UI/UX: identify screens, components, and interaction flows.

### Step 4: Draft Test Cases
For each acceptance criterion, design one or more test cases covering:

| Coverage | Description |
|---|---|
| Happy path | Standard successful execution |
| Edge case | Boundary conditions (empty state, max input, concurrent access) |
| Error handling | Invalid inputs, failed dependencies, timeout |

Write `docs/qa/test-plan-v{N}.md`:

```markdown
# Test Plan v{N}: {title}

## TC-001: {description}
- **Plan Task:** {task ID}
- **PRD Criterion:** {criterion text}
- **Precondition:** {state before test}
- **Steps:**
  1. {action}
  2. {action}
- **Expected:** {verifiable outcome}

## TC-002: ...
...

## Traceability Matrix
| Test ID | Plan Task | PRD Criterion |
|---|---|---|
| TC-001 | 9.1 | Toggle switches between light and dark themes |
| TC-002 | 9.4 | Workflow graph colors adapt to theme |
| ... | ... | ... |
```

Rules:
- Every test must have a `Plan Task` reference and a `PRD Criterion` reference in its header
- Expected results must be verifiable, not subjective
- Preconditions must set up the exact state needed
- Steps must be atomic actions
- Tests must be isolated — no test depends on another test's state

### Step 5: Write Test Stubs (Optional)
If applicable, write executable stubs in `tests/functional/`:

```typescript
// tests/functional/theme.e2e.ts
import { test, expect } from '@playwright/test';

test('TC-001: Toggle switches between light and dark themes', async ({ page }) => {
  // Given: default light theme
  // When: click theme toggle
  // Then: body class changes to 'dark'
});

test('TC-002: Workflow graph adapts to dark theme', async ({ page }) => {
  // Given: dark theme active
  // When: load workflow view
  // Then: node colors match dark palette
});
```

Use descriptive `test()` names with the TC-ID prefix. Include GIVEN/WHEN/THEN comments as placeholders until stepped.

### Step 6: Traceability Gate (Self-check)
Before reporting done:
- Every test case has a non-empty `Plan Task` field
- Every test case has a non-empty `PRD Criterion` field
- Every PRD acceptance criterion is covered by at least one test case
- No orphan tests (test with no trace to plan or PRD)

### Step 7: Done
Report: test plan path, test count, coverage (which PRD criteria are covered), any gaps.
