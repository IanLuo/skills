# PRD question ladder

Read this when grilling a `--type prd` doc. Ask one rung at a time. Don't advance to the
next until the answer clears the gate in [judging.md](judging.md). Lead with the
load-bearing rung — the one a fresh agent would get *wrong* without it.

The PRD is a **root** doc — `upstream: none` in the contract. Its `referrers` are
architecture, system-design, design-system, and any dev/design task that touches the
scope.

## Rungs (ask in order, but follow the risk)

1. **The problem.** What breaks for whom if we do nothing? One sentence. If you can't
   name a who *and* a break, the build is premature — keep grilling.
2. **The non-negotiable outcome.** The single thing that, if missing, the release is a
   failure — explicit, falsifiable ("ship 5 playable objects", not "feels fun").
   Confirm: would a tester pass/fail this without arguing?
3. **Scope in / out.** What's in V1, and what's deliberately *not*. The out list is where
   most PRDs leak — name at least three non-goals. Grill each: "why is this out, and what
   keeps it out?"
4. **Audience & primary flow.** Who runs this, the one path they take through it. If
   there are two primary flows, you have two products — make the user pick.
5. **Acceptance criteria.** Testable bullets, one per outcome. Each must be checkable
   by someone who didn't write it. Empty/edge inputs: what's the smallest input that
   should succeed? The smallest that should be rejected?
6. **Failure modes & non-goals.** What does the product *not* promise? KPIs that
   signal success (and the one that signals "this whole bet was wrong").

## Contract rung (last, before lock)

- **upstream**: none (root)
- **referrers**: architecture, system-design, design-system, (and dev/design tasks)

## What not to bake in

This doc is the *what* and *why*, never the *how*. If a rung's answer is turning into an
implementation choice ("we'll use Postgres"), stop — that belongs in system-design, and
its *acceptance* belongs here, not its mechanism.