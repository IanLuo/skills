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

1. Check the library exists: `ls ~/Documents/librarian/library/index.md` (or
   `$LIBRARY_ROOT/library` when set). If missing, run
   `bash ../librarian/scripts/init-library.sh` once — it is idempotent, so Research
   never needs to re-check.
2. Search it with ripgrep. Library root: `~/Documents/librarian/library`
   (`$LIBRARY_ROOT/library` overrides). Build ONE alternation from the question's
   distinctive terms, in the forms an entry would use — full name + abbreviation
   (`kubernetes|k8s`), synonyms (`terminal|emulator`). Short generic terms are a trap:
   `ai` substring-matches "said", `go` matches "good" — prefer longer terms or add `-w`.
   Two passes, `index.md` excluded:

   ```bash
   # 1) Title pass — entry paths carry the slugged title; a hit is on-topic by title
   rg --files "$LIBRARY_ROOT/library" --glob '!index.md' | rg -i "term1|term2"

   # 2) Body pass — recall + rank (match count, descending)
   rg -i -c "term1|term2" "$LIBRARY_ROOT/library" --glob '!index.md' \
     | sort -t: -k2 -rn
   ```

   `rg` scans full bodies, so a fact buried in a finding still hits. Title-pass hits
   outrank body-only hits regardless of count — a term in the slug means the whole
   entry is about that topic. Both stay fast at thousands of entries.
3. If matches return: read at most the top 3 — title-pass hits first, then body rank —
   and stop at the first entry that answers; don't read every match. Summarize tersely:
   bullets only, no framing prose, **with source links and confidence tags** — then stop.
   Time-sensitive question (versions, releases, prices, tooling)? Check `researched_at`
   in the frontmatter — top hits older than ~6 months are stale; say so and fall through
   to Research rather than answer from dated notes.
4. If no matches (or the user explicitly wants fresh info): fall through to Research.

   **Query is read-only — it never saves.** If the user asks to file something they
   found during Query, treat it as a Research-style entry and confirm before writing.

## Research workflow

Run ONLY after Query found nothing usable (Query already initialized the library; if
you skipped it, run `init-library.sh` first).

1. Decompose the question into N angles (N from the depth table). Make them **disjoint
   and jointly covering** — each phrased as a concrete sub-question one subagent can
   fully answer (a tool at `read`: what it does / how it works / adoption & ecosystem /
   pitfalls & limits). For `skim` (1 subagent), the ONE subagent covers the WHOLE
   question — it decomposes into sub-angles itself; you don't split it.
2. Fan out N `general-purpose` subagents via the Agent tool. Each researches with
   WebSearch/WebFetch and returns **structured output**:
   - **angle** — the angle(s) it covered.
   - **body** — bullet points, **≤300 words**, concrete facts only (numbers/versions/names).
   - **sources** — **≤5**, most authoritative first, URL + access date each.
   - **self-assessment** — one line: `source_quality=N claim_specificity=N gap_coverage=N`, 1–5 each.

   Paste this brief verbatim into each agent's prompt. Budgets are PER AGENT — cost
   scales linearly with N, so the brief is the cost control. When N > 1, append
   "sibling angles are <list> — don't cover them." Full rubric wording for your own
   review: `references/rubric.md`.

   ```text
   Research your assigned angle(s) with WebSearch/WebFetch. Return:
   - body — ≤300 words, verb + fact bullets ("v2.3 replaces X with Y"); every claim
     traces to a cited source; unsupported claims dropped. No intro, hedging, summary.
   - sources — ≤5, most authoritative first, URL + accessed date each.
   - self — source_quality=N claim_specificity=N gap_coverage=N, 1–5 each:
     source_quality = origin authority (1 blog spam/uncited … 5 official/primary docs);
     claim_specificity = concreteness (1 vague/"often helps" … 5 exact numbers, names,
     versions, mechanisms); gap_coverage = answers the assigned angle (1 off-topic/
     shallow … 5 fully). Be honest — over-scoring is the only failure mode.

   Discipline: search first — prefer WebSearch snippets; fetch ≤3 pages, only to
   settle a fact, extracting just the answer. Cover all your sub-angles in one pass;
   stop when you can answer (a tight 2–3-source answer beats a 10-source essay).
   ```
3. **Review the self-assessments yourself.** You are the orchestrator — read each
   finding and verify the self-scores against the rubric. Adjust any score that looks
   inflated or missed. This takes seconds; do not spawn scoring subagents for this. The
   binding score per finding is the **floor** (minimum of its three dimensions): a
   5/5/2 is a refine, not an accept.
4. **Gap-driven follow-up (targeted, not a fan-out).** If, after review, ONE finding is
   floor-low or one angle is thin, dispatch ONE targeted subagent to close it: hand it
   the thin angle, the floored dimension(s), and its prior finding — then ask for a
   revised body + fresh sources + re-scored self line under the same per-agent budgets.
   Re-review the revision. Do NOT re-dispatch everything; do NOT loop more than once —
   one gap-close beats re-running N angles.
5. **Accept.** Every finding whose floor ≥ the depth's threshold (3 for `skim`, 4 for
   `read`/`study`) is accepted; below-threshold findings are admitted as-is tagged
   `confidence: low` — honest, not a failure.
6. Synthesize accepted findings into one entry — one tight paragraph tying the angles
   together, no re-stating of the bullets. **Do not persist yet** — nothing is written
   to disk until the user asks.

7. Show the entry to the user as-is — the bullets ARE the deliverable; don't re-narrate
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
- **Bound the research.** Every budget is per agent — ≤300-word findings, ≤5 sources,
  **≤3 page fetches** (search first). Cost scales linearly with N; depth comes from
  more angles, not longer outputs. A concise answer you act on beats a long one you skim.
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
