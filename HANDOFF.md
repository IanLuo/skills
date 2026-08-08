# Handoff: librarian + handoff skill redesign — seq 1

## Goal

Improve the `librarian` skill (was slow, under-capturing) and create an improved
`handoff` skill, grounded in community research (GitHub + web). Then commit.

## Done

- [V] **Librarian speed fixed** — `skills/librarian/SKILL.md`. Cross-scoring fanout (≥2
  scorers/finding + refinement loop) replaced with **self-scoring single-pass**: research
  agents self-assess on the 3 rubric dimensions, orchestrator reviews in seconds. Agent
  count for `read` depth: 12–20 → 3–5.
- [V] **query.py deleted** — `skills/librarian/scripts/query.py` removed (117 lines).
  Query workflow now uses **ripgrep** one-liners in SKILL.md (full-body search,
  `--glob '!index.md'`, ranked by match count). Fixes the `len(t) > 2` acronym bug by
  eliminating tokenization entirely.
- [V] **Explicit save model** — `skills/librarian/SKILL.md` steps 6–7 + Rules. No
  auto-persist, no save-before-show. Show entry → ask "Save to library?" → persist only
  what the user names (whole entry or subset). Query is read-only.
- [V] **Handoff skill created** — `skills/handoff/SKILL.md` (new, untracked). Dead ends
  mandatory, `[V]`/`[?]` claim tags, artifacts cited by path, chain sequence numbers,
  source-of-truth rank on resume (code > tests > docs > HANDOFF > older handoffs).
- [V] **Deployed** — handoff replaced nix version at `~/.claude/skills/handoff`
  (symlink → repo), deployed to all 10 agents. task-core dangling symlinks pruned (7 agents).
- [V] **Validate** — `validate.py` passes for both `librarian` and `handoff`.
- [V] **Committed** — c7af840 "skill-set simplification" (34 files, README + AGENTS.md
  rewritten, CURSOR→SESSION, task-core eliminated) was committed earlier this session.

## Dead ends (do not repeat)

- **Cross-scoring subagents** (≥2 sibling scorers per finding + refine/plateau/cap loop)
  — the main slowness. Removed; scoring is mechanical and the orchestrator can review
  in seconds. Do not re-add.
- **Index-only grep** (Option B: search `index.md` only) — rejected because it loses
  body-level detail; a keyword in a finding's body never hits. Went with full-body `rg`.
- **write-entry.py in bash** — rejected; its complexity (YAML frontmatter, JSON, slug,
  deterministic index rebuild) is justified in Python. Replacing it would be harder, not easier.
- **`grep` vs `rg`** — this machine's `grep` is a shell function wrapping `ugrep`, but
  other agents may get BSD grep. Use `rg` explicitly; it's installed
  (`/etc/profiles/per-user/ianluo/bin/rg`).

## Decisions

- Use **ripgrep** over grep for queries — C-parallel, fast at thousands of entries, no
  tokenization (so no acronym bug). Match count is the relevance proxy.
- **Self-score, then orchestrator reviews** — no scoring subagents ever.
- **Explicit saves only** — library changes only on an explicit "save" from the user.
- **Handoff = repo-verified claims** — every Done claim tagged `[V]` (verified) or `[?]`
  (from memory, treat as lead).
- **Handoff keeps to 4 sections was insufficient** — expanded to 8 (Goal, Done, Dead ends,
  Decisions, In progress, Blocked, Next step, Parameters, Artifacts).
- **PreCompact hook dropped** (user decided) — handoff is run manually, not automated.

## In progress

- None — all planned edits are applied.

## Blocked / open questions

- **Librarian never live-tested end-to-end** — web was blocked, so the new flows
  (self-scoring, rg query, explicit save) have not run in a real session. First real
  `/librarian` research should be treated as a forward-test.
- **Memory stale** — `~/.claude/projects/.../memory/librarian-skill.md` still says
  "cross-scoring fanout"; update to self-scoring single-pass + explicit save.
- **Uncommitted** — librarian changes + new handoff skill are not committed yet.

## Next step

- Commit the uncommitted work: `git add -A && git commit` (librarian edits + new handoff skill).

## Parameters to resume

- repo: `/Users/ianluo/Documents/apps/skills`, branch: `main`
- key files:
  - `skills/librarian/SKILL.md` — rg query + self-scoring + explicit-save workflow
  - `skills/librarian/references/rubric.md` — self-scoring rubric
  - `skills/librarian/scripts/write-entry.py` — the only writer; `init-library.sh` bootstraps
  - `skills/handoff/SKILL.md` — new skill, this handoff follows its format
  - `skills/skill-man/scripts/validate.py` — run to validate skills
- env: none non-standard; `LIBRARY_ROOT` overrides library path (default `~/Documents/librarian`)

## Artifacts

- `~/Documents/librarian/library/` — user data, outside the repo; 2 entries
  (`agent-engineering/` handoff research, `technology/cni-basics`), `index.md` maintained
  by `write-entry.py`
- `skills/librarian/SKILL.md` — the librarian workflow
- `skills/handoff/SKILL.md` — this skill, whose format this document follows
