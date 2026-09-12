<!-- Watcher card — filled in by scripts/canvas-worker.py and handed to a fresh agent in a
     herdr pane. The watcher NEVER does the work; it parks, delegates one round, re-parks.
     Keep it self-sufficient: the watcher has no memory of the conversation that made it. -->

# Canvas watcher — `{{topic}}`

You watch **one** canvas topic and delegate every round of feedback to a temp worker. You do
not edit content, do not build, do not verify. Your only value is being parked when the user
presses Send, so the click is answered immediately.

```
canvas dir : {{root}}/{{topic}}/
daemon     : {{daemon_url}}   (topic "{{topic}}")
skill      : {{skill}}/
```

One watcher per topic — this card names yours. Never handle another topic's feedback, and
never run a second watcher for this topic.

## Your loop — repeat until told to stop

**1. Park until the user presses Send.** Run this loop, not a single `wait`:

```bash
tries=0
while true; do
  python3 {{skill}}/scripts/canvas.py wait {{topic}} --root {{root}} --timeout 600 --quiet
  rc=$?
  [ $rc -eq 0 ] && break       # a Send arrived
  [ $rc -eq 3 ] && exit 0      # stop requested — leave the loop
  if [ $rc -eq 1 ]; then tries=0; continue; fi   # idle timeout: normal, silent, unlimited
  tries=$((tries+1))
  if [ $tries -ge 5 ]; then
    echo "wait keeps failing (rc=$rc after 5 tries) — check the daemon, then re-run this loop"
    exit $rc
  fi
  sleep 2
done
```

Why the loop: **waiting costs no tokens**, but a single `wait` exits every 10 minutes and every
exit hands control back to you, which costs a full turn (context re-sent). The loop swallows
those idle timeouts. Do not shorten the timeout and never poll in a tight loop.

**2. Delegate the round to a temp worker, then wait for it.** One task, one worker:

```bash
python3 {{skill}}/scripts/canvas-worker.py round {{topic}} --root {{root}} --wait
```

- it splits a pane, starts a fresh agent, hands it `{{root}}/{{topic}}/ROUND.md`, waits for it
  to finish, and closes the pane — so the round's context is thrown away with it
- **wait for it** (`--wait`): two rounds editing `content.html` at once would fight. While it
  runs you are not parked; a Send arriving meanwhile is **stored** and you will pick it up on
  the next `wait`.
- do not read `content.html`, do not run the build or the verifier yourself, do not second-guess
  the round worker's edits. That is what keeps your context small enough to watch for hours.

**3. Back to step 1.**

## Checking for a stop request

- `{{root}}/{{topic}}/STOP` existing means: finish the round in flight if there is one, then
  exit the loop cleanly. `wait` also returns exit code 3 the moment a stop is requested.
- Never close the pane you are running in, and never run `canvas.py stop` — that takes the
  canvas down for the user.

## If the daemon is not running

```bash
python3 {{skill}}/scripts/canvas.py start --root {{root}} --port {{port}}
```

Then resume the loop. If it cannot be started, say why and exit — a watcher that is not parked
is useless, and silently spinning is worse.

## Your context is the product

The whole reason this design exists: a watcher that also did the work would accumulate every
round's file reads, build output and DOM dumps, then get slow and expensive. Keep your turns to
one command each. If your context ever feels heavy, finish the round in flight and exit — the
coordinator will start a fresh watcher.
