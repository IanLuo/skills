# skills

Personal skills repo + a deploy script that symlinks skills into the global
skills folders of supported coding agents.

```
skills/                 ← source of truth (one dir per skill)
  <skill-name>/
    SKILL.md            ← required frontmatter (name, description, ...)
bin/
  deploy.sh             ← symlinks every skill into each agent's global dir
```

## Layout

Each skill is a directory under [`skills/`](skills/) containing at minimum a
`SKILL.md` with YAML frontmatter:

```yaml
---
name: my-skill
description: What it does and WHEN to use it. This is what agents match on.
compatibility: Works across Claude, OpenCode, Codex, and other loaders.
metadata:
  audience: personal
  domain: general
---
```

See [`skills/skill-template/`](skills/skill-template/SKILL.md) for a skeleton.

## Deploy

Symlink every skill into every detected agent's global skills folder:

```bash
./bin/deploy.sh
```

Symlinking means edits you make in this repo are **instantly live** — no
re-deploy needed to pick up changes. The repo just has to stay at this path (if you
move it, re-run `./bin/deploy.sh` to repoint symlinks, or `--doctor` to check).

### Options

```bash
./bin/deploy.sh --list              # show skills + agents, deploy nothing
./bin/deploy.sh --skill my-skill     # deploy only named skill(s)
./bin/deploy.sh --agent claude       # deploy only to named agent(s)
./bin/deploy.sh --doctor            # health-check deployed symlinks
./bin/deploy.sh --dry-run            # show what would happen, change nothing
./bin/deploy.sh --no-skip-system     # also overwrite system-managed skills
```

Multiple `--skill` / `--agent` flags are allowed.

### Supported agents

| Agent    | Global skills dir            |
|----------|------------------------------|
| claude   | `~/.claude/skills`           |
| opencode | `~/.config/opencode/skills`  |
| codex    | `~/.codex/skills`            |
| agents   | `~/.agents/skills`           |
| cursor   | `~/.cursor/skills`           |
| gemini   | `~/.gemini/skills`           |
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

## skill-man — the authoring skill

[`skills/skill-man`](skills/skill-man/SKILL.md) is a meta-skill that teaches how to
create, validate, and deploy skills, with reference docs for the
[spec](skills/skill-man/references/skill-spec.md),
[best practices](skills/skill-man/references/best-practices.md), and a
[popular-skills catalog](skills/skill-man/references/popular-skills.md). It also
carries the scaffolding scripts (`new-skill.sh`, `validate.py`) and a
`sync-check.sh` that reports whether the repo's spec is behind upstream
`anthropics/skills`. Tests live in [`tests/`](tests) — run `bash tests/run.sh`.
