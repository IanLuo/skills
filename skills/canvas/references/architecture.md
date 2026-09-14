<!-- specs:locked:4533f19 2026-09-13 type=architecture -->

## Link contract
- **upstream** (this doc relies on): none
- **referrers** (must cite this when they change): skills/canvas/SKILL.md, skills/canvas/references/manual.md, skills/canvas/references/round-card.md, skills/canvas/references/coordinator-card.md, skills/dev-task

# Architecture — canvas liveness
Scope: the `canvas` skill and its runtime — the server, the CLI, the agents, and the topic
files. Not the skills repo. The decisions below are what the code cannot tell you: the
constraint that forced each choice and the alternative that lost.

Obeys `../skill-man/references/doc-format.md`: bullets, fixed headers, explicit `N/A`.

## Load-bearing structure

Six decisions. Changing any one invalidates large parts of `scripts/` and the task cards.

1. **One daemon serves every project.**
   - Choice: one long-lived server, one dashboard, projects registered at runtime.
   - Constraint: a Send must reach a listener with no human in the loop, and N projects
     meant N daemons, N ports, N failure surfaces.
   - Rejected: one daemon per root (the status quo) — multiplies the only always-on
     component by the number of projects.
   - Consequence: routes are project-namespaced, registration is explicit, and the
     "not a general web server" invariant is **amended**, not deleted (see Trust boundary).

2. **No listener is required.**
   - Choice: the daemon sweeps registered projects every **2 s** and dispatches a round when
     unread work exists. `wait`, `parked.json` and the heartbeat are **dropped in v1**.
   - Constraint: a park is a blocking call inside an agent turn — any interruption ends the
     turn, so nothing re-parks, and the harness aborts long calls unpredictably.
   - Rejected: a parked waiter as the correctness primitive. Measured: 18 of 21 wakes
     reached nobody, and `listening=1` was reported against heartbeats 11 438 s old.
   - Why 2 s: the interval *is* the delivery latency and the consumer is a human watching a
     page. Queue-service ceilings (SQS `WaitTimeSeconds` 20 s) measure a network, not local disk.

3. **The daemon owns waking.**
   - Choice: rate-limited; only when work is unread AND herdr reports the coordinator idle.
   - Constraint: the daemon is the only always-on component and already holds the state the
     decision needs — work unread, coordinator idle, target known.
   - Rejected: an OS supervisor (launchd/systemd) — repair on next contact is enough; and an
     agent re-parking itself, which dies with the aborted call.

4. **The wake target is `.coordinator.json`.**
   - Choice: the agent name or pane id recorded there, per project.
   - Constraint: the previous target — a per-topic `worker.json` — is written only by a
     rejected watcher. The path read a record nothing writes.
   - Rejected: keeping the orphaned lookup as a fallback.

5. **One writer per topic, by lease.**
   - Choice: `<topic>/writer.json` `{agent, pane, pid, ts}`, TTL ≈ round timeout, cleared when
     the pane closes; claimed on create (`--new`) and on round.
   - Constraint: `content.html` has no merge, and last-write-wins already produced a
     half-written edit.
   - Rejected: three-way merge of `content.html` — contradicts the no-merge invariant and is
     far larger than the problem.

6. **Ports are resolved, never guessed.**
   - Choice: one well-known record `~/.cache/canvas/daemon.json` `{port, pid, since}` plus
     `canvas.py url`; fixed default port, probe on start.
   - Constraint: one server serves projects that did not start it, so no project may assume a
     port. The record is the **only** global path, and it is a cache, not a config.
   - Rejected: "trust the URL the start command printed" — that only works for the project
     that ran `start`.

## Layers & ownership

Four layers. Dependencies point one way: agents → CLI → files, and page → server → files.

| layer | owns | never |
|---|---|---|
| Files (`<project>/.agents/canvas/<topic>/`) | the record — content, feedback, send, history, lease | — |
| Server (`canvas.py serve`) | socket, project registry, sweep, waking | edits a topic file |
| CLI (`canvas.py`, `canvas-worker.py`) | every human/agent action; file-only fallback when the server is down | depends on server memory |
| Agents (coordinator, round worker) | reasoning, content authorship | depends on server memory |

- **Boundary rule:** nothing depends on the server to be correct. A dead server costs latency
  and one wake, never a note.
- The page is **not** a layer — it is the server's projection, and it renders the same files
  from `file://` with no server at all.

## Repo tree

Scope is the skill, not the repo. One line per part:

- `SKILL.md` — the operating contract: roles, the round loop, the content contract.
- `scripts/canvas.py` — the server **and** every file command, one binary.
- `scripts/canvas-worker.py` — coordinator and round dispatch.
- `scripts/build-canvas.py` — content → shell. `scripts/verify-canvas.py` — headless render check.
- `references/` — `manual.md`, `graphics.md`, `design-system.md`, the task cards, this doc.
- `assets/` — vendored mermaid (gitignored).
- Outside the skill: `<project>/.agents/canvas/<topic>/` (the record) and the one endpoint
  record above.

## Cross-cutting concerns

- **Logging — every autonomous decision is logged.** Anything the server does without being
  asked (sweep dispatch, registration, refusal) appends `{kind, ok, why}` to the topic's
  `history.jsonl`, and the page renders it. Silence is the failure mode this rule kills.
- **Error model — two vocabularies, both already in use.** CLI exit codes (`0` work · `1` idle
  · `3` stop · `4` revision) for humans and agents; HTTP `4xx`/`5xx` **with a reason string**
  for the page. A refusal names its holder or its cause; never a bare failure.
- **State — files are the state, server memory is a cache.** A server restart must not change
  observable behaviour; the registry rebuilds on contact, never on a migration.
- **Trust boundary — 127.0.0.1 + explicit registration, nothing else.** No tokens, no auth. A
  root is served only after it registers, and registration is a local CLI action.

## Assumptions and edges

- **Smallest input that must succeed:** one registered project with one topic and one unread
  note → a dispatch within 2 s, with a `wake` entry in `history.jsonl`.
- **Smallest input that must be refused:** a page or request for an unregistered root → not
  served, and the refusal is logged.
- **Assumed, not verified:** herdr is present (absent it, the CLI falls back to file operations
  and nothing is dispatched); a project agent exists to be woken (absent it, work waits on disk
  by design); the 2 s sweep is affordable at the number of registered topics actually in use.
- **N/A:** multi-writer merge — serialise writers with the lease instead.

## Pointers

- `references/manual.md` — operating rules and recovery playbooks.
- `references/design-system.md` — locked visuals; `design:locked:4d7d5ab`.
- `references/graphics.md` — content vocabulary and the view ladder.
- **N/A:** no ADRs, no `CONVENTIONS.md` in this repo.

Last reviewed: 2026-09-12 · canvas /specs
