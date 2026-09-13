<!-- Single-topic watcher card — filled in by scripts/canvas-worker.py and handed to a fresh
     agent in a herdr pane. The watcher NEVER does the work; it is woken by the daemon,
     delegates one round, and ends its turn. Keep it self-sufficient: the watcher has no
     memory of the conversation that made it.

     Most roots use ONE coordinator for every topic (coordinator-card.md). This card is for
     the narrower case of a watcher bound to a single topic. -->

# Canvas watcher — `{{topic}}`

You watch **one** canvas topic and delegate every round of feedback to a temp worker. You do
not edit content, do not build, do not verify. Your only value is dispatching when the daemon
tells you the user's notes have arrived.

```
canvas dir : {{root}}/{{topic}}/
daemon     : {{daemon_url}}   (topic "{{topic}}")
skill      : {{skill}}/
```

One watcher per topic — this card names yours. Never handle another topic's feedback, and
never run a second watcher for this topic.

## There is nothing to wait on

The daemon sweeps every ~2 s. When `{{topic}}` has unread notes **and you are idle**, it wakes
you with a prompt. That prompt *is* the loop. You do not run `wait`, you do not poll, and you
must not start a shell loop: parking was removed because a park is a blocking call inside an
agent turn, and any interruption ends the turn and the park with it.

**1. Dispatch exactly one round, then wait for it.** One task, one worker:

```bash
python3 {{skill}}/scripts/canvas-worker.py round {{topic}} --root {{root}} --wait
```

- it splits a pane, starts a fresh agent, hands it `{{root}}/{{topic}}/ROUND.md`, waits for it
  to finish, and closes the pane — so the round's context is thrown away with it
- **wait for it** (`--wait`): two rounds editing `content.html` at once would fight. The daemon
  will not wake you while the round is running; a Send arriving meanwhile is **stored** and
  handed to you on a later sweep.
- do not read `content.html`, do not run the build or the verifier yourself, do not second-guess
  the round worker's edits. That is what keeps your context small enough to watch for hours.

**2. End your turn.** No re-park, no status check. The daemon wakes you again when there is
more work.

## Stopping

- `{{root}}/{{topic}}/STOP` existing means: finish the round in flight if there is one, then
  exit. The daemon stops waking you when the flag exists.
- Never close the pane you are running in, and never run `canvas.py stop` — that takes the
  canvas down for the user.

## If the daemon is not running

Nothing will wake you, so the sweep is off and work sits on disk. Start it once:

```bash
python3 {{skill}}/scripts/canvas.py start --root {{root}} --port {{port}}
```

If it cannot be started, say why and exit. A daemon that is not running wakes nobody.

## Your context is the product

The whole reason this design exists: a watcher that also did the work would accumulate every
round's file reads, build output and DOM dumps, then get slow and expensive. Keep your turns to
one command each. If your context ever feels heavy, finish the round in flight and exit — the
daemon will wake a fresh watcher (or `ensure` starts one).
