# State-tracking protocol

The full rulebook referenced by every project's AGENTS.md state-tracking section.
Read when you need the complete discipline — the AGENTS.md section is a summary;
this is the canonical text.

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

## Session ritual

```
START  → read CURSOR.md → reconcile synced:<sha> vs HEAD → work
END    → re-read diff → update affected fields → set synced:<new HEAD>
         → trim to ≤40 lines → verify every path → overwrite CURSOR.md
```
