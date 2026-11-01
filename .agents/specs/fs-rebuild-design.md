<!-- specs:locked:5d9392e 2026-10-31 type=system-design -->

## Link contract
- **upstream** (this doc relies on): .agents/specs/fs-rebuild.md
- **referrers** (must cite this when they change): AGENTS.md, skills/flagship/SKILL.md

# System design — fs coordination over shared memstore
## R1 Components & ownership
- **User** — owns task intent and authorization: goal, project scope, acceptance criteria, the checks a reviewer must run.
- **fs CLI** — owns coordination *policy*: validation, the session binding, the claim protocol, the completion gate, status formatting. Refuses to start processes, inspect conversation contents, run project checks, or keep any local record.
- **memstore** — owns durable rows and one atomic primitive: `Put` refuses a duplicate `(origin, origin_id)` inside its own transaction. fs adds **no memstore API, table, column or migration**; the rebuilt fs is a pure client of what exists today.
- **Harness/runtime** — owns a fresh context and worktree per task and a stable unique session identity. Refuses to reuse an identity across tasks.
- **Development agent** — owns one claimed task. Refuses any task it is not bound to; cannot verify its own work.
- **Review agent** — owns one review task, in a separate context and a detached worktree. Receives contract, diff and bounded evidence; refuses the developer transcript or conclusion.
- **Repository and check tools** — the subject of the work and the executor of checks. Refuse to be a coordination store.

## R2 Data model
- Every fs row lives in `scope = "fs:<project>"`, with `origin = "fs"` and `origin_id` = its coordination key. No other scope is read or written.
- **Four kinds, one job each:**

| kind | the row asserts | `origin_id` | `extra` |
|---|---|---|---|
| `fs.task` | this work exists | `task:<tid>` | goal, checks[], project_path, parent_dev, goal_hash |
| `fs.bind` | this session is used up | `session:<sid>` | task, session |
| `fs.claim` | this task is held at round N | `claim:<tid>:<n>` | task, session, round, ttl_s |
| `fs.event` | something happened | `ev:<tid>:<sid>:<n>` | state, role, command, exit, digest, note |

- **`fs.bind` exists to be refused.** It is written once per session, before any claim, so a second task for that session fails on memstore's own unique index. A session is spent by construction, not by remembering to check.
- **`fs.claim` exists to be refused for the same reason.** `claim:<tid>:<n>` is a key every racer computes identically, so exactly one insert wins and the loser is told by `ErrDuplicateOrigin`.
- **Liveness is derived, never written.** A claim of round *n* is live while some row by its session is newer than `now − ttl_s`. There is no "expire" write, no cleanup job, and no flag two readers can disagree about.
- **The keepalive is any write.** `fs report` is the heartbeat; there is no separate renew verb and no separate heartbeat row.
- **Completion is terminal.** `fs done` closes the task row via `CloseMemory`; a closed task is never reopened. Re-work is a new task.
- **Empty record refused before writing:** no goal, no project scope, no declared check, no session identity.
- **Rejected:** caching derived state, a local index, a second store, direct SQL, an fs-owned schema.

## R3 Boundaries & interfaces
- **The claim protocol — the only intricate part, and the one place a "simplification" breaks safety.** `fs claim`:
  1. `Put` `session:<sid>` → duplicate means the session is spent: refuse, write nothing else.
  2. Read the task's rows: current round = max seen; is round *current* live (by the rule above)?
  3. Live by another session → refuse, naming the holder and expiry.
  4. Live by me → already mine; success, and the `report` that follows keeps it alive.
  5. Otherwise `Put` `claim:<tid>:<round+1>` → duplicate means another racer won: retry from step 2, at most 3 attempts, then refuse.
  6. A failure after step 1 leaves the session burned and holding nothing. That is safe and accepted: the harness starts a new session.
- **Why two rows for one claim.** `Put` enforces exactly one unique key, so "one task per session" and "one holder per task" need one row each. Each row exists to be refused. Collapsing them into one row silently drops one invariant.
- **Never replace the claim with read-then-write.** A stale read may cost a refusal and a retry; it must never be able to produce two winners. A test fails if the protocol stops using `ErrDuplicateOrigin` as its arbiter.
- **fs → memstore surface used: `Put`, `CloseMemory`, `Recent`, `Get`, `Search`.** Nothing else, and nothing new.
- **Who owns time.** fs computes liveness from row timestamps against one host clock. Two hosts would need a different rule; V1 is one host, stated as an assumption rather than discovered later.
- **fs → harness.** The harness supplies `--session <stable-id>` and a worktree path; a claim without a session is refused. Pull-only: fs issues no start command.
- **Harness → fs: runtime status.** A harness that can observe process lifecycle reports it as `fs.event` with `role=runtime`. A harness that cannot leaves it absent, and absence reads as `unknown` — never as alive.
- **fs → reviewer.** The review input is assembled from the task row, the diff and the development task's `fs.event` rows — a fixed field list, not a transcript. Independence is a data shape, not an instruction.
- **fs → user.** JSON envelopes over `fs:` rows only. Aggregate queries cross projects; an agent's query is limited to its bound project.

