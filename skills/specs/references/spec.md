# Spec question ladder

Read this when grilling a `--type spec` doc. Ask one rung at a time. Don't advance to
the next until the answer clears the gate in [judging.md](judging.md). Lead with the
load-bearing rung — the one a fresh agent would get *wrong* without it.

The spec is a **root** doc — `upstream: none` in the contract. Its `referrers` are
architecture, system-design, design-system, and any dev/design task that touches the
scope. The structure follows the IEEE 29148 §9.6 SRS outline — a PRD is only the
*what/why* layer; this is the complete requirements contract.

## Rungs (ask in order, but follow the risk)

1. **Problem & who breaks.** What breaks for whom if we do nothing? One sentence each —
   a concrete **who** (persona, not "users") and a concrete **break** (a failure, not
   "pain"). If you can't name both, the build is premature — keep grilling.
2. **Non-negotiable outcome.** The single thing that, if missing, the release is a
   failure — explicit, falsifiable ("ship 5 playable objects", not "feels fun").
   Confirm: would a tester pass/fail this without arguing?
3. **Scope in / out + primary flow.** What's in V1, and what's deliberately *not*. Name
   at least three non-goals; grill each "why is this out, and what keeps it out?". Then
   the **primary-flow walkthrough**: the one path a user takes, step by step — a fresh
   agent should be able to follow it without asking. If there are two primary flows, you
   have two products — make the user pick.
4. **Acceptance criteria.** Testable bullets, one per outcome. Each must be checkable by
   someone who didn't write it. Gherkin (Given/When/Then) where behavior is stateful; 3–8
   per outcome. Empty/edge inputs: the smallest input that should succeed? The smallest
   that should be rejected? Unsupported claims are dropped, not hedged.
5. **KPIs & failure signal.** What "good" looks like in production, measured. And the ONE
   metric that signals "this whole bet was wrong" — the off-switch. If you can't name
   both, the outcome isn't load-bearing yet.
6. **Non-functional requirements.** Only the ones this product can't ignore, each
   quantified ("p95 < 200ms", "RPO = 0", "no secrets in logs"): performance, security,
   privacy, reliability/availability, usability, observability, cost, compatibility,
   operations. Grill: "what happens if it's 10× worse than this number?" If the answer is
   "nothing changes," the NFR is decorative — cut it.
7. **Assumptions & dependencies.** What we take for granted + external dependencies. For
   each: what would invalidate it, and what do we do then? A dependency without an
   invalidation trigger is a guess. This is a checklist, not open questions.
8. **Data requirements.** Required inputs (what data must exist), expected outputs (what
   must be produced), and what must survive (persistence, durability, retention). Edge:
   the empty record — accepted, defaulted, or rejected? "What must survive" includes
   what survives a crash or redeploy.
9. **Rollback & recovery.** What happens when a step fails destructively? The recovery
   path, and what "reverted" means for data and state. Fail-closed vs fail-open is a
   decision, not a default. Name the one failure that would wreck the product.
10. **Security & compliance.** Secrets redaction, destructive-action confirmation,
    provenance, and the regulatory/legal obligations that actually apply. If none apply,
    say so explicitly — don't invent a compliance section.
11. **Verification plan.** For each acceptance criterion and each kept NFR: how it gets
    proven — automated test, manual check, code review, observability metric. Traceable:
    every requirement maps to a verification case. The verification plan is the bridge
    to review-task.

## Design tree (dependencies)

The rungs are a decision tree, not a fixed queue. Work the **frontier** in rounds — ask
every rung whose prerequisites are settled, each with `➡️ your recommended answer`;
recompute after each round. Base edges (follow the real dependencies as answers surface
them — a rung waits only on what it actually needs):

| rung | depends on |
|---|---|
| R1 Problem & who breaks | — (root) |
| R7 Assumptions & dependencies | — (root, settle early — underpins everything) |
| R2 Non-negotiable outcome | R1 |
| R3 Scope in / out + primary flow | R1, R2 |
| R4 Acceptance criteria | R2, R3 |
| R5 KPIs & failure signal | R2, R3 |
| R6 Non-functional requirements | R2, R3 |
| R8 Data requirements | R3, R4 |
| R9 Rollback & recovery | R3, R8 |
| R10 Security & compliance | R3, R4 |
| R11 Verification plan | R4, R6 |

Facts are your job: before a rung that needs the environment (existing code, data,
behavior), dispatch a sub-agent — never ask the user to recite their repo.

## Contract rung (last, before lock)

- **upstream**: none (root)
- **referrers**: architecture, system-design, design-system, (and dev/design tasks)

## What not to bake in

This doc is the *what* and *why*, never the *how*. If a rung's answer is turning into an
implementation choice ("we'll use Postgres"), stop — that belongs in system-design, and
its *acceptance* belongs here, not its mechanism. Component data schemas, repo layout,
and runtime commands live in system-design / architecture / AGENTS.md, not here.

## Document format

The locked doc follows the house agent-oriented format — bullets only, completeness
via checklist (each rung above becomes a required non-empty section header; explicit
`N/A:` beats absence), stable machine-greppable anchors, explicit states, freshness
stamp. See the canonical standard:
[`skill-man/references/doc-format.md`](../../skill-man/references/doc-format.md).
