# Task-Core Protocol

Use this when creating a domain task skill or updating the shared task scripts.

## Shared Spine

Every task type has the same state spine:

- Goal: what the agent is trying to finish.
- Progress: current phase plus next action.
- Blockers: what is stuck and why.
- Open issues: unresolved assumptions or decisions.
- Health: green or broken with known broken items.
- Verification: claimed-done and verified-done evidence are separate.
- Decisions: one present-tense line per invariant.
- Active pointers: existing files or commands that carry the state.

## Domain Overlay

Domain skills decide:

- What phases exist.
- What artifact is durable.
- What counts as verification evidence.
- When to update progress.

Examples:

- `dev-task`: durable artifact is the test suite and code diff; evidence is command
  output, CI, or executable checks.
- `design-task`: durable artifact is `design-system.md` plus accepted concept images;
  evidence is visual QA, fidelity ledger, screenshot comparison, or user approval.
- `research-task`: durable artifact would be cited findings; evidence would be source
  citations and notes about verification freshness.

## Cursor Discipline

- Read the cursor before starting work.
- Rewrite it in place at the end of a session or after a phase change.
- Keep it under the local cap if the project defines one.
- Store only forward-looking state that a fresh agent could get wrong.
- Prefer pointers to artifacts over copied artifact contents.
- Verify every file path before recording it.
- Run `../task-core/scripts/check` at session start and before committing a
  rewritten cursor. The drift gate exits non-zero on stale pointers or a synced
  SHA that is behind or absent from git history — the exact cross-tool failure
  mode where an agent confidently resumes already-shipped work. Prose discipline
  is guidance; a non-zero exit is a gate.

## Evidence Gate

Never collapse "claimed" into "verified" by prose. To mark verified, quote fresh
evidence in the task state:

- command and result for development,
- screenshot/fidelity/user-approval evidence for design,
- cited sources and retrieval dates for research.

If evidence is absent, record the result as claimed and add the missing check under
open issues or blockers.
