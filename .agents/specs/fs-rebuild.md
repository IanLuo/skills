<!-- specs:locked:5d9392e 2026-10-31 type=spec -->

## Link contract
- **upstream** (this doc relies on): none
- **referrers** (must cite this when they change): AGENTS.md, skills/flagship/SKILL.md, .agents/specs/fs-rebuild-design.md

# Spec — fs: shared task coordination for isolated agents
## R1 Problem & who breaks
- **The user breaks:** describing work and finding out what agents are doing requires a coordinator-only ledger, dispatch/close ceremony and status trapped in fs-specific records.
- **Agents break:** they can share task context or claim the same work, and a review can inherit the developer's assumptions instead of judging the change independently.
- **The user and agents break:** project state and history can be duplicated in a coordination tool instead of being retrieved from shared memstore.

## R2 Non-negotiable outcome
- **Reliable, shared coordination for isolated agent tasks.** The user can add work; an agent can discover and atomically claim one task; one stable agent session can act on only that task; agents and the user can query coordination status; every development task is reviewed as a separate task in a fresh context; and a task cannot be reported complete without independent checks and evidence.
- **Falsifiable:** concurrent agents cannot both hold one task; a session bound to task A cannot write to task B; the reviewer has a different session identity from the developer; and completion without a successful independent check is refused.
- Context separation does not prove an agent is truthful. fs reports only evidence and checks it actually received or ran.

## R3 Scope in / out + primary flow
- **In:**
  - Add a task with a project/repository scope, goal, acceptance criteria and any required check command.
  - List eligible tasks and atomically claim one task through memstore.
  - Bind a unique agent-session identity to one task for that session's lifetime. Refuse task-changing operations for another task before writing.
  - Record the minimal task and related-agent coordination state needed to assign work, renew a lease, report progress, query status and verify completion.
  - Provide read-only fs commands for an agent to query its assigned task/project status and for the user to query aggregate fs work and agent status.
  - Require a separate review task for every development task. A reviewer runs in a fresh agent context and separate worktree, receives the task contract, diff and bounded evidence, and does not receive the developer transcript or conclusions.
  - Record the reviewer identity, exact check commands, exit results and bounded/redacted evidence. A failed or unrun required check leaves the task incomplete.
  - Use memstore's supported API for every persistent read and write. Use an fs-owned namespace within shared memstore. Require no memstore change: exclusivity is composed from memstore's existing refusal of a duplicate `(origin, origin_id)`.
  - Add no fs-owned schema, direct SQL, second store, local index or cache.
- **Out:**
  - A cap, single coordinator lease, dispatch/close event ledger, fs-owned database, persistent cache, direct SQL into memstore, or alternate persistent source of coordination state.
  - fs starting or managing agent processes in V1. The user or harness starts each agent; agents pull work through fs.
  - A visual dashboard, web server, TUI, remote coordination service, accounts, multi-user authorization, containers/VMs, automatic task decomposition, and automatic task planning.
  - Migration, import, archival, deletion, or re-entry of the legacy `~/.fs/store.db`. That data is not part of the new fs.
  - Duplicating project state, project history or durable project knowledge that already comes from memstore.
- **Primary flow:**
  1. User creates a task with a non-empty goal, project scope, acceptance criteria and required check details.
  2. Harness starts a fresh agent session and supplies a unique, stable session identity.
  3. Agent queries eligible work through fs; fs reads its namespace through memstore.
  4. Agent atomically claims one eligible task. Memstore prevents another session from claiming it concurrently and binds the session to that task.
  5. Agent works in the task's isolated worktree and reports progress/evidence through fs. Each mutation verifies the session/task binding.
  6. Agent reports development complete with evidence. This does not mark the task complete.
  7. A fresh reviewer session claims the separate review task in a separate worktree. It receives the contract, diff and bounded evidence, not the development session's transcript/conclusion.
  8. Reviewer independently runs required checks and records exact commands, exit results and bounded/redacted evidence.
  9. fs records completion only when required independent checks pass. Otherwise the task stays incomplete and the review records the reason.
  10. User and agents query current work and agent status through read-only fs commands backed only by memstore.
- **Task split:** one task is one unit of work. fs does not auto-split a broad request in V1; the user or harness creates separate tasks.

