# Pipeline

A deterministic 7-step development pipeline that separates LLM content generation from script-based validation. Scripts own gates, state, and routing — the LLM only generates.

## Install

**Remote (recommended):**

```bash
curl -sSL https://raw.githubusercontent.com/IanLuo/skills/main/install.sh | bash
```

**Local clone:**

```bash
git clone https://github.com/IanLuo/skills.git
cd skills && bash install.sh
```

The installer asks two questions:

1. **Target project directory** — where to install (defaults to current)
2. **Coding agent** — `opencode`, `claude`, `cursor`, or custom

Skills are copied to the agent's skill directory (e.g. `.opencode/skills/`, `.claude/skills/`). Scripts go to `scripts/pipeline/`. All agent-specific paths are auto-patched.

## Usage

```bash
# Run all 7 steps (stops only at approval gate or failure)
/pipeline-orchestrator run

# Run one step at a time
/pipeline-orchestrator step

# Reset pipeline state for a fresh run
bash scripts/pipeline/reset.sh
```

## Architecture

```
Pipeline (7 steps)
  Step 1:  ANALYST         → docs/prd/v{N}.md          [gate: user_approval]
  Step 2a: ARCHITECT        → docs/architecture.md       [gate: boundary_check]
  Step 2b: DESIGNER         → docs/ui-ux/                [gate: consistency_check]
  Step 2v: STEWARD          → review gate                [gates: schema, boundary, consistency]
  Step 3:  PLANNER          → docs/tasks/plan.md         [gate: self_consistency]
  Step 4:  STEWARD          → cross-review               [gates: persona_cascade, coherence]
  Step 5:  QA ARCHITECT     → docs/qa/test-plan.md       [gate: traceability]
  Step 6:  DEVELOPER        → code                       [dev-workflow]
  Step 7:  QA EXECUTOR      → docs/qa/report.md          [gate: qa_pass]
```

### Key principles

- **Scripts validate (exit 0/1), LLM generates** — gates are grep/count checks, never LLM judgment
- **Fresh sub-agent per step** — no conversation leaks between steps
- **All inputs inlined** — sub-agents receive file contents in their prompt, never read files
- **`pipeline.json` defines routing** — the orchestrator never decides where to go next
- **`plan.md` header tracks state** — `[ ]` → `[x]` checkboxes define what runs

### Output files

| File | Writer | When |
|---|---|---|
| `docs/prd/v{N}.md` | Step 1 | Every version |
| `docs/architecture.md` | Step 2a | Delta per version |
| `docs/detailed-components.md` | Step 2a | Delta per version |
| `docs/ui-ux/*` | Step 2b | Delta per version |
| `docs/tasks/plan.md` | Step 3 | Cumulative, per version |
| `docs/qa/test-plan-v{N}.md` | Step 5 | Every version |
| `docs/qa/report-v{N}.md` | Step 7 | Every run |
| `docs/pipeline/report-v{N}.md` | Orchestrator | Every run |

### Scripts

| Script | Purpose |
|---|---|
| `run.sh` | Main loop entry — pre-flight → dispatch → post-flight → gates → advance |
| `check-inputs.sh` | Verify all declared input files exist before step runs |
| `check-outputs.sh` | Verify all declared output files exist after step runs |
| `gate-boundary.sh` | Architecture boundary rules (5 vital slots, State <4KB, credentials isolation) |
| `gate-traceability.sh` | Every test case maps to a plan task and PRD criterion |
| `gate-schema.sh` | Output files match declared schema requirements |
| `advance.sh` | Mark step `[x]` in plan.md header |
| `reset.sh` | Reset all steps to `[ ]` for a fresh run |
| `report.sh` | Append event entries to the execution report |
| `init.sh` | Bootstrap docs/ directory and plan.md header |
