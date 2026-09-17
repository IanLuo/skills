---
name: skill-man
description: Create, revise, validate, audit, deploy, and sync personal skills in this repo, and decide what makes a good skill. Use when scaffolding a new skill; evaluating an existing skill or asking whether one is any good (triggers, reference integrity, structure, token economy, single source of truth); checking a skill against the frontmatter spec; deploying skills to agents' global folders; checking whether the repo's spec is in sync with upstream; or wanting best practices and popular-skill inspiration before authoring. Do NOT use for writing ordinary application code — only for managing skills themselves.
metadata:
  audience: personal
  domain: tooling
---

# skill-man

This repo is a personal-skills manager. Skills live in `skills/<name>/` and are
symlinked into agents' global folders by `bin/deploy-skills.sh`. This skill teaches how to
author good skills and ship them.

## Modes

Name the mode before you act, then take only that path. The point of the table is the last
column — each mode has something it must **not** do.

| Mode | Fires on | Action | Won't do |
|---|---|---|---|
| **create** | "new skill", "scaffold a skill", "make a skill for X" | §1 scaffold, fill the body | evaluate the draft it just wrote |
| **revise** | "tighten this skill", "the body is stale", "fix its description" | §1 edit in place → validate + audit | change the spec to fit the edit |
| **validate** | "is this valid", "check the frontmatter", "about to deploy" | §2 `validate.py` | judge quality — that's evaluate |
| **evaluate** | "is this skill any good", "audit/review this skill", "what's wrong with it" | §3 `audit.py` + [evaluation.md](references/evaluation.md), ranked by tier | edit anything |
| **deploy** | "deploy", "ship to agents", "install" | §4 `deploy-skills.sh` | deploy a skill that failed validate |
| **sync** | "are we behind upstream", "spec drift" | §5 `sync-check.sh` | re-pin without review |
| **upstream update** | "what's new upstream", "new official skills" | §6 `update.sh` | auto-apply |
| **study** | "how do good skills do X", "inspiration" | [popular-skills.md](references/popular-skills.md) | copy a skill wholesale |

Rules that cross modes:

- **validate and evaluate report; they never edit.** Fixing is `revise`.
- **evaluate ranks by failure impact.** A Tier A finding ends the audit — fix it before
discussing anything downstream. See [evaluation.md](references/evaluation.md).
- **deploy requires validate to pass** — nothing ships unvalidated.
- Two modes in one request → do them in pipeline order (create → validate → deploy).

## 1. Create or revise a skill

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
- **[doc-format.md](references/doc-format.md)** — the house standard for docs skills
  produce: agent-oriented (not human), minimum size, checklist-complete, freshness.
  Every doc-producing skill links to it.
- **[popular-skills.md](references/popular-skills.md)** — read when you want
  inspiration or want to study how well-known skills are structured.

### Revising an existing skill

Edit `SKILL.md` in place — deploy uses symlinks, so the change is live immediately with no
redeploy. Don't edit while evaluating: if the request is really "is this good?" or "is this
valid?", stop and report instead (§2, §3).

After editing, re-run `validate.py` (§2) then `audit.py` (§3). If you changed the
description, re-test the triggers ([evaluation.md](references/evaluation.md) A1). If you
added or moved a resource, re-audit so no reference or orphan is left behind. Renaming a
skill means renaming its folder too — `validate.py` enforces name == folder.

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

Spec conformance only. For quality — triggers, reference integrity, structure, token
economy — that is the next section.

## 3. Evaluate a skill

Spec-valid is not good. Audit quality separately, and report it **ranked by failure
impact**: a Tier A failure means the skill does not load or fires on the wrong requests,
which makes every other finding moot.

```bash
python3 skills/skill-man/scripts/audit.py                 # all skills
python3 skills/skill-man/scripts/audit.py skills/<name>   # one skill
```

`audit.py` covers the deterministic half — Tier A2 reference integrity (broken,
ambiguous, orphaned paths) and Tier C1 body size. Then read
[evaluation.md](references/evaluation.md) for the full ranked rubric: Tier A trigger and
claim accuracy, Tier B contract / degrees-of-freedom / boundaries, Tier C clarity, Tier D
single-source-of-truth and provenance. Report in tier order; a Tier A failure ends the
audit.

**Evaluate never edits.** It reports each finding as observation + `file:line` + concrete
fix. Applying them is `revise`.

## 4. Deploy

```bash
bash bin/deploy-skills.sh               # all skills → all detected agents
bash bin/deploy-skills.sh --skill <name> # one skill
bash bin/deploy-skills.sh --dry-run      # preview first
bash bin/deploy-skills.sh --doctor       # health-check deployed symlinks
```

Deploy symlinks each skill into the global skills folder of every detected agent
(claude, opencode, codex, cursor, gemini, windsurf, zed, aider, cline — see
`bin/deploy-skills.sh --list`). Because deploy uses symlinks, edits in this repo are live
immediately; no re-deploy needed to pick up changes. System-managed skills (real
files, e.g. nix-managed) are never overwritten. Restart the agent after a first deploy
so it discovers the new skill. `--doctor` reports dangling links (e.g. if the repo
moved) and real-dir divergence — run it after moving the repo or if an agent stops
seeing a skill.

## 5. Stay in sync with upstream

```bash
bash skills/skill-man/scripts/sync-check.sh   # are we behind anthropics/skills?
```

The spec is pinned to a commit of `anthropics/skills` (see `.upstream`). Run
sync-check to detect drift; if behind, diff the upstream `quick_validate.py` against
`validate.py`, update `SPEC_PINNED_REF` + `.upstream`, and re-run `tests/run.sh`.

## 6. Upstream update — search the authorized source

Run periodically to check what's new from the authorized source (`anthropics/skills`):

```bash
bash skills/skill-man/scripts/update.sh
```

It reports, from the authorized source, since the pinned ref:
- **Spec drift** — did `quick_validate.py` change upstream?
- **New / removed official skills** — inventory diff of `skills/*/SKILL.md`.

It **never auto-applies** — it surfaces what changed; you review, then re-sync the
spec (compare `quick_validate.py`, bump `SPEC_PINNED_REF` + `.upstream`, re-run
`tests/run.sh`) only if you choose. Exit 0 = in sync, 1 = updates available,
2 = pin refs disagree.

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
| `disable-model-invocation` | no | `true` — **explicit-only skill**: the model never auto-invokes it, only an explicit `/skill` call runs it. skill-man extension beyond the pinned spec (community skills.sh convention). ⚠ some harnesses disable explicit invocation too — verify before relying on it. |

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
