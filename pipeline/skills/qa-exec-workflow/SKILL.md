---
name: qa-exec-workflow
description: Structured loop for QA execution. Reads the test plan and PRD, runs tests against the application, classifies results, and writes a pass/fail report with rebound target. Used by pipeline Step 7 or standalone.
---

# QA Execution Workflow

## Overview
Runs functional and visual tests against the deployed application and produces `docs/qa/report-v{N}.md`. Follow this strict loop — no step may be skipped.

## Prerequisites
- You receive all input files inlined in your prompt. Do NOT read files yourself.
- You receive the test plan (`docs/qa/test-plan-v{N}.md`) and the PRD.
- The application must be running and accessible.

## Loop

### Step 1: Read Test Plan
Parse the inlined `docs/qa/test-plan-v{N}.md`. Extract all test cases with their:
- ID, description, precondition, steps, expected result
- Plan task reference and PRD criterion reference

### Step 2: Read PRD Expected Behavior
Parse the inlined PRD for acceptance criteria. These define "correct" behavior.

### Step 3: Execute Tests
Run each test case against the running application. Use the `browse` skill (gstack) for UI tests — navigate pages, interact with elements, take screenshots, verify state.

For each test:
1. Set up the precondition (navigate to page, set state)
2. Execute steps
3. Capture result: PASS or FAIL
4. If FAIL: capture what went wrong — screenshot, error message, actual vs expected

### Step 4: Classify Failures
For every failed test, classify the root cause:

| Classification | Meaning | Rebound Target |
|---|---|---|
| `implementation-bug` | Code doesn't implement what the plan/PRD specified | `step-6-dev` |
| `design-gap` | The architecture or UI/UX spec doesn't work as designed | `step-2a-architecture` |
| `prd-gap` | The PRD missed this scenario; it was never in the spec | `step-1-prd` |

Classification rules:
- If the test plan describes correct behavior but the app doesn't match → `implementation-bug`
- If the test plan AND the app match, but the behavior is wrong conceptually (design flaw) → `design-gap`
- If the test case exposes a scenario that the PRD never mentioned → `prd-gap`

### Step 5: Write Report
Write `docs/qa/report-v{N}.md`:

```markdown
# QA Report v{N}: {title}

**Result: {PASS | FAIL}**

| Test ID | Result | Detail |
|---|---|---|
| TC-001 | ✅ PASS | |
| TC-002 | ❌ FAIL | Graph stays in light colors after toggle |
| TC-003 | ✅ PASS | |

## Failed Tests

### TC-002: {description}
- **Classification:** implementation-bug
- **Rebound target:** step-6-dev
- **Expected:** {from test plan}
- **Actual:** {what happened}
- **Evidence:** {screenshot path or description}

## Summary
- Total: {N}
- Passed: {N}
- Failed: {N}
- Critical/High bugs: {N}
- Rebound target: {step-id or NONE}

## Failures by Classification
- implementation-bug: {N} → step-6-dev
- design-gap: {N} → step-2a-architecture
- prd-gap: {N} → step-1-prd
```

Rules:
- `Rebound target` is set to the classification with the most failures. If zero failures, set to `NONE`.
- Every failed test MUST have a classification, expected, actual, and evidence.
- If ALL tests pass → `Result: PASS`, `Rebound target: NONE`.

### Step 6: Gate (Self-check)
- If `Result` is `FAIL` and `critical_bugs > 0` → gate fails
- If `Result` is `PASS` → gate passes
- Verify the report format is correct (all sections present)

### Step 7: Done
Report: result (PASS/FAIL), test counts, rebound target if failed, classification breakdown.
