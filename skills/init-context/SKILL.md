---
name: init-context
description: >
  One-shot bootstrap of agent working context for a project.
  Analyzes the codebase and writes AGENTS.md (a compact index: Intent, run/build/test,
  hot invariants, architecture elevator, deeper docs) and SESSION.md (one-line session
  log: what happened, next step) so every future session can resume.
  Use when starting work on a new or unfamiliar codebase, onboarding to a repo,
  running /init, bootstrapping project context,
  or when a project lacks AGENTS.md.
  Triggers include /init, new project, new codebase, onboard to this codebase,
  bootstrap project context, set up agent context, project context, AGENTS.md.
  Do NOT use for installing dependencies, configuring .env/CI, writing app code,
  or per-file documentation.
metadata:
  audience: personal
  domain: general
---

# init-context

One-shot bootstrap. Writes `AGENTS.md` (compact stable index) + `SESSION.md`
(one-line session log) at the repo root. AGENTS.md is an **index, not a manifest** —
it embeds only what a fresh agent can't recover from code and would get wrong without.

## Workflow

### 1. Survey the project

Read in order:
- `README.md` (if exists), top-level docs
- package manifests: `package.json`, `pyproject.toml`, `Cargo.toml`, `go.mod`, etc.
- source tree: top 2 levels, entry points, top-level dir layout
- `git log --oneline -20`
- CI config (`.github/workflows/`, etc.)
- any existing `AGENTS.md`, `CLAUDE.md`, `.cursor/rules`, `GEMINI.md`

For repos with more than a few top-level dirs, fan out survey dimensions to subagents
in parallel, then synthesize into one AGENTS.md.

### 2. Write AGENTS.md — tier-routing, not section-filling

Copy **[assets/AGENTS.template.md](assets/AGENTS.template.md)** to `AGENTS.md` at
repo root. Then fill each section, routing every survey finding through this gate:

**Embed in AGENTS.md** iff both hold:
(a) not recoverable by one command/read,
(b) a fresh agent would get WRONG (not just slow) without it (e.g., a frozen "because" rule or verified command).

**Point** — list the path — iff a doc (PRD, ARCHITECTURE.md, CONVENTIONS.md,
design-system.md, ADRs) exists on disk that holds deeper detail.

**Drop** everything else.

Section-by-section guidance:

- **Intent** — ≤3 lines. The product goal a fresh agent can't re-derive.
- **How to run / build / test** — exact commands. **Verify each before writing.**
  If impractical to run (slow build, needs secrets, network), say so rather than
  write unverified — an unverified command is worse than no command.
- **Hot invariants** — only frozen "because" rules a fresh agent would silently
  break. A handful, never a catalog.
- **Architecture elevator** — 5 lines + a one-level repo tree with one-line purpose
  per dir. Deep rationale goes in ARCHITECTURE.md or ADRs.
- **Deeper docs** — list paths to locked docs that exist on disk (find with
  `grep -rl '<!-- specs:locked:\|<!-- design:locked:' *.md`). One line each:
  `<path>` — `<one-line purpose>`. Zero docs → omit the section. This is a
  snapshot, not a registry — consumers discover docs by grepping lock markers directly.

### 3. Write SESSION.md

Create `SESSION.md` at the repo root with one line:

```
<YYYY-MM-DD> · init-context · project bootstrapped. Next: choose the first task.
```

SESSION.md is append-only — every session adds one line. A fresh agent reads the
last line to know where things stand. Format: `<date> · <skill> · <what happened>. Next: <next step>.`

### 4. Verify

- AGENTS.md exists at repo root and matches the template structure.
- Every build/test command in AGENTS.md was actually run and succeeded.
- SESSION.md exists with the bootstrap line.
- `git status` shows the new files.

### 5. Hand off

Show the user the file tree + one-line summary of each file. Explain: every future
session reads the last line of SESSION.md, then AGENTS.md, then works. At session
end, append one line to SESSION.md.

## Re-running

Safe to run again as the project matures:
- **Preserve** user's manual prose in AGENTS.md.
- **Re-derive** Intent, Hot invariants, Architecture elevator, and Deeper docs list
  from a fresh survey.
- Don't touch SESSION.md — it's the running session log.

## Rules

- Never overwrite an existing AGENTS.md — merge. Templates in `assets/` are the
  source of truth for file structure.
- Build + test commands must be *verified* before writing. An unverified command is
  worse than no command.
- Add only what a fresh agent can't derive. Prefer concrete over prose.
- AGENTS.md is an index, not a manifest. Route everything through the embed/point/drop gate.
- AGENTS.md/SESSION.md follow the house agent-oriented doc format — bullets, concrete,
  checklist-complete, freshness date (see `skill-man/references/doc-format.md`).
