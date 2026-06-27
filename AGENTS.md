# AGENTS.md — skills repo

## Intent

Personal-skills manager — author, validate, and deploy AI coding-agent skills across
10 agent harnesses from a single repo. Deployability and spec-conformance are
non-negotiable.

## How to run / build / test

```bash
# Validate all skills against the spec
python3 skills/skill-man/scripts/validate.py

# Run test suite (10 fixtures + upstream-conformance cross-check)
bash tests/run.sh

# Deploy
./bin/deploy.sh
./bin/deploy.sh --skill <name>   # one skill
./bin/deploy.sh --dry-run        # preview
./bin/deploy.sh --doctor         # health-check symlinks

# Check upstream sync (are we behind anthropics/skills?)
bash skills/skill-man/scripts/sync-check.sh

# Create a new skill
bash skills/skill-man/scripts/new-skill.sh <name> [--resources scripts,references,assets]
```

## Hot invariants

- Never overwrite nix-managed skills (real files in `/nix/store/*`) — deploy.sh skips them unless `--no-skip-system`.
- Skill name = `^[a-z0-9-]+$`, no leading/trailing/double `-`, ≤64 chars; equals folder name.
- `description` is the primary trigger — enumerate literal triggers + negative trigger there, not in the body.
- One skill per folder: required `SKILL.md`; optional `scripts/`, `references/`, `assets/`. No README/CHANGELOG.
- Validate before deploying (`validate.py` is the source of truth for the spec, pinned to `anthropics/skills` `5754626`).
- Forward-test new skills with a fresh subagent (baseline-then-write: watch it fail without the skill first).

## Architecture elevator

```
skills/        — one folder per skill (skill-man, init-context, task-core, dev-task, design-task, skill-template)
bin/           — deploy.sh (symlinks skills into each detected agent's global skills dir)
tests/         — validation fixture tests + upstream-conformance cross-check
```

Skills call sibling skills' scripts by relative path (`../task-core/scripts/update-progress`).
Deploy uses symlinks — edits are live immediately; no re-deploy needed to pick up changes.

## Deeper docs

| When you need… | Read… |
|---|---|
| skill authoring rules, spec cheatsheet, best practices | `skills/skill-man/SKILL.md` |
| state-tracking protocol (cursor fields, inclusion gate, session ritual) | `skills/init-context/references/protocol.md` |
| task-core shared spine + domain overlay protocol | `skills/task-core/references/protocol.md` |
| deploy topology and constraints | `bin/deploy.sh` |

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
