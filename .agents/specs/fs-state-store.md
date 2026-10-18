<!-- specs:locked:fa13c939 2026-10-18 type=spec -->

## Link contract
- **upstream** (this doc relies on): cap-role.md
- **referrers** (must cite this when they change): skills/flagship/SKILL.md, cap-role.md

# Spec — fs state reads: memstore answers, the event store still decides
Type: spec · Owner: cap · Lock state: the marker above, nowhere else

This is slice 2 of the store-layer decision on `skills:t-b1a2e486`. The card that defines the
round is `.agents/dispatch/slice-2-state-store.md`; its locked inputs are the user's answers, and
this doc is those answers as a requirements contract. Slice 1 already exists and reads nothing:
`internal/store/projection.go`'s `ProjectEvents` folds a scope's events into a memstore file, and
`TestProjectionRealStoreCopy` proved it at parity with `query.BuildTree` on a copy of the live
event store (8 projects, 641 nodes, 0 diffs). Slice 2 switches the answers.

**Word rule.** Where the card says "the log" it means **the event store**, `~/.fs/store.db`.
[`fs-logging.md`](fs-logging.md) owns the word *log* for the running log at `~/.fs/logs/fs.log`,
and this doc does not reuse it. Where this doc says **the projection** it means the memstore file
at `~/.memstore/memory.db`, fs's rows under `Scope = fs:<project>`.

## Problem & who breaks

- **Who:** the cap, whose first act every turn is `fs pending` (absorbed on `cap-role.md`); and every
  later reader — `fs gaps`, `fs status`, `fs unfinished`, `task get`, `close`.
- **Break 1 — fs's state is derived, not stored.** Every read replays `~/.fs/store.db` and rebuilds
  the tree in memory. `fs pending` alone replays the cap scope and then every scope again for the
  backlog; at the live store (cap 2426 events, skills 1921, eight scopes) that is the whole history
  walked to answer one question. Nothing is queryable: a question not foreseen as a verb costs a
  full replay and a new command.
- **Break 2 — the derived tree and the stored state can disagree, and nothing checks.** `BuildTree`
  is a second implementation of "what is true now"; a bug in it is invisible until a reader acts on
  the wrong answer. The slice-1 projection is a third. Two implementations with no comparison is the
  defect the parity gate exists to catch, and today there is no gate.
- **Break 3 — the replacement is unstartable without a switch discipline.** `memstore` is a general
  memory store shared with personal memories in one file. Replacing fs's read layer wholesale, in
  one step, would put every read behind one untested change and could touch another writer's rows.
- **Cost of doing nothing:** the store-layer decision stays parked; fs keeps two state layers and
  pays a full replay per read; and the next slice has no seam to build on.

## Non-negotiable outcome

**`fs pending` answers its state questions from the memstore projection, and the answer is refused
if the projection and `query.BuildTree` disagree on any state question the read asks.** One read is
switched in this slice — `pending` — and it is switched only behind the parity gate. Falsifiable: a
switched `fs pending` returns the projection-derived answer, a deliberate divergence makes it exit
1 naming the difference instead of answering, and `FS_STATE_SOURCE=log fs pending` answers from the
event store again.

## Scope in / out + primary flow

**In**

- **One switched read: `fs pending`.** Its state questions come from the projection; the event store
  is kept as the oracle for the gate. No other read consults the projection in this slice.
- **The parity gate.** A switched read computes its store-derived answer twice — projection and
  `query.BuildTree` — compares them canonically, returns the projection's answer when they are equal
  and refuses when they differ. There is no automatic fallback.
- **The projection write path.** Every event appended to the event store is also folded into the
  projection in the same command step, incrementally, one event at a time. A fold may be a defined
  no-op (a `plan-recorded` event changes no answer any switched read asks).
- **The projection mapping `pending` needs**, added to slice 1's table: `delivery-recorded`, and the
  node's current row represented so a status change is visible. See Decisions D1 and D3.
- **`fs state verify` and `fs state rebuild`** — the drift checkpoint and the repair.
- **Scope safety**, **performance envelope**, **rollback**.

