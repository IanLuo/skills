---
name: design-task
description: Start and run product/interface design tasks with elicitation, references, style direction, design guide/tokens, component inventory, concept acceptance, and fidelity evidence. Use for "new design task", "design this app/site", "make a UI", "visual direction", "style guide", "design system", "mockup", "prototype", or "redesign". Do NOT use for ordinary backend/dev-only tasks or pure code refactors.
metadata:
  audience: personal
  domain: design
---

# design-task

Use this for visual, product, or interface design work where taste decisions need to
survive across implementation sessions.

## Start

1. Read `AGENTS.md` before editing.
2. Find locked docs on disk: `grep -rl '<!-- specs:locked:' *.md`. If a needed
   upstream doc (PRD, architecture) is absent, flag it and ask before committing
   a direction. Don't silently assume product intent.

## Work

### Elicit before committing

Capture: audience, product category, emotional tone, brand constraints, reference
products, typography, color/density/accessibility, device targets. Ask the user only
when a missing answer would materially change the result. Otherwise make a labeled
assumption and continue.

### Build the design artifact

Record decisions in a durable design artifact, usually `design-system.md`:
- design goals and accepted references
- palette tokens and contrast notes
- typography scale
- spacing/radius/elevation tokens
- component inventory and states

Treat accepted visual concepts as **frozen inputs**. Do not take creative liberties
after acceptance unless the user reopens the direction.

For multi-screen/component tasks, fan out subagents in parallel, then integrate into one artifact.

## Done

- A design task is verified only when evidence exists: screenshot review, accepted
  concept image, visual regression check, or explicit user approval.
- When verified, stamp a lock marker as the first line of the design artifact:
  `<!-- design:locked:<git sha> <date> -->`. This is the freeze signal — dev-task checks
  for it before implementing. Re-run design-task to update (overwrite the old marker).
- The design artifact follows the house agent-oriented doc format — bullets only,
  completeness via checklist, explicit `N/A:`, freshness stamp. See
  `skill-man/references/doc-format.md`.

### Verification evidence

Good: Screenshot reviewed at named viewport sizes. User explicitly approves a concept.
Accessibility/contrast check results recorded.

Not evidence: "Looks polished." "Modern and clean." An unreviewed screenshot.
