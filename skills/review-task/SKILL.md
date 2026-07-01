---
name: review-task
description: Review the current diff or a completed dev/design task for correctness — compare changes against the docs that define what was supposed to be done (AGENTS.md, CURSOR.md, design-system.md, task goal, PRD, ADRs), check for regressions and invariants violations, and verify that claimed evidence matches reality. Use for triggers like "review this", "check my work", "did this land correctly?", "verify the PR", "compare changes with the design", "is this task really done?", or "validate before merge". Do NOT use for ordinary code review (style, bugs, perf — use /code-review), for running the app to see it work (use /verify), or for creating/running new tasks (use /dev-task or /design-task).
metadata:
  audience: personal
  domain: development
---

# review-task

Use this to verify that completed or in-progress work matches its defining docs and
task goals. This is a correctness gate, not a code-quality review.

## Start

1. Read the project cursor (`CURSOR.md`) and `AGENTS.md` before inspecting changes.
2. Create or refresh the task state:

```bash
../task-core/scripts/create-goal --type review --summary "<review summary>"
```

3. Read [references/triggers.md](references/triggers.md) when deciding what progress
   and evidence to record.

## Work

### 1. Gather context

Collect the four inputs every review needs:

- **Task state** — `CURSOR.md` (goal, claimed progress, verification evidence,
  blockers, decisions). What was this task supposed to accomplish?
- **Defining docs** — the docs the implementer was told to follow. This set varies by
  task type but always includes `AGENTS.md` (invariants, run/build/test commands,
  architecture rules) when it exists. If no `AGENTS.md` is present, skip invariant
  checks and note it as ❓ uncertain. For dev tasks: spec files, ADRs, conventions
  docs. For design tasks: `design-system.md`, accepted concept images, fidelity goals.
- **Actual changes** — `git diff` against the base, or `git log` if the base is
  ambiguous. Capture file list, line-level diffs, and any untracked files.
- **Verification evidence** — run the test/build commands from `AGENTS.md` and the
  cursor's own verification commands. Fresh output only.

### 2. Compare

For each claim in the task state, check it against the actual diff and fresh
verification:

| What to check | How |
|---|---|
| Goal vs diff | Does the diff actually implement what the goal says? Any missing pieces? Anything extra that wasn't asked for? |
| Invariants | Did any change touch a hot-invariant boundary from `AGENTS.md`? If so, does it preserve the invariant? |
| Docs fidelity | For design tasks: does the output match `design-system.md` tokens, typography, components, and accepted concepts? For dev tasks: does the implementation follow the spec/ADR it references? |
| Evidence freshness | Is the cursor's `verified:` evidence stale? Re-run it — does it still pass? |
| Drift gate | Run `../task-core/scripts/check`. Is the cursor behind HEAD? Any stale pointers? |
| Regressions | Run the project's test suite. Does anything break that shouldn't? |

### 3. Classify findings

Each finding gets one of four verdicts:

- **✅ matches** — the change aligns with the docs; evidence is fresh and reproducible.
- **⚠️ gap** — the docs say X but the diff doesn't implement it (or implements it
  differently with no recorded decision).
- **🔴 regression** — a test broke, an invariant was violated, or the build fails.
- **❓ uncertain** — the docs are silent, the change is ambiguous, or you can't verify
  without the user.

### 4. Update progress

Record the review phase:

```bash
../task-core/scripts/update-progress --type review --status claimed --summary "<gaps, regressions, or clean>"
../task-core/scripts/update-progress --type review --status verified --evidence "<review verdict summary>"
```

### Delegation

For any non-trivial review (multi-file diff, multiple review dimensions, or broad
context gathering), delegate heavy work to subagents via the Agent tool. The
orchestrator that loaded this skill:

- **Owns** the goal, cursor state, the final verdict, and the decision to gate merge.
- **Delegates** context gathering (diff scanning, doc reading, evidence re-running,
  drift-gate checks) to subagents. Use independent subagents for each review dimension
  (invariants, docs fidelity, evidence freshness, regressions).
- **Merges** results — collect each subagent's findings, deduplicate, classify (✅/⚠️/🔴/❓),
  and update progress.
- **Does NOT** inline large diff reads or read dozens of docs itself. If the review
  spans more than a few files, spawn subagents.

For thorough reviews, fan out all review dimensions in parallel subagents, then
synthesize the verdict. The adversarial verification pattern (one subagent per
dimension, each trying to find failures) works well here.

## Done

- A review task is verified when every claim in the task state has been checked
  against the actual diff and fresh evidence, with a verdict per finding.
- If findings are blocking (🔴 regressions), record them under **Blockers** in the
  cursor and leave the task as `claimed`, not `verified`.
- If the review uncovers no issues, the verdict is clean: "Reviewed N claims, M files
  changed; all claims match the diff; evidence reproduces; no regressions; invariants
  intact."

## What this skill does NOT cover

- **Code quality review** (style, bugs, perf, simplification) — use `/code-review` or
  `/simplify`.
- **Running the app to see it work** — use `/verify`.
- **Security review** — use `/security-review`.
- **Design critique** (does it look good?) — that's part of `/design-task`'s own
  verification; this skill only checks fidelity to the artifact.