**Out (non-goals)**

- **Slice 3's migration run.** Moving the remaining state (personal memories, the scope default, the
  `kb` path) is slice 3. This slice seeds only the `fs:` scopes a switched read needs.
- **Slice 4's decommissioning of `query.BuildTree`.** In this slice `BuildTree` stays, and stays the
  gate's oracle. A gate whose oracle is the code being replaced is the point of ordering slices 2 and 4.
- **Any change to memstore itself.** fs re-vendors the pinned `7192bce` as **step 0 of the code
  round**; this round writes no code, so the vendored copy stays stale here (A1). fs adds no verb,
  no column, no schema version of its own. Where a memstore surface forces a decision, Decisions
  D1/D3 record it.
- **Any change to what the event store records.** Events, their types, their payloads and their
  ordering are frozen. The projection is derived from them, never the reverse.
- Switching any read other than `pending`. `gaps`, `status`, `unfinished`, `task get` stay on the
  event store; a later card switches each with its own gate.
- A per-read CLI flag (`fs pending --via-projection`) or a source selector in the JSON envelope.
- Reading, writing or counting a non-`fs:` scope in `~/.memstore/memory.db`.
- The `fs kb add` scope default and the lock-gate redesign (`cap:t-57f9fa75`).
- **A single cross-file transaction** — attaching `~/.memstore/memory.db` to the event store's
  connection so a write and its projection commit together. **REJECTED:** both files are WAL
  (`internal/store/store.go:102`; memstore `store.go:400` at `7192bce`), and SQLite makes an
  attached-database commit atomic only in rollback-journal mode, never under WAL, so the mechanism
  cannot deliver what it promises and would only hide the lag it pretends to remove. Do not
  re-propose it.

**Primary flow (one read, step by step)**

1. `fs pending` starts. The switched-read declaration says `pending` answers from the projection.
2. The read derives its store-derived answer from the projection: cap-scope dispatch nodes and their
   statuses, each dispatch's delivery record, each worker's status and reports, the cap claim, the
   open-gap set — every value the event store currently supplies.
3. The gate derives the same value from the event store (`BuildTree`), serializes both canonically,
   and compares.
4. Equal: the projection's value is decorated with the environment-derived fields — herdr's agent
   and pane states, the re-run checks, the herdr-unavailable warning — and returned. The running log
   records `source=projection`.
5. Different: the command refuses — exit 1, an error envelope naming the read, the scope and the
   first differing field, with both values. Nothing is returned from the event store, and the
   running log records the refusal.
6. `FS_STATE_SOURCE=log` short-circuits steps 2-4: the answer comes from the event store, the gate
   does not run, and the log records `source=log`. This is the field rollback, never an automatic one.

## Acceptance criteria

Every criterion names the command that proves it. Go test commands run under `src/flagship/`.

- **AC1 — exactly one read is switched, and it is `pending`.** The switched-read declaration contains
  `pending` and no other read name. Proven by `go test ./internal/dispatch -run TestSwitchedReads
  -count=1`, and live by the running log: `jq -r 'select(.command=="pending")|.source'
  ~/.fs/logs/fs.log | tail -1` prints `projection`, while the same for `status`, `gaps` and
  `unfinished` prints `log`.

- **AC2 — the projection answers every state question `pending` asks, and the gate compares them.**
  Proven by `go test ./internal/dispatch -run TestPendingParity -count=1` over a fixture containing
  every event type `pending` consumes (`task-created` with and without `kind`, `status-changed`,
  `delivery-recorded`, `report-recorded`, `cap-claimed`, and a `kind=gap` node carrying `severity`,
  `check` and `found_by`), asserting the projection-derived value equals the `BuildTree` value and
  that the returned answer is the projection's.