## R4 Acceptance criteria
- **AC1 — task creation.** Given a valid project/repository scope and a non-empty goal, when the user creates a task with acceptance criteria, fs stores it through memstore and returns its identifier. An empty goal or missing scope is refused without creating a record.
- **AC2 — exclusive claim.** Given one eligible task and two distinct sessions attempting to claim concurrently, exactly one claim succeeds. The other receives a clear already-claimed/no-eligible result. The decisive step is a single `Put` whose duplicate refusal is atomic inside memstore, not an fs read-then-write sequence.
- **AC3 — one task per session.** Given session S bound to task A, when S attempts any task-affecting operation on task B, fs refuses before writing and identifies A as the existing binding. This remains true after A completes. A fresh session can claim B when eligible.
- **AC4 — status queries.** Given coordination records in a project namespace, an agent can query only the task/project authorized by its binding; the user can query aggregate fs work. Results identify task state, bound session, agent-reported status, runtime-observed status, each status source and timestamp/freshness. An expired heartbeat is returned as unknown, never as live, idle or done.
- **AC5 — fresh review.** Given a development task, its review is a distinct task with a distinct session identity and a separate worktree detached at the reviewed commit, which is discarded after review. The review input contains the contract, diff and required bounded evidence; it does not contain the developer transcript or developer conclusion. The reviewer runs required checks independently.
- **AC6 — evidence-gated completion.** Given a development result, when completion is requested, a missing reviewer, missing required check, or non-zero check result leaves the task incomplete. The record includes the exact command and exit result for every required check. A worker's self-reported completion alone is insufficient.
- **AC7 — lease expiry and recovery.** Heartbeat interval is 30 seconds and a lease expires after 120 seconds without renewal. Given an expired lease, a new session may claim the still-incomplete task; the prior session may no longer write. Expiry never marks work complete. Attempt history remains available through memstore.
- **AC8 — memstore boundary and failure.** Given fs and non-fs records in shared memstore, fs reads and writes only its namespace and returns no non-fs records. If memstore is unavailable, times out or returns an ambiguous write result, fs does not fall back to another persistent source or make a claim from stale data. An ambiguous operation is resolved by querying memstore with its operation identity before retrying.
- **AC9 — task/status append history.** Task creation and completed task history are not reopened or overwritten by fs. New work is a new task. Current status and lease operations use only documented memstore lifecycle APIs; fs does not mutate records in place or bypass memstore.

## R5 KPIs & failure signal
- **Correctness targets:**
  - Zero duplicate successful claims in barrier-controlled multi-process concurrency tests, including repeated runs and process restarts.
  - Every task-changing operation from a session bound to another task is refused before a write.
  - Every development task has a distinct review task/session and worktree before completion.
  - No task is complete without the required independently run checks and recorded exit results.
  - No expired agent heartbeat is presented as live, idle or done.
- **Performance target:** claim and status query p95 below 1 second at 10,000 task records on the supported local host. This is provisional until measured against the chosen memstore API and representative data.
- **Off-switch:** stop assigning work if any duplicate active claim occurs, a cross-task write succeeds, completion bypasses independent review/checks, a stale agent is reported live/done, or fs reads/writes outside its memstore namespace.
- The barrier test proves the tested claim path; it is not a statistical reliability claim about production.

## R6 Non-functional requirements
- **Concurrency:** multiple local agent processes may contend. Exclusive claim and session binding are atomic through memstore's supported API. Exactly one concurrent claim wins. fs fails closed if the API is unavailable or the result is uncertain.
- **Lease recovery:** heartbeat every 30 seconds; expiry after 120 seconds without heartbeat. Expiry means unknown/reclaimable, never done. A stale session cannot write after a new session claims the task.
- **Isolation:** a stable unique session identity is supplied by the harness. One identity may be bound to one task for life. Review uses another identity and worktree. fs can enforce identity/task binding but cannot inspect hidden conversation context; fresh-context creation is a harness responsibility and dependency.
- **Data boundary:** memstore is shared infrastructure used by multiple services. fs is a client, not owner. It uses only supported API operations and reads/writes only its namespace. It owns no database, schema, private fork or persistent fallback.
- **Source of project state:** project state/history are retrieved from memstore. fs does not duplicate project state/history in its coordination records.
- **Durability and append semantics:** fs does not rewrite or reopen completed task records. New work is a new task. Any state/lease transition uses memstore's documented lifecycle operation. No fs-owned event ledger is authoritative.
- **Evidence:** store exact check commands and exit results plus only bounded, redacted excerpts needed for coordination. Do not store full transcripts, secrets, credentials or unbounded tool output.
- **Performance:** measure the provisional R5 p95 target. A performance issue does not permit a second store, stale-read assignment or direct SQL bypass.
- **Deployment:** V1 is local, single-user and offline at runtime. No remote service or multi-user authorization is required.

