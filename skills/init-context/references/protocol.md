# State-tracking protocol

The full rulebook referenced by every project's AGENTS.md state-tracking section.
Read when you need the complete discipline — the AGENTS.md section is a summary;
this is the canonical text.

## AGENTS.md index discipline

AGENTS.md is an **index, not a manifest.** It embeds only what a fresh agent can't
recover from code and would get wrong without:

- Intent (≤3 lines),
- verified commands,
- a handful of frozen invariants,
- a short architecture elevator.

Everything else (PRD, full architecture rationale, design guidelines, pattern
catalog, deploy detail) lives in **pointed-to docs** indexed in the Deeper-docs
jump-table. Each row encodes both *what to read* and *when to read it* ("When you
need X, read Y").

Rules for the index:

- **Derive from disk, not memory.** Scan the project's docs tree this run; add a
  row iff the doc file exists on disk. No file → no row. Never create stub files.
- **Re-run reconciles.** Each init-context run diff-merges: add rows for new
  docs, drop rows for docs that disappeared, refresh the "when you need…" cues.
  The Deeper-docs section is always current to the run that wrote it.
- **Never inline a doc.** The Deeper-docs table is a launchpad, not a container.
  Each row gives the agent a path it can open when the task demands it; the
  always-in-context window stays small.
- **Use the inclusion gate on every candidate.** Something belongs in the
  embedded tier (Intent, Hot invariants, verified commands, 5-line architecture)
  only if: (a) not recoverable by one command, (b) a fresh agent would get WRONG
  without it, and (c) it's stable. Otherwise point to it or drop it.

## Purpose

An AI coding agent, between sessions, has no memory. The cursor fills that gap:
a small, always-regenerated record of *forward-looking state* that git can't
express. Git IS the work-history record — the cursor never duplicates it.

## Fields

| Field | Content |
|---|---|
| synced | `<!-- synced: <git sha> -->` — staleness oracle |
| Position | current step + next action (one merged field) |
| Blockers | what's stuck + why |
| Open issues | unresolved questions / assumptions / pending decisions |
| Health | 🟢/🔴 + known-broken items |
| Verification | claimed-done vs verified-done + evidence (test/command @ sha) |
| Errors-that-changed-plan | only failures that redirected work — not transient retries |
| Decisions | one present-tense line per resolved invariant |
| Active pointers | file paths — verified to exist at write time |

## Inclusion gate

Record X iff:
(a) X is NOT recoverable by running one command against an artifact (git/code/CI), AND
(b) a fresh agent would plausibly get WRONG without it.

If X fails either gate, drop it. If it passes both, record the shortest form possible.

## Discipline

1. **Read at session start.** Before taking any task, read `CURSOR.md` in full.
   Compare `synced: <sha>` to `git rev-parse HEAD`. If different, reconcile before
   starting new work.

2. **Rewrite in-place at session end.** Never append. A cursor that only grows is a
   bug. Rederive the entire cursor against the inclusion gate; trim anything stale.

3. **Hard cap: ≤40 lines / ≤2000 characters.** Hitting the cap is a signal to
   *delete*, not grow. Context rot is a gradient — smaller is always better.

4. **Pointers over contents.** Where state lives in an artifact, store the
   *command* or *path*, never the output. "Failing test: `npm test -- auth.logout`"
   not "the auth logout test fails with an assertion error about..."

5. **Every path verified to exist at write time.** A path that doesn't exist wastes
   the next agent's turn. Check every referenced file before writing.

6. **Decisions are one present-tense line.** "Use SQLite, not Postgres — embedded
   deploys." Not a timeline of how you arrived there.

7. **Drop transient noise.** Greetings, tangents, resolved clarifications, retried
   failures that didn't change the plan — none of it belongs in the cursor.

8. **Run the drift gate.** At session start and before writing, run
   `../task-core/scripts/check` to validate every pointer and the synced SHA
   against git. A failed check stops you from acting on stale state.

## Session ritual

```
START  → read CURSOR.md → reconcile synced:<sha> vs HEAD
       → ../task-core/scripts/check          (drift gate: stale pointers? behind HEAD?)
       → work
END    → re-read diff → update affected fields → set synced:<new HEAD>
       → trim to ≤40 lines → verify every path
       → ../task-core/scripts/check          (re-verify before committing the cursor)
       → overwrite CURSOR.md
```
