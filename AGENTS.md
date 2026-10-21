# AGENTS.md — skills repo

## Intent

- Personal-skills manager: author, validate, and deploy AI coding-agent skills across 12 agent harnesses from one repo.
- Deployability and spec-conformance are non-negotiable.
- `fs` — built from `src/flagship` — is the coordination layer every other skill runs its work through.

## How to run / build / test

```bash
# Validate all skills against the spec
python3 skills/skill-man/scripts/validate.py

# Audit skill quality — reference integrity, orphans, body size (mechanized half of
# evaluation.md: `audit.py [skill] [--skills-dir PATH]`)
python3 skills/skill-man/scripts/audit.py

# Run test suite (10 fixtures + upstream-conformance + audit)
bash tests/run.sh

# fs: the Go suite, the vet, and the format — all three, no exceptions
nix develop --command sh -c 'cd src/flagship && go test ./... -count=1 && go vet ./... && gofmt -l .'

# Build fs into skills/flagship/scripts/fs (derived and gitignored — never edit it)
./bin/build-project.sh flagship

# Deploy
./bin/deploy-skills.sh
./bin/deploy-skills.sh --skill <name>   # one skill
./bin/deploy-skills.sh --dry-run        # preview
./bin/deploy-skills.sh --doctor         # health-check symlinks, artifacts, skill sources

# Global agent instructions (global/AGENTS.md → every agent's global instruction file)
./bin/deploy-instructions.sh           # link; --list / --doctor / --dry-run / --force

# Check upstream sync (are we behind anthropics/skills?)
bash skills/skill-man/scripts/sync-check.sh

# Create a new skill
bash skills/skill-man/scripts/new-skill.sh <name> [--resources scripts,references,assets]

# Build projects under src/ into their skill's scripts/ folder (derived, gitignored)
./bin/build-project.sh                 # every project; or name one
./bin/build-project.sh credentials     # cred-run → skills/credentials/scripts/

# Nix outputs: one pair per project, no `default` — name the project you want
nix build .#credentials                # hermetic; cred-run has no dependencies
nix develop                            # both toolchains; .#credentials / .#flagship for one
```

The `skills` repo has no separate integration suite: `tests/run.sh` is it, and `fs` is verified by its own Go suite above.

## Hot invariants

- Never overwrite nix-managed skills (real files in `/nix/store/*`) — deploy-skills.sh skips them unless `--no-skip-system`.
- Skill name = `^[a-z0-9-]+$`, no leading/trailing/double `-`, ≤64 chars; equals folder name.
- `description` is the primary trigger — enumerate literal triggers + negative trigger there, not in the body.
- One skill per folder: required `SKILL.md`; optional `scripts/`, `references/`, `assets/`. No README/CHANGELOG.
- Validate before deploying (`validate.py` is the source of truth for the spec, pinned to `anthropics/skills` `5754626`). Audit quality with `audit.py`.
- References resolve **against the skill's own dir** (the harness rule), never the skills root: use `references/x.md` for your own files, `../other-skill/references/x.md` for a sibling. The bare `other-skill/references/x.md` form is ambiguous and `audit.py` fails it.
- Forward-test new skills with a fresh subagent (baseline-then-write: watch it fail without the skill first).
- Each project under `src/` is a build unit with one output pair in the root flake (`packages.<project>` + `devShells.<project>`) — the flake owns every recipe. `./bin/build-project.sh` installs a project's binary into `skills/<project>/scripts/`. Those binaries are derived and gitignored — never edit them, and never restate a recipe outside `flake.nix`.
- `src/flagship` is the only source of `fs`. The event log at `~/.fs/store.db` is append-only; every node's status derives from replay, so no command may write that file directly.
- `fs`'s requirements contract is `.agents/specs/fs-contract.md`. Read it before changing a verb, the log, or a dispatch.
- The dispatch launch config is frozen in the binary: `engine=herdr` · `agent=pi` · `args=--provider magpie --model opencode-go/deepseek-v4.1-flash`. A worker started without it dies on a missing provider route and says nothing.
- A doc naming a command `fs` no longer has is worse than no doc: delete the command and its docs in the same commit.

## Architecture elevator

```
skills/          — the skills (README table is the index)
  grill/           cross-cutting critical thinking
  init-context/    bootstrap AGENTS.md
  specs/           elicit + lock formal spec/system-design/architecture docs
  design-task/     lock design-system.md
  dev-task/        implement with TDD/BDD
  review-task/     verify against locked docs; route findings back
  handoff/         verified session summary + chain across sessions
  herdr/           control herdr terminal workspace (external agents in panes)
  delegate/        in-process subagents — delegability gate + spawn→wait→read
  task-agent/       worktree-delegated tasks — start (dispatch) / end (merge back)
  annotate/         interactive HTML view of context + copy-paste annotation feedback
  canvas/           one live page per topic — daemon hot-swaps changed sections, annotations POST to disk
  librarian/       personal research library
  flagship/        drive fs: projects, tasks, dispatch, close, report
  skill-man/       create, revise, validate, audit, deploy skills
  skill-template/  starter skeleton
src/flagship/    — the fs Go binary (its contract: .agents/specs/fs-contract.md)
bin/             — deploy-skills.sh (symlinks skills into each detected agent's global skills dir)
tests/           — validation fixture tests + upstream-conformance cross-check + audit.py behaviour tests
```

The core pipeline is: init-context → specs → design-task → dev-task → review-task.
Lock markers (`<!-- specs:locked:... -->`, `<!-- design:locked:... -->`) freeze decisions;
consumer skills discover upstream docs by grepping for them. Deploy uses symlinks — edits are
live immediately.

## Deeper docs

| When you need… | Read… |
|---|---|
| fs's requirements contract — verbs, log, dispatch, launch config | `.agents/specs/fs-contract.md` |
| how to use fs — dispatch, close, report, the pending statuses | `skills/flagship/SKILL.md` |
| skill authoring rules, spec cheatsheet, best practices | `skills/skill-man/SKILL.md` |
| deploy topology and constraints | `bin/deploy-skills.sh` |
| specs elicitation ladders (spec, system-design, architecture) | `skills/specs/references/` |
| librarian entry format and rubric | `skills/librarian/references/` |
