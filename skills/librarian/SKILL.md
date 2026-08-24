---
name: librarian
description: Research questions with self-scoring subagents (one at default depth, targeted follow-up on gaps), curate verified results into a durable library at ~/Documents/librarian/library/, and answer later questions by querying it first. Use for "research X", "look into X", "study X", "what do I know about X", "find out about X", or any find/verify/synthesize request that belongs in a queryable library. Depth argument — skim (default), read, or study. Do NOT use for codebase exploration or software-architecture research (use Explore); for a one-shot report with no library curation, prefer deep-research.
metadata:
  audience: personal
  domain: research
compatibility: Research workflow requires internet (WebSearch/WebFetch) and the Agent tool. Query workflow runs offline against the local library. Scripts require Python 3 and bash.
---

# librarian

A personal research librarian. Two workflows, in order:

1. **Query** — answer from the library if it already has usable notes. **MANDATORY
   FIRST STEP**: run the rg search before any web research.
2. **Research** — only when the library is thin. Lean: self-scoring subagents, one at
   default depth, one targeted follow-up on gaps.

## Depth

Default `skim`.

| depth | subagents | style |
|---|---|---|
| `skim`  | 1 | ONE subagent covers the whole question — it decomposes into sub-angles itself |
| `read`  | 2–3 | one subagent per angle |
| `study` | 3–5 | one subagent per angle, deeper coverage |

## Query workflow — STEP 0, mandatory

You MUST run this before any web research. If the library answers, stop — no Research.

1. Check if the library exists: `ls ~/Documents/librarian/library/index.md`. If missing,
   run `bash ../librarian/scripts/init-library.sh` once.
2. Search it with ripgrep — full-body search, ranked by match count. The library root
   is `~/Documents/librarian/library` (`$LIBRARY_ROOT/library` overrides). Build an
   alternation from the question's key terms (`term1|term2`):

   ```bash
   # Files that mention any term (full-body search; index.md excluded)
   rg -il "term1|term2" "$LIBRARY_ROOT/library" --glob '!index.md'
   # -> absolute paths, one per line

   # Ranked by relevance (match count, descending)
   rg -i -c "term1|term2" "$LIBRARY_ROOT/library" --glob '!index.md' \
     | sort -t: -k2 -rn
   ```

   `rg` searches the full body, not just titles — details buried in a finding still
   hit. Match count is the relevance proxy (a file that mentions the term 20× is more
   relevant than one that mentions it once). It stays fast at thousands of entries.

3. If matches return: read the top entries, summarize the key facts tersely — bullets
   only, no framing prose — **with source links and confidence tags** for the user, and stop.
4. If no matches (or the user explicitly wants fresh info): fall through to Research.

   **Query is read-only — it never saves.** If the user asks to file something they
   found during Query, treat it as a Research-style entry and confirm before writing.

## Research workflow

Run ONLY after Query found nothing usable.

1. If the library is missing, run `init-library.sh`.
2. Decompose the question into N angles (N from the depth table). For `skim` (1
   subagent), the ONE subagent covers the WHOLE question — it decomposes into sub-angles
   itself; you don't split it.
3. Fan out N `general-purpose` subagents via the Agent tool. Each researches with
   WebSearch/WebFetch and returns **structured output**:
   - **angle** — the angle(s) it covered.
   - **body** — bullet points, **≤300 words**, concrete facts only (numbers/versions/names).
   - **sources** — **≤5**, most authoritative first, URL + access date each.
   - **self-assessment** — 1–5 on each rubric dimension below.

   Paste the research discipline + rubric into each agent's prompt:

   *Research discipline (keeps it cheap — paste into each prompt):*
   - **Search first, fetch ≤3 TOTAL.** Prefer WebSearch snippets; fetch only pages that
     will settle a fact, and extract just the answer — don't read whole pages.
   - **Cover all your sub-angles in one pass.** Split the question, search each part,
     stop when you can answer it. A targeted 2–3-source answer beats a 10-source essay.
   - **Facts only, terse.** No intro, no "here's what I found", no closing summary, no
     hedging. Bullets are verb + fact ("v2.3 replaces X with Y"). Every claim traces to
     a cited source; unsupported claims are omitted.

   *Rubric (self-scoring):*
   | dimension | question | 1 | 5 |
   |---|---|---|---|
   | source_quality | Is the origin authoritative/primary? | blog spam, no cite | official docs, peer-reviewed, primary source |
   | claim_specificity | Concrete vs hand-wavy? | "often helps", vague | exact numbers, names, versions, mechanisms |
   | gap_coverage | Does it answer the assigned angle? | off-topic or shallow | fully addresses the sub-question |

   Each agent scores itself 1–5 per dimension. The binding score is the **floor**
   (minimum of the three). A 5/5/2 finding has a floor of 2 — it's a refine, not an accept.
