<!-- specs:locked:ef930d50 2026-10-11 type=system-design -->

## Link contract
- **upstream** (this doc relies on): playbook-mechanism.md
- **referrers** (must cite this when they change): none

# Spec — one cap, reachable: the claim, the report, and the guard
Type: spec · Owner: cap · Lock state: the marker above, nowhere else

Upstream: [`playbook-mechanism.md`](playbook-mechanism.md) owns the dispatch lifecycle this extends — the delivery record it routes by, and the plan it does not touch. This **re-opens** what gap `t-04cd26ef` closed. That gap's closing decision rested on the *wait* rule — "the cap learns a worker finished by WAITING IN THE CALL (prompt --wait settles on idle/done/blocked) and reads the pane when it settles" — and both of its legs have since been forbidden: the cap's playbook now reads **never wait and never poll**, and the design's rule is **read the node, never the message and never the transcript**. The need it dismissed is therefore back, and the mechanism here is the opposite shape: a push from the worker, not a wait from the cap.

**Revised in place for `t-67def1bd`, and one acceptance criterion moved.** `fs pending` gained a state of its own for a worker that has **stopped**: herdr reports an agent `blocked` when its pane is parked on a prompt or a modal that nothing in that session will dismiss, and fs read that as `running`, whose documented meaning is *let it work*. A worker stuck on a permission prompt and a worker that had simply not finished were the same word, so the cap could not tell that anything was waiting on it. **AC7 moved** — it required only that the cap's own address and liveness be visible, and it now also requires that a stopped worker be distinguishable from a thinking one, which is the half of `t-67def1bd` this doc owns. The distinction is drawn on herdr's word and not on a heuristic: `blocked` becomes its own state, `working` **and** `idle` both stay `running`, because a worker between two tool calls has not stopped and a state that fires on both is a state the cap learns to ignore. **This does not cover every way a worker can stall, and the gap it does not cover is named:** a harness that raises its own undismissable notice in words herdr's detection does not recognise is still read as `running`, and the notice that motivated this card is exactly that case (gap `t-6c1e8d33`; the differential is on the node of the dispatch that made this change). The report obligation is unchanged and is still the other half — a worker waiting on a *decision* reads `running`, and the report on its node is what makes that wait visible.

## Problem & who breaks

- **Who:** the user in a long session; the cap, whose context is the scarce resource; and a worker that needs a decision.
- **Break 1 — a cap is not an identity.** `cap` is a scope name in the store and nothing more. Any session that runs `fs kb prompt` becomes a cap by reading its own rules, two caps can hold the scope at once with neither able to tell, and **a worker can run `fs dispatch` and become a coordinator.**
- **Break 2 — a worker has no way to report.** `worker.yaml` never mentions the cap. Its only channel is its own node, read when someone looks, so a worker that needs a decision waits invisibly and a cap learns of it only by looking.
- **Break 3 — the cap's liveness is unknowable.** A dead cap reads exactly like a quiet one, so nothing tells the user whether coordination is available or a fresh session may take it.
- **Break 4 — coordination costs context in proportion to attention.** The cap's contract is to start every turn with `fs pending`, which is the only way it ever learns anything; the more often it must check, the more of its context is spent on checking.
- **Cost of doing nothing:** long sessions stall with no visible reason, a blocked worker is indistinguishable from a working one, and a dead cap freezes dispatch silently.

## Non-negotiable outcome

- **A worker can report its status to the cap without the cap looking, and a worker cannot coordinate.**
- Falsifiable, both halves in one sitting: a worker runs `fs report <node> --blocked --line "…"` and the event lands in the store **and** arrives in the live cap's session; the same worker runs `fs dispatch …` and is refused, naming the node it worked.

## Scope in / out + primary flow

**In**

