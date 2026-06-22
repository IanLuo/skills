---
name: init-context
description: >
  One-shot bootstrap of agent working context for a project.
  Analyzes the codebase and writes AGENTS.md (stable: purpose, how-to-run, conventions, deploy, gotchas)
  and CURSOR.md (volatile: current position, next action, blockers, open issues, health, verification)
  with a self-contained state-tracking protocol so every future session can resume.
  Use when starting work on a new or unfamiliar codebase, onboarding to a repo,
  running /init, bootstrapping project context,
  or when a project lacks AGENTS.md and a state-tracking cursor.
  Triggers include /init, new project, new codebase, onboard to this codebase,
  bootstrap project context, set up agent context, project context, AGENTS.md, CURSOR.md.
  Do NOT use for installing dependencies, configuring .env/CI, writing app code,
  per-file documentation, or ongoing cursor maintenance —
  the state-tracking protocol embedded in AGENTS.md handles maintenance; this skill only bootstraps.
metadata:
  audience: personal
  domain: general
---

# init-context

One-shot bootstrap. Writes `AGENTS.md` (stable) + `CURSOR.md` (volatile) at the
repo root, with a self-contained state-tracking protocol so every future session
can resume. Read the full protocol at
**[references/protocol.md](references/protocol.md)** — the AGENTS.md section is a
summary; this is the canonical rulebook.

## Workflow

### 1. Survey the project

Read these in order, building the mental model:
- `README.md` (if it exists), any top-level docs
- package manifests: `package.json`, `pyproject.toml`, `Cargo.toml`, `go.mod`, etc.
- source tree: top 2 levels, entry points, top-level dir layout
- `git log --oneline -20` (recent work)
- CI config (`.github/workflows/`, etc.)
- any existing `AGENTS.md`, `CLAUDE.md`, `.cursor/rules`, `GEMINI.md`

Goal: understand *purpose*, *stack*, *how to run/build/test*, *conventions*, *deploy model*, *gotchas*.

### 2. Write AGENTS.md from template

Copy **[assets/AGENTS.template.md](assets/AGENTS.template.md)** → `AGENTS.md` at
repo root. Fill every section from survey findings:

- **What this project is** — one sentence. If a README exists and says it well, quote it.
- **How to run / build / test** — exact commands. **Verify each command works
  *before* writing it** — run it, confirm it succeeds, then write the verified
  form. Verify-then-write is sequential per command, not a deferred batch at the
  end. If a command is impractical to run (slow build, needs secrets, network),
  say so explicitly rather than write it unverified — an unverified command is
  worse than no command. Example: `npm test`, `cargo build`, `pytest`.
- **Conventions** — code style, naming, architectural rules, workflow. The non-obvious
  things a fresh agent gets wrong.
- **Deploy / infra** — how it ships, where it runs, secrets/config locations.
- **Gotchas / don't touch** — files that break in non-obvious ways, constraints not
  visible in code.

Keep it terse. Stable context is read every session — ruthlessly cut platitudes.

The template already carries the **State-tracking protocol** section (inclusion gate,
cursor fields, discipline). Do not modify it — it's the contract. If the project
already has an AGENTS.md, merge into it: preserve existing content the template
doesn't cover, add missing sections, and ensure the protocol section is present.

### 3. Write CURSOR.md from template

Copy **[assets/CURSOR.template.md](assets/CURSOR.template.md)** → `CURSOR.md` at
repo root. Fill initial state:

- **synced** — set to current `git rev-parse HEAD`.
- **Position / Next** — the effort-goal and the first next-action. If the survey
  surfaced an obvious starting point, name it. Otherwise: "Initial bootstrap —
  next: use the state-tracking protocol in AGENTS.md to begin work."
- **Blockers** — (empty or "none").
- **Open** — surfacing any questions the survey couldn't resolve, assumptions made,
  decisions visibly pending.
- **Health** — 🟢 if build + test pass (you verified in step 2); 🔴 if anything fails.
- **Verification** — the commands you ran and their results (evidence not claims).
- **Decisions** — any invariants resolved during survey.
- **Active pointers** — key files referenced, each verified to exist.

### 4. Verify

- Both files exist at repo root.
- `CURSOR.md` ≤40 lines, ≤2000 chars.
- Every file path listed in CURSOR.md exists on disk.
- `synced: <sha>` matches current HEAD.
- `git status` shows the two new files.

### 5. Hand off

Show the user the file tree + a one-line summary of each file. Explain the contract:
every future session reads CURSOR.md first, reconciles `synced:` vs HEAD, works,
rewrites CURSOR.md in-place at session end against the protocol rules. The protocol
lives in AGENTS.md for reference.

## Rules

- Never overwrite an existing AGENTS.md — merge into it. For CURSOR.md, overwrite
  only if it's a fresh bootstrap (no existing cursor); if one exists, ask.
- Templates in `assets/` are the single source of truth for file structure — copy
  them verbatim. The section headers and protocol block must be byte-identical
  across projects so agents can rely on them.
- Build + test commands in AGENTS.md must be *verified* before writing — run them
  and confirm they succeed. An unverified command is worse than no command.
- The model is already smart. Add only what it doesn't already know. Prefer
  concrete examples over prose.
