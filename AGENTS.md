# AGENTS.md — skills repo

## Intent

Personal-skills manager — author, validate, and deploy AI coding-agent skills across
12 agent harnesses from a single repo. Deployability and spec-conformance are
non-negotiable.

## How to run / build / test

```bash
# Validate all skills against the spec
python3 skills/skill-man/scripts/validate.py

# Run test suite (10 fixtures + upstream-conformance cross-check)
bash tests/run.sh

# Deploy
./bin/deploy-skills.sh
./bin/deploy-skills.sh --skill <name>   # one skill
./bin/deploy-skills.sh --dry-run        # preview
./bin/deploy-skills.sh --doctor         # health-check symlinks

# Global agent instructions (global/AGENTS.md → every agent's global instruction file)
./bin/deploy-instructions.sh           # link; --list / --doctor / --dry-run / --force

# Check upstream sync (are we behind anthropics/skills?)
bash skills/skill-man/scripts/sync-check.sh

# Create a new skill
bash skills/skill-man/scripts/new-skill.sh <name> [--resources scripts,references,assets]
```

## Hot invariants

- Never overwrite nix-managed skills (real files in `/nix/store/*`) — deploy-skills.sh skips them unless `--no-skip-system`.
- Skill name = `^[a-z0-9-]+$`, no leading/trailing/double `-`, ≤64 chars; equals folder name.
- `description` is the primary trigger — enumerate literal triggers + negative trigger there, not in the body.
- One skill per folder: required `SKILL.md`; optional `scripts/`, `references/`, `assets/`. No README/CHANGELOG.
- Validate before deploying (`validate.py` is the source of truth for the spec, pinned to `anthropics/skills` `5754626`).
- Forward-test new skills with a fresh subagent (baseline-then-write: watch it fail without the skill first).

## Architecture elevator

```
skills/          — 14 skills (see README table)
  grill/           cross-cutting critical thinking
  init-context/    bootstrap AGENTS.md + SESSION.md
  specs/           elicit + lock formal spec/system-design/architecture docs
  design-task/     lock design-system.md
  dev-task/        implement with TDD/BDD
  review-task/     verify against locked docs; route findings back
  handoff/         verified session summary + chain across sessions
  herdr/           control herdr terminal workspace (external agents in panes)
  delegate/        in-process subagents — delegability gate + spawn→wait→read
  task-agent/       worktree-delegated tasks — start (dispatch) / end (merge back)
  artifact/         interactive HTML view of context + copy-paste annotation feedback
  librarian/       personal research library
  skill-man/       create, validate, deploy skills
  skill-template/  starter skeleton
bin/             — deploy-skills.sh (symlinks skills into each detected agent's global skills dir)
tests/           — validation fixture tests + upstream-conformance cross-check
```

The core pipeline is: init-context → specs → design-task → dev-task → review-task.
Lock markers (`<!-- specs:locked:... -->`, `<!-- design:locked:... -->`) freeze decisions;
consumer skills discover upstream docs by grepping for them. SESSION.md is an
append-only one-line session log at the repo root. Deploy uses symlinks — edits are
live immediately.

## Deeper docs

| When you need… | Read… |
|---|---|
| skill authoring rules, spec cheatsheet, best practices | `skills/skill-man/SKILL.md` |
| deploy topology and constraints | `bin/deploy-skills.sh` |
| specs elicitation ladders (spec, system-design, architecture) | `skills/specs/references/` |
| librarian entry format and rubric | `skills/librarian/references/` |
