<!-- specs:locked:7be9f0a4 2026-10-21 type=spec -->

## Link contract
- **upstream** (this doc relies on): none
- **referrers** (must cite this when they change): skills/dev-task/SKILL.md, skills/review-task/SKILL.md, skills/init-context/SKILL.md

## R1 Problem & who breaks
- The coordination layer does not keep the three promises fs makes, and it fails in silence.
- **cap** — its own read can report clean while not-done nodes stand outside the cap scope; its instructions to a worker reach a pane, not a node.
- **worker** — started with no declared environment, so it dies on a missing provider route and nothing says so; no resume path when it dies.
- **user** — every round pays ceremony (`--confirm`, `--also`, card paths, marker rotations, plan and gate and close); 41 of 50 open gaps describe that layer rather than the work.
- Evidence: sessions of 2026-10-19 and 2026-10-20; the gaps are on disk.

## R2 Non-negotiable outcome
- **One record.** Events are append-only. Every node is derived from them.
- **One path.** Every state read answers from exactly one source.
- **That source is the event log.** memstore leaves fs's read path. It stays a published library with its own roadmap.
- **Accepted cost:** fs is no longer memstore's demonstration.
- **One document.** A fresh reader learns fs from this page.

## R3 Scope in / out + primary flow
- **In** — the ledger (append-only events, one file); nodes (task, gap, decision, knowledge, review); the 8 verbs (`task add|get|update`, `report`, `dispatch`, `close`, `pending`, `unfinished`, `gaps`, `query`); the cap lease; dispatch = worker node + rendered brief + herdr delivery; close = evidence line + pane teardown.
- **Out** — the playbook resolver and its 13 playbooks; entry/work/exit plans and the gates they run; traces as a separate file; `check-specs`, `check-deliverable`, `check-parallel`, binary-freshness gating; the projection, the parity gate, `state verify|rebuild`, `FS_MEMSTORE`; the six superseded specs; worktrees and the batch/member/integration protocols.
- **Primary flow** — resume → `pending` → `dispatch` (node + brief) → worker acts in a pane → `report` → `close` with evidence.
- **The line that does the work** — `dispatch = node + brief + delivery`. It keeps the coordination and drops the protocol engine.

## R4 Acceptance criteria
- **AC1** the 8-command budget: resume ≤2, open ≤3, hand off + close ≤5. Proved by a scripted pass on a fresh HOME.
- **AC2** no override in the happy path: no `--confirm`, no `--also`, no card path, no marker. Proved by the same pass, recorded command by command.
- **AC3** one source per read: `fs --help` has no `state`; `grep -r FS_MEMSTORE` in the tree returns nothing.
- **AC4** a dispatched worker starts from this doc's launch config with no playbook present. Proved by one live dispatch with the args read off the brief.
- **AC5** the suite is green and the ledger's tests survive intact: `go test ./...` over store, query, command, dispatch.
- **AC6** open gaps fall to the survivors only: `fs gaps` count ≤12.

## R5 KPIs & failure signal
- commands to resume and see what is open: 5 with overrides → ≤5, zero overrides.
- documents needed to explain fs: 19 (6 specs, 13 playbooks) → 1.
- open gaps: 50 → ≤12.
- CLI verbs: 40 → ≤10.
- `fs pending`: 28 ms → ≤100 ms.
- **Brakes — stop, do not work around.** A deletion step needs a second attempt to go green. A step grows a new flag to work. A read needs a second source again. A second document appears "for clarity".

## R6 Non-functional requirements
- **determinism** — a read derives from the log alone; the same log gives the same answer.
- **performance** — `pending` ≤100 ms at 5,000 events; linear growth accepted.
- **durability** — append-only; no step migrates data; `git revert` restores every step.
- **concurrency** — one writer at a time is an assumption and stays a filed gap, not a sentence this contract makes true.
- **portability** — one user, one machine, no network at runtime.
- **testability** — the suite is the only oracle; no gate stands between a change and its proof.

## R7 Assumptions & dependencies
- The harness stays herdr + pi with the magpie provider route.
- One user, one machine, one set of repos.
- **The suite is trustworthy** — load-bearing: if it is not, stop and fix the suite before any deletion.
- Deleting a layer never touches the event log's data.
- The gap split was measured on 2026-10-20 and the provisional claim was wrong: **27 gaps went moot** with their layer, **21 survive** — 17 of them fs's own (build, `lock.sh`, the ledger's addressing and record, the registry, coordination) and 4 that belong to other tools (canvas, librarian, the herdr skill document, memstore's own comment).
- A fresh HOME needs no seeding.
- **Dependency fs cannot control** — the provider credential behind the launch config. The route is frozen here; the key is not.

## R8 Data requirements
- **Record** — one SQLite file `~/.fs/store.db`, table `events`, append-only: `id · timestamp · type · project_id · node_id · parent_node_id · commit_sha · payload`. Nodes are derived; there is no node table.
- **Volume** — 4,951 events, ~10 MB. No pruning. Replay is linear.
- **The brief** — rendered text carried in the dispatch's own event payload, so a worker's instructions live on a node.
- **Launch config, frozen here and not in a playbook** — `engine=herdr` · `agent=pi` · `args=--provider magpie --model opencode-go/deepseek-v4.1-flash`. A fresh agent gets this wrong without being told; it did.
- **The old projection** `~/.memstore/memory.db` — fs stops writing its rows. Nobody deletes them. Deleting is a memstore operation that needs an explicit request.
- **Migration** — none. The event format does not change in any step.
- **Secrets** — none stored. The provider route is a name and a path.

## R9 Rollback & recovery
- One commit per step; `git revert <sha>`.
- A revert restores the code and the verbs. Data never moved, so there is nothing to restore on that side.
- **Guard before every commit** — suite green, `go vet` clean, `gofmt -l` silent, the 8 verbs exercised. A red suite is never committed.
- **"Reverted" means** — `fs --help` lists the deleted verbs again; the suite is green at the earlier commit; no event differs.
- Abandoning mid-plan leaves a working fs; stopping after any step is a legitimate end state.
- **The one irreversible act** — forgetting memstore rows. It is not in the plan.

## R10 Security & compliance
- `N/A:` local, single-user, single-machine; no network at runtime; no credential stored; no personal data beyond task text the user already owns.
- The one boundary worth stating: the brief travels into a worker pane on the same machine and is as trusted as the terminal.

## R11 Verification plan
- **Each step** — `go test ./... -count=1`, `go vet ./...`, `gofmt -l`, the 8 verbs exercised.
- **The simplification** — the R4 scripted pass on a fresh HOME.
- **The KPIs** — the round-0 baseline against the end state, as a table.
- **One source** — `fs --help` has no `state`; `grep -r FS_MEMSTORE` is empty.
- **The launch config** — one live dispatch with no playbook present.
- **The honest gap** — "a fresh reader learns fs from one page" is an `ask:` in the final review, not a check. A plan claiming to machine-check it would be the false gate this contract deletes.
- **Independence** — one review at the end, against this doc.

## Supersedes
- `.agents/specs/batch-protocol.md` · `cap-role.md` · `fs-logging.md` · `fs-state-store.md` · `playbook-mechanism.md` · `playbook-system.md`
- Superseded by this document. They stay on disk, marked superseded, until the final step, so no citation dead-ends.

Last reviewed: 2026-10-20 · cap
