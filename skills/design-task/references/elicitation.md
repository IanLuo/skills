# Design Task Elicitation

Read this before choosing a visual direction for a design task.

## Gather Or Infer

Capture the smallest useful set:

- Audience and primary workflow.
- Product category and emotional tone.
- Brand constraints, forbidden styles, or existing design system.
- Reference products, screenshots, mood images, or competitor patterns.
- Typography preferences or constraints.
- Color preferences, accessibility requirements, and density.
- Device targets and viewport priorities.

Ask the user only when a missing answer would materially change the result. Otherwise
make a labeled assumption and continue.

## Durable Design Artifact

Create or update `design-system.md` when the task spans more than a tiny visual tweak.
Include:

- design goals and accepted references,
- palette tokens and contrast notes,
- typography scale,
- spacing/radius/elevation tokens,
- component inventory and states,
- fidelity ledger for screenshots, prototypes, or accepted concept images.

After the user accepts a concept, treat it as frozen. Future implementation should
match it unless the user explicitly changes direction.
