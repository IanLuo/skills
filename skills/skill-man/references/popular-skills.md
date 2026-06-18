# Popular skills — a study catalog

Read this when you want inspiration or want to study how well-known skills are
structured. For each entry: what it does, where it comes from, and a confidence
level.

> **Sourcing note.** High-confidence entries were verified live against
> github.com/anthropics/skills (GitHub contents API) and the on-disk Codex
> `.system` skills on 2026-06. Medium-confidence community entries were also
> verified live (repo + README fetched) on 2026-06. Re-verify quarterly — repos
> rename, move, and disappear. Prefer cloning to study; staleness surfaces as a
> dead link below.
>
> **Last verified: 2026-06.**

## High confidence — verified on disk (Codex `.system` skills)

These live at `~/.agents/skills/.system/` (symlinked into `~/.codex/skills/.system/`).
They are ports/parallels of Anthropic's official skills and are excellent reference
implementations.

| Skill | What it does | Study for |
|---|---|---|
| `skill-creator` | Guide for creating effective skills (the on-disk Codex port) | The canonical authoring guide — the spec in [skill-spec.md](skill-spec.md) and practices in [best-practices.md](best-practices.md) are distilled from it. Scripts: `init_skill.py`, `quick_validate.py`, `generate_openai_yaml.py`; references `openai_yaml.md`. |
| `skill-installer` | Install skills into `$CODEX_HOME/skills` from a curated list or GitHub repo | How to bundle helper scripts that do network installs; clean script-per-task layout (`list-skills.py`, `install-skill-from-github.py`, `github_utils.py`). |
| `plugin-creator` | Scaffold plugin dirs with `.codex-plugin/plugin.json` + marketplace entries | A skill that's almost entirely workflow + a scaffold script; shows a SKILL.md that drives `scripts/create_basic_plugin.py` with references for the exact JSON spec. |
| `imagegen` | Generate images via OpenAI image APIs | Tool-integration skill with scripts and assets. |
| `openai-docs` | Reference into OpenAI API docs | A `references/`-heavy skill — loads doc slices on demand. |

## High confidence — official Anthropic skills (github.com/anthropics/skills)

The official repo ships 17 skills. The document skills are the canonical examples
of well-structured skills with `scripts/`, `references/`, and `assets/`. Clone and
read: `git clone https://github.com/anthropics/skills.git`.

| Skill | What it does | Study for |
|---|---|---|
| `pdf` | Create, edit, analyze PDFs (extract text, fill forms, merge, rotate, redact) | The flagship example: scripts for deterministic ops + references for the format. |
| `docx` | Create/edit Word docs with tracked changes, comments, formatting preservation | Multi-tool skill; `references/` for OOXML/redlining split out from the body. Uses "Triggers include:" + "Do NOT use for…" patterns — see [best-practices.md](best-practices.md) §4. |
| `pptx` | Create/edit PowerPoint decks | Asset/template-heavy skill. |
| `xlsx` | Create/edit spreadsheets, preserve formulas | Script + reference balance. |
| `web-artifacts-builder` | "Suite of tools for creating elaborate, multi-component claude.ai HTML artifacts using modern frontend web technologies (React, Tailwind CSS, shadcn/ui). Use for complex artifacts requiring state management, routing, or shadcn/ui components — not for simple single-file HTML/JSX artifacts." | Asset/template-driven skill; negative-trigger disambiguation. |
| `skill-creator` | "Create new skills, modify and improve existing skills, and measure skill performance. Use when users want to create a skill from scratch, edit, or optimize an existing skill, run evals to test a skill, benchmark skill performance with variance analysis, or optimize a skill's description for better triggering accuracy." | More than an authoring guide — it centers on **evals/measurement**: scripts include `run_eval.py`, `aggregate_benchmark.py`, `improve_description.py`, `run_loop.py`, `generate_report.py`. The on-disk Codex port above is a slimmer subset (no eval machinery). |
| `mcp-builder` | Guide for creating high-quality MCP servers (Python/FastMCP or Node/TS SDK). Use when building MCP servers to integrate external APIs/services. | The MCP-server skill shape: `references/` for the MCP spec + `scripts/` for scaffolding. |

### Also worth studying (real, confirmed)

