---
name: pipeline-orchestrator
description: Deterministic pipeline orchestrator. Scripts handle ALL validation and state. This skill is a thin dispatch loop — reads signals from scripts, dispatches sub-agents, hands back to scripts for validation. Zero LLM decision-making.
---

# Pipeline Orchestrator

This skill is a dispatch loop. Scripts (`scripts/pipeline/`) own validation, gates, and state. You own only: reading script output, dispatching Task sub-agents, and following script signals.

---

## Execution

### Step Mode: `/pipeline-orchestrator step`

One step. Stop after advance.

### Run Mode: `/pipeline-orchestrator run`

Loop until complete, approval gate, or failure.

---

## Loop

### 1. Run Pre-flight

```bash
bash scripts/pipeline/run.sh step v{N}
```

This script:
- Reads `plan.md` header, finds first `[ ]` step
- Validates all declared inputs exist
- Writes initial state to report
- Writes a dispatch prompt to `/tmp/pipeline-prompt-{step_id}.txt`
- Outputs: `DISPATCH:{step_id}:{version}`

If exit code ≠ 0 → pre-flight failed. Present failure. STOP.

### 2. Read Dispatch Signal

Read `/tmp/pipeline-prompt-{step_id}.txt`. Extract `step_id` and `version`.

### 3. Construct and Dispatch Sub-agent

Read `pipeline.json` for this `step_id`. Get `persona`, `workflow`, `inputs`, `outputs`, `gates`.

Construct a Task prompt using the EXACT format below. Inline ALL input file contents. Dispatch via `Task` tool (subagent_type=general).

```
You are the WORKER executing {step_id}: {step_name}

PERSONA:
[content of {PIPELINE_SKILLS_DIR}/pipeline-orchestrator/personas/{persona}.md]

WORKFLOW:
Load the `{workflow}` skill and follow its loop exactly. Do NOT skip steps.

DENY: Do NOT proceed to the next workflow step until the current step's output is written and verified.
DENY: Do NOT read any files — all inputs are inlined below.

CONTEXT:
--- {input_file_1} ---
[full content]
--- {input_file_2} ---
[full content]

OUTPUT:
Write to: {resolved output files}
[Schema: {schema_path} if defined]

GATE:
{gate descriptions — sub-agent writes content, scripts validate}

After writing, report: what you wrote, where, and the result.
```

**CRITICAL:**
- Inline ALL inputs. Never say "read file X."
- The sub-agent gets ZERO conversation history.
- If persona is `null`, omit PERSONA section.
- If workflow is `null`, the sub-agent runs as a standard agent (e.g., steward).

### 4. Run Post-flight

After sub-agent completes:

```bash
bash scripts/pipeline/check-outputs.sh {step_id} {version}
bash scripts/pipeline/report.sh {step_id} post-flight pass ""
```

If exit code ≠ 0 → post-flight failed. Log failure. Rebound per `on_fail.target`. STOP.

### 5. Run Gates

For each gate in the step definition, run:

```bash
bash scripts/pipeline/gate.sh {step_id} {gate_type} {version}
bash scripts/pipeline/report.sh {step_id} gate pass ""
```

Available gate scripts:
| gate_type | Script | What it checks |
|---|---|---|
| `boundary_check` | `gate-boundary.sh` | 5 vital slots present, State <4KB, credentials isolated |
| `traceability` | `gate-traceability.sh` | Every test has Plan Task + PRD Criterion |
| `schema_validation` | `gate-schema.sh` | Output files match declared schema |

If any gate exit code ≠ 0 → FAIL. Log. Rebound per `on_fail.target`.

### 6. Handle User Approval Gate

If gate type is `user_approval`:
- Output the PRD summary from the sub-agent result.
- Ask user: "Approve to continue, or reject for rework?"
- STOP and wait for user response.
- On approval → proceed to advance.
- On rejection → log failure, rebound to `on_fail.target`.

### 7. Advance

```bash
bash scripts/pipeline/advance.sh {step_id} "$(date +%Y-%m-%d\ %H:%M)"
bash scripts/pipeline/report.sh {step_id} advance pass ""
```

Commit: `git add docs/tasks/plan.md && git commit -m "pipeline: {step_name} passed [{step_id}]"`

### 8. Continue (run mode only)

- If `on_pass` is `null` → COMPLETE. Present final report. STOP.
- If `on_pass` has `user_approval` gate → STOP. Tell user pipeline awaits approval.
- Otherwise → read next `step_id` from `get_next_step`. If `[x]` → skip (log skip event), loop. If `[ ]` → loop to step 1.

In step mode → STOP after advance.

---

## Failure Handling

If any step fails:
- Log the failure via `report.sh {step_id} fail fail "{reason}"`
- Read `on_fail.target` from pipeline.json
- If target is a step ID → set that step `[ ]` in plan.md header, STOP
- If target is `null` → read rebound from steward/QA report

---

## Status Blocks

On every phase transition, emit:

```
═══ PIPELINE v{version} ═══ STEP: {step_id} ═══ PHASE: {phase} {✅|❌|⏸}
{timestamp}
{detail}
```

---

## Non-negotiable Rules

1. **Never skip a step.** Scripts validate. You follow their exit codes.
2. **Never skip a gate.** Every gate script must run and return 0 before advance.
3. **Never infer state.** `get_next_step` reads plan.md fresh. No cached assumptions.
4. **Never leak context.** Sub-agents get inlined files only.
5. **Never decide routing.** `pipeline.json` defines `on_pass`/`on_fail.target`.
6. **Zero LLM judgment in validation.** Scripts return exit codes. You obey them.
