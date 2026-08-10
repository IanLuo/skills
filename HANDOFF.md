# Handoff: librarian deep-edit + handoff skill shipped + subagent research — seq 2

## Goal

Continue the skill-set simplification. This session: evolve the librarian skill through multiple optimization rounds, ship the new handoff skill, and research subagent-management patterns for a future skill.

## Done

- [V] **Librarian query: query.py replaced with ripgrep** — `skills/librarian/scripts/query.py` deleted (117 lines). `skills/librarian/SKILL.md` Query workflow now uses `rg -il` / `rg -i -c` one-liners for full-body search with match-count ranking. Fixes the `len(t) > 2` acronym bug (no tokenization in rg). Commit: `fa0d647`.
- [V] **Librarian scoring: cross-scoring → self-scoring** — Cross-scoring fanout (≥2 sibling scorers per finding + refinement loop) replaced with self-scoring single-pass. Research agents self-assess on rubric dimensions; orchestrator reviews in seconds. Agent count for `read` depth: 12–20 → 3–5. Commit: `fa0d647`.
- [V] **Librarian saves: auto-persist → explicit-only** — Step 6-7 rewritten: show entry first, ask "Save to library?", persist only what the user explicitly names (whole entry or subset). Query is read-only. New rule at top: "Explicit saves only." Commit: `fa0d647`.
- [V] **Handoff skill created, deployed, exercised** — `skills/handoff/SKILL.md` created (8-section format, `[V]`/`[?]` tags, dead ends mandatory, artifacts-by-path, chain sequence numbers, source-of-truth rank on resume). Deployed over nix-managed version to all 10 agents. Run twice now: seq 1 (prior session) and seq 2 (this session). Commit: `fa0d647`.
- [V] **Handoff design validated** — archive-on-write model confirmed against community research (all tools converge on this). Entry saved to librarian library: `~/Documents/librarian/library/agent-engineering/handoff-archive-model-consume-on-resume-vs-supersede-on-write.md`. Commit: `fa0d647`.
- [V] **Librarian entry: handoff ecosystem 2026** — Research on community handoff patterns saved to library during prior session. 3 entries total in library now.
- [V] **README.md + AGENTS.md rewritten** — 10-skill table, 5-skill pipeline diagram, lock markers, SESSION.md protocol. Commit: `c7af840`.
- [V] **Committed** — `fa0d647` (librarian self-scoring + rg queries + new handoff skill). Working tree clean.

## Dead ends (do not repeat)

- **Index-only grep for librarian queries** (Option B: search `index.md` only) — rejected because it loses body-level detail; a keyword buried in a finding's body never hits. Went with full-body `rg`.
- **"Write-to-files" pattern as default for subagent delegation** — grilled; concluded it's unnecessary for simple delegation. Subagent final text IS the result. Files only needed for: cross-agent coordination, >10K-token results, or cross-session persistence. Default mode: spawn → wait → read final text → report. No files.
- **Hooks-based automation for handoff** (PreCompact safety net) — user explicitly rejected. Handoff is manual-only.

## Decisions

- **ripgrep over Python for queries** — C-parallel, fast at thousands of entries, no tokenization. Match count is relevance proxy.
- **Self-score over cross-score** — scoring is mechanical; orchestrator review in seconds beats spawning 8 scoring subagents.
- **Explicit saves over auto-persist** — library changes only on explicit "save" from user. Show first, write second.
- **Supersede-on-write over consume-on-resume** for handoff archiving — crash-safe, compaction-safe. All community tools agree.
- **Delegate skill should NOT default to file-output** — subagent final text is the result. Grill confirmed.

## In progress

- **Subagent-management research completed but NOT saved** — 4-agent fan-out (native tools, community skills, context isolation, orchestration patterns) returned rich findings. Two skill ideas survived: `delegate` (lightweight fan-out to subagents for noisy work) and `agents-health` (worktree cleanup, orphan detection). These were NOT written to the librarian library (user never said "save"). The transcripts live in `~/.claude/projects/{project}/{sessionId}/subagents/agent-{id}.jsonl`.
- **No subagent skills implemented yet** — research is done, synthesis was presented, but the user moved on to grill before confirming next steps.

## Blocked / open questions

- **Librarian never live-tested end-to-end** — web was blocked during development. New flows (self-scoring, rg query, explicit save) exist on paper only.
- **Memory notes may be stale** — `librarian-skill.md` memory may still describe old cross-scoring model.

## Next step

- Decide whether to implement `delegate` and/or `agents-health` skills based on the subagent research, or move on to other priorities.

## Parameters to resume

- repo: `/Users/ianluo/Documents/apps/skills`, branch: `main`
- key files:
  - `skills/librarian/SKILL.md` — rg query + self-scoring + explicit-save workflow
  - `skills/handoff/SKILL.md` — session handoff skill (format this document follows)
  - `skills/grill/SKILL.md` — cross-cutting critical thinking
  - `AGENTS.md` — repo index; `README.md` — skills table + design
- env: none non-standard
- subagent transcripts: `~/.claude/projects/.../subagents/agent-*.jsonl` — subagent research findings

## Artifacts

- `HANDOFF.md` — this file, seq 2. seq 1 archived to `.agents/handoff-history/2026-08-06-seq1.md`
- `SESSION.md` — two-line session log (seq 1 + seq 2)
- `~/Documents/librarian/library/` — 3 entries (handoff ecosystem, CNI basics, handoff-archive-model)
- `~/.claude/projects/.../subagents/agent-*.jsonl` — subagent research transcripts (not in repo)
