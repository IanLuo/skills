---
name: skill-man
description: Create, validate, and deploy personal skills in this repo, and decide what makes a good skill. Use when scaffolding a new skill, checking a skill's frontmatter against the spec, deploying skills to agents' global folders, or checking whether the repo's spec is in sync with upstream. Also use when you want best practices or popular-skill inspiration before authoring. Do NOT use for writing ordinary application code — only for managing skills themselves.
metadata:
  audience: personal
  domain: tooling
---

# skill-man

This repo is a personal-skills manager. Skills live in `skills/<name>/` and are
symlinked into agents' global folders by `bin/deploy.sh`. This skill teaches how to
author good skills and ship them.

The end-to-end loop is **create → validate → deploy**, plus **study** for ideas.

## 1. Create a skill

Scaffold a valid skill directory from the repo root:

```bash
bash skills/skill-man/scripts/new-skill.sh <name> [--resources scripts,references,assets]
```

`<name>` must be lowercase-hyphen-case (`^[a-z0-9-]+$`, no leading/trailing/double
hyphens, ≤64 chars). The scaffold writes a `SKILL.md` with the only frontmatter fields
the spec allows.

While authoring the body, read these (each loaded only when you need it):

- **[skill-spec.md](references/skill-spec.md)** — read when you need the exact
  frontmatter fields, validation rules, or directory layout. Authoritative rules are
  summarized in the cheatsheet below; read the reference for the full anatomy.
- **[best-practices.md](references/best-practices.md)** — read before and while
  writing the body. Covers progressive disclosure, conciseness, degrees of freedom,
  description-as-trigger, and forward-testing.
- **[popular-skills.md](references/popular-skills.md)** — read when you want
  inspiration or want to study how well-known skills are structured.

## 2. Validate before deploying

```bash
python3 skills/skill-man/scripts/validate.py                 # all skills
python3 skills/skill-man/scripts/validate.py skills/<name>   # one skill
```

Checks every skill in `skills/` against the spec (frontmatter keys, name rules,
description length/characters, `compatibility` length, body-length warning). Exit
non-zero on any failure. Fix everything before deploying — an invalid skill may be
silently ignored by agents. (`validate.py` is the repo's source of truth for the spec;
see [references/skill-spec.md](references/skill-spec.md).)

## 3. Deploy

```bash
bash bin/deploy.sh               # all skills → all detected agents
bash bin/deploy.sh --skill <name> # one skill
bash bin/deploy.sh --dry-run      # preview first
bash bin/deploy.sh --doctor       # health-check deployed symlinks
```

Deploy symlinks each skill into the global skills folder of every detected agent
(claude, opencode, codex, cursor, gemini, windsurf, zed, aider, cline — see
`bin/deploy.sh --list`). Because deploy uses symlinks, edits in this repo are live
immediately; no re-deploy needed to pick up changes. System-managed skills (real
files, e.g. nix-managed) are never overwritten. Restart the agent after a first deploy
so it discovers the new skill. `--doctor` reports dangling links (e.g. if the repo
moved) and real-dir divergence — run it after moving the repo or if an agent stops
seeing a skill.

## 4. Stay in sync with upstream

```bash
bash skills/skill-man/scripts/sync-check.sh   # are we behind anthropics/skills?
```

The spec is pinned to a commit of `anthropics/skills` (see `.upstream`). Run
sync-check to detect drift; if behind, diff the upstream `quick_validate.py` against
`validate.py`, update `SPEC_PINNED_REF` + `.upstream`, and re-run `tests/run.sh`.

## Spec cheatsheet (needed on every authoring task)

Canonical spec: https://agentskills.io/specification (validator pinned at
`anthropics/skills` `5754626`; `validate.py` is this repo's source of truth).

**Frontmatter** — only these keys are allowed:

| key | required | rule |
|---|---|---|
| `name` | yes | `^[a-z0-9-]+$`, no leading/trailing/double `-`, ≤64 chars, equals the folder name |
| `description` | yes | ≤1024 chars, no `<` or `>` (validator-enforced), the **primary trigger** — state both what it does and when to use it |
| `license` | no | short license name or bundled-license-file reference (e.g. `MIT`, `Proprietary. LICENSE.txt has complete terms`) |
| `allowed-tools` | no | space-separated string (e.g. `Bash(git:*) Read`); experimental/harness-dependent |
| `metadata` | no | free-form map (e.g. `audience`, `domain`); harness-dependent |
| `compatibility` | no | ≤500-char string of environment requirements; most skills omit it |

**Body** — keep under 500 lines (spec guidance; `validate.py` warns). Use progressive
disclosure: put core workflow here, move detail into `references/` and link to it with
a one-line "read this when…".

**Directory** — one skill per folder; required `SKILL.md`; optional `scripts/`,
`references/`, `assets/` (and `agents/` for Codex-specific UI metadata). Do **not** add
README, CHANGELOG, or install docs — the skill is for an agent, not a human reader.

## What makes a good skill (quick rules)

A deliberate always-in-context summary; detail in [best-practices.md](references/best-practices.md).

- The `description` decides whether the skill triggers. Put "when to use" there, not
  in the body. Enumerate literal triggers (`.docx`, `"word document"`) and add a
  negative trigger ("Do NOT use for…") when a sibling skill could also match.
- The model is already smart — add only what it doesn't already know. Prefer concrete
  examples over prose.
- Match specificity to fragility: fragile/risky steps → a strict script; many valid
  approaches → text guidance. See [best-practices.md](references/best-practices.md) §3.
- Forward-test on real tasks with a fresh subagent; better, baseline-then-write
  (watch an agent *fail* without the skill first). See
  [best-practices.md](references/best-practices.md) §6.
