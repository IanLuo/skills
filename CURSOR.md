<!-- synced: cee7b68 -->

# CURSOR — skills repo (2026-06-20)

**Position:** init-context skill + deploy --prune + dogfood AGENTS.md/CURSOR.md committed as `de7bfe3` on `feat/skill-man`. Next: push branch and open PR to main.

**Blockers:** none.

**Open:** `docs.anthropic.com` vs `agentskills.io` spec unverified (region-blocked, low priority). Whether a `new-task` companion skill is needed — answer via dogfooding. (Cursor name + protocol location settled: CURSOR.md, protocol embedded in AGENTS.md.)

**Health:** 🟢 tests 10/10, doctor clean (no dangling links), 3 skills validate. WebFetch/WebSearch harness tools broken (curl workaround works).

**Verification:** @ f35e61d: `tests/run.sh` 10/10, `deploy.sh --doctor` clean, `validate.py` ✓ for all 3 skills. Cold test on `ss`: `pytest` 4 passed, `nix build` exit 0, CLI runs — evidence in that repo's CURSOR.md.

**Errors-that-changed-plan:** `ss` cold-test nearly shipped unverified `nix build` in AGENTS.md (8-min build) → led to SKILL.md step-2 fix (verify-then-write is per-command, not deferred).

**Decisions:** init-context architecture = hybrid (global skill + per-project AGENTS.md/CURSOR.md, protocol embedded in AGENTS.md, no separate global protocol file). `--prune` added to deploy.sh for the delete lifecycle gap.

**Active pointers:** `AGENTS.md`, `skills/init-context/SKILL.md`, `skills/init-context/assets/AGENTS.template.md`, `skills/init-context/references/protocol.md`, `bin/deploy.sh`, `skills/skill-man/scripts/validate.py`, `tests/run.sh`
