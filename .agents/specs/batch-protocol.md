<!-- specs:locked:ef930d50 2026-10-11 type=spec -->

## Link contract
- **upstream** (this doc relies on): playbook-system.md, playbook-mechanism.md
- **referrers** (must cite this when they change): none

# Spec — a batch: fan out into worktrees, merge one at a time, clean up as one thing
Type: spec · Owner: cap · Lock state: the marker above, nowhere else

Upstream: this doc assumes the model in [`playbook-system.md`](playbook-system.md) — playbooks matched by tags, plans, the trace — and the realization fields of [`playbook-mechanism.md`](playbook-mechanism.md). It adds a *unit* those docs do not have.

## Problem & who breaks

- **Who:** the user running more than one task against one project at once; and the cap co-ordinating it.
- **Break 1 — the workflow's first step is in no playbook.** Nothing creates a worktree. `parallel-entry` only *lists* them (`herdr worktree list`), and `fs dispatch --worktree WS` only *names* an existing one, so the cap creates them by hand (gap `t-eab3196b`). The only instruction is one `say` in `cap.yaml`: *"every card names the resources its worker may open — pane, tab, workspace, worktree, branch — and says to remove and verify each"*. A four-step workflow held together by the cap's memory.
- **Break 2 — the teardown is declared twice, byte-identically.** `parallel-exit` and `integration-exit` share 6 of their 8 steps verbatim: the branch delete, the worktree removal, the `herdr workspace close`, and their three postconditions. Filed as `t-6d95b3a2`.
- **Break 3 — nothing knows what a batch is.** An ask exists only because no node holds the fan-out: *"is this the next merge in order"* cannot be answered without knowing which members are already integrated, and nothing tracks that — the cap holds it in its head. A batch node makes the **unintegrated set a fact**. It does not make the *order* a fact (the order is the user's policy) and it does not make the *size* a fact (that is a judgement about a person's attention). One of the two asks becomes answerable rather than blind; neither becomes unnecessary.
- **Break 4 — the two situations overlap without being one thing.** `parallel` (a member's dispatch) and `integrate` (a member's merge) are phases of a single workflow split across two names, so neither name describes what the user is doing.
- **Cost of doing nothing:** every fan-out is re-derived by hand; a resource forgotten at the start is discovered at the end; and the "all done" that decides when to clean up exists only in a person's head.

## Non-negotiable outcome

- **A batch's resources are all removed once every member is integrated, and "is this batch complete?" is answerable from the store rather than from memory.**
- Falsifiable: a 2-member batch where both members are integrated closes and leaves no worktree, branch or herdr workspace behind; the same batch with one member unintegrated **refuses to close, naming that member**. A tester passes or fails this without arguing.

## Scope in / out + primary flow

**In**

- **`kind: batch`** — a cap-scope node that holds a fan-out. Its `node_id` is what a member names as its parent, so `parent_node_id` carries the membership and `fs status` already renders it as a tree.
- **The batch is created by a dispatch** — `fs dispatch --project P --type batch --cards a,b,c` — so it has an **entry gate** like everything else that matters. `fs task add` has no gates, and the cards' disjointness check and the reviewability ask have to run somewhere; they run here, **once for the batch** instead of once per member. The batch dispatch is not delivered: it has no worker of its own, and the node it leaves is the parent.
- **`fs dispatch --batch <node>`** — records the member as that batch's child. A member is otherwise an ordinary dispatch.
- **`fs dispatch --worktree-new`** — the mechanism creates the worktree (`herdr worktree create`), records the workspace and checkout path on the delivery record exactly as `--worktree` does today, and the existing exit machinery removes it. Gap `t-eab3196b`.
- **`fs batch <node>`** — a read with an exit code, mirroring `fs integrated`: exit 0 iff every member of the batch has a `done` integration naming it, naming the unintegrated ones otherwise. This is the fact that replaces an ask.
- **`batch`** (playbook, `applies_when: type=batch`, `engine: herdr`, `creates: batch`) — the fan-out's own protocol, one file with two sections. Its **`entry:`** is the one gate over the cards: `test -d .git`, `herdr worktree list --cwd .`, `"$FS_BIN" script check-parallel.sh $FS_CARDS` (their file sets are disjoint), and the reviewability ask — asked **once** for the fan-out instead of once per member. Its **`exit:`** is **the one place the fan-out's teardown is declared**, gated first on `fs batch $FS_NODE`, then every member's branch, worktree and workspace, then the postconditions.
- **`member`** (playbook, `applies_when: member`) — one file for the whole member. Its **`work:`** is what a member's worker is told: you work in your own worktree, you never touch main, your branch is merged by a dispatched integration, and your tree outlives your own close. Its **`exit:`** is the member's own gate — `fs integrated $FS_NODE`, then the ask that this result was read from its worker node. **No teardown**: the six shared steps live in `batch`.
- **`integration`** (playbook, `applies_when: type=integrate`) — one file for the merge: its member-scoped gate at `entry:`, the suite question and its ask at `exit:`. It carries no teardown, and no longer declares `engine: herdr` — it lost the steps that needed it. The suite is asked about rather than run because the command that runs a project's tests is the project's; a repo that has one answers with a playbook of its own scope, matching the same `type=integrate`.
- **The migration of today's four gates** — nothing is deleted without its replacement being named:

