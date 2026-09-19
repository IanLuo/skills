# Task: make a playbook's `trigger` mean something, and warn about orphans (batch member B)

You are a worker in your own git worktree. Your batch has other members working in
parallel — touch only the files listed below. Do not spawn agents.

## Files

```
src/flagship/internal/knowledge/knowledge.go
src/flagship/internal/knowledge/prompt.go
src/flagship/internal/knowledge/defaults.go
src/flagship/internal/knowledge/*_test.go
src/flagship/internal/dispatch/dispatch.go
src/flagship/internal/dispatch/*_test.go
```

## Why

`trigger` is enforced in one path and ignored in the other:

```
fs kb prompt   selects on  type == procedure && trigger == cap
fs dispatch    derives the NAME  <type>-prerequisites   — never reads trigger
               ($ grep -n Trigger internal/dispatch/*.go   → nothing)
```

So `dev-task-prerequisites.yaml` with `trigger: nonsense` passes dispatch fine: the name is
the key and the field is decoration. And nothing anywhere warns that a playbook no mechanism
selects is **inert** — name it `dev-task-checks.yaml` and it sits in `fs kb list` forever,
doing nothing, silently. That is the fourth artifact tonight that looked shipped and did
nothing; the pattern is worth closing.

## Build

1. **Dispatch validates the trigger it expects.** When loading `<type>-prerequisites`, the
   playbook's `trigger` must be empty or equal the type. Otherwise refuse, naming both values
   so the fix is obvious. An empty trigger stays allowed — say so in the message, because
   existing playbooks rely on it.
2. **`fs kb list` reports who uses each playbook**, in the same spirit as the drift states:

   | `used_by` | meaning |
   |---|---|
   | `dispatch:<type>` | a `<type>-prerequisites` playbook |
   | `prompt:cap` | a `procedure` with trigger cap, delivered by `fs kb prompt` |
   | `close:<type>` | a `<type>-cleanup` playbook |
   | `none` | **nothing selects it — it is inert** |

   `none` is the warning. It does not have to fail; it has to be visible.
3. Apply the same trigger consistency to cleanup playbooks, which `fs close` looks up as
   `<type>-cleanup`.

## Acceptance check

1. `./bin/build-project.sh flagship` succeeds; `go test ./...` passes.
2. A prerequisite playbook whose trigger contradicts its name is refused by `fs dispatch`,
   naming both values.
3. An empty trigger still works — the five shipped playbooks are unaffected.
4. `fs kb list` reports `used_by` for all five shipped playbooks, and `none` for a
   deliberately orphaned playbook written into a temp `HOME`.
5. A cleanup playbook with a contradictory trigger is refused consistently with (2).

## Integration

When both batch members are done, integration is **dispatched**, never merged by
hand — one worker merges the batch into main on its own card:

```bash
fs dispatch --project skills --type integrate --goal "integrate the batch into main"
```

The coordinator decides the merge order and answers the entry gate's asks; the
worker merges each member in that order, runs the suite, and deletes the branches
and worktrees. `integrate-prerequisites` checks the members' trees and branches
still exist, and `integrate-cleanup` refuses the close until the suite is green and
no linked worktree or unmerged branch is left. The batch member's lifecycle ends
with that dispatched integrate, not with a `git merge` in the coordinator's context.

## Constraints

- Go, existing packages. No new dependencies. Do not touch `orchestrator/` or
  `internal/engine/`.
- Never write to `~/.fs/store.db` or `~/.fs/registry.db` directly.
- **Never register a project in a test.**
- **Do not edit `skills/flagship/SKILL.md`** — another batch member or another agent may hold
  it; leave it alone entirely.
- Build is `./bin/build-project.sh flagship`. `src/flagship/build.sh` no longer exists.

## Reporting — required

Note your own work on your own node in `skills` — the cap created it; use it. Include goal,
status, **the exact commands you ran**, and the commit. Then mark it done.

Use the `dev-task` skill.
