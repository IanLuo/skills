# AGENTS.md — <project-name>

## What this project is

<one sentence: what the project IS — its purpose, what it produces, who it's for>

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

## Conventions

- <code style, naming conventions, architectural rules>
- <workflow: how PRs/reviews/releases work>
- <testing discipline>

## Deploy / infra

- <how it deploys, where it runs, what envs exist>
- <secrets management, config locations>
- <monitoring / logs>

## Gotchas / don't touch

- <things that break in non-obvious ways>
- <files/modules that should never be hand-edited>
- <constraints that aren't obvious from code>

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
