# Triage — the 80 open gaps, grouped into cards

Drafted 2026-09-27, at `32ca4a65`. Groupings come from the files each gap's goal names, plus the
themes; **read each gap before carding it** — a goal is a claim at the time it was written, which is
the whole lesson of the round that produced this file.

## How to use this

- **Grouping, not batching.** Ordinary dispatches are serialized per cap (the unresolved-dispatch
  gate), so one-by-one is already the default. A card carrying several related defects is the cheap
  win — that is how `gate-integrity-1` (three defects) and today's `gap-check-and-query` (A+B+C) were
  built. A `batch` fan-out is the *only* parallelism available, and it runs on machinery with five
  open defects (`cap:t-5c11ab3e`, `t-8153023b`, `t-24159072`, `t-f3189eeb`, `t-5cab4ed0`). Do not
  batch a cluster before proving the path with a 2-member batch on a scratch repo.
- **One card per cluster**, in the order below. Dispatch serially.
- **Each card gets a review** before close — `fs dispatch --type review-task --reviews <cap node>`.
  Budget for it: today's round spent roughly half its dispatches on reviews and fix rounds.
- **Entries marked DECISION are yours**, not a worker's. Do not card them until you have ruled.

---

## CARD 1 — the teardown and branch seams  (do this first)

The densest cluster and the only one that can destroy the repo. `t-be00ae44` is one wrong
`$FS_WORKTREE_PATH` from deleting `main`, and I dispatched around it all session.

| gap | |
|---|---|
| `t-be00ae44` | **SEVERE** — branch teardown deletes `main` when the path is the project root |
| `t-7dcb1dd0` | both teardown branch steps refuse **silently** on an unnamed status |
| `t-a9a891a0` | `branch-decision.sh` refuses (3) for a worktree on an unborn branch |
| `t-9c2ecbc6` | the branch step identifies the project's checkout **by position** |
| `t-5d0d1ee6` | the branch step can only name a worktree's branch while its checkout exists |
| `t-6d95b3a2` | the two teardown `do` commands are duplicated verbatim across two playbooks |
| `t-24159072` | `batch.yaml` reads a member's **workspace id as its path** |
| `t-587119f8` | `resolve-path.sh` folds a trailing `.`/`..` to the **empty string** |
| `t-e3e9c281` | `resolve-path.sh` strips a trailing slash once, before the fold |
| `t-f641306b` | `resolve-path.sh` — found by the round-five review |
| `t-91e4bb8f` | a commit message claims a resolve-path suite result that the suite does not show |
| `t-1b9e2465` | `$FS_BRANCH` was added to the close's exit env; neither doc that enumerates it gained it |

Files: `defaults/scripts/resolve-path.sh`, `defaults/scripts/branch-decision.sh`,
`defaults/kb/code.yaml`, `defaults/kb/batch.yaml`, the two cleanup playbooks.

## CARD 2 — the check re-verification surface  (today's own)

All four are in `internal/command/check.go` or one hop from it. Card them together because they are
one design question: what a recorded check means and what a reader may conclude from it.

| gap | |
|---|---|
| `t-703188f9` | `"$FS_BIN"` in a check exits **127** under every reader — the convention every card teaches |
| `t-b36a5a72` | no timeout on a re-run; `fs gaps` now shells out per node, so one hang stalls the cap's first read |
| `t-55d46f0d` | the reader count is kind-dependent; the remaining sites must go count-free |
| `t-275a1d9f` | a third route into `running` where nothing was read |

## CARD 3 — contracts, locks, and spec precision

Split into the mechanism and the prose if it grows; both are `.agents/`-facing.

| gap | |
|---|---|
| `t-7c8551d6` | **`lock.sh` destroys a locked contract on a wrong argument — three times** |
| `t-01e8d176` | `check-specs.sh` cannot validate a rotation, so exit 0 is not evidence |
| `t-a82d6712` | a second lock regime failing in the canvas skill |
| `t-0940a7ba` | `playbook-mechanism.md:195` describes the residual hole imprecisely |
| `t-a9ab6988` | `playbook-mechanism.md` carries counts the audit deleted |
| `t-f27c2ee7` | `playbook-mechanism.md:187` states the branch postcondition's blind spot wrongly |
| `t-bbcca0c8` | `fs-logging.md` AC4 promises what the code now does |
| `t-80a8677d` | `fs-logging.md` — `SCOPE:NODE` splitting |
| `t-408b5fce` | the record around `t-80a8677d` is stale in two places |
| `t-856aa199` | `cap-role.md`'s "cap-scope writes require the claim" is unimplemented |
| `t-afdfcb4f` `t-7d2c2d36` `t-c3b9463c` | **DECISION** — the untracked-contracts knot (see below) |

## CARD 4 — dispatch, deliver, and worktree delivery

