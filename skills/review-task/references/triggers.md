# Review Task Progress Triggers

Read this when deciding when to call task-core progress scripts.

## Update Progress When

- Context gathering completes (task state, defining docs, actual diff, verification
  evidence).
- A gap is found between the defining docs and the actual changes.
- A regression is confirmed and traced to a specific change.
- An invariant from `AGENTS.md` is found to be violated.
- Evidence from the cursor fails to reproduce on fresh run.
- The drift gate (`../task-core/scripts/check`) fails.
- All claims are checked and the final verdict is ready.

## Evidence Examples

Good verification evidence:

- `../task-core/scripts/check` exits zero (cursor synced, all pointers valid).
- `npm test` passes all 42 tests, 0 failures (fresh run at `<sha>`).
- `git diff main...HEAD --stat` shows 3 files, 47 insertions, 12 deletions — all
  within the goal's scope.
- `design-system.md` §Colors tokens match the rendered output: `#1a1a2e`, `#e94560`,
  `#0f3460` all present in `styles/theme.css`.
- `AGENTS.md` hot invariant "never serialize raw passwords" is preserved — no new
  serialization paths touch the User model.

Not verification evidence:

- "I read the diff and it looks fine."
- "The cursor says verified so it must be."
- A stale test run from before the change landed.
- Assertions about design fidelity without comparing actual rendered output against
  the artifact.
