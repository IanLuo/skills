# Task: make `--parent` trustworthy and orphans visible (batch member A)

You are a worker in your own git worktree. Your batch has other members working in
parallel — touch only the files listed below. Do not spawn agents.

## Files

```
src/flagship/internal/query/query.go
src/flagship/internal/query/query_test.go
src/flagship/internal/command/command.go
src/flagship/internal/command/command_test.go
```

## Why

Two related silences, both of which have already cost real data tonight:

1. `fs task add --goal X --parent <typo>` **succeeds**. At replay the link pass finds no
   parent (`if parent, ok := tree.Nodes[node.ParentID]; ok`), so the node is neither a root
   nor a child: it never appears in `fs status`, while its events sit in the log forever.
   Events are immutable, so the mistake cannot be corrected.
2. `fs status` walks `tree.Roots` only, so anything unreachable is invisible — while
   `fs unfinished` reads the store directly and *does* list it. Two views of one store that
   disagree about what exists.

## Build

- **Reject an unknown parent at write time.** In `TaskAdd`, when a parent is given, confirm a
  node with that id exists in the project. Refuse with `ok:false`, exit 1, naming the parent
  and the project. Write no event.
- **Surface orphans in `fs status`.** A node whose parent id is not present should appear,
  marked as an orphan (e.g. `"orphan": true` on the row), so pre-existing ones become visible
  and closable. Keep the existing row shape otherwise.
- Tests: unknown parent refused and nothing written; an orphan — constructed by appending a
  `task-created` with a nonexistent parent straight through the store — appears in `status`
  and is marked.

## Acceptance check

1. `./bin/build-project.sh flagship` succeeds; `go test ./...` passes.
2. `fs task add --goal X --parent t-nonexistent --project P` → `ok:false`, exit 1, names the
   parent, **and the scope's event count is unchanged** (assert it).
3. An orphan node appears in `fs status --project P` with the orphan marker.
4. `fs status` and `fs unfinished` agree about which nodes exist.

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
