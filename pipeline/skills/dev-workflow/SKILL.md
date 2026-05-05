---
name: dev-workflow
description: Enforces a strict 6-step development loop for Teldrassil. Orchestrator mode dispatches fresh subagents per task (clean context per task). Worker mode executes one task: plan, design alignment, TDD, review gate (pass/fail), commit, and persist. Use when starting any code implementation, bug fix, or configuration change.
---

# dev-workflow

## Mode Selection: Orchestrator vs Worker

**Determine which role you are BEFORE proceeding:**

### Orchestrator Mode (you see multiple `[ ]` tasks in plan.md)
You are the session agent. For each pending dev task, spawn a FRESH subagent — each gets a clean context with zero conversation history. This enforces that `plan.md`, `architecture.md`, and `memory.md` are self-sufficient without leaked context from prior tasks.

**Orchestrator loop (Script-Driven):**

Use `workflow-runner.sh` (sibling file in this skill folder) as the deterministic state machine. The LLM only dispatches workers.

1. Run `bash {PIPELINE_SKILLS_DIR}/dev-workflow/workflow-runner.sh recover` to heal crashed workers.
2. Run `bash {PIPELINE_SKILLS_DIR}/dev-workflow/workflow-runner.sh next`. This finds the first `[ ]`, marks it `[⏳]`, and prints `LINE|active|DESCRIPTION`.
3. If output is `DONE` — all tasks complete. Stop.
4. If output starts with `STALE` — a crashed `[⏳]` exists. Run `recover` or handle manually. Do NOT proceed.
5. Construct a subagent prompt containing:
   - The task description from the script output
   - Relevant constraints from architecture.md / detailed-components.md
   - Relevant lessons from memory.md
   - Instructions to load `dev-workflow` and `personas` skills, then execute Worker Steps 1-6.
   - A strict directive to execute and return immediately after Step 6.
6. Dispatch: `Task(description="Execute task [ID]", prompt="[constructed prompt]", subagent_type="general")`.
7. **CRITICAL TRIGGER:** When `Task` returns, run `bash {PIPELINE_SKILLS_DIR}/dev-workflow/workflow-runner.sh done <LINE>` to mark `[x]`. IMMEDIATELY loop to Step 2. No pause. No summary. No permission ask.

**DENY in orchestrator mode:** Do NOT execute task code yourself. Always dispatch to a fresh subagent. Do NOT pause between subagents. Do NOT manually edit plan.md — the script handles all state transitions.

### Worker Mode (you are a subagent executing ONE specific task)
Follow the 6-step loop below. Your task was already assigned by the orchestrator — you have exactly one task to complete.

**Non-dev tasks** (design proposals, architecture discussions, documentation, investigation) are collaborative — discuss freely with the user, propose drafts, and let the user decide when done. Do NOT apply this workflow to non-dev tasks. When unsure whether a task qualifies, ASK the user.

---

## CRITICAL: 6-Step Development Loop

This workflow applies ONLY to development tasks: code implementation, bug fixes, configuration changes. You MUST follow these steps IN ORDER for every dev task. Each step has a required output. Skip NONE.

### Step 1: Initialize & Plan
- Read `docs/tasks/plan.md` and identify the next `[ ]` task.
- Read `docs/memory.md` for relevant past lessons.
- **Sanity check:** Verify no stale `[⏳]` tasks exist from previous sessions. If any `[⏳]` task appears completed (code exists, tests pass), fix it by marking it `[x]` before continuing.
- Edit `docs/tasks/plan.md`: change `[ ]` to `[⏳]` (In Progress).
- **Verify the edit:** Re-read the plan.md line for this task to confirm `[⏳]` is present before proceeding.
- Output a step-by-step execution plan BEFORE writing any code or running commands:
  ```
  ## Execution Plan for [Task ID]
  1. [Concrete action]
  2. [Concrete action]
  ...
  ```
- **DENY:** Do NOT write code or run commands before the execution plan is output.

### Step 2: Design Alignment
- Read `docs/architecture.md` and `docs/detailed-components.md` for relevant sections.
- Verify no Micro-Kernel boundary violations:
  - No domain logic in the Kernel
  - No payload data in the State Manager
  - No credentials in LLM context
- If ambiguous, ASK the user before proceeding.

### Step 3: TDD Loop (Tester -> Developer -> Tester)
- **Load the `personas` skill** to adopt Tester and Developer mindsets before writing code.
- **[Tester]** Write the test file FIRST. Define inputs, outputs, edge cases.
- **[Tester]** Run test: confirm it FAILS (Red).
- **[Developer]** Write minimum code to make the test pass.
- **[Tester]** Run test: confirm it PASSES (Green).
- **DENY:** NEVER write implementation before a failing test exists.

### Step 4: Review Gate (PASS or FAIL)

- **The `personas` skill is already loaded from Step 3** — the Gatekeeper persona is now available along with all other personas in a single skill.
- **[Gatekeeper]** You MUST adopt the Gatekeeper persona to rigorously evaluate the changes against the architecture, scope, and security.
- **[Gatekeeper]** Output this checklist and check EVERY item before proceeding. If this checklist does not appear in your response, the review did not happen.

```
## Review Gate
- [ ] All tests pass (`pnpm test`)
- [ ] Every requirement from the task description is met
- [ ] All planned test cases are implemented
- [ ] Edge cases covered
- [ ] No security issues (credential leaks, plaintext secrets, side effects)
- [ ] No extraneous changes beyond task scope
```

- **PASSED** → All boxes checked → Proceed to Step 5 (Commit).
- **FAILED** → Any box unchecked → Return to Step 3 and fix the gaps. Output what specifically failed and what needs to change. Do NOT proceed to commit.

### Step 5: Commit Changes
- Run `git add` for the relevant source files AND test files (NOT docs/tasks/plan.md).
- Run `git commit` with a descriptive message summarizing the task.
- **DENY:** Never commit or stage `docs/tasks/plan.md` in this step. It MUST be handled separately in Step 6.
- **DENY:** Do NOT combine this step with Step 6 in a single bash call. They must be separate actions.

### Step 6: Persist State
- Edit `docs/tasks/plan.md`: change `[⏳]` to `[x]` (Completed) for the current task.
- **Verify the edit:** Re-read the plan.md line for this task and confirm `[x]` is present. If the edit failed, re-apply it. Do NOT skip verification.
- Run `git add docs/tasks/plan.md` and `git commit` with message describing the status update.
- If meaningful lessons were learned (gotchas, new patterns, design decisions), append to `docs/memory.md` in a separate commit.
- **DENY:** Task is NOT complete until `[x]` is committed AND verified in plan.md. Memory is optional — only write lessons worth future sessions knowing.
- **DENY:** Do NOT combine plan.md commit with source file commit. They are separate tracked events.
