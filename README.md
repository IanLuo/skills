# skills

Personal skills repo — a deploy script that symlinks skills into the global
skills folders of 11 coding agents, plus the skills themselves.

```
skills/                 ← source of truth (one dir per skill)
  init-context/         ← bootstraps AGENTS.md + CURSOR.md for a project
  skill-man/            ← meta-skill: authoring, validation, deploy, upstream-sync
  skill-template/       ← starter skeleton
  task-core/            ← shared task-state spine (create-goal, update-progress, update-doc, check)
  dev-task/             ← dev tasks with TDD/BDD + coding conduct
  design-task/          ← design tasks with elicitation + tokens + fidelity evidence
bin/
  deploy.sh             ← symlinks every skill into each agent's global dir
tests/
  fixtures/             ← validation test fixtures (10 cases)
  run.sh                ← test suite + upstream-conformance cross-check
AGENTS.md               ← this repo's own stable agent context
CURSOR.md               ← this repo's own volatile resume cursor (dogfood)
```

## Skills

| Skill | What it does |
|---|---|
| **[skill-man](skills/skill-man/SKILL.md)** | Create, validate, and deploy skills. Carries the spec, best-practices reference, popular-skills catalog, and upstream-sync check. The meta-skill that manages this repo. |
| **[init-context](skills/init-context/SKILL.md)** | One-shot bootstrap of agent working context. Writes `AGENTS.md` (a compact index: Intent, run/build/test commands, hot invariants, architecture elevator, and a disk-sourced deeper-docs pointer table) and `CURSOR.md` (volatile resume cursor) with a state-tracking protocol so every future session can resume. Rerunnable — re-derives the docs index from disk on each run. |
| **[task-core](skills/task-core/SKILL.md)** | Non-triggerable shared spine for task-family skills. Owns four scripts — `create-goal`, `update-progress`, `update-doc`, and `check` (a fail-loud drift gate that validates CURSOR pointers and synced SHA against git). Domain skills call these by sibling-relative path. |
| **[dev-task](skills/dev-task/SKILL.md)** | Start and run software development tasks with task-core tracking plus TDD/BDD, coding conduct, implementation, verification, and progress updates. Triggers on "implement", "fix bug", "add feature", "refactor", "write tests". |
| **[design-task](skills/design-task/SKILL.md)** | Start and run product/interface design tasks with task-core tracking plus elicitation, references, style direction, design guide/tokens, component inventory, concept acceptance, and fidelity evidence. Triggers on "design this", "make a UI", "visual direction", "mockup", "redesign". |
| **[skill-template](skills/skill-template/SKILL.md)** | Minimal valid skill skeleton — use as a starting point for new skills. |

## Layout

Each skill is a directory under `skills/` containing at minimum a `SKILL.md`
with YAML frontmatter:

```yaml
---
name: my-skill
description: What it does and WHEN to use it. This is what agents match on.
metadata:
  audience: personal
  domain: general
---
```

Optional subdirs: `scripts/`, `references/`, `assets/`. No README, CHANGELOG, or
install docs — skills are for agents, not humans.

## Deploy

Symlink every skill into every detected agent's global skills folder:

```bash
./bin/deploy.sh
```

Symlinking means edits in this repo are **instantly live** — no re-deploy needed.
If you move the repo, re-run `./bin/deploy.sh` (or `--doctor` to check symlinks).

### Options

```bash
./bin/deploy.sh --list              # show skills + agents, deploy nothing
./bin/deploy.sh --skill my-skill     # deploy only named skill(s)
./bin/deploy.sh --agent claude       # deploy only to named agent(s)
./bin/deploy.sh --doctor            # health-check deployed symlinks
./bin/deploy.sh --prune             # remove symlinks to skills deleted from the repo
./bin/deploy.sh --dry-run            # show what would happen, change nothing
./bin/deploy.sh --no-skip-system     # also overwrite system-managed skills
```

Multiple `--skill` / `--agent` flags are allowed.

### Keeping deployed skills in sync

Because deploy uses **symlinks** (not copies), changes propagate automatically:

- **Edit a skill** → live immediately in every agent. No re-deploy; just edit and commit.
- **Add a skill** → run `./bin/deploy.sh --skill <name>` once to create the symlink, then it's live forever.
- **Delete a skill** → `rm -rf skills/<name>`, then `./bin/deploy.sh --prune` to remove the now-dangling symlinks.
- **Move the repo** → re-run `./bin/deploy.sh` to repoint all symlinks (`--doctor` detects broken ones).

`--prune` only removes symlinks that point *into this repo* — it never touches real
files (nix-managed skills) or third-party symlinks.

### Supported agents

| Agent    | Global skills dir            |
|----------|------------------------------|
| claude   | `~/.claude/skills`           |
| opencode | `~/.config/opencode/skills`  |
| codex    | `~/.codex/skills`            |
| agents   | `~/.agents/skills`           |
| cursor   | `~/.cursor/skills`           |
| gemini   | `~/.gemini/skills`           |
| hermes   | `~/.hermes/skills`           |
| windsurf | `~/.codeium/skills`          |
| zed      | `~/.config/zed/skills`       |
| aider    | `~/.aider/skills`            |
| cline    | `~/.cline/skills`            |

On this machine `~/.claude/skills`, `~/.config/opencode/skills`, and
`~/.agents/skills` are all symlinks to the **same** shared folder
(`~/.agents/skills`), so deploying to any one of them covers all three.

Agents whose top-level config dir isn't present are skipped automatically —
use `--agent <name>` to force-deploy to one that isn't detected.

### Safety

- **Never overwrites real files/dirs.** Existing entries that aren't symlinks
  (e.g. nix-managed skills like `grill`, `handoff`, `nix-config`) are skipped.
  Pass `--no-skip-system` to overwrite them.
- **Existing symlinks are refreshed** (repointed to this repo).
- **Nothing is deleted** that wasn't created by this script.

## Workflow

```bash
# 1. create a skill (scaffolds valid frontmatter + optional resource dirs)
bash skills/skill-man/scripts/new-skill.sh my-skill --resources scripts,references
$EDITOR skills/my-skill/SKILL.md          # fill in the description (the trigger) + body

# 2. validate it against the spec
python3 skills/skill-man/scripts/validate.py skills/my-skill

# 3. deploy it
./bin/deploy.sh --skill my-skill

# 4. iterate — edits in the repo are live immediately (symlinked)

# 5. commit
git add skills/my-skill && git commit -m "feat: my-skill"
```

Restart the target agent after the first deploy so it picks up the new skill.

## Repo context

This repo dogfoods its own `init-context` skill. [`AGENTS.md`](AGENTS.md) holds
the stable context (Intent, run/build/test, hot invariants, architecture elevator,
deeper-docs pointer table, and state-tracking protocol). [`CURSOR.md`](CURSOR.md)
holds the volatile resume cursor (current position, next action, blockers, open
issues, health, verification) — read it before every session. The protocol is:
git IS history; the cursor carries only forward-looking state git can't express.
