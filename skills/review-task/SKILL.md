---
name: review-task
description: Verify that a completed task's changes match its defining docs and task goal. Catch regressions, invariant violations, and stale evidence.
metadata:
  audience: personal
  domain: development
---

# review-task

Use this to verify that completed or in-progress work matches its defining docs and
task goals. This is a correctness gate, not a code-quality review.

## Start

1. Read `AGENTS.md` before inspecting changes.
2. Find locked docs on disk: `grep -rl '<!-- specs:locked:\|<!-- design:locked:' *.md`.
   If a defining doc the diff touches is absent, report "no locked doc for `<area>`;
   can't verify fidelity, only invariants." Don't guess a path.

## Work

### 1. Gather context

Four inputs every review needs:
- **Defining docs** — locked docs on disk (find with grep), plus `AGENTS.md` itself
  (invariants, commands, architecture rules). If no task-specific docs, flag "no locked
  defining doc — fidelity unverifiable."
- **Actual changes** — `git diff` against the base. Capture file list, line-level diffs,
  untracked files.
- **Verification evidence** — run test/build commands from `AGENTS.md`. Fresh output only.

### 2. Compare

| What to check | How |
|---|---|
| Goal vs diff | Does the diff implement what was intended? Missing pieces? Extras? |
| Invariants | Did any change touch a hot-invariant boundary from `AGENTS.md`? Is it preserved? |
| Docs fidelity | Design: does output match `design-system.md` tokens/typography/components/concepts? Dev: does the implementation follow the spec/ADR? |
| Regressions | Run the project's test suite. Does anything break? |

### 3. Classify

- **✅ matches** — change aligns with docs; evidence fresh and reproducible.
- **⚠️ gap** — docs say X but diff doesn't implement it (or differs with no recorded decision).
- **🔴 regression** — test broke, invariant violated, or build fails.
- **❓ uncertain** — docs are silent, change is ambiguous, or can't verify without user.

For multi-file reviews, spawn one subagent per review dimension (invariants, doc fidelity, evidence, regressions), then synthesize.

## Done

- Every claim checked against actual diff and fresh evidence, with a verdict per finding.
- **Route non-clean findings back to the owning skill:**
  - ⚠️ gap in spec/PRD/architecture fidelity → re-run `specs` on the relevant rung, or `dev-task` to close the gap.
  - ⚠️ gap in design fidelity → re-run `design-task` to reconcile the artifact.
  - 🔴 regression or invariant violation → re-run `dev-task` to fix.
  - Tell the user which skill to re-run and why.

### Verification evidence

Good: `npm test` passes all tests at `<sha>`. `git diff main...HEAD --stat` shows files
within goal scope. `design-system.md` tokens match rendered output. `AGENTS.md` hot
invariant preserved.

Not evidence: "I read the diff and it looks fine." A stale test run from before the
change. Design fidelity assertions without comparing actual rendered output against
the artifact.

## What this skill does NOT cover

- **Code quality** (style, bugs, perf) — use `/code-review` or `/simplify`.
- **Running the app to see it work** — use `/verify`.
- **Security review** — use `/security-review`.
- **Design critique** (does it look good?) — part of `/design-task`'s own verification;
  this skill only checks fidelity to the artifact.
