# System-design question ladder

Read this when grilling a `--type system-design` doc. One rung at a time; answer must
clear [judging.md](judging.md) before advancing. A system-design doc is **not** the
PRD and **not** the architecture — it holds the *component-level how* (components,
data, interfaces, failure) that the architecture layer loads on.

Contract defaults: `upstream: spec`; `referrers: architecture, design-system`.

## Rungs

1. **Components & ownership.** List every component with a one-line job and an owner
   (a system area, a team-size-1, a boundary). If a component has no owner, it's a
   leak. Grill: "what does this component *refuse* to do?"
2. **Data model.** The persistent state and its shape. For each entity: who writes it,
   who reads it, what happens on stale reads. Edge input: what's the record with every
   optional field empty — accepted, defaulted, or rejected?
3. **Boundaries & interfaces.** The contract between every pair of components. If two
   components share a schema, freeze the schema here — it's the boundary that keeps
   them independent.
4. **Failure modes.** For each component: what's the behavior when its dependency is
   down, slow, or returns garbage? Fail-closed vs fail-open is a decision, not a default.
   Name the one failure that would wreck the product.
5. **Scale assumptions.** The orders of magnitude the design assumes — simultaneous
   users, write rate, data size. A design silent on scale is a design that breaks at
   10×.

## Contract rung (last, before lock)

- **upstream**: the locked spec (name its path)
- **referrers**: architecture, design-system, dev tasks

## What not to bake in

This is the *system-level how*. Do not pull PRD scope re-statements (that's re-reading
the parent, not re-designing the system) and do not pull repo-layout rationale (that's
architecture). Keep this to components + data + edges + failure.