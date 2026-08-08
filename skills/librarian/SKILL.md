---
name: librarian
description: Research any question by fanning out self-scoring subagents, then curate verified results into a durable knowledge library at ~/Documents/librarian/library/ and answer later questions by querying that library first. Use for "research X", "look into X", "study X", "what do I know about X", "find out about X", or any request to find, verify, and synthesize information into a queryable library. Control breadth with a depth argument — skim, read (default), or study. Do NOT use for codebase exploration, local-file reading, or software-architecture research (use Explore subagents); for a one-shot research report with no library curation, prefer deep-research.
metadata:
  audience: personal
  domain: research
compatibility: Research workflow requires internet (WebSearch/WebFetch) and the Agent tool. Query workflow runs offline against the local library. Scripts require Python 3 and bash.
---

# librarian

A personal research librarian. Two workflows:

- **Query** — answer from the library if it already has usable notes.
- **Research** — fan out self-scoring subagents, one pass, curate.

Always run Query first; only Research when the library is thin.

## Depth

The user controls breadth per call. Default `read`.

| depth | use when | subagents |
|---|---|---|
| `skim`  | "what's the gist of X?" | 2–3 |
| `read`  | "I need a solid understanding of X." | 3–5 |
| `study` | "I need to know everything important about X." | 5–8 |

## Query workflow

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

3. If matches return: read the top entries, summarize them **with their source
   links and confidence tags** for the user, and stop.
4. If no matches (or the user explicitly wants fresh info): fall through to Research.

   **Query is read-only — it never saves.** If the user asks to file something they
   found during Query, treat it as a Research-style entry and confirm before writing.

## Research workflow

1. If the library is missing, run `init-library.sh`.
2. Decompose the question into N research angles (N from the depth table). The angles
   should cover the question — a 4-angle `read` might be: definition, mechanisms,
   evidence/examples, open problems.
3. Fan out N `general-purpose` subagents via the Agent tool — one per angle. Each
   researches with WebSearch/WebFetch and returns **structured output**: an angle name,
   bullet-point body with concrete facts (numbers/versions/names), sources with
   URLs + access dates, AND a **self-assessment** on the three rubric dimensions:

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
5. **One pass, no refinement loop.** Accept every finding whose floor ≥ the depth's accept
   threshold (3 for `skim`, 4 for `read`/`study`). Findings below threshold at any depth
   are admitted as-is and tagged `confidence: low` — honest, not a failure. Do not
   re-dispatch; the user can re-run at `study` depth later if they want deeper coverage.
6. Synthesize accepted findings into one entry. **Do not persist yet** — nothing is
   written to disk until the user asks.

7. Show the entry to the user and ask explicitly: **"Save to library?"** Persist only
   what the user explicitly asks to save:
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
- Run Query before Research. Most repeat questions are already answered on disk.
- **Self-score, then review.** Research agents self-assess on the rubric; you verify.
  Never spawn a subagent just to score another subagent's output — that's the whole point
  of this design.
- **One pass.** Do not re-dispatch findings that fall below threshold. Tag them
  `confidence: low` and move on. The refinement loop was removed — it doubled agent count
  with diminishing returns.
- Accept `low-confidence` honestly into the library — it is a durable record of a gap,
  not a failure. Name the gap in `## Open questions`.
- Keep entries sourced. A finding without a source URL scores low on source quality by
  design; the bar downstream will catch it.
- The library is user data outside this repo. Scripts default to
  `~/Documents/librarian`; `--root` and `$LIBRARY_ROOT` override for testing.
