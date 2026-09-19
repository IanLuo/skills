---
name: task-agent
description: Hand a task to a NEW agent working on its own git worktree of the same project, then collect and merge the result. Two modes, both run from the main checkout — start (create worktree, write task card, spawn the worker via herdr, hand off) and end (verify, review-task, then dispatch the merge to a worker on its own card and close it). Use for "hand this task to an agent", "spawn a task agent", "worktree task", "run this in its own worktree", "parallel task", "delegate to a new agent session". Do NOT use for in-process subagents (use delegate), research fan-out (use librarian), or external agents in panes without a worktree (use herdr).
metadata:
  audience: personal
  domain: agent-orchestration
compatibility: Requires git worktrees and herdr running (HERDR_ENV=1) to spawn the worker. Worker kind is a parameter (pi, claude, codex, …).
---

# task-agent

Hand a task to a NEW agent working on its own worktree of the same project, then
collect and merge the result. The coordinator owns everything — both modes run in
the **main checkout**; the worker is a pure executor that just implements the task
card in its worktree and commits. Isolation is the safety: the worker never touches
main.

Merging is a worker's job too, not the coordinator's: `end` dispatches an
`integrate` task on its own card and reads the verdict on that worker's node. The
coordinator decides the merge order and answers the entry gate's asks; it never
runs `git merge` by hand. A hand merge puts the batch's integration cost in the
coordinator's own context, which is exactly what dispatching exists to prevent.

Two modes, both invoked by you in the main checkout:

| mode | what it does |
|---|---|
| `start` | create worktree, write task card, spawn worker, hand off |
| `end` | verify, review-task, then **dispatch the merge** to a worker on its own card and close it (or `--abort`) |

## start

1. **Create the worktree** (from main, project cwd):
   ```bash
   git worktree add .worktrees/<task> -b <task>
   ```
2. **Write the task card** at `<worktree>/TASK.md` — the durable state `end` reads.
   Follow the house doc format (`../skill-man/references/doc-format.md`): bullets, no
   padding, explicit N/A:
   - **Goal** — one line, the deliverable
   - **Acceptance criteria** — testable bullets; this is the review gate
   - **Owned files** — exactly which files the worker may touch; everything else read-only
   - **Constraints** — don't touch shared files, don't touch the main checkout
3. **Spawn the worker** into the worktree via herdr (agent-agnostic):
   ```bash
   P=$(herdr pane split --current --direction right --cwd "$PWD/.worktrees/<task>" --no-focus | jq -r '.result.pane.pane_id')
   herdr agent start <task> --kind pi --pane "$P"     # pi | claude | codex
   herdr agent prompt <task> "Implement the task in TASK.md. Work only in this worktree; commit when done." --wait --timeout 30000
   ```
   The worker needs no skill — it just reads the card and works.

## end  (you run this when the worker is done — the merge itself is dispatched)

1. **Read the card** → find the worktree + branch (`.worktrees/<task>`, branch
   `<task>`). If `<wt>/TASK.md` is missing, stop and ask.
2. **Sanity check.** Refuse if the worktree has uncommitted changes (worker still
   running): `git -C <wt> status --porcelain`. If dirty, say so and wait (or `--abort`).
3. **Review.** Run `review-task` against the card's ACs — the independent gate. Only
   a pass is worth integrating.
4. **Dispatch the merge on its own card.** The merge is a worker's task, not yours;
   your part is to decide the order and answer the gate. From main:
   ```bash
   fs dispatch --project <project> --type integrate --goal "merge <task> into main"
   ```
   The entry gate checks the member's tree and its branch still exist, and asks you
   to confirm the order and that main has no foreign staged changes — answer it.
   Deliver the brief (`--deliver`): that worker merges, runs the suite, and deletes
   the branch and worktree. Then **read the worker's node** — the node, not the
   dispatch's success, is the truth about whether the merge happened — and close it:
   ```bash
   fs close --node <cap-node> --decision "<verdict from the worker's node>"
   ```
   `fs close` runs the `integrate-cleanup` exit gate (suite green, no linked
   worktree left, no unmerged branch left) before it marks the dispatch done. The
   gate is about the whole batch: integrate a batch's members in one dispatch — or
   have merged and torn down every earlier member — before closing, or it refuses.
5. **Confirm the teardown.** `fs close` removes the worktree the delivery names and
   the integration worker deletes the branch. If either remains, the exit gate
   refuses — fix it rather than closing anyway.
### end --abort

Task failed or abandoned — remove the worktree + delete the branch, no merge, no review:
```bash
git worktree remove <wt>
git branch -D <task>
```

## Rules

- **Coordinator-only.** Both modes run in the MAIN checkout. The *task* worker never
  merges or touches main — that's the isolation guarantee. The merge is a separate
  worker's job, dispatched by the coordinator on its own `integrate` card.
- **`end` is yours to drive, not to do.** No auto-done signal — you decide when the
  worker is done, then dispatch the integration, read the verdict on its node, and close.
- **Task card is the contract.** `end` reads `<wt>/TASK.md`; missing card = stop and ask.
- **Short-lived.** Keep the branch <24h — integration cost is ~zero under a day,
  superlinear after.
- **Review before merge.** `end` runs review-task; no merge on failure (that's what `--abort` is for).
- **Scope:** in-process subagents → delegate · research fan-out → librarian · external
  panes without a worktree → herdr.
