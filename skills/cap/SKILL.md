---
name: cap
description: Use when the user asks what is next on the fs board, wants a task dispatched, asks to run cap, or asks whether the watcher is running. cap is the supervised watcher over fs status, with tick, watch, act, status, start, stop and logs. This skill fixes how to present cap to the user - short, plain, one line per task, a diagram only when structure helps, never a raw command or trace dump. Do NOT use for the flagship coordinator role (that is flagship) or for pane work (that is herdr).
metadata:
  audience: personal
  domain: dev
---

# cap

## Job

cap watches the fs board and recommends work. The user names a task; cap
launches one command from knowledge and records one `cap.act`. Nothing
launches on the 30 s clock.

## Run it with JSON

Call the CLI, read the JSON, then summarize.

```bash
cap status --json            # running, pid, last tick, last error
cap tick --json              # changes + ranked recommendations
cap act <task> --json        # one named instruction
cap logs --tail 20 --json    # the trace, for diagnosis
```

Never run `cap watch` from an agent: launchd supervises it.

## How to answer

Lead with the answer. Then at most five lines.

- **Unchanged board:** "No board change. Nothing to act on." Stop there.
- **Changed board:** the new task or tasks first, then the top recommendation.
- **One line per task:** id, project, kind, why it ranks, and whether a
  command exists.
- **No raw JSON, no trace lines, no command text.** The full command is in
  `--json` and in the `cap.act` record. Name it only when the user asks.
- **A diagram only when the rank or the act flow is the point.** Keep it to
  six lines. Text is fine for everything else.

Rank, when it is the point:

```text
unheld review  t-abc  balcony  first
unheld dev     t-def  cap      older
```

What one act did, when asked:

```text
cap act t-abc
  fs status (one read)
  knowledge 5533 -> herdr
  cap.act 5538: confirmed by claude-e1258e7a
```

## Safety

- Act only on the task the user names. "What's next" is not an instruction:
  name the top pick and ask.
- One act per task per 90 s. A refusal names the earlier launch. Report it;
  do not retry.
- cap never writes fs and never claims. Do not claim on its behalf.
- Do not edit knowledge from this skill. Knowledge is the user's.

## Failures

- `running:false`: offer `cap start`.
- `last_error`: quote it in one line, then stop.
- `stale:true`: the process is alive but not ticking; offer
  `cap stop && cap start`.

## Example

The user asks "what's next".

You run `cap tick --json`. It shows one new dev task and ranks an unheld
review first.

Reply:

> One new dev task. Top pick is `t-abc` (balcony review): unheld reviews rank
> before dev. No review command exists yet, so cap can only recommend it.
> Want me to act on it, or on the new dev task?
