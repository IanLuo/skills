---
name: handoff
description: Extract a verified, facts-only session summary so a fresh session can resume without guessing. Captures dead ends, decisions with rationale, and repo-verified claims. Writes HANDOFF.md to the project root and chains across multi-session work streams. Use when wrapping up, switching agents, resuming work, or before /compact. Do NOT use for mid-session progress notes or general documentation.
metadata:
  audience: personal
  domain: general
---

# handoff

Produce a handoff document a fresh agent can resume from. What makes this different:
dead ends are mandatory, claims are verified against the repo (not trusted from memory),
and existing artifacts are cited by path instead of duplicated.

## Start

Before writing a single claim, verify repo state:

```bash
git status --short && echo "---" && git diff --stat && echo "---" && git log --oneline -5
```

Re-read every file the handoff will name. Run tests if claiming they pass.
Sniff for prior handoffs: `ls HANDOFF.md .claude/handoff/SESSION.md 2>/dev/null`.
If a prior handoff exists, read it — chain by incrementing its sequence number;
don't repeat what it already captured.

## Work

Scan the full session. Extract only what a fresh agent can't derive from code alone.
Drop greetings, resolved clarifications, transient retries, and tangents.

### Format

Write `HANDOFF.md` at the project root (overwrite; a single file, not a log):

```
# Handoff: <one-line topic> — seq <N>

## Goal
- <1–2 sentences on what we were trying to accomplish.>

## Done
- [V] <concrete outcome with file paths.>   ← [V] = verified against repo during handoff
- [?] <outcome recalled from memory.>       ← [?] = not re-verified; treat as a lead

## Dead ends (do not repeat)
- <Approach tried. Exact error or reason it failed. Why abandoned.>
  If none: "None — no dead ends this session."

## Decisions
- Decided <X> because <Y>. Rejected <Z>.

## In progress
- <file:line — current state. What's unfinished.>

## Blocked / open questions
- <What's stuck, what input is needed, what decisions are pending.>

## Next step
- <ONE concrete, immediately actionable step. Not a list.>

## Parameters to resume
- repo: <path>, branch: <name>
- key files: <paths that must be re-read, max ~5>
- env / tool versions: <only if non-standard>

## Artifacts
- <path-to-existing-doc:section> — <what it captures>
  (cite PRDs, ADRs, specs, design docs by path instead of restating them.)
```

### Section rules

- **Every claim in Done gets a tag:** `[V]` means you verified it against the repo during
  this handoff (re-read the file, ran the command, checked the output). `[?]` means you're
  recalling from memory — a fresh agent must treat it as a lead, not a fact.
- **Dead ends are mandatory.** If nothing failed, write "None." This section exists
  so a fresh agent doesn't repeat expensive mistakes. Include the exact error, not a
  paraphrase.
- **Artifacts over inlining.** If a PRD, ADR, design doc, or spec already records a
  decision, cite `path:section` — don't restate. Keeps the handoff short and can't
  drift from the source.
- **One next step.** A fresh agent should know exactly what to do first. Not a list.

## Done

- Every claim in Done is tagged `[V]` or `[?]`.
- Every file path in the handoff exists at write time.
- Dead ends section is populated (even if "None").
- Decisions include rationale ("because…").
- Artifacts section cites existing docs by path rather than restating.
- If a prior handoff existed: sequence number incremented, prior handoff archived to
  `.claude/handoff-history/<YYYY-MM-DD>-seq<N>.md`.
- HANDOFF.md written to project root.
- Append one line to SESSION.md:
  `<date> · handoff · seq <N>, HANDOFF.md written. Next: resume from HANDOFF.md.`

## Resuming from a handoff

When a fresh session reads HANDOFF.md, apply the **source-of-truth rank**:

```
running code > tests > docs > PROGRESS.md > HANDOFF > older handoffs
```

If live code contradicts the handoff, the code wins — say so and update.
Re-verify `[?]` claims against the current repo before acting on them.
Surface open questions first, confirm the plan with the user, then continue.
