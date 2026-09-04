---
name: handoff
description: Carry a task across a context break. When you need to clear/compact context or wrap a task but will resume it, write a transient handoff document that a fresh session reads to restore. Invoke deliberately — never for mid-session notes, never a permanent record. After restore, the document is deleted — no chain, no archive.
metadata:
  audience: personal
  domain: general
---

# handoff

Carry a task across a context break. You invoke it deliberately when you need to clear
context (`/compact`, wrapping up, switching sessions) but the task will resume. It
writes ONE transient handoff document; a fresh session restores from it; then it's
deleted. Not a permanent record — no chain, no archive, no SESSION log.

## When to use

- Before `/compact` or clearing context on a task you'll resume
- Wrapping a big task midway
- Switching sessions or agents mid-task

## Write

1. Verify repo state: `git status --short`, `git log --oneline -5`.
2. Capture ONLY what a fresh session can't derive from code — terse, facts-only,
   following the house doc format (`skill-man/references/doc-format.md`):
   - **Goal** — one line
   - **Done** — concrete outcomes, `[V]` verified against the repo / `[?]` from memory
   - **Dead ends** — don't repeat these (name the exact failure)
   - **Decisions** — chose X because Y; rejected Z
   - **In progress** — `file:line`, current state, what's unfinished
   - **Next step** — ONE concrete, immediately actionable action
   - **Key files** — paths to re-read, max ~5
3. Write to a transient location, e.g. `<project>/.agents/context/handoff.md`.
4. Then clear/compact.

## Restore

1. A fresh session reads the handoff doc — it exists only because a context break was
   pending.
2. Re-verify `[?]` claims against the current repo, don't do any action, if there's pending action, ask user.
3. After the task is resumed, **delete the doc** — it has served its purpose.

## Rules

- **On-demand only.** Never write a handoff unless you're clearing context on a task
  that will resume. Not for mid-session progress notes or general documentation.
- **Transient.** The doc exists for the context break, then is deleted. No chain, no
  archive, no persistent record.
- **Facts, not state.** The repo + git is the live state; the handoff carries only
  what code can't — intent, decisions, dead ends, next step.
