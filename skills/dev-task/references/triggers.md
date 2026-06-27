# Dev Task Progress Triggers

Read this when deciding when to call task-core progress scripts.

## Update Progress When

- A goal is accepted or materially reframed.
- A failing or characterizing test is found.
- Implementation changes land.
- Verification passes, fails, or cannot run.
- A blocker changes the plan.
- The final answer is about to claim completion.

## Evidence Examples

Good verification evidence:

- `pytest tests/test_auth.py -q` passes.
- `npm test -- --runInBand` fails on a named existing unrelated test.
- GitHub Actions check `build` is green for commit `<sha>`.
- Manual reproduction no longer reproduces, with exact steps listed.

Not verification evidence:

- "Looks right."
- "I inspected the code."
- "Should work."
- A stale command from before the change.
