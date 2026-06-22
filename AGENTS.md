# AGENTS.md — skills repo

## What this project is

A personal-skills manager. Skills are one-folder-per-skill in `skills/`; `bin/deploy.sh`
symlinks them into supported coding agents' global skills folders (10 agents). Edits are
live immediately (symlinked). Three skills: `skill-man` (the meta-skill — authoring,
validation, deploy, upstream-sync), `skill-template` (a starter skeleton), and
`init-context` (bootstraps AGENTS.md + CURSOR.md for a project).

## How to run / build / test

```bash
# Validate all skills against the spec
python3 skills/skill-man/scripts/validate.py

# Run test suite (10 fixtures + upstream-conformance cross-check)
bash tests/run.sh

# Deploy
./bin/deploy.sh
./bin/deploy.sh --skill my-skill   # one skill
./bin/deploy.sh --dry-run          # preview
./bin/deploy.sh --doctor           # health-check symlinks

# Check upstream sync (are we behind anthropics/skills?)
bash skills/skill-man/scripts/sync-check.sh

# Create a new skill
bash skills/skill-man/scripts/new-skill.sh <name> [--resources scripts,references,assets]
```

## Conventions

- Skill name = lowercase-hyphen-case (`^[a-z0-9-]+$`, no leading/trailing/double `-`, ≤64 chars).
- One skill per folder. Required `SKILL.md`; optional `scripts/`, `references/`, `assets/`.
  No README, CHANGELOG, or install docs — the skill is for an agent.
- `description` is the primary trigger. Enumerate literal triggers + a negative trigger.
- Progressive disclosure: body ≤500 lines; split detail into `references/` linked with
  one-line "read this when…" cues.
- Validate before deploying (`validate.py` is the single source of truth for the spec,
  pinned to `anthropics/skills` at `5754626`).
- Deploy uses symlinks. Never overwrite real files/dirs (nix-managed skills are skipped).
- Forward-test new skills with a fresh subagent (baseline-then-write: watch it fail without
  the skill first).

## Deploy topology (this machine)

`~/.claude/skills`, `~/.config/opencode/skills`, and `~/.agents/skills` are all symlinks
to the shared `~/.agents/skills`. `~/.codex/skills` is separate. Nix-managed skills
(`grill`, `handoff`, `nix-config`) live in `/nix/store/…` — **never overwrite.** Deploy
targets 10 agents; absent ones are skipped automatically.

## Don't touch

- `/nix/store/*` — nix-managed skills; deploy.sh skips them unless `--no-skip-system`.
- `tests/fixtures/` — validation test fixtures; they're the spec's integration tests.

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
