<!-- specs:locked:36dc4bc9 2026-10-19 type=system-design -->

## Link contract
- **upstream** (this doc relies on): none
- **referrers** (must cite this when they change): none

# Spec — a running log for fs: what the process did, kept
Type: spec · Owner: cap · Lock state: the marker above, nowhere else

Related, and deliberately separate: [`playbook-system.md`](playbook-system.md) defines the **trace**, which is a dispatch's execution *record*. This doc defines the **log**, which is running logging. The two are not each other and must not converge — the difference is stated in Non-functional requirements, because it is the whole content of this spec.

## Problem & who breaks

- **Who:** the user, diagnosing why an `fs` invocation did what it did; and the cap, the same.
- **Break 1 — fs has running logging and keeps none of it.** `cmd/main.go:25` builds `slog.New(slog.NewJSONHandler(os.Stderr, nil))` and ~12 call sites emit `logger.Info("command executed", "command", …, "node_id", …, "event_id", …)`. Every line goes to **stderr** and dies with the process. There is no sink, no file, no retention.
- **Break 2 — the level is not adjustable.** The handler takes no options: no `LevelVar`, no `--verbose`, and not one `logger.Debug` call site exists. Nothing can be turned up when a dispatch misbehaves.
- **Break 3 — the words are already muddled.** `fs log` reads *events*, and the dispatch trace was filed in a directory called `logs/` under the project it acted on. Under a distinction the user stated plainly — *events are events, logs are running logging, they are different* — both were misnamed, and the misnaming is why a cap (this one) reported the event store as "the real log with history". The trace half of this is fixed by the sibling change: it now lives at `~/.fs/traces/<project>.<node>.jsonl`, flat and named for the dispatch, so `logs` means exactly one thing in the tree. `fs log` reading events remains the misnomer this change does not fix.
- **Cost of doing nothing:** a refusal, a hook that never fired, or a store write that failed leaves no evidence outside the pane or the tool output that happened to catch stderr. The session that follows cannot ask what the previous one did.

## Non-negotiable outcome

- **Every `fs` invocation appends its running log to a file that outlives the process, and the level can be turned up.** Falsifiable: run `fs status` with stderr redirected to `/dev/null`, then find that command's line in the log file; run `fs status -v` and find DEBUG lines that the default run did not write.

## Scope in / out + primary flow

**In**

- One sink: `~/.fs/logs/fs.log`, JSON lines, append-only, written by every `fs` process.
- The project as a **field** on each line, never a directory split — one run spans scopes.
- Level control: default INFO; `-v`/`--verbose` for DEBUG; `FS_LOG_LEVEL` as the environment fallback (workers and playbook steps invoke `fs` from shell and cannot always pass a flag). The flag wins over the environment.
- Rotation at **4 MiB**, keeping **3** files (`fs.log`, `fs.log.1`, `fs.log.2`) — bounded at ~16 MiB, no timers, no daemon, no external tool.
- The call sites: the existing `command executed` lines keep their shape; refusals, delivery, close-out and store-write failures gain lines; DEBUG carries the shapes a diagnosis needs (resolved tags, plan, playbook paths).

**Out (non-goals)**

- **Not the trace.** Nothing here is read back by a gate; see NFRs.
- **Not a `fs logs` reader command.** The log is read with `tail`, `grep` and `jq`, all of which filter it on any field. `fs log` already exists and reads *events*, so a second reader next to it would recreate the collision — and renaming `fs log` is a breaking change to a shipped command, deliberately not bundled here.
- **Not the event store.** Events remain the state; this adds nothing to them and changes none of them.
- **Not remote shipping, not a collector, not structured metrics.** Local file, local reading.
- **Not command output.** The log records what fs *did*, never what a command *printed*.
- **Not a DEBUG of the worker.** fs sees its own process; the worker's internals belong to the worker's harness.

**Primary flow (one path, step by step)**

1. Any `fs` invocation starts; the logger opens `~/.fs/logs/fs.log` for append.
2. If the file is at or over 4 MiB, it rotates before the first write: `fs.log.2` is discarded, `fs.log.1` becomes `fs.log.2`, `fs.log` becomes `fs.log.1`.
3. The command runs, writing one line per notable step at the configured level.
4. The process exits; the lines are already on disk. Nothing reads them back.

## Acceptance criteria

