# skills

Personal skills repo — a deploy script that symlinks skills into the global
skills folders of 12 coding agents, plus the skills themselves.

## Design

A **5-skill pipeline** for structured software work, plus cross-cutting and auxiliary skills.

```
SESSION.md ── one-line append-only session log (every skill appends on completion)

grill ── cross-cutting critical thinking (applies to every turn)

init-context → prd → design-task → dev-task → review-task
   │            │          │            │            │
   │            │          │            │            └── verify against locked docs;
   │            │          │            │               route ⚠️/🔴 back to owning skill
   │            │          │            │
   │            │          │            └── implement one deliverable with TDD/BDD
   │            │          │
   │            │          └── lock design-system.md (frozen tokens, components, concepts)
   │            │
   │            └── lock PRD/system-design/architecture docs (elicit + freeze decisions)
   │
   └── bootstrap AGENTS.md + SESSION.md for a project
```

**Lock markers** freeze decisions so fresh agents don't re-litigate:
- `<!-- prd:locked:<sha> <date> -->` on spec docs
- `<!-- design:locked:<sha> <date> -->` on design-system.md

Consumer skills discover upstream docs by grepping lock markers on disk:
`grep -rl '<!-- prd:locked:\|<!-- design:locked:' *.md`

**SESSION.md** is an append-only one-line log at the repo root. Every session reads
the last line to know where things stand, then appends one line on completion:
`<date> · <skill> · <what happened>. Next: <next step>.`

**review-task** closes the loop — ⚠️ gaps and 🔴 regressions route back to the owning
skill (prd, dev-task, or design-task) for follow-up.

## Skills

| Skill | What it does |
|---|---|
| **[grill](skills/grill/SKILL.md)** | Cross-cutting critical thinking — pressure-test ideas, offer alternatives, cite sources. Applies to every turn. |
| **[init-context](skills/init-context/SKILL.md)** | One-shot bootstrap of agent working context. Writes `AGENTS.md` (a compact index: Intent, run/build/test, hot invariants, architecture elevator, deeper docs) and `SESSION.md` (one-line session log). Rerunnable — re-derives from a fresh survey. |
| **[prd](skills/prd/SKILL.md)** | Interactive elicitation — one rung at a time — that locks decisions into durable PRD, system-design, or architecture docs. On re-entry, shows the locked doc and re-elicits only what changed. |
| **[design-task](skills/design-task/SKILL.md)** | Visual/product/interface design with elicitation, tokens, component inventory, concept acceptance, and fidelity evidence. Locks `design-system.md` when verified. |
| **[dev-task](skills/dev-task/SKILL.md)** | Software development with TDD/BDD, coding conduct, implementation, and verification. One dev-task = one deliverable. Checks for locked upstream docs before starting. |
| **[review-task](skills/review-task/SKILL.md)** | Correctness gate — verify that a completed task's changes match its defining docs. Catch regressions, invariant violations, and stale evidence. Routes findings back to the owning skill. |
| **[herdr](skills/herdr/SKILL.md)** | Control the herdr terminal workspace manager — spawn agents in panes, send prompts, wait for results, read output, clean up. Agent-agnostic. |
| **[librarian](skills/librarian/SKILL.md)** | Personal research library — fan out subagents that cross-score each other, then curate verified results into `~/Documents/librarian/library/`. Query-first, research on cache miss. |
| **[skill-man](skills/skill-man/SKILL.md)** | Meta-skill — create, validate, and deploy skills. Carries the spec, best-practices reference, popular-skills catalog, and upstream-sync check. |
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
./bin/deploy.sh --list               # show skills + agents, deploy nothing
./bin/deploy.sh --skill my-skill      # deploy only named skill(s)
./bin/deploy.sh --agent claude        # deploy only to named agent(s)
./bin/deploy.sh --doctor             # health-check deployed symlinks
./bin/deploy.sh --prune              # remove symlinks to skills deleted from the repo
./bin/deploy.sh --dry-run             # show what would happen, change nothing
./bin/deploy.sh --no-skip-system      # also overwrite system-managed skills
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
| pi       | `~/.pi/agent/skills`         |
| cursor   | `~/.cursor/skills`           |
| gemini   | `~/.gemini/skills`           |
| hermes   | `~/.hermes/skills`           |
| windsurf | `~/.codeium/skills`          |
| zed      | `~/.config/zed/skills`       |
| aider    | `~/.aider/skills`            |
| cline    | `~/.cline/skills`            |

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
# 1. Create a skill (scaffolds valid frontmatter + optional resource dirs)
bash skills/skill-man/scripts/new-skill.sh my-skill --resources scripts,references
$EDITOR skills/my-skill/SKILL.md          # fill in the description (the trigger) + body

# 2. Validate it against the spec
python3 skills/skill-man/scripts/validate.py skills/my-skill

# 3. Deploy it
./bin/deploy.sh --skill my-skill

# 4. Iterate — edits in the repo are live immediately (symlinked)

# 5. Commit
git add skills/my-skill && git commit -m "feat: my-skill"
```

Restart the target agent after the first deploy so it picks up the new skill.

## Repo context

This repo dogfoods its own `init-context` skill. [`AGENTS.md`](AGENTS.md) holds
the stable context (Intent, run/build/test, hot invariants, architecture elevator,
deeper docs). [`SESSION.md`](SESSION.md) holds the one-line append-only session log —
read the last line before every session; append one line at session end.
git history IS the work-history record.
