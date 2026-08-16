---
name: task-agent
description: Hand a task to a NEW agent working on its own git worktree of the same project, then collect and merge the result. Two modes, both run from the main checkout — start (create worktree, write task card, spawn the worker via herdr, hand off) and end (manual, verify, review-task, merge back, clean up). Use for "hand this task to an agent", "spawn a task agent", "worktree task", "run this in its own worktree", "parallel task", "delegate to a new agent session". Do NOT use for in-process subagents (use delegate), research fan-out (use librarian), or external agents in panes without a worktree (use herdr).
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

Two modes, both invoked by you in the main checkout:

| mode | what it does |
|---|---|
| `start` | create worktree, write task card, spawn worker, hand off |
| `end` | manual — verify, review-task, merge back, clean up (or `--abort`) |

## start

1. **Create the worktree** (from main, project cwd):
   ```bash
   git worktree add .worktrees/<task> -b <task>
   ```
2. **Write the task card** at `<worktree>/TASK.md` — the durable state `end` reads.
   Follow the house doc format (`skill-man/references/doc-format.md`): bullets, no
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
4. **Record** in SESSION.md:
   `<date> · task-agent · started <task> in <wt>. Next: /task-agent end when done.`

## end  (manual — you run this when the worker is done)

1. **Read the card** → find the worktree + branch (`.worktrees/<task>`, branch
   `<task>`). If `<wt>/TASK.md` is missing, stop and ask.
2. **Sanity check.** Refuse if the worktree has uncommitted changes (worker still
   running): `git -C <wt> status --porcelain`. If dirty, say so and wait (or `--abort`).
3. **Review.** Run `review-task` against the card's ACs — the independent gate. Merge
   only on pass.
4. **Merge back** (from main):
   ```bash
   git -C <wt> rebase main                # replay worker's commits onto current main (local branch, safe)
   git merge --no-ff <task>               # keep the branch's history
   # or: git merge --squash <task> && git commit    # one clean commit
   ```
5. **Clean up:**
   ```bash
   git worktree remove <wt>
   git branch -d <task>
   ```
6. **Record** in SESSION.md:
   `<date> · task-agent · merged <task> (<sha>).`

### end --abort

Task failed or abandoned — remove the worktree + delete the branch, no merge, no review:
```bash
git worktree remove <wt>
git branch -D <task>
```

## Rules

- **Coordinator-only.** Both modes run in the MAIN checkout. The worker never merges
  or touches main — that's the isolation guarantee.
- **`end` is manual.** No auto-done signal — you decide when the worker is done and run `end`.
- **Task card is the contract.** `end` reads `<wt>/TASK.md`; missing card = stop and ask.
- **Short-lived.** Keep the branch <24h — integration cost is ~zero under a day,
  superlinear after.
- **Review before merge.** `end` runs review-task; no merge on failure (that's what `--abort` is for).
- **Scope:** in-process subagents → delegate · research fan-out → librarian · external
  panes without a worktree → herdr.
