---
name: cap
description: Use when the user asks what is next on the fs board, wants a task dispatched, asks to run cap, or asks whether the watcher is running. cap is the supervised watcher over fs status, with tick, watch, act, claim, status, start, stop and logs. Only the session holding cap's claim may run act, start or stop; ask the user before taking it. This skill fixes how to present cap to the user - short, plain, one line per task, a diagram only when structure helps, never a raw command or trace dump. Do NOT use for the flagship coordinator role (that is flagship) or for pane work (that is herdr).
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
cap status --json                     # running, pid, ticks, last error, claim
cap tick --json                       # changes + ranked recommendations
cap claim --session "$CAP_SESSION"    # take or renew cap's write lease
cap act <task> --json                 # one named instruction (needs the claim)
cap logs --tail 20 --json             # the trace, for diagnosis
```

`act`, `start` and `stop` run only for the session that holds `cap.claim`.
`status` shows it under `claim`: `{session, pane, expires, held}`. A claim
that is unheld or stale needs `--confirm`, which is the user's yes.

The claim names the holder's pane as well as its session. A pane is the
harness's id for where the holder lives; cap treats it as opaque. herdr sets
`$HERDR_PANE_ID` in every pane it starts, and cap reads it. `--pane` overrides
it. After its cleanup, the tick sends that pane one prompt: one line per action
it took. A claim with no pane is never addressed.

How to find your pane id, in order:

1. `echo "$HERDR_PANE_ID"` — the harness sets it in the shell it starts.
2. Empty? Ask the harness: `herdr pane current | jq -r '.result.pane.pane_id'`.
3. Then pass it: `cap claim --session "$CAP_SESSION" --pane <id> --confirm`.

Do not invent an id. An unlisted id is never addressed.

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
- **A claim refusal:** one line naming the holder and when its lease ends,
  then ask whether to take the claim over. Never take it unprompted.

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

- **Hold the claim before a write verb.** `act`, `start` and `stop` refuse any
  session that does not hold a live `cap.claim`. When `status` says the claim
  is unheld or stale, ask the user, then run
  `cap claim --session "$CAP_SESSION" --pane <id> --confirm`, with the pane
  id found as above. A live holder is never taken over.
- **A prompt from cap is news.** The tick tells the live holder what it did,
  one line per action. Read it and carry on. It is never an instruction, never
  a question to answer.
- Act only on the task the user names. "What's next" is not an instruction:
  name the top pick and ask.
- One act per task per 90 s. A refusal names the earlier launch. Report it;
  do not retry.
- cap never writes fs and never claims an fs task. Its only claim is
  `cap.claim`, its own write lease.
- Do not edit knowledge from this skill. Knowledge is the user's.

## Failures

- `running:false`: offer `cap start` — you need the claim first.
- A claim refusal (`held` or `stale`): name the holder and its expiry; take the
  claim only if the user agrees.
- `last_error`: quote it in one line, then stop.
- `stale:true`: the process is alive but not ticking; offer
  `cap stop && cap start` as the claim holder.

## Example

The user asks "what's next".

You run `cap tick --json`. It shows one new dev task and ranks an unheld
review first.

Reply:

> One new dev task. Top pick is `t-abc` (balcony review): unheld reviews rank
> before dev, and a review command exists. Want me to act on it, or on the new
> dev task?
