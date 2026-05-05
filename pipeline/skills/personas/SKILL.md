---
name: personas
description: Defines core mindsets for Teldrassil. Each persona lives in its own file — read only the one(s) needed for the current task.
---

# Personas

This file is a thin dispatch index. Each persona has its own file with full instructions.

## Dispatch Table

| Persona | File | Used By |
|---|---|---|
| **Developer** | personas/developer.md | `dev-workflow` Step 3 |
| **Tester** | personas/tester.md | `dev-workflow` Step 3 |
| **Gatekeeper** | personas/gatekeeper.md | `dev-workflow` Step 4, pipeline Step 4 |
| **Reviewer** | personas/reviewer.md | `project-steward`, pipeline Step 4 |
| **Strategist** | personas/strategist.md | `project-steward`, pipeline Step 4 |
| **Document Maintainer** | personas/document-maintainer.md | `dev-workflow` Step 6 |

## Pipeline Personas

These live in the `pipeline-orchestrator` skill:

| Persona | File (in pipeline-orchestrator) | Pipeline Step |
|---|---|---|
| **Analyst** | personas/analyst.md | Step 1 |
| **Architect** | personas/architect.md | Step 2a |
| **Designer** | personas/designer.md | Step 2b |
| **Planner** | personas/planner.md | Step 3 |
| **QA Architect** | personas/qa-architect.md | Step 5 |

## Usage

When adopting a persona, read the corresponding file. When multiple personas are needed (e.g., a persona cascade in `project-steward`), read them in the specified order.
