# dev-workflow

Deterministic 6-step TDD pipeline with portable plan execution.

```
{PIPELINE_SKILLS_DIR}/dev-workflow/
├── SKILL.md              ← LLM instructions (TDD, review gate, commit discipline)
└── workflow-runner.sh    ← deterministic state machine (which task to do next)
```

## Triggering

**Inside opencode** — just say `execute the plan`. The skill auto-loads and enters orchestrator mode.

**Outside opencode** — call the script directly. Works in any shell, CI, Makefile, or other LLM tool.

## Script commands

```bash
# Run from project root (where docs/tasks/plan.md lives)
S={PIPELINE_SKILLS_DIR}/dev-workflow/workflow-runner.sh

bash $S status              # list all tasks with states
bash $S next                # get next task (marks [⏳], prints "LINE|active|DESC")
bash $S done <LINE>         # mark task [x]
bash $S fail <LINE>         # revert [⏳] → [ ]
bash $S recover             # heal crashed [⏳] tasks
```

## Plan format

Both conventions work:
```
- [ ] Task 1: description here
* [ ] Task 1: description here
```

## The loop

```
recover → next → dispatch worker → done → next → ... → DONE
```

The script handles all state transitions and commits. The LLM only executes the task between `next` and `done`.

## Dependencies

bash 4+, git, grep. No opencode or LLM-specific dependencies.