| today | becomes |
|---|---|
| `parallel-entry` | `batch`'s `entry:` section — same checks and ask, `applies_when: type=batch`, asked once for the fan-out instead of once per member |
| `parallel-exit`, first two steps | `member`'s `exit:` section — the `"$FS_BIN" integrated` gate and its ask |
| `parallel-exit` + `integration-exit`, the shared six | `batch`'s `exit:` section — declared once, scoped to the batch's members |
| `integration-entry` | `integration`'s `entry:` section — the merge's gate, member-scoped as it already is |
| `integration-exit` | `integration`'s `exit:` section — keeps the suite question and its ask, **loses the six** to `batch` |
| — | `member` — work guidance and the member's gate in one file, new |
- **A member's tree is owned by the batch, not by the member.** `code`'s declared teardown — its `exit:` section — defers when the dispatch belongs to a batch, so a member closing does not remove the tree the batch is about to assert over. The mechanism is one marker: `FS_BATCH` is exported to exit steps when the delivery belongs to a batch, and the shared teardown guards on it. One declaration, one deferral, reviewable as a line.

**Out (non-goals)**

- **Not a batch of batches.** One level of fan-out. A nested batch is a different design.
- **Not the merge algorithm.** A merge is a dispatched `integrate`, one at a time, as today; this doc does not change how a merge is performed, only who owns the batch's lifecycle.
- **Not a change to `--cards` or to the disjointness script's behaviour.** The cards remain the members' briefs and `check-parallel.sh` remains the check; only *where it runs* changes — the batch's entry, once, instead of every member's. Its *address* has since changed twice — from `bin/check-parallel.sh` to `"$FS_SCRIPTS/check-parallel.sh"`, because the batch is dispatched into any project and the old path resolved in one, and then to `"$FS_BIN" script check-parallel.sh` when the materialized copy went away and the script was read out of the binary instead — which is a move, not a redesign: the script's body is unchanged both times.
- **Not the reviewability ask.** *"Is the batch small enough that the merges are reviewable one at a time"* stays a question for the user — a node cannot judge a person's attention, and pretending otherwise would turn a real judgement into a rubber stamp.
- **Not the merge order.** The batch makes the unintegrated set readable; it does not choose which member merges next. That order is the user's policy, and a mechanism that invented one would be deciding something it was not asked to.
- **Not a scheduler.** Nothing starts a merge on its own; the cap dispatches them, in the order the user set.
- **Not a change to `fs integrated`.** The member-side gate keeps working exactly as it does; `fs batch` is the aggregate of it.

**Primary flow (one path, step by step)**

1. The cap opens the batch: `fs dispatch --project P --type batch --cards a,b,c --goal "…"`. Its `entry:` section runs — git, the worktree listing, the cards' disjointness, the reviewability ask — and leaves `kind: batch` node `t-batch`. Not delivered; no worker; the node is the parent.
2. Per card: `fs dispatch --project P --type dev-task --tag code --batch t-batch --worktree-new --deliver`. The mechanism creates the worktree, records it on the delivery, and the plan includes `member`.
3. Each member works in its own tree; its node goes `done`. Its close waits — a member's exit gate is `"$FS_BIN" integrated $FS_NODE`, which is false until its merge lands.
4. Per member, one at a time: `fs dispatch --project P --type integrate --tag batch --integrates t-member --deliver`, and close it. The merge consumes the branch; the tree survives.
5. When `fs batch t-batch` exits 0, the cap closes the batch: `fs close --node t-batch --decision "…" --confirm`.
6. The batch's `exit:` section runs: the gate `"$FS_BIN" batch $FS_NODE`, then each member's teardown — branch, worktree, workspace — then the postconditions, all scoped to the batch's members.
7. The members close; their own exits find their trees already gone, which is the goal state and not a failure.