4. **Review the self-assessments yourself.** You are the orchestrator — read each finding
   and verify the self-scores against the rubric. Adjust any score that looks inflated
   or missed. This takes seconds; do not spawn scoring subagents for this.
5. **Gap-driven follow-up (targeted, not a fan-out).** If, after review, ONE finding is
   floor-low or a specific angle is thin, dispatch ONE targeted subagent to fix exactly
   that gap — then re-review it. Do NOT re-dispatch everything, do NOT loop more than once.
   This is what makes research cheap AND better: one gap-close beats re-running N angles.
6. **Accept.** Every finding whose floor ≥ the depth's threshold (3 for `skim`, 4 for
   `read`/`study`) is accepted; below-threshold findings are admitted as-is tagged
   `confidence: low` — honest, not a failure.
7. Synthesize accepted findings into one entry — one tight paragraph tying the angles
   together, no re-stating of the bullets. **Do not persist yet** — nothing is written
   to disk until the user asks.

8. Show the entry to the user as-is — the bullets ARE the deliverable; don't re-narrate
   or add commentary. Then ask explicitly: **"Save to library?"** Persist only what the
   user explicitly asks to save:
   - whole entry → `write-entry.py` with all findings;
   - a subset → save just the named finding(s) — `findings.json` holds only those;
   - "no" → return the answer and stop; the library is untouched.

   Flag `confidence: low` items and open questions as part of the review. No
   auto-persist, no save-before-show — the library changes only on an explicit yes.

   ```bash
   python3 ../librarian/scripts/write-entry.py \
       --topic "<topic title>" --category <a-z0-9-> --tags a,b,c \
       --findings findings.json --synthesis "<one paragraph>" \
       --confidence low|medium|high [--open-questions "gap1|gap2"]
   ```

   `findings.json` is a list of `{"angle", "body", "sources": [...]}` for the findings
   you're saving (all, or the named subset). `--synthesis` is yours to write.
   `--confidence` is your read of the batch: `high` = all accepted, `medium` = a
   minority low, `low` = majority low.

## Entry format

Entries live at `~/Documents/librarian/library/<category>/<slug>.md` with YAML
frontmatter (title, category, tags, sources, confidence, researched_at). The schema and
`index.md` layout are in [references/library-format.md](references/library-format.md).
`write-entry.py` is the only thing that writes entries — never hand-write the
frontmatter; the script keeps it consistent so full-body search (`rg`) can rely on it.

## Rules

- **Explicit saves only.** Never write to the library without an explicit "save" from
  the user. Show the entry first, write second. Save only what the user names — the
  whole entry or a subset. If the user says nothing about saving, nothing is saved.
- **Query is the mandatory first step.** Run the rg search before ANY web research. If
  the library answers, stop. Most repeat questions are already answered on disk.
- **Self-score, then review.** Research agents self-assess on the rubric; you verify.
  Never spawn a subagent just to score another subagent's output — that's the whole point
  of this design.
- **One pass + at most ONE targeted gap follow-up.** Do not re-dispatch everything; do
  not loop. If a single angle is thin, one surgical follow-up subagent fixes it, then
  accept. Findings still below threshold are tagged `confidence: low` — honest, not a
  failure.
- **Bound the research.** Cap findings at ~300 words, ≤5 sources, **fetch ≤3 TOTAL**
  (search first). Depth comes from more angles, not longer outputs. A concise answer you
  act on beats a long one you skim.
- **Terse output everywhere.** Facts only, at every layer — subagent findings, your
  synthesis, user-facing summaries. No intro/conclusion padding, no hedging. A claim
  without a source is dropped, not hedged: accuracy beats completeness.
- Saved entries follow the house agent-oriented doc format (`skill-man/references/doc-format.md`):
  bullets only, checklist-complete sections, explicit `N/A:`, freshness date in frontmatter.
- Accept `low-confidence` honestly into the library — it is a durable record of a gap,
  not a failure. Name the gap in `## Open questions`.
- Keep entries sourced. A finding without a source URL scores low on source quality by
  design; the bar downstream will catch it.
- The library is user data outside this repo. Scripts default to
  `~/Documents/librarian`; `--root` and `$LIBRARY_ROOT` override for testing.
