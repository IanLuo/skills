---
name: design-task
description: Start and run product/interface design tasks with task-core tracking plus elicitation, references, style direction, design guide/tokens, component inventory, concept acceptance, and fidelity evidence. Use for triggers like "new design task", "design this app/site", "make a UI", "visual direction", "style guide", "design system", "mockup", "prototype", or "redesign". Do NOT use for ordinary backend/dev-only tasks or pure code refactors.
metadata:
  audience: personal
  domain: design
---

# design-task

Use this for visual, product, or interface design work where taste decisions need to
survive across implementation sessions.

## Start

1. Read the project cursor or equivalent state file before editing.
2. Create or refresh the task state:

```bash
../task-core/scripts/create-goal --type design --summary "<task summary>"
```

3. Read [references/elicitation.md](references/elicitation.md) before choosing a
   visual direction.
4. Read [references/triggers.md](references/triggers.md) when deciding what progress
   and evidence to record.

## Work

- Elicit or infer audience, style references, typography, color, density, and brand
  constraints before committing to a direction.
- Record decisions in a durable design artifact, usually `design-system.md`, with
  tokens, typography, components, states, and accepted concepts.
- Treat accepted visual concepts as frozen inputs. Do not take creative liberties
  after acceptance unless the user reopens the direction.
- Update progress after each material phase:

```bash
../task-core/scripts/update-progress --type design --status claimed --summary "<what changed>"
../task-core/scripts/update-progress --type design --status verified --evidence "<visual QA or fidelity evidence>"
```

### Delegation

For any non-trivial task (multi-screen, multi-component, or requires broad exploration),
delegate heavy work to subagents via the Agent tool. The orchestrator that loaded this
skill:

- **Owns** the goal, cursor state, design direction, and final acceptance gate.
- **Delegates** elicitation, reference gathering, component design, mockup generation,
  and fidelity checks to subagents. Each subagent gets a focused prompt with the
  specific scope it owns and the design artifact it must read.
- **Merges** results — collect subagent outputs, reconcile against the design
  artifact, and update progress.
- **Does NOT** inline large design explorations or read dozens of reference files
  itself. If the task spans more than 2-3 components or screens, spawn subagents.

For complex design tasks, fan out independent components/screens in parallel
subagents, then integrate into a single coherent artifact.

## Done

- A design task is verified only when evidence exists: screenshot review, fidelity
  ledger, accepted concept image, visual regression check, or explicit user approval.
- If the design artifact is stale, update it before calling the task complete.