## Acceptance criteria

- **AC1 — a batch is a dispatched node, and membership is structural.** `fs dispatch --type batch --cards …` creates it as `kind: batch` and runs its entry gate; `fs dispatch --batch <node>` records a member's `parent_node_id`; `fs status` renders the fan-out as a tree under the batch. A `--batch` naming a node that is not a batch node is refused, naming what it is — an immutable event cannot be corrected afterwards, so the link is refused at the point it would be written.
- **AC2 — a batch does not block its own members, and they do not block each other.** The unresolved-dispatch gate counts `kind: dispatch`, and exempts a dispatch that declares itself to be about the open node it names — over the relations the command line already parses. `--integrates` and `--reviews` name one node each; `--batch` names a node whose open children are the dispatch's siblings. A `batch` node is not a `kind: dispatch`, so opening a batch never blocks the members that give it meaning; and the `--batch` relation exempts a member's own open dispatch for its siblings, so a fan-out's members dispatch one after another with no `--allow-unresolved`. The relation is read from the flags and never searched for in the goal text — a node id written into a goal exempts nothing, and a flag naming a node that does not exist is refused before anything is written.
- **AC3 — `--worktree-new` creates and records.** The worktree exists after the dispatch, the delivery record carries its workspace and checkout path, and the exit machinery removes it without further flags.
- **AC4 — `fs batch <node>`** exits 0 iff every member has a `done` integration naming it; otherwise it exits 1 naming every unintegrated member. A batch with no members is refused rather than read as complete (vacuously true is not complete).
- **AC5 — the batch's exit gate.** `fs close` on a batch whose `fs batch` is not satisfied refuses, naming the unintegrated members, **before** any teardown runs.
- **AC6 — the teardown happens once, and the batch owns it.** With every member integrated, closing the batch removes each member's branch, worktree and herdr workspace, and their postconditions pass. Closing it again is a no-op, not an error. A member closing on its own leaves its tree in place.
- **AC7 — the deferral is visible.** `FS_BATCH` is exported to exit steps for a dispatch that belongs to a batch and absent otherwise; `code`'s `exit:` teardown defers when it is set. A `dev-task --worktree` **outside** a batch still tears down at its own close — the fix for `t-33799786` must not be lost to this change.
- **AC8 — it builds and it passes.** `nix build`, `go test ./...` under `src/flagship`, `bash tests/run.sh` green, with tests for `fs batch`, the `--batch` refusal, and the deferral.

## KPIs & failure signal

- **Good:** a fan-out whose resources are all gone when the batch closes, with the batch's trace showing the gate, the teardowns and their postconditions — and the unintegrated set read from the store rather than recalled.
- **Off-switch:** a worktree, branch or herdr workspace surviving a closed batch. That is the leak this doc exists to end, and it is detected by the batch's postconditions — if one ever passes with a resource still present, the postcondition is wrong, not the resource.

## Non-functional requirements

- **Ownership is single and stated.** A batch owns its members' trees; a member does not. The deferral is one marker and one guard, and it must be visible in the playbook rather than implied by a naming convention.
- **Idempotence.** Every teardown step treats "already gone" as the goal state, as the existing ones do, so a batch closed twice, or closed after a member was cleaned by hand, is not an error.
- **A refusal is inert.** The batch's gate refuses before any teardown runs, so a batch that cannot close is a batch whose trees are still there to look at.
- **Cost:** one `fs batch` read per batch close; the teardown is the same shell work the two exits did, done once instead of twice.
- **Compatibility:** `--worktree` (naming an existing workspace) keeps working unchanged; `--worktree-new` is additive. A dispatch with neither is unaffected, and its exit steps see no `FS_BATCH`.

## Assumptions & dependencies

