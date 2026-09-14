# code-quality — the house code-health bar

Read before writing code in a dev task; verify against it in a review. Both skills point
here, so this file is the single source of truth for what "well-structured" means here.

**The bar:** the diff must not lower code health. Working code is not enough — code is
read and changed far more than it is written, so structure is judged by the next reader,
not the author.

Grounded in: Google's code-review standard (the most important thing to cover is the
overall design; is it more complex than it should be?), Ousterhout's complexity symptoms
(change amplification, cognitive load, unknown unknowns; "working code isn't enough"),
and Code Complete's cohesion/coupling checks.

## Checks

Run all twelve over the diff. Every finding cites `file:line` and names the concrete fix.

| # | Check | Fails when |
|---|---|---|
| 1 | Change amplification | One logical change touched many files, or makes a foreseeable small change touch many. → the boundary is in the wrong place. |
| 2 | Cohesion | A unit does unrelated things; its name or summary contains "and". Split on the axis of change. |
| 3 | Coupling | Callers reach into internals; a lower layer imports upward; editing one module forces edits in unrelated ones. |
| 4 | Indirection & depth | A wrapper, interface, or abstraction with one implementation and no test seam — or an interface as complex as what it hides. Delete it or deepen it. |
| 5 | Speculation | Config, flag, param, or extension point nothing sets today. Delete; add it when a second caller exists. |
| 6 | Naming | Names describe mechanism, not intent (`data2`, `handleStuff`, `Manager`); a name and its body disagree. |
| 7 | Error paths | Swallowed exceptions, catch-all handlers, no path for empty / missing / unauthorized / timeout. |
| 8 | Comments | Comment restates the code. Keep *why*, constraints, and non-obvious invariants; delete narration. |
| 9 | Duplication | Copy-pasted logic that must now change in two places. Same shape ≠ same concept — don't merge on shape alone. |
| 10 | Orphans | This change left unused imports, params, branches, or config behind. |
| 11 | Consistency | Differs from the file's existing style or the project's linter/formatter config. |
| 12 | Tests as structure | The test reaches into private internals or needs heavy mocking — the seam is wrong, not the test. |

## Scope limits

- **Not a finding:** formatting, syntax taste, or anything the project's formatter or
  linter already enforces. Defer to repo tooling and `AGENTS.md`.
- **Not a finding:** pre-existing dead code this diff didn't create. Mention it; don't
  require it.
- **Not this file:** correctness bugs, performance, and security — separate concerns
  with their own gates.