## R7 Assumptions & dependencies
- **Memstore's single unique key.** Exclusivity rests on `Put` refusing a duplicate `(origin, origin_id)` inside its own transaction. fs composes both invariants from that one primitive by writing two rows with deterministic keys, so no memstore change is required. *Invalidated by* a memstore release that removes or weakens that refusal, makes it a pre-check outside the transaction, or makes `origin_id` non-comparable — then fs must stop assigning work until the primitive is restored.
- **Shared memstore.** Other services use the same memstore file. fs may touch only its own namespace and must not rely on private schema. If namespace isolation cannot be enforced by the supported API, stop and revise the API before fs work proceeds.
- **Harness identity.** The agent harness creates a fresh context per task and supplies a stable unique identity. If it cannot provide this identity or create an independent review session, fs cannot enforce one-task-per-session or prove the review context is separate; do not claim those guarantees.
- **Worktree isolation.** Development runs in a writable worktree. Review runs in a disposable worktree detached at the reviewed commit (`git worktree add --detach <sha>`): no branch is created, the reviewer reads only committed state, and the checkout is discarded after review. If a worktree cannot be provided, review remains blocked rather than running in the developer's checkout or context.
- **The worktree cost is accepted, not optimised away.** One working directory cannot hold two conflicting live branches, so a shared-checkout branch tool (for example a virtual-branch client) cannot supply this isolation. The cost to bound is the build/import cache, not the checkout: `git worktree add` is cheap, and the review worktree needs no build cache because it only runs the task's declared checks.
- **Local host.** One user/machine and local memstore are the V1 deployment. If multiple users or remote agents are required, re-open the security and coordination design before adding them.
- **Legacy data.** The existing `~/.fs/store.db` and its disposition are outside this specification. No assumption is made that the new fs reads, imports, archives, or deletes it.
- **Checks.** Each development task provides acceptance criteria and required checks that a reviewer can run. If no independent check can be named, the task cannot meet the evidence-gated completion outcome and must be clarified before work starts.

## R8 Data requirements
- **Inputs:** project/repository scope; task goal; acceptance criteria; required check commands; harness-provided session identity; memstore's fs namespace; agent status and heartbeat updates; development diff and bounded evidence for review.
- **Coordination data fs may store:** task identity and assignment; task type (development/review); project scope; bound session identity; task/agent status with source and timestamp; lease/heartbeat and attempt information; related development/review task references; required checks and exact results; bounded/redacted evidence necessary to determine whether completion criteria passed.
- **Project data:** project state, history and durable project knowledge are retrieved from memstore and are not copied into fs-owned state.
- **Outputs:** task identifier; claim/refusal result; status-query result; progress/evidence record; independent review/check result; completion only after required checks pass.
- **Must survive:** task and review records; assignment/attempt history; status provenance; successful and failed check results needed to explain completion or refusal. All are stored through memstore's supported API.
- **Finished task:** remains finished and is not reopened. New work is represented by a new task. Historical coordination records are not re-entry candidates in this scope.
- **Empty/invalid record:** empty task goal or missing project scope is rejected without writing. Missing session identity is rejected for any claim or task-affecting agent operation. Missing/expired heartbeat is unknown, not success.
- **Legacy data:** import, migration, archival, deletion and re-entry of `~/.fs/store.db` are explicitly outside scope.
- **Growth:** no full transcript or unbounded command output is stored. Retention and compaction of memstore records are governed by memstore's own policy; fs does not prune shared records.

## R9 Rollback & recovery
- **Fail closed:** memstore unavailable, claim timeout, namespace error or uncertain write never falls back to an fs database or stale cached state. No new claim or completion is reported until the result is resolved through memstore.
- **Ambiguous operation:** every state-changing request uses an operation identity supported by memstore. On timeout/connection loss, fs queries by that identity before retrying. It never blindly repeats a claim, report or completion that may have committed.
- **Expired agent:** lease expiry makes an incomplete task eligible for a new session. The previous session loses write authority. The task remains incomplete; no result is inferred from process disappearance.
- **Partial review/completion:** incomplete or failed required checks leave the task incomplete with the reason recorded. Recovery is a new coordination operation through memstore; it does not erase history or mark checks passed.
- **Release rollback:** reverting fs code does not delete or rewrite shared memstore data. The legacy event DB is untouched by this product. No automatic rollback may remove records another memstore client can own.
- **One failure that would wreck the product:** two sessions both receive authority for one task, or a session can write to a task other than its binding. Either violates the core safety property; stop assignment until fixed.