| # | Assumption | Invalidated by | Then |
|---|---|---|---|
| A1 | `herdr worktree create` produces a workspace whose root pane sits at the checkout, as `WorktreeRootPane` expects | the created workspace has no such pane | the delivery refuses rather than falling back to splitting the cap's pane — the existing `ErrNoRootPane` path already does |
| A2 | One integration per member is enough to call it integrated | a member needs re-merging after a conflict | `fs integrated` returns the *first* done integration, so a second merge changes nothing; the batch gate follows `fs integrated`, so it stays consistent |
| A3 | The members' cards name their resources, so the batch's assertion has a known set to check | a card omits a resource | the postconditions are per-member from the **delivery records**, not from the cards — a card is prose and cannot be asserted on |
| A4 | A batch's members are all in one project | a fan-out spans projects | `fs batch` reads cap-scope nodes, and each member's delivery names its project, so the read still works; the teardown would then span roots, which is untested |

## Data requirements

- **Inputs required:** the batch node (cap scope); per member, the delivery record carrying `Worktree`, `WorktreePath`, `PaneID` and `Project` — the same fields the existing teardown already reads.
- **Outputs produced:** the membership (`parent_node_id` on each member), the worktree (created by `--worktree-new`), and the batch's own trace — whose plan is the batch's, not any member's.
- **Must survive:** the batch node and its children outlive the worktrees. The batch's trace is the record that its resources were removed, and it outlives the trees as all traces do.
- **Empty record:** a batch with no members is refused by `fs batch` (AC4) and by the close gate, so "complete" is never vacuously true.

## Rollback & recovery

- **Revert:** the shipped playbooks ride in the binary and revert with the commit — a copy on disk is transparent unless it is an override, so nothing has to be re-seeded; `fs kb reset <name> --yes` writes a default back over an override, or the file is deleted. The code reverts by commit; the `batch` kind is additive to `ValidKind`, and existing nodes are unaffected.
- **"Reverted" means:** `kind: batch` is refused as an unknown kind again, `--batch`/`--worktree-new` are refused as unknown flags, and a batch node left in the store is inert — a node of a kind nothing reads.
- **The one failure that would wreck it:** a batch that reports complete while a member is unintegrated, or that tears down a member whose branch was never merged. Both are the same defect — trusting a summary instead of each member's own gate — which is why `fs batch` is defined in terms of `fs integrated` rather than re-deriving integration itself.

## Security & compliance

- **Destructive actions:** the batch's teardown deletes branches and trees, but only for members whose integration is `done`, and only after the gate passes. A member whose branch is not merged refuses (the existing `git merge-base --is-ancestor` step), so a batch cannot delete unmerged work.
- **Provenance:** the batch's trace names every member's teardown, and the delivery records name the resources, so "what did this batch remove" is answerable after the fact.
- **N/A:** no personal data, no network egress, no credential.

## Verification plan

| Requirement | Proven by |
|---|---|
| AC1 batch node + membership | `fs status` on a scratch batch showing the tree; the `--batch` refusal on a non-batch node |
| AC2 no self-blocking | `TestPrepareGatesOnOtherOpenDispatchesButNotOnItsBatchSiblings` in `src/flagship/internal/dispatch/dispatch_test.go` — a sibling dispatches while a member is open, a goal-text-only mention exempts nothing, and an open dispatch outside the batch still refuses and is named |
| AC3 `--worktree-new` | a scratch HOME with a fake `herdr` on `PATH`: the create is called, the record carries the workspace and path |
| **AC4 + AC5 the fact and its gate (the non-negotiable)** | 2 members, one integrated: `fs batch` exits 1 naming the other; `fs close` on the batch refuses naming it; then integrate the second, `fs batch` exits 0, and the close succeeds |
| AC6 teardown once, idempotent | the same cycle, then a second close; and a member closed alone leaving its tree |
| AC7 the deferral | a `dev-task --worktree` outside a batch still tears down; inside one it does not |
| AC8 build + suite | `nix build`, `go test ./...`, `bash tests/run.sh` |
| the design's reason for existing | the diff: the six shared steps are gone from where they were declared twice and appear once, in `batch`'s `exit:` section |

- Independent check: a dispatched `review-task` reads the worker's node against **this locked doc**, and is asked specifically whether a batch can report complete while a member is unmerged, and whether the `t-33799786` fix survives the deferral.