- **AC3 — a difference refuses; there is no fallback.** Against a projection deliberately diverged
  (one row's status changed, one row deleted, one row added, in three cases), `fs pending` exits 1,
  stdout is the error envelope naming the scope and the differing field with both values, and no
  event-store answer is returned. Proven by `go test ./internal/dispatch -run
  TestPendingParityRefuses -count=1`; live, the refusal leaves a WARN or ERROR line naming the read
  and the difference.

- **AC4 — the gate never fires on a clean projection, and the seed makes it clean.** After
  `fs state rebuild`, `fs state verify` exits 0 over every scope in the live event store, and a
  subsequent `fs pending` answers. Proven by the sequence: `"$FS_BIN" state verify` before the seed
  exits 1 (the live projection is empty, which is the first-run state) → `"$FS_BIN" state rebuild` →
  `"$FS_BIN" state verify` (exit 0) → `"$FS_BIN" pending` (exit 0).

- **AC5 — every append projects, in the same step, without ever rolling back the event.** The
  append surface is the **14 event-store sites** at the lock commit — 11 `store.Append` and 3
  `store.AppendBatch` call sites in `internal/command/command.go`. (The card's earlier *28* added the
  14 `trace.Log.Append` sites in `internal/dispatch/{deliver,close,dispatch}.go`; those are the
  **trace**, which stays and is not projected — D1 — so the event figure is 14.) Every site's fold
  lives **in the append path**: `Append`/`AppendBatch` invoke it, a caller does not loop beside
  them. **A batch folds every event it appends — N folds for N events, never one row for the
  batch.** A representative write set — `fs project create`, `fs task add`, `fs task update`,
  `fs task edit`, `fs task block`, `fs task unblock`, `fs task knowledge`, `fs task merge`,
  `fs report`, `fs cap claim`, `fs dispatch` — leaves `fs state verify` clean. Proven by `go test
  ./internal/command -run TestEveryAppendProjects -count=1` (static: no unwrapped call site, and
  every batch site folds per event) and `go test ./internal/dispatch -run
  TestWriteSetStaysAtParity -count=1`. A projection failure must leave the event present and the
  command's write result intact, proven by `go test ./internal/command -run
  TestProjectionFailureKeepsEvent -count=1`.

- **AC6 — scope safety is enforced, not intended.** A scratch memstore seeded with a non-`fs:` row
  (the personal scope `""` and a literal `personal` scope) is byte-identical in its non-`fs:` rows
  after `fs state verify`, after `fs state rebuild`, and after any switched read; no read returns a
  non-`fs:` row; no projection write constructs a scope that does not start `fs:`. Proven by
  `go test ./internal/store -run TestProjectionScopeSafety -count=1` and by `grep -rn '"fs:"'
  internal/store/projection*.go` being the only scope literal in the projection.

- **AC7 — rebuild and verify exist and do what they say.** Removing one projected row makes
  `fs state verify` exit 1 naming that scope and the differing field; `fs state rebuild` restores it
  and a second `fs state verify` exits 0; `rebuild` changes no non-`fs:` row. Proven by
  `go test ./cmd -run TestStateVerifyRebuild -count=1`.

- **AC8 — the switch stays inside the read's envelope.** At the live store (cap 2426 events), the
  median wall time of 20 `"$FS_BIN" pending` runs with the gate on is at most **2×** the median of 20
  `FS_STATE_SOURCE=log "$FS_BIN" pending` runs, and the projection-alone read is at most **1.25×**
  the log-only time. Proven by `for i in $(seq 20); do /usr/bin/time -p "$FS_BIN" pending >/dev/null;
  done` for each arm (warm), and by `go test ./internal/dispatch -run '^$' -bench
  BenchmarkPendingProjectionCost -benchtime=20x`.

- **AC9 — no `pending` answer loses information to a missing memstore column.** `severity`, `check`
  and `found_by` are parsed out of the projection row's `Extra`; the gap count and the check-derived
  status decoration equal the event store's. Proven by the same fixture as AC2 plus
  `fs pending`'s `data.undispatched_gaps` compared field by field: `go test ./internal/dispatch -run
  TestPendingGapFields -count=1`.

- **AC10 — a failed fold heals by re-fold, and the failure tells the user not to retry the
  command.** A fold that fails leaves the event in place and reports `recorded; only the projection
  failed for <scope> — run "fs state rebuild"` (and does not report the write as failed). Re-running
  the fold over the already-projected prefix skips those rows via `errors.Is(err,
  ErrDuplicateOrigin)` instead of erroring; `fs state rebuild` after a failed fold restores parity
  and `fs state verify` exits 0. Proven by `go test ./internal/command -run TestFoldFailureMessage
  -count=1`, `go test ./internal/store -run TestRefoldSkipsDuplicates -count=1`, and the live
  sequence in AC4.

## KPIs & failure signal

- **Good:** after the seed, the parity gate is silent in normal use; `fs state verify` stays clean
  across a working day of writes; `fs pending` answers inside AC8's envelope; the cap never learns
  the source from its answer, only from the log line.
- **Off-switch:** the gate fires on the same scope twice with no intervening write — the projection's
  mapping is wrong, and the read goes back to the event store (`FS_STATE_SOURCE=log`) until it is
  fixed. A second signal: `fs pending` refuses right after an ordinary write — a write site is not
  projecting, and the append surface (AC5) is the place to look. Either firing twice without a fix
  is grounds to revert the switch, not to relax the gate.

## Non-functional requirements

- **The rule that cannot be waived — divergence is a refusal, never a preference.** A parity
  difference is a defect in the projection, the fold, or the read. The command fails closed: exit 1,
  the difference named, no event-store answer returned. Any code path that returns the event store's
  answer because the projection disagreed is a regression against this spec, not a resilience
  feature. The only way to answer from the event store is to say so (`FS_STATE_SOURCE=log`), which is
  a person's decision and is recorded.
- **Ordering — the reading of the card's D4 (`writes append the event and project in the same
  step`) that the two-file fact forces.** The event store and the projection are two files, and **one
  atomic commit across both is impossible**: both are WAL (`internal/store/store.go:102`; memstore
  `store.go:400` at `7192bce`), and SQLite makes an attached-database commit atomic only in
  rollback-journal mode, never under WAL. "The same step" is therefore three properties, not one
  transaction:

  1. **Ownership.** The fold is written by the append path — `store.Append`/`AppendBatch` — in one
     step. It is not a lazy hook, not a background reconciler, and not a caller's loop beside the
     append.
  2. **Order.** The event is appended first and committed; the fold runs second. A crash between the
     two leaves the projection **behind** the event store of that step, never ahead of it. The event
     store is the truth; the projection is derived.
  3. **Heal is a re-fold, not a retry.** The fold keys each row on its event id (D3), and memstore
     refuses a duplicate `(origin, origin_id)` with `ErrDuplicateOrigin`, so an overlapping re-fold is
     safe: `errors.Is(err, ErrDuplicateOrigin)` is **already projected, continue**, never an error.
     That skip is what the provenance round (`f1f4a60`) bought, and it is why the heal is a re-fold
     rather than a hand repair.
  4. **The retry hazard, named.** The event is durable before the fold, so a fold failure means the
     *action is recorded* and only the projection is behind. A user who retries the command appends
     a **second event** and doubles the state. The failure must say so and name the heal — the
     message reads `recorded; only the projection failed for <scope> — run "fs state rebuild"` — and
     must not read as an invitation to retry the command. The command does not report its write as
     failed.
  5. **D2 is the detector.** A lagging projection is not silent: the parity gate catches it at read
     time (the next switched read refuses, naming the difference) and `fs state verify` catches it
     at rest. Keeping the lag in the recoverable direction is what gives the detector something to
     catch and the re-fold something to fix.

  This is also the batch's story: a batch of N events is one atomic append (today's `AppendBatch`)
  followed by N folds invoked from inside that append path, each fold atomic. No row is ever
  half-written, and the projection can lag the batch as a whole — `fs state verify` and `fs state
  rebuild` are what make that visible and repairable.
- **Immutability — the projection only puts, closes and links.** memstore's content is immutable, and
  its only lifecycle writer is `CloseMemory` (which writes `closed`); no verb changes a row's status
  to an arbitrary value, and adding one is out of scope. The projection therefore writes a new row
  for any change to a node's displayed state and closes the row it replaces, and it never updates a
  row in place. See D3.
- **Determinism of the comparison.** The two answers are serialized with `encoding/json` (which
  orders map keys) after sorting each list by its stable key, and compared byte for byte. Values that
  do not come from the event store — herdr's agent and pane states, re-run check results, the
  herdr-unavailable warning — are computed once and applied after the comparison, never compared
  across the two sources.
- **Concurrency.** Two `fs` processes may append to the same scope. The fold of one event is one
  memstore write transaction, so a reader sees the event folded or not folded, never half; the gate
  compares a consistent snapshot of the projection against the event store's committed state. fs is
  the only writer of `fs:` rows (A2), and it is a **concurrent** writer to one shared file — which is
  why the code round re-vendors at `7192bce` first (A1).
- **Performance.** Per AC8. The gate costs one extra derivation of the read's store-derived answer
  while it exists; it is removed in slice 4, when `BuildTree` is decommissioned. If the projection
  alone is slower than the event store for this read, the read is not kept switched past the gate —
  the switch buys an end to the whole-history replay, and a read that regresses is reverted and the
  measurement recorded on the node.
- **Scope and secrets.** The projection writes only rows whose `Scope` starts `fs:`, and reads only
  those. It copies event payloads that the event store already holds and nothing else — no credential
  value, no command output, no file content. The memstore file is the user's, mode `0700`/user-owned,
  and is not shipped, logged or networked.
- **Compatibility.** `fs pending`'s JSON envelope is unchanged: the same keys, the same states, the
  same `counts`, `undispatched_gaps`, `cap`, `warning`. A consumer that parses today's output parses
  the switched output. The `source` field is added only to the running log line.

## Decisions this spec settles

The card left seven questions; each is a frozen answer here.

- **D1 — fields memstore has no column for.** `severity`, `check` and `found_by` stay in each row's
  `Extra` as the raw event payload, and reads parse `Extra`; memstore's schema is not extended. fs's
  `found_by` (a `PROJECT:NODE` address) is deliberately *not* memstore's `found_by` column, which is
  the memory-graph "found by" relation — a different sense that a shared column would corrupt. **No
  `pending` answer loses a value this way.** The fields are present but unindexed, so a read that
  *filters or sorts* on them scans rather than indexes; `pending` neither filters nor sorts on them
  (it counts gaps and re-runs the `check`). The first read that would feel this is `fs gaps`, which
  sorts by severity — it is not switched in this slice, and the spec that switches it must decide
  whether a scan is acceptable.
- **D2 — what "switched" means, mechanically.** Per read: a switched-read declaration in fs's tree
  names the reads that answer from the projection. Slice 2's declaration contains exactly `pending`.
  There is no flag and no source selector; the source is a property of the read. A switched read
  always runs the parity gate, and the gate's refusal is the *only* response to a divergence. The one
  override is `FS_STATE_SOURCE=log`, a person's explicit per-process rollback that answers from the
  event store and records `source=log`. Every state read (`pending`, `status`, `gaps`, `unfinished`,
  `task get`) records its source on the running log's `command executed` line as `source` —
  `projection` for a switched read, `log` otherwise — so AC1 can tell which reads are switched from
  the log alone; the JSON envelope is not changed.
- **D3 — the projection's write path.** Per event, incremental, in the same command step as the
  append; never a re-fold of a scope (the cap scope is 2426 events). Every append site folds; the
  fold of an event that changes a node's displayed state writes a new row and closes the one it
  replaces, because memstore's exported verbs cannot set an arbitrary status (NFR Immutability). The
  current row of a node is its newest row that is not `closed`. `delivery-recorded` is added to the
  mapping as its own row carrying the payload in `Extra`; `plan-recorded` folds to nothing because no
  switched read asks it (`close`'s plan read stays on the event store). Cross-node effects a single
  event carries — a merge closes the absorbed node and links it to the survivor — are applied by that
  event's fold. A folded row writes its provenance — `Origin="fs"`, `OriginId=<event id>`,
  `Ref=<commit>`, `FsNode=<node>` — so the row names the event it came from instead of hiding that
  identity in `Extra` (A1). That is also what makes a **re-fold** safe, and the re-fold is the
  heal's whole mechanism: `Put` refuses an existing `(origin, origin_id)` with `ErrDuplicateOrigin`,
  so an overlapping fold must treat `errors.Is(err, ErrDuplicateOrigin)` as **already projected,
  continue** — never as an error — and a heal can run over a stream that is partly folded. That
  skip is what the provenance round (`f1f4a60`) bought; without it a heal could not be run safely.
  **A batch is not one fold.** `AppendBatch` appends N events, and the batch's fold is N folds, one
  per event, never a single row summarising the batch; the fold is owned by the append path, so it
  runs inside the append step rather than in a caller's loop beside it. See the Ordering NFR for the
  step's three properties — ownership, order, heal — and the retry hazard it creates.
- **D4 — rebuild and drift.** `fs state verify` re-folds every `fs:` scope from the event store into
  a scratch memstore file and compares each scope's derived state to the live projection with the
  same canonical comparison the gate uses; it writes nothing. On a difference it exits 1 naming the
  first scope and the first differing field with both values, and names `fs state rebuild` as the
  remedy; it never repairs in place. `fs state rebuild` re-folds the event store into the live file:
  for every `fs:` scope it deletes that scope's rows and re-folds the scope's events, in one
  transaction per scope, touching no non-`fs:` row; scopes present in the projection but absent from
  the event store are removed. Slice 1's `TestProjectionRealStoreCopy` is this comparison's ancestor
  and its previous result — 8 projects, 641 nodes, 0 diffs — is the pre-switch baseline.
- **D5 — scope safety.** Enforced by construction, not convention: the projection package builds a
  scope only as `"fs:" + projectID`; every projection read filters on `scope = "fs:<scope>"` for a
  scope the event store named; `rebuild` deletes only `fs:` rows; and `verify` covers only `fs:`
  scopes. Proven by AC6, which seeds a non-`fs:` row and asserts it survives every operation.
- **D6 — performance.** Per the NFR and AC8: the gated read ≤ 2× the log-only read; the projection
  read alone ≤ 1.25×; if the projection read is slower than the event store, the read is reverted and
  the measurement recorded, because the switch's whole purchase is ending the whole-history replay.
- **D7 — rollback.** `FS_STATE_SOURCE=log` returns a switched read to the event store per process,
  gate off, and records `source=log`. Reverting the code is the durable rollback: the event store is
  untouched and complete, no read consults `~/.memstore/memory.db`, and the `fs:` rows there become
  inert data that may be deleted without loss. The failures that would wreck it are named in
  Rollback & recovery.

## Assumptions & dependencies

| # | Assumption | Invalidated by | Then |
|---|---|---|---|
| A1 | fs re-vendors memstore at **`7192bce`** as **step 0 of the code round**; this spec round writes no code, so the vendored copy stays stale here. The pin is load-bearing and must land before the gate is switched on. | the code round's first commit does not re-vendor, or a later memstore moves the columns | the gate is not switched on; this spec's acceptance is measured against the code round that re-vendors |
| A1b | **Collateral of A1: the driver moves under fs's own store.** memstore `7192bce` requires `modernc.org/sqlite` **v1.60.1**, against fs's **v1.59.0** today. | the bump regresses `internal/store`, `internal/query`, `internal/dispatch` or `cmd` beyond a focused fix | stop and report the failing test verbatim; do not work around the driver — the switch waits |
| A2 | fs is the only writer of `fs:` rows in the user's `~/.memstore/memory.db`; every other scope is another writer's | a second writer touches an `fs:` scope | scope safety (D5) is broken at the source; partition the file or move fs to its own, and re-open this spec |
| A3 | The event store is the order of record: `Replay` orders by event id ascending, and the fold follows that order, so a tie in a batch is broken by insertion order | an event type that must be ordered by something else is added | the fold and `BuildTree` must agree on the new order before the gate can pass |
| A4 | The event store's contents and its readers do not change in this slice | a new event type a switched read must see | the fold's mapping extends first, then the read switches |
| A5 | memstore's `Recent(scope, k)` answers "the rows of a scope" and is the only scope-listing verb | the read cannot get a complete scope without scanning the whole file | name the missing verb on the node; a memstore change is out of scope, so the read's design changes |

**A1, in full — why `7192bce` and not the copy that is vendored now.** Slice 1 passes today on the
vendored `9e51a69` because its seam is a library that calls only `Put`, `Link` and `Get`, from one
goroutine, in one process: nothing it does exercises a shared file. The switch removes that cover.
It makes fs a **concurrent writer** to one shared `~/.memstore/memory.db` — the cap, the gap read
and every worker's command may write `fs:` rows at the same time — and that is exactly the defect
class `7192bce` carries: `f61295a` (a fresh file opened by two processes), `54c1ad8` (the schema
and its version row become visible together), `9586fa7` (the v2/v3 upgrade re-read under a held
lock). The second load-bearing change is **provenance** (`f1f4a60`): `Put` writes
`Origin`/`OriginId`/`Ref`/`FsNode` from its caller (empty `origin` → `native`, the rest NULL) and
refuses a duplicate `(origin, origin_id)` with `ErrDuplicateOrigin`, under `schema_version` 5's
partial unique index. That is how a projected row names the event it came from instead of hiding
its identity in the `Extra` envelope, and it is what makes the fold idempotent. The vendored copy
at this lock is stale: `grep -n 'schemaVersion = ' vendor/github.com/IanLuo/memstore/store.go`
prints `= 4`, and its `Put` hardcodes `"native", nil, nil, nil`.

**A1b, in full — the risk the re-vendor drags in.** The pin moves `modernc.org/sqlite`
**v1.59.0 → v1.60.1** across fs's own event store. Slice 1's card
(`.agents/dispatch/fs-store-memstore.md`) called the entire existing suite staying green after that
driver bump *the round's only real risk* — `internal/store`, `internal/query`, `internal/dispatch`,
`cmd` — and it is the same risk in the code round. The code round's acceptance must carry
`go test ./... -count=1`, `go vet ./...` and `gofmt -l` (non-vendor) green, not only the new tests.

## Data requirements

- **Inputs required:** the event store (`~/.fs/store.db`), the event store's scope set, and — for a
  switched read — the projection (`~/.memstore/memory.db`).
- **Outputs produced:** projection rows with `Scope = "fs:<project>"`; one row per node version, one
  row per decision/knowledge/report/block, one row per delivery, one row per cap claim, one row per
  project. Fields memstore has no column for ride in `Extra` (D1).
- **Must survive:** the event store survives everything and is never derived from the projection. The
  projection's loss is not data loss: it is reconstructible by `fs state rebuild` from the event
  store alone. The projection's rows may be deleted at any time without losing a fact.
- **Empty record:** a scope with no events has no projection rows, and a read of it answers as the
  event store does (empty). An empty or missing projection together with a switched read is a
  divergence: the read refuses, naming `fs state rebuild`, and does not seed or write during a read.
- **Growth:** projection rows are bounded by events plus one row per node-version; a status change
  adds a row (D3). Nothing here adds unbounded growth beyond the events it mirrors.

## Rollback & recovery

- **Per-read, in the field:** `FS_STATE_SOURCE=log` makes every read answer from the event store and
  skip the gate. It is explicit, per process, and recorded in the running log (`source=log`). It is
  not a fallback: nothing sets it on a divergence.
- **Durable:** revert the switch declaration (and, if wanted, the fold and the write path). The event
  store is complete and untouched, so every read answers as it did before the slice. "Reverted" means
  no read consults `~/.memstore/memory.db`, no command fails when the file is absent, and the `fs:`
  rows there are inert.
- **Repair:** a drifted projection is repaired by `fs state rebuild`, which re-folds the event store
  into the live file for every `fs:` scope and touches nothing else. Verify with `fs state verify`.
- **The one failure that would wreck it:** a switched read that answers from the event store when the
  projection disagrees — the silent fallback that hides the divergence the gate exists to catch — or
  a fold that unwrites a committed event. Both are forbidden; both are the reason the ordering is
  append-first and the refusal is fail-closed.

## Security & compliance

- **Secrets:** the projection copies only what the event store already holds. No credential value, no
  command output, no file content is read for it, and `Extra` is the raw event payload, nothing more.
- **Provenance:** every row carries the event's id, project, commit and node in `Extra` (slice 1's
  envelope), so a projected row names the event it came from.
- **Destructive actions:** `fs state rebuild` deletes rows; it deletes only rows whose `Scope` starts
  `fs:`, never a path or a scope derived from user input, and never a non-`fs:` row. `fs state verify`
  writes nothing. No new network egress, no new credential.
- **Isolation:** the memstore file is shared with personal memories. fs's obligation is to touch only
  its own `fs:` rows (D5); it does not read, count or infer anything about the others.
- **N/A:** no regulatory obligation applies; the projection is derived, local, and user-owned.

## Verification plan

| Requirement | Proven by |
|---|---|
| AC1 one read switched | `go test ./internal/dispatch -run TestSwitchedReads`; live `jq` over `~/.fs/logs/fs.log` per command |
| AC2 projection answers, gate compares | `go test ./internal/dispatch -run TestPendingParity` over the `pending` fixture |
| AC3 divergence refuses, no fallback | `go test ./internal/dispatch -run TestPendingParityRefuses` (three injected differences); live WARN/ERROR line |
| AC4 seed makes it clean | live `fs state verify` → `fs state rebuild` → `fs state verify` → `fs pending` |
| AC5 every append projects | `go test ./internal/command -run TestEveryAppendProjects`; `go test ./internal/dispatch -run TestWriteSetStaysAtParity`; `TestProjectionFailureKeepsEvent` |
| AC6 scope safety | `go test ./internal/store -run TestProjectionScopeSafety`; `grep -rn '"fs:"' internal/store/projection*.go` |
| AC7 rebuild and verify | `go test ./cmd -run TestStateVerifyRebuild` |
| AC8 performance | 20-run `/usr/bin/time -p` medians per arm; `go test ./internal/dispatch -run '^$' -bench BenchmarkPendingProjectionCost -benchtime=20x` |
| AC9 no lost `pending` field | `go test ./internal/dispatch -run TestPendingGapFields`; field-by-field `undispatched_gaps` comparison |
| AC10 fold failure heals; no-retry message | `go test ./internal/command -run TestFoldFailureMessage`; `go test ./internal/store -run TestRefoldSkipsDuplicates`; AC4's live sequence |
| D4 order/ownership/heal (NFR Ordering) | review: only the append path folds (no lazy hook, no caller loop); a re-fold skips `ErrDuplicateOrigin`; the failure message names `fs state rebuild` |
| D4 cross-file transaction REJECTED | `internal/store/store.go:102` and memstore `store.go:400` both WAL; no `ATTACH` of `~/.memstore/memory.db` in the tree |
| D3 immutability (no in-place update) | review of `internal/store/projection.go`: only `Put`, `CloseMemory`, `Link` are called; no `UPDATE`/`sql.DB` bypass |
| D5 scope safety as construction | the same AC6 grep plus the seeded-file test |
| D6 performance buys the switch | AC8's projection-alone arm against the log-only arm; a regression reverts the read |
| D7 rollback | `FS_STATE_SOURCE=log "$FS_BIN" pending` against a diverged projection exits 0 and records `source=log` |
| A1 the pin (code round, step 0) | `go.mod` names memstore `7192bce`; `grep -n 'schemaVersion = 5' vendor/github.com/IanLuo/memstore/store.go`; vendor matches the pin |
| A1b the driver collateral | `go test ./... -count=1`, `go vet ./...`, `gofmt -l` (non-vendor) green after the re-vendor; `go.mod` names `modernc.org/sqlite v1.60.1` |

- **Independent check:** a dispatched `review-task` reads this doc against the slice-2 worker's node,
  and is asked specifically whether any code path can return the event store's answer on a divergence,
  and whether any projection write can construct or touch a non-`fs:` scope.
