---
name: architect-workflow
description: Structured loop for system architecture updates. Reads the PRD, maps impacted components, updates architecture docs, and validates against boundaries. Used by pipeline Step 2a or standalone.
---

# Architect Workflow

## Overview
Updates `docs/architecture.md` and `docs/detailed-components.md` to satisfy the PRD delta. Follow this strict loop — no step may be skipped.

## Prerequisites
- You receive all input files inlined in your prompt. Do NOT read files yourself.
- You receive the PRD (`docs/prd/v{N}.md`), current architecture, and current detailed-components.

## Loop

### Step 1: Read PRD Architecture Changes
Parse the PRD. Extract ONLY the `## Changes Required / ### Architecture` section. Ignore UI/UX and plan changes — those are not yours.

List the concrete changes required:
- New rules or boundaries to add
- Existing rules to modify
- Components to add to detailed-components.md
- Interfaces to update or add

### Step 2: Read Architecture Rules (Fresh)
Read the inlined `docs/architecture.md` in full. Identify every immutable boundary rule:
- Vital plugin slots (State, Memory, Vault, Driver, Trace)
- Data boundary rules (State <4KB, Memory for payload, Trace for ephemeral)
- Security rules (credentials never in LLM context, signed URIs)
- Plugin governance (Provider-Instance pattern)

### Step 3: Check for Violations
Map each required change against existing rules. If any change violates an immutable boundary:
- STOP. Report the violation with file + section reference.
- Do NOT make the change.
- Suggest an alternative that respects the boundary.

Example:
```
VIOLATION: PRD asks to store workflow artifacts in State Manager.
  → architecture.md §2.3: State Manager never stores payloads >4KB.
  → Suggestion: Store in Memory Engine, return URI to State Manager.
```

### Step 4: Update architecture.md
Apply only the delta. Do NOT rewrite the file.
- Add new sections for new rules/patterns
- Append to existing sections for modifications
- Never remove existing rules unless the PRD explicitly deprecates them
- Each change must cite the PRD section that drove it

### Step 5: Update detailed-components.md
Apply only the delta.
- Add new component sections (follow existing format: ## N. Component Name)
- Add or modify interface definitions
- Add or modify data structure schemas
- Each change must cite a specific task or requirement

### Step 6: Validate
- Re-read both updated files. Confirm no existing content was lost.
- Confirm every change traces to a PRD requirement.
- Confirm no immutable boundary was violated.
- Run boundary check: do all vital slots still exist? Does State Manager still enforce <4KB? Are credentials still isolated?

### Step 7: Done
Report: files changed, changes made (one-line each with PRD reference), any tradeoffs documented.
