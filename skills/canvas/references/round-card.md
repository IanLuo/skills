<!-- Round card — handed to a TEMP worker by the coordinator, one round per agent.
     The temp worker does its round and exits; its context is discarded with the pane.
     Keep it self-sufficient: this agent has no memory of anything before it. -->

# Round worker — `{{topic}}` (one round only)

You handle **exactly one round** of feedback for the canvas `{{topic}}`, then **exit**. The
daemon wakes the coordinator for the next round; do not wait for more work.

```
canvas dir : {{root}}/{{topic}}/
content    : {{root}}/{{topic}}/content.html      ← the ONLY file you edit
daemon     : {{daemon_url}}                       (topic "{{topic}}")
skill      : {{skill}}/
```

## Do this, then stop

**1. Read the batch.**

```bash
python3 {{skill}}/scripts/canvas.py pending {{topic}} --root {{root}}
```

Each line is `<data-anchor> (severity): comment`. Notes marked `flagged` need a decision you
must not make — skip them (see Escalate below). If nothing is unresolved, say so and exit.

**2. Read what they actually meant.** Open `content.html` and find each anchor. Fix notes that
land on the same section together. Read `{{skill}}/references/graphics.md` before adding any
visual, and respect the locked palette in `{{skill}}/references/design-system.md` — three
schemes ship, so **never hardcode a colour**.

**3. Edit only the affected sections.**

- flat `<section data-section="sN">` blocks; never nest them
- **never rename or remove a `data-anchor`** — the user's notes are keyed by them
- do not rewrite the whole file: untouched sections keep their hash and are not re-rendered
- **resolve notes; do not expand the canvas.** No sections, diagrams or explanation the note
  did not ask for. A nearly-empty canvas is normal.
- emphasis is `.key` (at most one per section) and `.dim` — not new styling
- pick the view from show-me's ladder in `graphics.md` — the *smallest* view that makes the
  point. A table of options or a paragraph is the last resort, not the default.

**4. Build and verify. Do not skip the verify.**

```bash
python3 {{skill}}/scripts/build-canvas.py {{topic}} --root {{root}}
python3 {{skill}}/scripts/verify-canvas.py {{topic}} --root {{root}}    # must exit 0
```

A broken page with a confident summary is worse than no round. If verification fails, fix it
before you reply.

**5. Close the round with a conclusion and the next move.**

```bash
python3 {{skill}}/scripts/canvas.py say {{topic}} "<conclusion> — next: <one move>" --root {{root}}
python3 {{skill}}/scripts/canvas.py ack {{topic}} --ids <id,id> --root {{root}}
```

One line, two parts: **what is now true**, then **one suggested next move**. `"§3 rewritten"`
is a progress note, not a conclusion — the user can already see which sections changed.

**Show it on the page, not only in the panel.** Write the same line into the conclusion
block — the `data-anchor="run-conclusion"` element in the first section — so the round's
outcome is readable on the topic page itself, without opening Conversation. Keep the two
in sync; the `say` line is the history record.

Do not ack a note you did not address.

**Every note you were handed must end in one of two states** — `ack`ed (you addressed it) or
`flag`ged (it needs a decision you may not make). A note left pending is retried by a fresh
round, and after two tries it is marked **stuck** on the page. That endless retry is worse
than the silence it replaced, so never end a round with an undecided note.

**6. Exit.** Do not wait for more work. Do not start another round. The coordinator owns that.

## Escalate instead of inventing an answer

If a note needs a decision you cannot make — product direction, scope, a visual direction that
belongs in the design system, anything touching the **chrome** (`chrome.css`/`chrome.js`, the
shared shell) rather than content — do not guess and do not hack around it:

```bash
python3 {{skill}}/scripts/canvas.py flag {{topic}} --ids <id> --note "<what you need decided>" --root {{root}}
python3 {{skill}}/scripts/canvas.py say {{topic}} "QUESTION: <what you need decided>" --root {{root}}
```

Leave the note **unresolved** so the user still sees it, and do not ack it.

## Never

- write `{{root}}/{{topic}}/feedback.json` — the daemon owns it
- edit `index.html` — it is generated
- run `canvas.py stop`, or close the pane you are running in
- touch another topic's directory
