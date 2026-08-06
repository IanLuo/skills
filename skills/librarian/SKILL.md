---
name: librarian
description: Research any question by fanning out subagents that cross-score each other until the findings clear a quality bar, then curate the verified results into a durable knowledge library at ~/Documents/librarian/library/ and answer later questions by querying that library first. Use for "research X", "look into X", "study X", "what do I know about X", "find out about X", or any request to find, verify, and synthesize information into a queryable library. Control breadth with a depth argument — skim, read (default), or study. Do NOT use for codebase exploration, local-file reading, or software-architecture research (use Explore subagents); for a one-shot research report with no library curation, prefer deep-research.
metadata:
  audience: personal
  domain: research
compatibility: Research workflow requires internet (WebSearch/WebFetch) and the Agent tool. Query workflow runs offline against the local library. Scripts require Python 3 and bash.
---

# librarian

A personal research librarian. Two workflows:

- **Query** — answer from the library if it already has usable notes.
- **Research** — fan out + cross-score subagents until findings clear a bar, then curate.

Always run Query first; only Research when the library is thin.

## Depth

The user controls breadth per call. Default `read`.

| depth | use when | subagents |
|---|---|---|
| `skim`  | "what's the gist of X?" | 2–3, single pass, accept at 3/5 |
| `read`  | "I need a solid understanding of X." | 3–5, 2 rounds, accept at 4/5 |
| `study` | "I need to know everything important about X." | 5–8, 3 rounds, accept at 4/5 |

## Query workflow

1. Ensure the library exists, then search it (does not load every entry into context):

   ```bash
   bash   ../librarian/scripts/init-library.sh
   python3 ../librarian/scripts/query.py "<the user's question>"
   # -> absolute path<TAB>title<TAB>score<TAB>confidence  (best first)
   ```

2. If matches return: read the top entries, summarize them **with their source
   links and confidence tags** for the user, and stop.
3. If no matches (or the user explicitly wants fresh info): fall through to Research.

## Research workflow

1. If the library is missing, run `init-library.sh`.
2. Decompose the question into N research angles (N from the depth table). The angles
   should cover the question — a 4-angle `read` might be: definition, mechanisms,
   evidence/examples, open problems.
3. Fan out N `general-purpose` subagents via the Agent tool — one per angle. Each
   researches with WebSearch/WebFetch and returns **structured output**: an angle name,
   bullet-point body with concrete facts (numbers/versions/names), and sources with
   URLs + access dates.
4. Cross-score: fan out scoring subagents so **each** finding is scored by ≥2 sibling
   subagents that did **not** author it, on the three dimensions in
   [references/rubric.md](references/rubric.md) (source quality, claim specificity,
   gap coverage; 1–5). The binding number is the **floor** — the min of the three
   dimension means. A 5/5/2 is a refine, not an accept.
5. **Converge as the orchestrator.** You own the loop — no script gates it:
   - accept a finding when its floor ≥ the depth's accept threshold;
   - if a finding is below threshold and rounds remain, re-dispatch *that angle* via
     the Agent tool with the specific feedback ("below 4 on claim_specificity");
   - stop when every finding is accepted **or** you've hit the depth's round cap **or**
     you re-dispatched and nothing improved (plateau — stop even if some stay below).
   Cap the rounds at the depth's max. Any finding still below threshold at the cap is
   accepted and tagged `confidence: low` — honest, not a failure.
6. Synthesize accepted findings into one entry and persist it:

   ```bash
   python3 ../librarian/scripts/write-entry.py \
       --topic "<topic title>" --category <a-z0-9-> --tags a,b,c \
       --findings findings.json --synthesis "<one paragraph>" \
       --confidence low|medium|high [--open-questions "gap1|gap2"]
   ```

   `findings.json` is a list of `{"angle", "body", "sources": [...]}` for the
   accepted findings. `--synthesis` is yours to write. `--confidence` is your read of
   the batch: `high` = all accepted, `medium` = a minority low at cap, `low` = majority low.

7. Return the entry to the user. Flag `confidence: low` items and the named open
   questions. Offer to re-run those angles at `study` depth later.

### Delegation

- **Delegate** all research + scoring to subagents. Each subagent gets a focused prompt
  naming its angle (or its finding-to-score) and the strict output shape it must return.
- **Own** the loop: keep the round-by-round state in your head, decide accept/refine/stop,
  and write the entry at the end via `write-entry.py`. Don't run extra rounds past the cap
  or past a plateau "to be sure" — the whole point of the cap is termination.

## Entry format

Entries live at `~/Documents/librarian/library/<category>/<slug>.md` with YAML
frontmatter (title, category, tags, sources, confidence, researched_at). The schema and
`index.md` layout are in [references/library-format.md](references/library-format.md).
`write-entry.py` is the only thing that writes entries — never hand-write the
frontmatter; the script keeps it consistent so `query.py` can rely on it.

## Rules

- Run Query before Research. Most repeat questions are already answered on disk.
- Never override a plateau or the round cap with "just one more round." The cap
  guarantees termination; hand-tuning reintroduces infinite loops.
- Accept `low-confidence` honestly into the library — it is a durable record of a gap,
  not a failure. Name the gap in `## Open questions`.
- Keep entries sourced. A finding without a source URL scores low on source quality by
  design; the bar downstream will catch it.
- The library is user data outside this repo. Scripts default to
  `~/Documents/librarian`; `--root` and `$LIBRARY_ROOT` override for testing.