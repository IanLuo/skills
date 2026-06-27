---
name: init-context
description: >
  One-shot bootstrap of agent working context for a project.
  Analyzes the codebase and writes AGENTS.md (a compact index: Intent, run/build/test,
  hot invariants, architecture elevator, deeper-docs pointer table, state-tracking
  protocol) and CURSOR.md (volatile: current position, next action, blockers, open
  issues, health, verification) so every future session can resume.
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

One-shot bootstrap. Writes `AGENTS.md` (compact stable index) + `CURSOR.md` (volatile
state) at the repo root, with a self-contained state-tracking protocol so every future
session can resume. Read the full protocol at
**[references/protocol.md](references/protocol.md)** — the AGENTS.md section is a
summary; this is the canonical rulebook.

AGENTS.md is an **index, not a manifest.** It embeds only what a fresh agent can't
recover from code and would get wrong without: the project intent, verified commands,
a handful of frozen invariants, and a short architecture elevator. Everything else
(PRD, full architecture rationale, design guidelines, pattern catalog, deploy detail)
is pointed to from a "Deeper docs" jump-table — one row per doc that exists on disk
this run. No docs yet → no rows. Runs are reconciling: re-derive the table from disk
state each time; add rows for new docs, drop rows for those that disappeared.

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

### 2. Write AGENTS.md — tier-routing, not section-filling

Copy **[assets/AGENTS.template.md](assets/AGENTS.template.md)** to `AGENTS.md` at
repo root. Then fill each section, routing every survey finding through this gate:

**Embed in AGENTS.md** iff all three hold:
(a) not recoverable by one command/read,
(b) a fresh agent would get WRONG (not just slow) without it, AND
(c) it's a frozen "because" rule or a verified command (tiny token cost).

**Point to from the Deeper-docs table** iff: a doc (PRD, ARCHITECTURE.md, CONVENTIONS.md, design-system.md, docs/adr/NNNN-*, etc.) exists on disk that holds it. Source the table from what's actually on disk this run. Omit rows for docs that don't exist yet. Do not create stub files.

**Drop** everything else. The model is smart; let it derive what it can.

Section-by-section guidance:

- **Intent** — ≤3 lines. The product goal the agent must not satisfice past.
  This is the one thing code doesn't encode and a fresh agent can't re-derive.
- **How to run / build / test** — exact commands. **Verify each command works
  *before* writing it** — run it, confirm it succeeds, then write the verified
  form. If a command is impractical to run (slow build, needs secrets, network),
  say so explicitly rather than write it unverified — an unverified command is
  worse than no command.
- **Hot invariants** — only the frozen "because" rules a fresh agent would
  silently break. If it's a style preference the model already knows, skip it.
  If it's "never touch this boundary because the serializer depends on its shape,"
  embed it. A handful, never a catalog.
- **Architecture elevator** — 5 lines + a one-level repo tree with one-line
  purpose per dir. Deep rationale goes in `ARCHITECTURE.md` or `docs/adr/` — if
  those exist on disk, add rows to the Deeper-docs table.
- **Deeper docs** — scan disk for: `docs/prd.md`, `ARCHITECTURE.md`,
  `CONVENTIONS.md`, `design-system.md`, `docs/adr/*.md`, `docs/release.md`,
  `docs/security.md`. For each that exists, add a row to the table: a "when you
  need…" cue and the path. If zero docs exist, the table has zero rows.
- **State-tracking protocol** — the template carries this block verbatim. Do not
  modify it — it's the contract. The section headers and protocol block must be
  byte-identical across projects so agents can rely on them.

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
- Run `../task-core/scripts/check` (if task-core is deployed) — ensure the drift
  gate passes on the fresh cursor.

### 5. Hand off

Show the user the file tree + a one-line summary of each file. Explain the contract:
every future session reads CURSOR.md first, reconciles `synced:` vs HEAD, works,
rewrites CURSOR.md in-place at session end against the protocol rules. The protocol
lives in AGENTS.md for reference.

## Re-running

init-context is safe to run again as the project matures. On a re-run:

- **Preserve** the user's manual prose and the State-tracking protocol block.
- **Re-derive** the Intent, Hot invariants, and Architecture elevator from a fresh
  survey — update them if the project direction or invariants changed.
- **Re-derive** the Deeper-docs table from current disk state: add rows for new
  docs, drop rows for docs that no longer exist, refresh the "when you need…"
  cues. Never assume a fixed doc set.
- **Don't touch** CURSOR.md unless the user asks — it's volatile; overwriting it
  would lose live task state.

## Rules

- Never overwrite an existing AGENTS.md — merge into it. For CURSOR.md, overwrite
  only if it's a fresh bootstrap (no existing cursor); if one exists, ask.
- Templates in `assets/` are the single source of truth for file structure — copy
  the State-tracking protocol block verbatim. The section headers and protocol
  block must be byte-identical across projects so agents can rely on them.
- Build + test commands in AGENTS.md must be *verified* before writing — run them
  and confirm they succeed. An unverified command is worse than no command.
- The model is already smart. Add only what it doesn't already know. Prefer
  concrete examples over prose.
- AGENTS.md is an index, not a manifest. Route everything through the embed/point/drop gate.
