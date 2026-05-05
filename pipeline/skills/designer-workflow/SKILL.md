---
name: designer-workflow
description: Structured loop for UI/UX design updates. Reads PRD and architecture, maps impacted screens, updates design docs, and validates consistency. Used by pipeline Step 2b or standalone.
---

# Designer Workflow

## Overview
Updates files in `docs/ui-ux/` to satisfy the PRD delta. Follow this strict loop — no step may be skipped.

## Prerequisites
- You receive all input files inlined in your prompt. Do NOT read files yourself.
- You receive the PRD, architecture, and current UI/UX docs.

## Loop

### Step 1: Read PRD UI/UX Changes
Parse the PRD. Extract ONLY `## Changes Required / ### UI/UX`. List each change and determine which files need updating:
- `design-system.md` — if new tokens are needed
- `components.md` — if components are added or modified
- `screens/*.md` — if screen layouts change

### Step 2: Read Architecture Constraints
Read the inlined `docs/architecture.md`. Extract UI-relevant constraints:
- State management patterns (Zustand store boundaries)
- API surface (what data is available to render)
- Plugin boundaries (what UI can and cannot access directly)

### Step 3: Read Current UI/UX State
Read the inlined UI/UX files. Map the current design:
- Active design tokens (colors, spacing, typography)
- Existing components and their states
- Existing screens and layouts

### Step 4: Design Updates
Apply deltas — never rewrite files from scratch.

For `design-system.md`:
- Add new tokens under existing category headings
- Never remove tokens that existing components depend on

For `components.md`:
- Add new components with: name, purpose, states (default/hover/active/disabled/error), props/modes
- Update existing components only if the PRD modifies them
- Each component must reference design system tokens

For `screens/*.md`:
- Add new screen files for new views
- Update existing screens only for changed views
- Include: layout description, component references, interaction flow, responsive breakpoints

### Step 5: Consistency Check
- Every screen references components that exist in `components.md`
- Every component references tokens that exist in `design-system.md`
- Every UI element has a backing data model from `architecture.md`
- No UI element accesses data that architecture restricts

### Step 6: Validate
- Re-read all updated files. Confirm no existing content was lost.
- Confirm every change traces to a PRD UI/UX requirement.
- Confirm consistency with architecture constraints.

### Step 7: Done
Report: files changed, components added/modified, screens updated, any design decisions to document.
