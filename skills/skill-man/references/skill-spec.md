# Skill specification

Canonical rules live at **https://agentskills.io/specification** (validator
reference: `quick_validate.py` @ `anthropics/skills` commit `5754626`, synced
2026-06). This file is a local cheatsheet of what `scripts/validate.py` enforces —
re-sync against the canonical source before changing it (see
`scripts/sync-check.sh`). The cheatsheet in [../SKILL.md](../SKILL.md) is the
always-in-context summary.

## Anatomy

```
skill-name/
├── SKILL.md            (required) frontmatter + markdown body
├── agents/             (optional, Codex-specific — see below)
├── scripts/            (optional) executable code — deterministic, token-efficient
├── references/         (optional) docs loaded into context on demand
└── assets/             (optional) files used in output (templates, images, fonts)
```

- The folder name **must equal** the `name` field.
- One skill per folder. No nested skills.

## SKILL.md

Two parts:

1. **YAML frontmatter** — the only thing read to decide whether the skill triggers.
2. **Markdown body** — loaded only *after* the skill triggers.

### Frontmatter fields

Only these keys are allowed. Any other key fails validation.

| key | required | type | constraint |
|---|---|---|---|
| `name` | yes | string | `^[a-z0-9-]+$`; no leading/trailing/double hyphens; ≤64 chars; equals folder name |
| `description` | yes | string | ≤1024 chars; no `<` or `>` (validator-enforced); the **primary trigger** — what it does AND when to use it |
| `license` | no | string | short license name or reference to a bundled license file, e.g. `MIT`, `Apache-2.0`, or `Proprietary. LICENSE.txt has complete terms` (official skills use the latter forms; not SPDX-formatted) |
| `allowed-tools` | no | space-separated string | e.g. `Bash(git:*) Bash(jq:*) Read`. **Experimental / harness-dependent:** honored by some harnesses (e.g. Codex) to restrict tool use; not read by all — verify your target harness enforces it before relying on it as a security control. |
| `metadata` | no | map | free-form (e.g. `audience`, `domain`). **Harness-dependent:** read by some harnesses for UI/chips; not universal. |
| `compatibility` | no | string | ≤500 chars; environment requirements, e.g. `Requires git, docker, jq, and access to the internet` or `Requires Python 3.14+ and uv`. Most skills do not need it. |

Do **not** invent other top-level keys (`version`, `triggers`, …). Put non-standard
metadata under `metadata`.

### `description` is the trigger

This is the single most important field. The harness matches the user's request
against skill descriptions to decide which skill (if any) to load. Rules:

- State **what** the skill does and **when** to use it.
- Put all "when to use" information here — **not** in the body. A "When to Use this
  Skill" section in the body is useless because the body is only read after the
  description has already won the trigger.
- Bad: `"Helps with PDFs."`
- Good: `"Create, edit, and analyze PDF documents: extract text, fill forms, merge,
  rotate, redact. Use when working with .pdf files for any document task."`

### `name`

- Lowercase letters, digits, hyphens only (`^[a-z0-9-]+$`).
- No leading/trailing hyphens, no consecutive hyphens.
- ≤64 chars.
- Prefer short, verb-led phrases (`address-comments`, `rotate-pdf`).
- Namespace by tool when it aids triggering (`gh-address-comments`,
  `linear-address-issue`).
- Normalize human titles to hyphen-case: `"Plan Mode"` → `plan-mode`.

### Body

- Keep under **500 lines** (official spec/skill-creator guidance; complementary
  target: <5000 tokens). `validate.py` warns, doesn't fail, on this. Approaching
  the limit, split detail into `references/` and link from the body.
- Use imperative/infinitive form.
- Reference bundled files explicitly and say *when* to read each, so the reader knows
  they exist: `See [forms.md](references/forms.md) for the form-filling guide.`

## Bundled resources

### `scripts/`

Executable code (Python/Bash/etc.) for tasks needing deterministic reliability or that
get rewritten repeatedly. Token-efficient: the agent can execute a script without
reading it into context.

- Include when the same code is being rewritten repeatedly, or deterministic behavior
  matters.
- Test added scripts by actually running them.
- The agent may still read a script to patch it for the environment — keep them readable.

### `references/`

Documentation loaded into context on demand.

- Include for schemas, API docs, domain knowledge, detailed workflows.
- Keeps SKILL.md lean; loaded only when the agent decides it's needed.
- If a reference is large (>~10k words), include grep/search patterns in SKILL.md so
  the agent can search rather than read whole.
- **Don't duplicate** between SKILL.md and references — pick one home for each fact.
- Keep references **one level deep** from SKILL.md (no references linking references).
- For reference files >100 lines, put a table of contents at the top.

### `assets/`

Files used in the *output* the agent produces (templates, images, icons, fonts,
boilerplate). Never loaded into context — referenced or copied into results.

### `agents/`

**Codex/OpenAI-harness-specific — not part of the agentskills.io spec.** Product
metadata read by the harness, not the model. On Codex this is
`agents/openai.yaml` with `interface` (display name, icons, brand color, default
prompt), `dependencies.tools` (MCP servers), and
`policy.allow_implicit_invocation`. Include it only if you target the Codex/harness
UI; if you do, see the on-disk `~/.agents/skills/.system/skill-creator/references/openai_yaml.md`
for field definitions. Other harnesses ignore this directory.

## What NOT to include

A skill should contain only files that directly support its function. Do **not** add:

- `README.md`, `INSTALLATION_GUIDE.md`, `QUICK_REFERENCE.md`, `CHANGELOG.md`
- Setup/testing notes, process docs, user-facing documentation

The skill is for an agent, not a human reader. Auxiliary files add clutter and
confusion.

## Validation

Run from the repo root:

```bash
python3 skills/skill-man/scripts/validate.py
```

Checks every skill in `skills/`: frontmatter well-formed, only allowed keys, name
rules, description length/characters, body-length warning. Exit non-zero on any
failure.
