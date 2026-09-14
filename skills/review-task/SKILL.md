---
name: review-task
description: Verify that a completed task's changes match its defining docs and task goal. Catch regressions, invariant violations, stale evidence, and code-health problems. Checks the diff's structure against the house code-quality bar.
metadata:
  audience: personal
  domain: development
---

# review-task

Use this to verify that completed or in-progress work matches its defining docs and
task goals, and that its structure clears the house bar. It finds problems; it does not
rewrite code.

## Start

1. Read `AGENTS.md` once — invariants, commands, test/build commands. Reference it in
   every check; don't re-read per check.
2. Find locked docs on disk: `grep -rl '<!-- specs:locked:\|<!-- design:locked:' *.md`.
   If a defining doc the diff touches is absent, report "no locked doc for `<area>`;
   can't verify fidelity, only invariants." Don't guess a path.

## Work

### 1. Gather context

Four inputs every review needs:
- **Defining docs** — locked docs (found at Start) plus the `AGENTS.md` you already
  read. If no task-specific docs, flag "no locked defining doc — fidelity unverifiable."
- **Actual changes** — `git diff` against the base. Capture file list, line-level diffs,
  untracked files.
- **Verification evidence** — run `AGENTS.md`'s test/build commands **once**, fresh
  output only. Its result is the Regressions row's answer. If the diff has no runtime
  surface (docs/config-only), skip the run and say so.

### 2. Compare

| What to check | How |
|---|---|
| Goal vs diff | Does the diff implement what was intended? Missing pieces? Extras? |
| Invariants | Did any change touch a hot-invariant boundary from `AGENTS.md`? Is it preserved? |
| Docs fidelity | Design: does output match `design-system.md` tokens/typography/components/concepts? Dev: does the implementation follow the spec/ADR? |
| Regressions | Did the evidence run (above) break anything new — vs. the task's own recorded run, when one exists? |
| Code health | Run every check in `../dev-task/references/code-quality.md` over the same diff. Structure only — the file names its own scope limits. |

### 3. Classify

- **✅ matches** — change aligns with docs; evidence fresh and reproducible.
- **⚠️ gap** — docs say X but diff doesn't implement it (or differs with no recorded decision).
- **🔴 regression** — test broke, invariant violated, or build fails.
- **🟡 quality** — works and matches docs, but the structure makes the next change harder
  (change amplification, speculation, orphan, swallowed error, wrong seam). Cite `file:line`.
- **❓ uncertain** — docs are silent, change is ambiguous, or can't verify without user.

Run all five checks in this one context over the gathered inputs — do not fan out per
dimension. Every dimension needs the same full diff and docs, so subagents would only
re-gather what you already hold. Only for a very large diff, delegate **file slices**
(each slice runs all five checks on its files) to subagents; hand each slice its diff
and doc paths — never let a subagent re-run the suite or re-discover docs. Synthesize
the verdicts.

## Done

- Every claim checked against actual diff and fresh evidence, with a verdict per finding.
- **Route non-clean findings back to the owning skill:**
  - ⚠️ gap in spec/PRD/architecture fidelity → re-run `specs` on the relevant rung, or `dev-task` to close the gap.
  - ⚠️ gap in design fidelity → re-run `design-task` to reconcile the artifact.
  - 🔴 regression or invariant violation → re-run `dev-task` to fix.
  - 🟡 quality finding → re-run `dev-task` to restructure, behavior-preserving with tests
    staying green. Quality findings never block a correctness verdict — report both.
  - Tell the user which skill to re-run and why.

### Verification evidence

Good: `npm test` passes all tests at `<sha>`. `git diff main...HEAD --stat` shows files
within goal scope. `design-system.md` tokens match rendered output. `AGENTS.md` hot
invariant preserved.

Not evidence: "I read the diff and it looks fine." A stale test run from before the
change. Design fidelity assertions without comparing actual rendered output against
the artifact.

## What this skill does NOT cover

- **Rewriting the code** — this skill reports 🟡 quality findings; fixing them is a
  `dev-task`. It does not restructure anything itself.
- **Style and formatting** — the repo's linter/formatter and `AGENTS.md` own that.
- **Correctness bugs the docs don't name, and performance work** — separate tasks with
  their own gates.
- **Running the app to see it work** — use `/verify`.
- **Security review** — use `/security-review`.
- **Design critique** (does it look good?) — part of `/design-task`'s own verification;
  this skill only checks fidelity to the artifact.