- **AC1 — durable.** After any `fs` command, its lines are in `~/.fs/logs/fs.log`, with stderr discarded. A command run with `2>/dev/null` still leaves its record.
- **AC2 — the level moves.** `-v`/`--verbose` writes DEBUG lines that a default run does not; `FS_LOG_LEVEL=debug` does the same without the flag; the flag overrides the environment. An unparseable level is refused, naming the accepted values, rather than silently falling back.
- **AC3 — bounded.** `fs.log` never exceeds 4 MiB by more than one line's worth; rotation keeps exactly 3 files; a rotation that fails does not lose the line that was being written.
- **AC4 — filterable and complete.** Every line is one JSON object carrying at minimum `time`, `level`, `msg`, and — where the command had one — `project`. **A refusal is one of those lines and not an exception to them:** it carries `project` whenever the invocation *named* one — `--project P`, the address a `PROJECT:NODE` value carries where the command declares that flag one (`close --worker P:N`, `log --node P:N`, each declared as `addressFlags` in that command's own spec), or the first positional of a command whose positional may be an address rather than a bare node (`task get P:N`, `report P:N`, `tell P:N`, each declared as `address` in that command's own spec) — read from the caller's own arguments and walked the way `checkArgs` walks them, so a value that merely reads like a flag names nothing, and the flag is read wherever it sits — after a positional as readily as before it. The glued `--project=P` (and `--worker=P:N`) names P as well, even though the CLI accepts no `--flag=value` form and refuses that token as an unknown flag: the token is flag-shaped and carries the name, so it is the caller naming a project, where a value that merely reads like a flag is not. A refusal fires before anything is resolved and cannot know more than the caller said, so an invocation that named no project carries the command and the reason and no project key at all, rather than a guessed one. An invocation that named **two** projects and left them disagreeing — `--project P` beside an address naming Q — is refused as the conflict it is, and its line carries neither of them: the command was answered by neither, so carrying one would be the same guess this forbids and would contradict the diagnostic reason printed beside it. Filtering by project is a `jq` predicate over one file, never a choice of directory. Four shapes name none, each named here rather than left as a silent hole, and the residue they add up to is `t-bbcca0c8`:

  - **a bare positional node** — `task get t-nope`, `report t-nope`, `tell t-nope`.
  - **a bare node in a flag value** — `close --node t-nope`, `close --worker t-nope`, `log --node t-nope`. A value is not a positional, so the walk reads each as a flag's value and stops, and a bare value names no project whether or not its command declares the flag an address (`log --node` and `close --worker` are declared addresses, `close --node` is not). `close --node` is named here and not among the positionals above because it is not one.
  - **`trace <node>`** — its `--project` is an assertion rather than a selector, so it carries a project only when the caller passes one.
  - **`integrated`, `reviews`, `batch`** — each takes one positional that *does* split a `SCOPE:NODE` reference (`fs integrated cap:nope` refuses `no done integration dispatch in scope cap names member nope`), but the qualifier is a **scope**, and its only legal value is `cap` (`fs integrated skills:nope` refuses `skills:nope names scope skills; a member lives in scope cap`). `cap` is not a project — `fs project get cap` answers `registry: project "cap" not found` — and none of the three declares `--project`, so that address shape is not a project form and writes no project key. The one way these three do carry one is the ordinary way: a `--project` on the command line is read from the caller's own arguments like any other, so `fs integrated --project skills t-nope` is refused as an unknown flag (exit 2) and its line carries `"project":"skills"` all the same — a usage refusal is a refusal, and the caller named a project (`t-80a8677d`). `close --node cap:nope` is the contrast: `close --node` is an exact node id rather than an address, so it does not split and echoes the reference whole (`close: no node cap:nope in scope cap`).
- **AC5 — what a refusal leaves.** Every refusal that returns a JSON error envelope also writes a line at WARN or ERROR carrying the command and the reason. A cap reading only the log can tell that a refusal happened and which command made it.
- **AC6 — the log never gates.** Nothing in `fs` reads the log back to make a decision. A log write failure — an unwritable `~/.fs/logs`, a full disk — produces at most a WARN on stderr and **never** fails the command. This is the opposite of the trace, where a write failure is a refusal, and the difference is deliberate: see NFRs.
- **AC7 — it builds and it passes.** `nix build`, `go test ./...` under `src/flagship`, and `bash tests/run.sh` are green, and the new behaviour has tests: rotation, level selection, and that an unwritable sink does not change the command's exit code or stdout.

## KPIs & failure signal

- **Good:** a dispatch that misbehaved can be explained from `~/.fs/logs/fs.log` alone, without the pane, the transcript, or the tool output that caught stderr.
- **Off-switch:** the log file stops growing while commands still run — the sink is broken and silently so. Check by timestamp: the newest line should never be older than the most recent `fs` invocation.

## Non-functional requirements

- **The rule that cannot be waived — the log never gates, and its failure is never fatal.** This is not a preference; it is the property that keeps logging from becoming a second gate. A trace line that is missing *refuses a close* (AC6 of the playbook spec); a log line that is missing must refuse nothing. Consequently the log is written best-effort and any sink error is swallowed to a WARN. A future change that makes a log line load-bearing is a regression against this spec, not an improvement.
- **Cost:** one line per notable step, ≤ 1 KiB each; the append is a single write per line; ≤ 2 ms per command at INFO. Bounded at ~16 MiB on disk by AC3.
- **Secrets:** the log carries fs's own shapes — command names, ids, counts, statuses, playbook names, error strings. It **never** carries a command's output and never a value read from the credentials store. Step bodies are not logged; they live in the playbook files already.
- **Reliability:** no daemon, no lock beyond O_APPEND, no in-memory buffer that can be lost — a line is on disk when the call returns. Concurrent `fs` processes appending is the normal case and must not corrupt lines.
- **Usability:** `jq -c 'select(.project=="skills")' ~/.fs/logs/fs.log` and `grep` are the readers; no new command to learn.
- **Compatibility:** stderr keeps receiving the same lines it does today, so an existing caller that captures stderr is unaffected. `FS_LOG_LEVEL` absent means INFO, exactly today's behaviour.
- **Operations:** deleting `~/.fs/logs/` is harmless — the next command recreates it. A log directory that cannot be created is a WARN, not an error.