- **`fs cap claim`** — records the claiming pane, tab and agent as the cap's **address**. Refused when the registered pane is still alive, naming it. When the registered pane is gone, the claim is a **take-over**: it records the handover (the old address, when it was last heard) and requires `--reconstructed "<what you read>"`, recorded as a decision on the handover.
- **`fs cap who`** — the registered address and its liveness, read from herdr. Exit 0 when a live cap exists, 1 when none does, so a script or a playbook step can use it.
- **`fs report <PROJECT:NODE> --line "<one line>" [--blocked|--question|--done]`** — the worker's report. It is **an event in the store first and always**: durable, addressed to no cap, readable by whoever holds the cap now or later. Only then, best-effort, it notifies the live cap through herdr. **The store is the channel; the notification is an accelerant.**
- **`fs tell <PROJECT:NODE> "<text>"`** — the cap's reply, resolved through that dispatch's delivery record and sent via herdr. It is a **message, not a record**: what the cap decided lands on the node as a decision, where it outlives the delivery.
- **The order is record, then notify, and one command holds it.** The event is written before any notification is attempted, so a cap can never learn something the store does not already hold and a failed notification costs only promptness. Two commands would make that a discipline instead of a property: one that notifies without recording pings about something nobody wrote down, one that records without notifying leaves a block sitting silently.
- **The kind decides whether the cap is interrupted.** A **block**, a **question** and a **completion** notify — the three a cap must not miss — while a progress note records silently for whoever reads. **No worker can force a ping** for a kind that does not notify: a worker who can interrupt at will can spend the cap's context back up, which is the resource this feature exists to protect.
- **The guard** — held by `fs`, not by a playbook: cap-scope writes require the claim, and a pane `fs` holds a delivery record for is refused **coordinating acts** outright while **reporting stays allowed**, because reporting is not acting as cap.
- **The report obligation** — `worker.yaml` gains it: one line, recorded with `fs report`, for a block, a question, or a finished unit of work. It is *a pointer*, never the content — the node remains the record, and the cap still reads the node. A report is written **whether or not a cap exists**, because the worker cannot know and must not have to.
- **`fs pending` names the cap's liveness, surfaces every open report, and gives a stopped worker a state of its own**, so a dead cap, a worker waiting on a decision and a worker parked on a prompt are each visible from the read the cap already makes — which is what `t-67def1bd` needs, in both halves. A worker waiting on a decision reads as `running`, whose documented meaning is *let it work*, and the report on its node is what makes that wait visible. A worker herdr reports **`blocked`** reads as `blocked`, which is the one unfinished state that is not work in progress: the harness stopped and only a person can move it, so it is reported beside `ready` and not left to look like thinking. `idle` is deliberately **not** blocked — a worker between two tool calls is not stuck, and a state that fires on both is a state the cap learns to ignore.

**Out (non-goals)**

- **Not a message queue, and not a refusal either.** The store is the channel, so a report is written with or without a cap and is never held *for* one: with no live cap only the notification is skipped, and the report says so, so the worker knows nobody was pulled.
- **Not a cap-shaped interface.** `fs report` names a node and never a cap — a worker cannot tell which cap is reading, and must not need to. That is what lets coordination survive a cap's death with the worker noticing nothing.
- **Not a conversation channel.** A report carries status and a node id, never the content of what happened — the cap's context is the resource this feature exists to protect.
- **Not a heartbeat.** herdr's own liveness is the heartbeat: a registered pane that still exists is a live cap, and one it no longer has is provably dead. No fs-side timer, no expiry window, no `--take-over` flag — a take-over is simply what a claim *is* when the address is gone.
- **Not multi-cap.** One at a time. Everyone else reads; only the holder writes in the cap scope.
- **Not a transcript reader.** `fs` never reads a worker's pane output. The report is a message pushed to the cap; the record is the node.
- **Not an answer channel for the cap's thinking.** The cap replies with `fs tell <node>`; what it decided lands on the node as a decision, where it outlives the conversation.

**Primary flow**

1. A session runs `fs cap claim`. With no claim, it becomes the cap. With a live one, it is refused naming the pane; with a dead one, it takes over and records what it reconstructed.
2. The cap dispatches. The delivery record carries the worker's pane, which is both the guard's evidence and the route for the cap's replies.
3. The worker needs a decision: `fs report <project>:<node> --line "blocked: <one line>" --blocked`. The **event lands in the store**; the live cap, if there is one, is notified through herdr and learns it without having looked.
4. The cap reads the **node** — never the report alone — decides, records the decision on the node, and answers with `fs tell <node> "…"`. If the cap is dead, the report is still there for whoever claims next.
5. The cap closes the dispatch through the existing gate. The claim is untouched by any of this.
6. If the cap dies: `fs cap who` and `fs pending` report the address gone; a fresh session claims it, and the take-over's reconstruction decision is what the store gets in exchange.

