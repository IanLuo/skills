<!-- synced: 2b5089b -->

# CURSOR — skills repo (2026-06-27)

**Position:** init-context redesigned (index+intent AGENTS.md + tier-routing), task-core drift gate (`check`) built + e2e tested. Next: commit and push feat/skill-man.

**Blockers:** none.

**Open:** Deploy the three new task-family skills to agents (untracked dirs — commit first, then deploy). Cross-harness context-file conventions surfaced in research (Claude Code <200 lines, Factory ≤150, Cursor <500/rule, Windsurf 6000/12000 chars).

**Health:** 🟢 validator ✓ all 6 skills; tests 10/10; drift gate e2e 5/5 scenarios pass; protocol block byte-identical across template and dogfood.

**Verification:** working tree: `python3 skills/skill-man/scripts/validate.py` ✓ all 6; `bash tests/run.sh` 10/10; `check` e2e 5/5 (clean pass, stale pointer fail, behind HEAD fail, bogus sha fail, missing marker fail).

**Errors-that-changed-plan:** CURSOR.md backtick-wrapped paths caused false `stale pointer` → added backtick/comma/semicolon stripping to `check` parser.

**Decisions:** AGENTS.md is an index (embed intent+invariants+commands; point to everything else via disk-sourced Deeper-docs table). Drift gate validates pointers + synced SHA against git — prose is guidance, exit non-zero is enforcement.

**Active pointers:** AGENTS.md, CURSOR.md, skills/init-context/SKILL.md, skills/init-context/assets/AGENTS.template.md, skills/init-context/assets/CURSOR.template.md, skills/init-context/references/protocol.md, skills/task-core/SKILL.md, skills/task-core/references/protocol.md, skills/task-core/scripts/check, skills/dev-task/SKILL.md, skills/design-task/SKILL.md, skills/skill-man/scripts/validate.py, tests/run.sh
