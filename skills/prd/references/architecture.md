# Architecture question ladder

Read this when grilling a `--type architecture` doc (usually `ARCHITECTURE.md` or the
held-over top-level file). One rung at a time; [judging.md](judging.md) gates each.

The architecture doc is the **load-bearing structure** — layers, ownership, repo tree,
cross-cutting concerns. It is *not* a duplicate of AGENTS.md's architecture-elevator
(that's the index) and *not* a duplicate of system-design (that's component-level how).
This doc holds the elevator + the rationale for *why these choices*, which init-context
deliberately leaves to a pointer.

Contract defaults: `upstream: prd, system-design`; `referrers: dev/design tasks`.

## Rungs

1. **Load-bearing structure.** The 3-5 decisions that, if changed, would make large
   parts of the repo wrong. For each: the choice, the constraint that forced it, and
   the one alternative rejected (and why). "Because it works" is not a reason — grill.
2. **Layers & ownership.** A one-level view of the structural layers and which owns
   what. The boundary rule between them (who depends on whom, one direction).
3. **Repo tree.** One line per top-level dir: its single responsibility. Deeper nesting
   belongs only if a subdir owns an invariant the rest of the repo assumes.
4. **Cross-cutting concerns.** Logging, auth, error model, state — the few global
   conventions that don't belong to any one layer. Each: where the convention lives,
   and how a new component obeys it (one-line, not a manual).
5. **Pointers.** ADRs (`docs/adr/*`), CONVENTIONS.md, the system-design doc — list the
   ones that exist on disk. Don't describe them; point to them. This keeps the
   architecture doc an index-of-rationale, not a manifest.

## Contract rung (last, before lock)

- **upstream**: the locked PRD + locked system-design (name both paths)
- **referrers**: dev tasks, design tasks

## What not to bake in

No runtime commands (AGENTS.md owns verified run/build/test). No component-level data
schemas (system-design owns them). No UI/visual guidance (design-task / design-system
owns those). If a rung starts re-stating something a sibling doc already holds, stop and
point to it instead — re-derive/pointers-over-contents is the discipline.