## Acceptance criteria

- **AC1 — one cap.** `fs cap claim` records pane, tab, agent and time as the address. A second claim while that pane is alive is refused, naming it and when it was seen.
- **AC2 — a take-over is a claim over a dead address.** With the registered pane gone from herdr, a claim succeeds, records the handover (the old address and its last-seen time) and **requires** `--reconstructed`, recorded as a decision. A take-over without it is refused.
- **AC3 — the address and its liveness are readable by anyone.** `fs cap who` prints the address and whether herdr still has it; exit 0 with a live cap, 1 without. With no claim at all it says so and exits 1.
- **AC4 — a worker can report, and the report survives.** `fs report` from a pane with a delivery record writes the event **whether or not a cap is live**, and with a live cap also sends the notification — in that order, in one command, with no way to do either half alone. With no live cap, or a herdr that refuses, it exits 0 saying *recorded, not notified*, and the event is readable afterwards by `fs pending` and `fs log`. A kind that does not notify sends nothing.
- **AC9 — the worker's interface never names a cap.** `fs report` takes a node and a line; there is no command by which a worker addresses a cap, and a report behaves identically with no cap, a live one, or a dead one.
- **AC5 — the cap can answer.** `fs tell PROJECT:NODE "<line>"` resolves that dispatch's delivery record and sends. An unknown node, or one with no delivery record, is refused naming which.
- **AC6 — reporting is allowed, coordinating is not.** A cap-scope write from a pane with a delivery record is refused, naming the node that pane worked. **The same pane's `fs report` succeeds** — the two are different acts and the refusal says so.
- **AC7 — a dead cap is visible from the cap's own read, and a stopped worker is not a thinking one.** `fs pending` reports the cap's address and its liveness, so a cap discovers its own supersession and a fresh session sees that coordination is available; and it distinguishes a worker that has stopped from one that is merely between two tool calls — herdr's `blocked` is reported as its own state beside `ready`, while `working` and `idle` both stay `running`. A state that fired on a pausing worker as well as a parked one would be ignored, which is how `running` became useless.
- **AC8 — it builds and it passes.** `nix build`, `go test ./...`, `bash tests/run.sh` green, including the playbook honesty test over the changed `cap.yaml` and `worker.yaml`.

## KPIs & failure signal

- **Good:** a worker's block reaches the cap without the cap having looked, and the cap's own polling drops — the pull replaces the poll.
- **Off-switch:** **a report that herdr accepts and nothing surfaces.** If a worker believes it reported and the cap never sees it, the channel is a lie and the playbook must lead with the node instead. The second signal: a cap-scope write succeeding from a pane that holds no claim — the guard has a hole.

## Non-functional requirements

- **The payload is bounded because the cap's context is the resource.** A report is ≤200 bytes: a status word, one line, a node id. Enforced by convention in `worker.yaml`, stated here as the number.
- **Sending never blocks the worker.** `fs tell` returns once herdr accepts the submission; it does not wait for the cap to read or reply.
- **A report is a record; a reply is a message.** The report is stored (`report-recorded`) because it is an *interaction* — the node says what the work is, the report says that the worker spoke — so it is not a duplicate of anything. The notification and its outcome are log lines (`fs-logging.md`), so a lost notification is diagnosable without a store write.
- **Reading the claim is free and side-effect-free.** `fs cap who` and `fs pending` write nothing; there is no command by which a non-cap releases another's claim.
- **Degradation is loud.** A session with no `HERDR_PANE_ID` cannot claim, and is refused saying why rather than silently becoming an unregistered coordinator.

## Assumptions & dependencies