## R10 Security & compliance
- **Trust boundary:** V1 is one local user. Agents run with the user's local permissions. fs is not a sandbox and does not protect local files from a same-user agent.
- **Authorization:** agent session identity limits coordination operations to its bound project/task; user-level aggregate status is available to the user/coordinator. This is a coordination rule, not a security boundary against a process running as the same user.
- **Shared-store isolation:** fs uses memstore's supported API and filters every operation to its own namespace. It does not read or alter another service's records.
- **Secrets and privacy:** never persist credentials, secrets or complete private conversation transcripts. Persist only task text, status and bounded/redacted check evidence required for coordination.
- **Provenance:** status carries its source and timestamp; check evidence identifies reviewer session, command and exit result. An agent assertion is not represented as an independently verified fact.
- **Destructive actions:** no legacy DB deletion or import is in scope. A completed task is never reopened or overwritten; new work creates a new task.
- **Compliance:** N/A for the stated local single-user use. No regulatory requirement was supplied; do not invent one.

## R11 Verification plan
- **AC1:** fs integration test with valid, empty-goal and missing-scope cases; assert the invalid cases create no memstore record.
- **AC2:** memstore API barrier test using concurrent processes against one task; exactly one succeeds. Repeat under race detector and after process restart.
- **AC3:** fs integration test binds session S to A, attempts every task-affecting operation on B, and proves refusal plus byte/state equivalence for both records. Prove a fresh session can claim B after eligibility.
- **AC4:** status-query tests for authorized project, unauthorized project, aggregate user query, missing heartbeat and expired heartbeat; assert status source/timestamp and no false live/done state.
- **AC5:** harness integration test starts development and review in distinct fresh sessions/worktrees; assert the review worktree is detached at the reviewed commit and is removed afterwards; inspect the actual review input to prove it contains contract/diff/evidence and excludes developer transcript/conclusion. Do not claim this proves hidden context content beyond the harness boundary.
- **AC6:** tests for no reviewer, missing check, failing check, and passing independently run check. Only the passing case permits completion; exact command and exit result are stored.
- **AC7:** fake-clock or controlled-time lease tests at 30s heartbeat and 120s expiry; old session refusal after reassignment; expiry never marks done; attempt history preserved.
- **AC8:** seed fs and non-fs namespaces; run every fs read/write and assert only fs namespace changes/returns. Inject memstore unavailable, timeout and ambiguous-write results; assert no fallback and identity-based resolution.
- **AC9:** verify completed task cannot be reopened; new task receives a new identity; no fs path imports or manages `~/.fs/store.db`.
- **R5 performance:** benchmark claim/status at 10,000 task records on the supported local host; report p95, dataset shape and hardware. If the provisional target fails, review the API/index design; do not add an fs-side store.
- **Build quality:** memstore suite and fs suite, race/concurrency tests, vet, format and package builds must pass. Build a fresh clone and exercise the documented CLI.
- **End-to-end:** on disposable/copied state, run one user-created development task with two competing agents, query status, complete the development task, start a different-context review agent, execute checks, query final status and confirm no duplicate claim or cross-task write.
- **Independent review:** a fresh reviewer reads this locked spec and the final diff, runs the acceptance checks independently, and may reject the result. The implementer cannot mark its own review passed.

## Supersedes
- **Supersedes:** `.agents/specs/fs-contract.md` as the requirements contract for new fs behavior. Its cap, event-ledger, dispatch/close and launch-config requirements do not govern the rebuild.
- **Refines:** `.agents/specs/fs-state-store.md` and the store-layer decision recorded on `skills:t-b1a2e486`: fs uses memstore as a client, through its supported shared API, for coordination data; no fs-owned projection, second state store, or copied project history is part of this product contract.
- **Does not govern:** memstore's generic API implementation details, the historical data in `~/.fs/store.db`, or how that legacy file is eventually archived/deleted. Those require separate decisions/specifications.

Last reviewed: 2026-10-31 · requirements elicitation
