# Design: Stronger Event Logging for fs CLI

**Status:** Proposed  
**Date:** 2026-09-22  
**Problem:** When agents get deep into debugging, they stop calling `fs task knowledge` / `fs task update --decision`. Knowledge is silently lost. The event store only captures what agents *remember* to record.

## Analysis of Approaches

### Approach 1: Agent Hooks (PostToolUse / Stop)

**Mechanism:** Pi and Claude Code both support `hooks` in `settings.json`. A `PostToolUse` hook fires after every tool call with the tool name, input, and response on stdin as JSON. A `Stop` hook fires when the agent finishes a turn.

**What it could do:**
- **PostToolUse on `Bash`:** Detect `git commit` in the command, extract the commit message, auto-record as knowledge on the active task.
- **Stop hook:** Before the agent stops, check if there are active tasks with recent git activity but no recent `fs` events, and inject a reminder (`decision: "block"` + `reason`) forcing the agent to record before ending.

**Assessment:**
- ✅ Deterministic — runs every time, outside LLM control
- ✅ Works with existing fs CLI — just calls `fs` commands from the hook script
- ⚠️ PostToolUse for *every* tool call is noisy — need careful matchers
- ⚠️ Extracting "decisions" from raw tool output requires heuristics or is just logging activity (not knowledge)
- ⚠️ Hook reliability varies — there's a [known bug](https://github.com/anthropics/claude-code/issues/6305) with PostToolUse not firing in Claude Code
- ❌ Cannot meaningfully extract *decisions* from tool calls — a file edit doesn't encode *why*

**Verdict: Useful for the Stop hook (checkpoint enforcement). PostToolUse is too noisy and can't distinguish decisions from activity.**

### Approach 2: `fs status` Git Activity Detection

**Mechanism:** Enhance `fs status` to detect uncommitted work and recent git activity, then surface warnings about unrecorded events.

**What it could do:**
- Show "⚠ 3 commits since last fs event" in status output
- Show "⚠ N files changed, uncommitted" 
- Flag active tasks with no knowledge/decisions recorded

**Assessment:**
- ✅ Zero new infrastructure — pure CLI enhancement
- ✅ Agents already call `fs status` at session start (skill enforces this)
- ✅ Surfaces gaps without flooding the store
- ⚠️ Only works when agent *calls* `fs status` — which is at session start (good for cross-session) but not mid-session
- ❌ Doesn't prevent the gap, only reports it after the fact

**Verdict: Valuable for cross-session continuity (new session sees the gap). Implement.**

### Approach 3: `fs watch` / Session-Level Auto-Capture

**Mechanism:** A long-running `fs watch` process monitors `git reflog` / file system events and auto-creates knowledge events.

**Assessment:**
- ❌ Requires a daemon/background process — complex lifecycle management
- ❌ Git commits are activity, not decisions — auto-recording them floods the store with noise
- ❌ Cannot extract semantic meaning from diffs
- ❌ Violates the fs architecture (events should be intentional, not automatic)

**Verdict: Rejected. Automates the wrong thing — captures activity, not knowledge.**

### Approach 4: Playbook Checkpoint Enforcement

**Mechanism:** Add checkpoint steps to playbooks (especially captain) that verify events were recorded at key workflow transitions.

**What it could do:**
- Before marking a task "done," verify at least one decision exists
- Before delegating, verify the plan was recorded
- Captain collect step verifies knowledge was recorded before closing a worker

**Assessment:**
- ✅ Works within existing skill/playbook system
- ✅ Targets the *right moments* (task transitions, not every tool call)
- ⚠️ Playbook steps are advisory — agents can skip them
- ⚠️ Only enforced when playbooks are loaded and followed

**Verdict: Valuable as defense-in-depth. Combine with Stop hook for hard enforcement.**

---

## Recommended Design: Three-Layer Defense

```
Layer 1: fs status --check     (detect gaps on entry)
Layer 2: Stop hook             (enforce recording before exit)  
Layer 3: Playbook checkpoints  (verify at workflow transitions)
```

### Layer 1: `fs status` Gap Detection (CLI change)

Add a `warnings` field to `fs status` output that surfaces:

1. **Commits since last event:** Compare the timestamp of the latest `fs` event for the project against `git log --after=<timestamp>`. If there are commits not reflected in any event, warn.
2. **Active tasks with no knowledge:** Flag tasks in `active` status that have zero `knowledge-added` or `decision-recorded` events.
3. **Stale active tasks:** Flag tasks marked `active` whose last event is >24h old.

```json
{
  "ok": true,
  "data": {
    "project_id": "skills",
    "tasks": [...],
    "playbooks": [...],
    "warnings": [
      {
        "type": "unrecorded_commits",
        "message": "3 commits since last fs event (abc1234..def5678)",
        "task_ids": ["t-abc12345"],
        "details": {
          "commits": ["abc1234 fix: auth token refresh", "def5678 refactor: extract middleware"],
          "last_event_at": "2026-09-22T05:00:00Z"
        }
      },
      {
        "type": "bare_active_task",
        "message": "Active task t-xyz has no decisions or knowledge recorded",
        "task_ids": ["t-xyz"]
      },
      {
        "type": "stale_active_task", 
        "message": "Task t-old has been active for 2 days with no updates",
        "task_ids": ["t-old"]
      }
    ]
  }
}
```

**Implementation:** Add a `detectWarnings(projectID string) []Warning` method to `command.Handler`. Uses `git log --after=<timestamp> --oneline` (already have git helpers in `internal/command/git.go`) and replays events to check for bare tasks.

### Layer 2: Stop Hook Script

A shell script installed as a `Stop` hook in the agent's settings.json. When the agent tries to stop:

1. Detect the project from git root
2. Check if there are active tasks in fs
3. If active tasks exist and the last fs event is older than 5 minutes, block the stop with a reason asking the agent to record.

```json
{
  "hooks": {
    "Stop": [
      {
        "hooks": [
          {
            "type": "command",
            "command": "skills/flagship/scripts/fs-stop-hook.sh"
          }
        ]
      }
    ]
  }
}
```

The script reads stdin JSON, calls `fs status`, checks warnings, and outputs `{"decision": "block", "reason": "..."}` if recording is needed. On second consecutive block (`stop_hook_active` is true on stdin), it lets the agent through to avoid infinite loops.

### Layer 3: Playbook Checkpoint Enhancement

Add a `recording-checkpoint` convention playbook:

```yaml
name: recording-checkpoint
type: convention
trigger: dev-task, coordinator
description: >
  Before any task status transition (active→done, active→blocked),
  verify at least one decision or knowledge event exists for the task.
  
rules:
  - before-done: >
      Before marking a task done, run: fs log --node <id> --type decision-recorded
      and fs log --node <id> --type knowledge-added.
      If both return empty, record at minimum a decision summarizing the outcome.
  - before-delegate: >
      After planning subtasks, record the decomposition rationale as a decision
      on the parent task.
  - after-debug: >
      After resolving a bug or investigation, record what was learned as knowledge,
      even if it seems obvious. Future sessions don't have your context.
```

---

## Implementation Plan

### Phase 1: `fs status` Warnings (Go — implement now)

Changes:
- `internal/command/git.go` — add `GitLogAfter(timestamp string) ([]string, error)` 
- `internal/command/command.go` — add `Warning` type, `detectWarnings()`, include in `StatusResult`
- `cmd/main.go` — pass project root path to handler for git operations
- Tests for warning detection

### Phase 2: Stop Hook Script (Shell — implement now)

- `skills/flagship/scripts/fs-stop-hook.sh` — reads stdin, calls fs, decides block/pass
- Documentation in SKILL.md on how to install

### Phase 3: Convention Playbook (YAML — implement now)

- `~/.fs/kb/recording-checkpoint.yaml`

---

## What This Does NOT Do (and why)

- **Auto-record git commits as events:** Commits are activity, not decisions. The event store captures *why*, not *what*. Git already captures *what*.
- **Record every file edit:** Noise. An edit to `auth.go` doesn't tell you "chose PKCE over implicit grant."
- **Replace manual recording:** The goal is to *remind and enforce*, not to generate synthetic events. Human-quality knowledge requires the agent's judgment about what matters.

## Noise Budget

- `fs status` warnings: 0 new events (read-only analysis)
- Stop hook: 0 new events (just blocks the stop until agent records)
- Playbook convention: Agent records 1-2 events per task transition (meaningful, not noise)

Total new automated events: **0**. All new events come from the agent being *reminded* to record, which preserves the quality bar.