| gap | |
|---|---|
| `t-5cab4ed0` | `--deliver` does not wait for the pane's shell prompt → intermittent agent-start failure |
| `t-f3189eeb` | a failed worktree delivery abandons the wrong pane and **leaks the git worktree** |
| `t-de5e4ae7` | refuses when the task type exceeds 12 characters |
| `t-86bf15d3` | `--type` never checked against herdr's agent-name grammar |
| `t-94394a96` | **panic** when the worker node id exceeds 31 characters |
| `t-10ea8b5b` | the two emitters of `herdr agent start` disagree; three docs say they are one |
| `t-6f8f4a82` | a dead second builder of `herdr agent start` |
| `t-89a9d14e` | a worktree under a symlink is refused |
| `t-769abde2` | `WorktreeRootPane` compares paths as strings |
| `t-87ef6746` | a flag before the positional is accepted, but `args[0]` is read as the node id |
| `t-67da3ad2` | `--deliver`'s effect is undocumented |
| `t-42844313` | realization refusals double their own prefix |
| `t-e371064d` | a refusal line can name the wrong project |
| `t-306ecd1d` | nothing owns the environment between dispatches |

## CARD 5 — the review life cycle

| gap | |
|---|---|
| `t-911fd5a3` | a done review satisfies the close gate **forever**, even for later commits |
| `t-500c6fc5` | a fix round cannot be dispatched while the dispatch it fixes is open |
| `t-4c9a3169` | the missing-protocol signal does not exist |
| `t-cdb7c127` | a reviewer edited the artifact under review |
| `t-1f463635` | the dev-task exit gate cannot tell whether the deliverable reached the repo |
| `t-3abeb7f9` | a dispatch record misdescribes what the spec-side batch moved |

## CARD 6 — playbooks and the worker contract

| gap | |
|---|---|
| `t-cc0ec26f` | four prerequisite playbooks missing |
| `t-0ff6b1cc` | **SEVERE** — `fs kb add` writes to the live global scope (+ DECISION on the fix shape) |
| `t-4c9a3169` | (also CARD 5) the missing-protocol signal |
| `t-6c1e8d33` | auto mode on a non-Anthropic gateway emits a blocking modal |
| `t-5a657e75` | the shipped default worker cannot survive an undismissable notice |
| `t-7343eee8` | a gap filed in the wrong scope because a worker's pane cannot write to the cap's |
| `t-407711d8` | task-agent's end step can no longer dispatch an integration |
| `t-bf33461a` | task-agent dispatches `integrate` with no `--integrates` |
| `t-d3d613c3` | an inline `#` on a step line is not stripped |
| `t-ea4bf9ca` | the shipped default's synthetic identity can collide with a real path |

## CARD 7 — readers, status, and the backlog

| gap | |
|---|---|
| `t-fce0e913` | a node's record shows a superseded claim before its correction, unmarked |
| `t-5a64f687` | readers do not use the identifier path (`fs log --node skills:t-x` returns empty) |
| `t-296d9ebb` | nothing surfaces the conjured nodes |
| `t-44060833` | `fs query help` prints usage instead of searching for "help" |

## CARD 8 — herdr, and the unproven batch/integrate path

| gap | |
|---|---|
| `t-5c11ab3e` | the batch teardown has **never run against a real worktree** |
| `t-8153023b` | the integrate exit gate has never run through a live close |
| `t-f0b63443` | a dead worker has no resume path |
| `t-dd8f5272` | socket listener for `pane_agent_status_changed` — retires snapshot polling |
| `t-82434260` | the herdr skill does not document the socket event stream |

**`t-5c11ab3e` is the gate on batching.** Until a real 2-member batch against a scratch repo has
been observed tearing down its worktrees, branches and workspaces, do not fan out.

## CARD 9 — infra, tooling, registry

| gap | |
|---|---|
| `t-495af92d` | the freshness check couples any in-flight worker's edit to every other close |
| `t-d82b1a2a` | the freshness check is bound to a dispatch's exit phase only |
| `t-29b0a2a4` | the nix-store `fs` predates the tree |
| `t-160fed66` | `flake.nix`'s path literal is copied from tracked files only |
| `t-bbe2ce71` | `cmd/args.go` is not gofmt-clean |
| `t-a6ee89ed` | `orchestrator/` is dead code that breaks AC9's grep |
| `t-a479942f` | DEBUG coverage is one call site |
| `t-7bd2c63b` | no `fs project remove`; two junk registry rows are permanent |
| `t-572e7773` | no scope enumeration or registry rebuild |

## CARD 10 — other projects

| gap | |
|---|---|
| `t-7febe328` | librarian `write-entry.py` has no file-based input |
| `t-41363375` | no in-process Agent tool, so librarian work loses per-angle fan-out |
| `t-49bcbdb2` | canvas: a `.answer` block flattens the page |

---

## Decisions — yours, not a worker's

1. **Should `.agents/` be versioned?** `t-afdfcb4f`, `t-7d2c2d36`, `t-c3b9463c` are one decision
   wearing three node ids: `.agents/specs/` and `.agents/dispatch/` are untracked and not ignored,
   so a lock marker cannot be honoured by any other checkout and can never name the commit that
   contains it. Today's re-lock is uncommitted because of this. Until you rule, every re-lock is
   local only.
2. **How should `fs kb add` be made safe?** `t-0ff6b1cc`. `--dry-run`, a write-time warning when the
   new playbook would match an existing dispatch type, or fixing the doc's example. The gap argues
   `--dry-run` is the one that would have prevented it.
3. **How should the release be built?** `t-29b0a2a4` and `t-160fed66` — the nix-store binary vs the
   tree, and flake path literals copying tracked files only.
4. **What is the ground truth for a `--type`?** `t-3f10f041` — research, not a diff.
