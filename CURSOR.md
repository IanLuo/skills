<!-- synced: b3d95ec -->

# CURSOR — skills repo (2026-06-27)

**Goal:** 27ac77abc842

**Position:** review task verified: progress updated. Next: continue from the latest evidence.

**Blockers:** none.

**Open issues:** none.

**Health:** 🟢 verified by fresh evidence.

**Verification:** claimed: review progress updated; verified: All review findings fixed: (1) Goal field added to CURSOR.md schema — create-goal writes stable 12-char hex id on fresh cursor, survives session rewrites because update-progress awk only touches Position line, all 4 doc locations (AGENTS.md, AGENTS.template.md, init-context/protocol.md, task-core/protocol.md) updated with Goal row; (2) register-doc pipe-in-cue bug fixed: bash rejects unescaped | in --cue (exit 2), _register_doc.py escapes | as | on write and un-escapes on read. End-to-end: fresh-cursor→lock→register→session-rewrite→Goal-survives. validate.py ✓ all 9 skills; tests/run.sh 10/10..

**Errors-that-changed-plan:** CURSOR.md backtick-wrapped paths caused false `stale pointer` → added backtick/comma/semicolon stripping to `check` parser.

**Decisions:** AGENTS.md is an index (embed intent+invariants+commands; point to everything else via disk-sourced Deeper-docs table). Drift gate validates pointers + synced SHA against git — prose is guidance, exit non-zero is enforcement.

**Active pointers:** AGENTS.md, CURSOR.md, skills/init-context/SKILL.md, skills/init-context/assets/AGENTS.template.md, skills/init-context/assets/CURSOR.template.md, skills/init-context/references/protocol.md, skills/task-core/SKILL.md, skills/task-core/references/protocol.md, skills/task-core/scripts/check, skills/dev-task/SKILL.md, skills/design-task/SKILL.md, skills/skill-man/scripts/validate.py, tests/run.sh