| # | assumption | invalidated by | then |
|---|---|---|---|
| A1 | herdr routes a prompt to a pane or an agent name, and it lands in that pane's session | a target that resolves for neither | the claim records both pane and agent; a claim naming neither is refused at claim time, not at report time |
| A2 | `HERDR_PANE_ID` is present in the environment of a herdr-hosted cap | a cap running outside herdr | the claim is refused naming that; herdr-hosted coordination is the only kind, deliberately |
| A3 | the delivery record is durable, so the guard can always identify a worker pane | a record lost or a pane reused for a new dispatch | the guard degrades to *unclaimed* rather than *worker*, and the refusal text must say which it is |
| A4 | the cap's harness surfaces an incoming prompt as a message | a harness that swallows it | reports would sit unseen; the node stays the authoritative channel and the report is only ever an accelerant |

## Data requirements

- **Inputs required:** the claim (a store event); the delivery records (already recorded); `HERDR_PANE_ID`; herdr's own workspace/agent state for the liveness answer.
- **Outputs produced:** the `cap-claimed` event (pane, tab, agent, taken-over-from, timestamp); the take-over's reconstruction decision; `report-recorded` per report, plus the existing `task-blocked` when the report is a block (the line as its reason, so the node's status and every reader that already handles a block are untouched); log lines for every notification and refusal. **No message is stored** — a report is a record, a reply is a message.
- **Must survive:** the claim, so a dead cap is detectable and a take-over is legible after the fact. It is one event, so the store's replay already carries it.
- **Empty record:** no claim → `fs cap who` exits 1 saying no cap has claimed, and a **report is still written** — the store is the channel, so the only thing absent is the notification, and `fs report` says so. A claim whose pane is gone is *not* empty: it is a dead cap, and the two must not read alike.

## Rollback & recovery

- **Revert:** the claim is one event type, additive; reverting the code leaves an inert event nothing reads. No migration, no state to unwind.
- **"Reverted" means:** no command consults the claim, cap-scope writes are unchecked again, `fs tell` does not exist, and the playbooks no longer instruct reporting.
- **The failure that would wreck it:** **a cap that reads only notifications and never the store.** A report made while the cap was dead notifies nobody, so a cap that trusts its inbox over its own read has a hole exactly where this design is strongest. The mitigation is the cap's existing first obligation — start every turn with `fs pending` — plus the rule that the record precedes the notification, so the store is always the superset.
- **A second one:** a claim lost while its cap lives (a wiped store, a copied `~/.fs`) leaves a live cap unable to write. Recovery is to claim again — with no claim present, a claim simply succeeds.

## Security & compliance

- **The guard is a discipline guard, not a security boundary**, and says so: `HERDR_PANE_ID` is an environment variable any process can unset. It exists so a worker cannot *accidentally* coordinate, which is what was asked; the store is local, single-user, and holds nothing worth defending from its own user.
- **Nothing secret moves.** The claim is an address, not a credential; the message is a status line. No credential store is read, and nothing leaves the machine — herdr is a local socket and the store is a local file.
- **`N/A:`** no personal data, no telemetry, no network egress, no regulatory surface.

## Verification plan

| requirement | proven by |
|---|---|
| AC1 one cap, refusal | `go test ./...` on the claim; live: a second claim refused naming the pane |
| AC2 take-over demands reconstruction | live under a temp HOME with a fake herdr: kill the recorded pane from the fake's state, claim, assert the handover event and that a missing `--reconstructed` refuses |
| AC3 `fs cap who` | test both exit codes; live with and without a claim |
| AC4 a worker can report | live with a fake herdr: assert the exact `agent prompt` argv it was handed |
| AC5 the cap can answer | live: `fs tell PROJECT:NODE` resolves the delivery record and sends |
| **AC6 reporting allowed, coordinating refused (the sharpest)** | one test, one pane: `fs dispatch` refused naming the node, `fs report` succeeding from the same environment |
| AC7 a dead cap is visible, and a stopped worker is not a running one | live: kill the fake's pane, then `fs pending` reports the address as dead; and a fake herdr reporting `blocked` for a live worker's agent is classified `blocked` while the same worker reported `idle` is classified `running` — both cases in one table test, so the pause is measured against the block |
| AC8 build + suite | `nix build`, `go test ./...`, `bash tests/run.sh` |
| the payload bound | read `worker.yaml`'s wording and assert the honesty test still passes over the changed defaults |

- Independent check: a dispatched `review-task` reads the worker's node against **this locked doc**, and is asked specifically whether a worker can reach a coordinating act by any route, and whether a failed report can be mistaken for a delivered one.
