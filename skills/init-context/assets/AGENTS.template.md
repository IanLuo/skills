# AGENTS.md — <project-name>

## Intent

<≤3 lines: the product goal the agent must not satisfice past. What problem it solves, for whom, the one non-negotiable outcome. This is the irreversible-blindspot content — the code doesn't encode it; a fresh agent without it picks the wrong metric. One sentence is fine.>

## How to run / build / test

```bash
# Build
<build command>

# Run
<run command>

# Test
<test command>

# Lint / type-check
<lint command>
```

Each command verified before writing — if impractical to run, note why explicitly.

## Hot invariants

- <never X, because Y>
- <always Z, because W>
- <do not touch A unless B>

Only the frozen "because" rules a fresh agent would silently break — a handful, not a catalog. Longer conventions live in `CONVENTIONS.md` (see Deeper docs).

## Architecture elevator

<5 lines: the load-bearing structure — layers, ownership, boundaries. A one-level repo tree is enough. Deep rationale (why these choices) lives in ARCHITECTURE.md or docs/adr/.>

```
src/          — <one-line purpose>
tests/        — <one-line purpose>
docs/         — <one-line purpose>
```

## Deeper docs

<when X, read Y table — one row per doc that exists on disk this run. Omit rows for docs that don't exist yet. Re-derive this section on each init-context run so it stays current.>

| When you need… | Read… |
|---|---|
| product intent, scope, acceptance criteria | `docs/prd.md` |
| system shape, component boundaries, ownership | `ARCHITECTURE.md` |
| why a choice was made | `docs/adr/NNNN-title.md` |
| UI / visual guidelines | `design-system.md` |
| coding patterns not inlined above | `CONVENTIONS.md` |
| how to release / deploy | `docs/release.md` |
| security constraints and threat model | `docs/security.md` |

---

## State-tracking protocol

Current state is tracked in **`CURSOR.md`** at the repo root. Read it before every
session. It carries forward-looking state git can't express. git history IS the
work-history record. Rewrite `CURSOR.md` in-place at the end of every session.
Never append — append is rot. Hard cap: ≤40 lines / ≤2000 characters.
Every file path in it must exist at write time.

### Inclusion gate — record X iff:
(a) X is NOT recoverable by running one command against an artifact (git/code/CI), AND
(b) a fresh agent would plausibly get WRONG without it.

### Cursor fields

| Field | Content |
|---|---|
| synced | `<!-- synced: <git sha> -->` — staleness oracle (compare to `git rev-parse HEAD`) |
| Position | current step + next action, merged into one field |
| Blockers | what's stuck + why — to avoid re-hitting the wall |
| Open issues | unresolved questions / assumptions / pending decisions |
| Health | 🟢 green or 🔴 broken + known-broken items |
| Verification | claimed-done vs verified-done, with evidence (test/command @ sha) |
| Errors-that-changed-plan | only failures that redirected the work, not transient retries |
| Decisions | one present-tense line per resolved invariant, not a deliberation timeline |
| Active pointers | file paths → verified to exist at write time |

### Rules
- **Rewrite in-place, never append.** A cursor that only grows is a bug.
- **Pointers over contents.** Where state lives in an artifact, store the *command* or *path*, not the output. Can't drift; costs less.
- **Every path must exist at write time.**
- **Decisions collapse to one present-tense line.** Not a timeline.