| Skill | Shape to study |
|---|---|
| `claude-api` | `references/`-heavy skill loaded on demand; uses a block-scalar (`description: |-`) frontmatter — a good parse test for skill-man's validator. |
| `webapp-testing` | The verification skill shape: drive a browser/app to confirm a change works. |
| `canvas-design` | Asset/design-driven skill. |
| `brand-guidelines` | Domain-knowledge + assets skill. |
| `doc-coauthoring`, `frontend-design`, `theme-factory`, `internal-comms`, `algorithmic-art`, `slack-gif-creator` | Other canonical instances across shapes. |

## Medium confidence — community collections (verified live 2026-06)

| Collection | What it is | Why look |
|---|---|---|
| `obra/superpowers` (230k+ stars) | "A complete software development methodology for your coding agents, built on top of a set of composable skills." 14 skills, all dev-process: `brainstorming`, `writing-plans`, `executing-plans`, `test-driven-development`, `subagent-driven-development`, `systematic-debugging`, `requesting-code-review`, `receiving-code-review`, `using-git-worktrees`, `verification-before-completion`, `writing-skills`, `dispatching-parallel-agents`, `finishing-a-development-branch`, `using-superpowers`. Multi-harness (Claude Code, Codex, Cursor, Gemini, Copilot, OpenCode). | Multi-skill **composition** and skill-to-skill triggering; the TDD-for-skills pattern in `writing-skills` (see [best-practices.md](best-practices.md) §6); the evidence-before-assertions pattern in `verification-before-completion`. |
| `wshobson/agents` (37k+ stars) | "Production-ready agentic workflow building blocks: 84 plugins, 192 agents, 156 skills, 102 commands — built for Claude Code and consumed natively by OpenAI Codex CLI, Cursor, OpenCode, Gemini CLI, and GitHub Copilot from a single Markdown source." A multi-harness plugin marketplace. | Breadth of short, focused skills + trigger-word patterns across a marketplace. |
| `wshobson/commands` (2.5k+ stars) | "A collection of production-ready slash commands for Claude Code." | Trigger-word and description patterns (adjacent to skills). |
| `hesreallyhim/awesome-claude-code` (47k+ stars) | An awesome-list aggregating Claude Code tools/skills/agents. **Caveat:** as of 2026-06 the README was mid-refactor — a placeholder ("Table of Contents … I. TODO"), not a usable index. Browse the repo tree directly rather than relying on the ToC. | Discovery index — browse the tree, don't trust the ToC until it's rebuilt. |

## Common skill shapes to learn from

Most good skills fall into a few shapes. Pick the shape that matches your idea:

1. **Tool-integration skill** — wraps an API or CLI the model shouldn't re-derive
   each time (`imagegen`, a `stripe` skill). Usually `scripts/` for the call +
   `references/` for auth/params.
2. **Document-format skill** — read/write a complex file format deterministically
   (`pdf`, `docx`). `scripts/` for mutations, `references/` for format detail,
   `assets/` for templates.
3. **MCP-server skill** — build/scaffold a Model Context Protocol server
   (`mcp-builder`). `references/` for the MCP spec + `scripts/` for scaffolding.
   When a skill wraps an MCP server, prefer declaring it via the harness
   tool-dependency mechanism rather than reimplementing calls in scripts.
4. **Workflow / procedure skill** — a repeatable multi-step process
   (`skill-creator`, `plugin-creator`, this `skill-man`). Lean SKILL.md driving a
   scaffold/validate script + references for the spec.
5. **Domain-knowledge skill** — company schemas, policies, conventions
   (`bigquery`-style). Mostly `references/`; SKILL.md is a navigation index.
6. **Template / boilerplate skill** — scaffold a project (`web-artifacts-builder`,
   `frontend-webapp-builder`). Mostly `assets/`; SKILL.md says when and how to
   copy them.

## How to use this catalog

- Before authoring, find 1–2 skills of the matching **shape** above and skim their
  SKILL.md to internalize the structure.
- Steal the *pattern* (how frontmatter is phrased, how references are linked, how
  scripts are invoked), not the content.
- After authoring, forward-test (see [best-practices.md](best-practices.md) §6)
  against a real task the way these skills would be used.
