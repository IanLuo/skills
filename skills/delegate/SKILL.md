---
name: delegate
description: Move self-contained or noisy work out of the main session into an in-process subagent (Claude Code Agent tool) to keep the parent context lean. Use for "delegate this to a subagent", "spawn a subagent", "run this in the background", "fan out", "parallel subtasks", or any task that would pollute the main context with intermediates you won't re-reference. Do NOT use for external agents in panes (use herdr), trivial 1-3-tool-call work (do inline), or wide deterministic sweeps (use the Workflow tool).
metadata:
  audience: personal
  domain: agent-orchestration
compatibility: Requires a harness with an in-process subagent tool (Claude Code Agent tool). No other deps.
---

# delegate

Move self-contained or noisy work into an in-process subagent so the main context
stays lean. Final text IS the result; files are escalation only.

## When to delegate (the gate)

Delegate only when ALL THREE hold — otherwise do the work inline:

| condition | meaning |
|---|---|
| **self-contained** | no mid-run back-and-forth needed; the task can be specified up front |
| **verifiable without redoing** | the result can be checked without repeating the work |
| **noisy** | high output-to-conclusion ratio — test logs, build output, large diffs, broad searches, audits |

Also delegate when: subtasks are parallelizable (fan-out), the work would pollute
context with intermediates you won't re-reference, or the work is long-running.

Do NOT delegate: trivial <4-tool-call work (the spawn tax exceeds the benefit),
or serial dependent work where each step reads the previous — pipelines live in
the parent.

## Workflow

1. **Run the gate.** If any of the three conditions fails, do it inline and stop.
2. **Spawn.** `Agent` tool, one self-contained prompt stating the exact deliverable.
   End the prompt with a status-marker instruction:
   `End your reply with ## Status: complete | blocked | partial.`
   - Fan-out: 2–5 subagents (hard cap 8).
   - Tell leaf agents: **never spawn your own subagents.**
3. **Run.** Sync (`run_in_background: false`) for fast tasks; async (default) for
   long ones. Never hard-timeout a subagent mid-mutation — bound only the parent's wait.
4. **Read the final text.** That IS the result. No files by default.
5. **Verify.** Check the status marker:
   - `complete` → use the result.
   - `blocked` → read the subagent's transcript, diagnose, retry once with more
     context, or escalate to the user.
   - `partial` → use what's valid, report the gap.
   Never blind-retry or fabricate a subagent result.

## File escalation (only 3 cases)

Escalate from final text to a file ONLY when:

1. **Result >10K tokens.**
2. **Cross-agent coordination** — another agent must consume the output.
3. **Cross-session persistence** — the work must survive a context reset / handoff.

For noisy work, ask the subagent to `return only the conclusion` (<100 chars) and
write details to a file you read on demand. Use worktree isolation
(`isolation: "worktree"`) only for parallel write-collision risk — overkill for
read-only or research work.

## Rules

1. Final text IS the result. Files are escalation, never the default.
2. Gate first — fail it, do the work inline.
3. Trivial tasks stay inline; the spawn tax exceeds the benefit.
4. One-shot ephemeral subagents only. No warm persistent specialists (context grows
   monotonically).
5. Fan-out 2–5, hard cap 8. Leaf agents never spawn subagents (recursion = token burn).
6. Every result ends with a status marker: `complete | blocked | partial`.
7. Never blind-retry or fabricate a subagent result — diagnose from its transcript,
   then retry once with more context or report the gap.
8. Long work spawns async; a wait timeout stops waiting, not the child.
9. Serial dependent work stays in the parent — delegate parallelizable units only.
10. Scope: in-process subagents. External panes → herdr. Wide deterministic sweeps →
    the Workflow tool. Both are out of scope here.