## R4 Failure modes
- **memstore unavailable, slow, or wrong** — exit non-zero, no claim, no status change, no completion. No queue, no cache, no fallback store. Fail-closed everywhere.
- **Ambiguous write** (timeout after send) — re-attempt the same key: `ErrDuplicateOrigin` means it landed, which is all fs needs to know. No read-back verb, no blind retry of a different key.
- **Two claimers** — serialised inside `Put`; exactly one key inserts. The loser retries with a higher round and then loses the task on liveness, not on luck.
- **Holder dies** — its rows stop being newer than the TTL, so the task frees itself. The task stays incomplete, and the dead session's later writes are refused because its claim is no longer live.
- **Session identity reused** — refused at step 1, permanently, by `fs.bind`.
- **Reviewer cannot build in a detached worktree** — the review worktree runs only the declared checks. A task whose checks need a full build must declare that cost; the reviewer gets no cache.
- **One failure that would wreck the design** — two live claims on one task, or a write from a session that holds none. Either destroys the only property the rebuild provides.

## R5 Scale assumptions
- **Tasks:** 10,000 per project as the sizing case; one project active at a time normally.
- **Agents:** 5–20 concurrent sessions on one machine.
- **Writes:** two rows per claim, plus one `fs.event` per report. At a 30 s report interval that is ~2,880 rows/agent/day — order 10 MB/day for 20 agents. Acceptable for a working period, not indefinitely; compaction is memstore's call and fs must not prune shared rows.
- **Reads:** every read is a scope listing (`Recent`) plus an in-process filter — measured 0.49 s at 60k rows through the CLI, roughly 0.3 s library-side. Linear, so it is a known ceiling rather than a design assumption. **Revisit trigger:** a measured status query above 0.3 s p95, at which point a filtered listing is justified — by measurement, not by anticipation.
- **Derived state costs nothing to keep consistent** because none is persisted: a longer history costs read latency, not a store to repair.

## R6 What fs deliberately does not do
- No cap, lease coordinator, dispatch brief, close evidence line or launch config.
- No process launching, no UI, no TUI, no server, no accounts.
- No import, migration, archival or re-entry of `~/.fs/store.db`.
- No copying of project state or history out of memstore.
- No new memstore API. If the claim protocol ever appears to need one, the first question is whether the protocol is wrong.
- Five verbs total: `add`, `claim`, `report`, `done`, `status`.

## R7 Structure — layers, tree, and the delete list
- **Folded deliberately.** The architecture rung (layers, repo tree, cross-cutting) lives here rather than in a third document: the rebuild is one product, and a reader who needs three pages to learn it will read one. `fs-rebuild.md` is the contract; this page is the whole structure.
- **Layers, one direction — `cmd → coord → memory → memstore`:**

| layer | owns | refuses |
|---|---|---|
| `cmd/` | flags, JSON envelope, exit codes | any policy or record knowledge |
| `internal/coord/` | task shape, the claim protocol, the completion gate, status derivation | importing memstore |
| `internal/memory/` | the only importer of memstore: scope guard, key construction, liveness arithmetic | knowing what a task is |
| `internal/logging/` | diagnostics | gating anything |

- **Why this seam.** The claim protocol is correct only if both racers build the same key. Key construction exists in exactly one package, so there is one place to read and one place to break. A test fails if any package outside `internal/memory` imports the memstore module.
- **The tree:**

```text
src/flagship/
  cmd/            main.go, output.go            flags → coord → JSON; no policy
  internal/coord/ task.go claim.go gate.go status.go
  internal/memory/ store.go keys.go scope.go   the only memstore importer
  internal/logging/ logging.go                 unchanged, reused as-is
```
- **Deleted in the same change** — `internal/command`, `internal/dispatch`, `internal/engine`, `internal/query`, `internal/registry`, `internal/store` (~6,100 non-test lines). Not deprecated, not kept behind a flag: a reader who finds a cap, a dispatch brief or a second store in the tree is right to believe in it, and the previous two rounds of work were spent deleting exactly such dead paths.
- **Three load-bearing decisions:**
  - *Replace in place, delete the old layers in one change.* Constraint: two authorities is the defect being removed. Rejected: a `--legacy` flag or a second binary — same defect with more syntax.
  - *memstore as a vendored dependency, never a copy.* Constraint: fs is a client, and a fork makes it an owner that drifts. Rejected: folding memstore's store package into fs.
  - *Two layers, one seam.* Constraint: the correctness argument is about keys, not about files. Rejected: a single package — it would put scope-building and task semantics in one file, where neither can be tested alone.
- **Dependency cost, measured:** memstore requires `modernc.org/sqlite v1.60.1` and Go 1.27; fs already pins both. Adding it is an addition, not an upgrade, so the driver risk that stalled the last attempt does not exist here.
- **Cross-cutting conventions (one line each, so a new component obeys without a manual):**
  - *Errors:* `{"ok":false,"error":"…"}` with exit 1; a malformed command line exits 2; every failure path is fail-closed.
  - *Logging:* `internal/logging` is reused unchanged, writes ids and counts only, never task text, and never gates a decision.
  - *Scope and keys:* the `fs:` scope prefix and the `session:`/`claim:`/`ev:`/`task:` key prefixes are constants in `internal/memory`; nothing else formats one.
  - *Time:* read through one function in `internal/memory`, against the single host clock.
  - *Tests:* the barrier test and the "no read-then-write" test sit beside the claim protocol, because that is the invariant they defend.
  - *Legacy data:* no code path references `~/.fs/store.db`. Its absence is enforced by the delete list, not by discipline.

Last reviewed: 2026-10-31 · system design