## Assumptions & dependencies

| # | Assumption | Invalidated by | Then |
|---|---|---|---|
| A1 | A line-per-write append is safe across concurrent fs processes | interleaved or torn lines appear in practice | add an advisory lock, or write with a per-process temporary and rename |
| A2 | 4 MiB × 3 is enough history at one INFO line per command | the log rolls within a day in normal use | raise the cap, or make it configurable — the rotation must not be the reason evidence is missing |
| A3 | `FS_LOG_LEVEL` is visible to the processes that matter | a worker's harness does not propagate the environment | the flag is the reliable lever; the env var is a convenience and its absence degrades to INFO, never to silence |

## Data requirements

- **Inputs required:** nothing new. The logger needs `~/.fs` to be resolvable, which every command already requires.
- **Outputs produced:** `~/.fs/logs/fs.log` plus its rotations. JSON lines; the key set is slog's (`time`, `level`, `msg`) extended with the command's own fields (`command`, `project`, `node_id`, `event_id`, counts, and on a refusal the reason). No schema is imposed on payloads beyond `time`/`level`/`msg`/`project`.
- **Must survive:** the process, the pane, and the session. The log is the only artifact that outlives a run without being state; that is its purpose.
- **Empty record:** no `fs` invocation yet → no file, and that is not an error. A file that exists and is empty is indistinguishable from a missing one in effect, and neither is reported as a fault.
- **Growth:** bounded by AC3. Nothing else in `~/.fs` grows without a bound that a person chose.

## Rollback & recovery

- **Revert:** the file is not load-bearing for any decision (AC6), so reverting the code leaves `~/.fs/logs/` as inert files to delete. `rm -rf ~/.fs/logs` is the whole recovery.
- **"Reverted" means:** no code reads the path, no command fails when the directory is absent, and stderr carries what it carried before.
- **The one failure that would wreck it:** a log write becoming fatal, or a log line becoming something a gate reads. Either turns diagnostics into a dependency, and the second is how this spec's own subject — the trace — came to be confused with a log in the first place.

## Security & compliance

- **Secrets:** as the NFR above — no command output, no credential value, ≤ 1 KiB per line. A check step that prints a secret cannot land it in the log.
- **Provenance:** every line carries `time`; every line from a command whose invocation named a project carries `project` — a refusal included, and read from the caller's own arguments rather than from anything fs resolved.
- **Destructive actions:** none. The log writes and rotates only inside `~/.fs/logs/`, and rotation deletes only `fs.log.2` — never a path derived from user input.
- **N/A:** no regulatory obligation applies, no personal data is processed, no network egress, no credential is read.

## Verification plan

| Requirement | Proven by |
|---|---|
| AC1 durable | `go test ./...` on the sink; live: run a command with `2>/dev/null` and `tail` the file |
| AC2 the level moves | table test over flag/env/default and an unparseable value; live `-v` run compared against a default run |
| AC3 bounded | a test that writes past 4 MiB and asserts three files, the newest content intact |
| AC4 filterable | `jq -c 'select(.project=="skills")'` over a live file returns only those lines, refusals among them — `go test ./cmd/` asserts that a refusal whose invocation named a project carries it in every form that names one (a `--project`, wherever it sits — after a bare positional and after a bare `--worker` among them — a declared address flag's `PROJECT:NODE` value, a `--worker` on close and a `--node` on log, and a positional address), that a flag value which merely reads like a flag does not, that a `--node` whose command does not declare it an address does not either, that a glued `--project=P` does, that a two-named conflict — `--project P` beside an address naming Q — carries no key, and that the bare forms, positional and flag value alike, carry no key |
| AC5 a refusal leaves a line | force a refusal (`fs close` on a non-dispatch node) and read the log |
| **AC6 the log never gates (the non-negotiable)** | point `~/.fs` at an unwritable directory and assert the command's exit code, stdout and stderr are unchanged |
| AC7 build + suite | `nix build`, `go test ./...`, `bash tests/run.sh` |
| secrets NFR | grep the code for what is passed to the logger; run a check step whose output contains a marker and assert the marker is absent from the log |
| concurrency (A1) | a test running N fs processes concurrently and asserting N intact lines |

- Independent check: a dispatched `review-task` reads the worker's node against **this locked doc**, and is asked specifically whether any log line is load-bearing for a decision.
