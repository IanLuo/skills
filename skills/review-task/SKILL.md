---
name: review-task
description: Verify that a completed task's changes match its defining docs and task goal, independently of the developer's account. Builds a test matrix from the locked spec's acceptance criteria and runs it, then catches regressions, invariant violations, stale evidence, and code-health problems. Use for "review this diff", "verify this is done", "check this against the spec", "test the code against the spec", "did I miss anything", "is this ready to merge". Do NOT use to write or fix the code (dev-task).
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
3. **Standalone.** You test the spec against the code, not the developer's account of it.
   - Do not read the dev's plan, notes, report, or transcript as evidence. The spec and the
     diff are your inputs.
   - A claimed passing run is not evidence. Re-run every command you rely on, yourself.
   - If the only way to check a row is the dev's own test, run that test in a clean state
     and treat it as one row among many, not proof of the whole.

## Work

### 1. Build the matrix from the spec

Before running anything, derive the checks. One row per acceptance criterion and per kept
NFR in the locked spec:

| spec id | what must be true | how you check it | result |
|---|---|---|---|
| AC1 | … | `<command>` / inspection / manual repro | … |

- Write the `how` yourself from the spec text. Do not copy the dev's commands — a check the
  author designed tests the author's understanding, not the spec.
- A row with no check you can run is `unverifiable`. Say so and say why; do not mark it pass.
- The spec's Gherkin or Given/When/Then lines become the test cases directly.

### 2. Gather context

Three inputs every review needs:
- **Defining docs** — locked docs (found at Start) plus the `AGENTS.md` you already read.
- **Actual changes** — `git diff` against the base. Capture file list, line-level diffs,
  untracked files.
- **Your own runs** — the project's test/build commands from `AGENTS.md`, fresh, plus each
  row's `how` check. If the diff has no runtime surface (docs/config-only), skip the run and
  say so.

### 3. Execute the matrix

Run every row independently. A row passes on its own output, not because a neighbouring row
passed. Record the command and the result for each row.

### 4. Compare

| What to check | How |
|---|---|
| Spec conformance | The matrix above. This is the primary result, not one row among many. |
| Goal vs diff | Does the diff implement what was intended? Missing pieces? Extras? |
| Invariants | Did any change touch a hot-invariant boundary from `AGENTS.md`? Is it preserved? |
| Docs fidelity | Design: does output match `design-system.md` tokens/typography/components/concepts? Dev: does the implementation follow the spec/ADR? |
| Regressions | Did your own run break anything new — vs. the base commit, not vs. the dev's claimed run? |
| Code health | Run every check in `../dev-task/references/code-quality.md` over the same diff. Structure only — the file names its own scope limits. |

### 5. Classify

- **✅ matches** — spec row checked, and the check passed on your own run.
- **⚠️ gap** — docs say X but diff doesn't implement it (or differs with no recorded decision).
- **🔴 regression** — test broke, invariant violated, or build fails.
- **🟡 quality** — works and matches docs, but the structure makes the next change harder
  (change amplification, speculation, orphan, swallowed error, wrong seam). Cite `file:line`.
- **❓ uncertain** — docs are silent, change is ambiguous, or can't verify without user.

Run the matrix and all six comparison rows in this one context over the gathered inputs —
do not fan out per dimension. Every dimension needs the same full diff and docs, so
subagents would only re-gather what you already hold. Only for a very large diff, delegate
**file slices** (each slice runs the matrix and comparisons on its files) to subagents;
hand each slice its diff and doc paths — never let a subagent re-run the suite or
re-discover docs. Synthesize the verdicts.

## Done

- **Report the matrix.** Every row gets pass, fail, or `unverifiable` with a reason. A
  review is not clean while any acceptance criterion is unmet or unchecked.
- Every claim checked against actual diff and your own fresh runs, with a verdict per
  finding.
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
