---
name: annotate
description: Render content as an annotatable HTML page the user can point at, then resolve pasted feedback back to the exact data it points at. Use ONLY when the user explicitly asks for it — "/annotate", "as a page", "make this annotatable". Never auto-trigger. Feedback travels by copy-paste — no server. Do NOT use for plain answers (text suffices); for a response the user must understand in depth across rounds (that is canvas — annotate answers a question, canvas explains a response); for charts or dashboards (dataviz); for writing specs (specs); or for design files (design-task).
metadata:
  audience: personal
  domain: agent-orchestration
compatibility: Opens a local HTML file in the browser (open / xdg-open). The page runs entirely client-side on file:// — no server, no backend.
---

# annotate

Generate a response to the user's request as an interactive HTML page the user can
annotate, then resolve the pasted feedback back to the exact data it points at. The
artifact IS the response — you produce fresh content for the request, not a render of
existing context. The feedback loop is copy-paste — no server, no POST.

## When to use

**Explicit-only.** Generate an artifact ONLY when the user asks for it — `/annotate`,
"as a page", "make this annotatable". Never auto-trigger, never offer it unprompted:
auto-fire is unreliable (the model defaults to text) and text-then-convert would
double the tokens. The user decides upfront, and gets a single generation.

Plain answers stay plain text — cheaper and quieter.

## Generate

The artifact IS your response — generate the content fresh for the user's request,
then render it. Do NOT also write a separate text reply (that would double the tokens).
The build script inlines the static chrome so the artifact is ONE self-contained file
(works from `file://`, which treats each page as a unique origin and blocks
`<link>`/`<script src>` to sibling files).

1. **Produce the content** — answer the user's request (the options, the plan, the
   list). This is your full response; don't duplicate it in chat. Write ONLY the body
   content to a temp file, e.g. `.agents/artifacts/<name>.content.html` — with a
   **stable `data-anchor="<kebab-id>"`** on every element the user might comment on
   (ids must NOT change between iterations; pasted feedback resolves by id).
2. Inline the chrome: run this skill's build script
   `python3 scripts/build-artifact.py <name> <name>.content.html` — it merges
   `references/chrome.css` + `references/chrome.js` + the template + your content into
   one self-contained `.agents/artifacts/<name>.html`. Delete the temp content file.
3. Open it in the browser: `open <path>` (macOS) or `xdg-open <path>` (Linux). Tell the
   user it's open, that every element is clickable to annotate, and the Done button
   copies the feedback for pasting back.

## Answering a previous round

When the artifact answers notes from an earlier round, the page carries two authored
voices and must say which is which. **Quote each note verbatim and pair it with your
answer as one thread** — never paraphrase a note into prose, because then the reader
cannot tell their words from yours. That, and a note quietly vanished, is the failure
this vocabulary exists to prevent.

```html
<div class="ev user" data-thread="r1e" data-anchor="r1e">
  <span class="who">you</span><div class="txt">edit or create?</div></div>
<div class="ev agent" data-thread="r1e" data-anchor="r1e-answer" data-status="new">
  <span class="who">me</span><div class="txt">Both — the lease is on the topic…</div></div>
```

`data-thread` is the note's original anchor id. The user row keeps it as `data-anchor`
so note ids survive every round; the answer row takes `data-anchor` too, so the next
round can annotate the answer. Both rows are required. The vocabulary is canvas's —
`.ev`, `.who`, `.txt`, `.secpill`, `data-status` — so a reader's habit carries across
both surfaces; don't invent parallel names.

Then mark what is fresh, or the reader sees one undifferentiated wall:

- `data-status="new" | "changed" | "settled" | "open"` per element (a row with none is
  settled). On a `.ev` row the state shows beside the voice label; elsewhere it renders
  as a pill.
- the round's delta in the opening `.meta-row`, e.g. `<b>3</b> new · <b>1</b> changed`.
- `<span class="secpill new">3 new</span>` on the round heading.

Build with `--prev` and the script fails on a note that was neither carried forward nor
retired — so loss is loud, not silent. A finished thread is retired explicitly by id:
`data-retired="r1a,r1b"` on the round wrapper.

```bash
python3 scripts/build-artifact.py <name> <name>.content.html \
  --prev .agents/artifacts/<previous-round>.html
```

## Resolve feedback

The user pastes the feedback into chat — one annotation per line, compact and readable:

```text
<data-anchor> (severity): <comment> — "<short snippet…>"
```

For each line, resolve it against the artifact file:
1. **Locate** the element by `data-anchor` id in the artifact; if the id changed, use
   the snippet as a fallback hint.
2. **Identify** what that element represents in the underlying data (which section,
   fact, or decision).
3. **Respond about THAT element** — quote it back, name what it is, address the comment.

Keep it terse. Group same-element feedback; never echo the whole artifact.

## Context summary

In the conversation, reference the artifact by path + a one-line summary — never inline
the HTML:

`artifact at .agents/artifacts/<name>.html · N annotations pending`

Read the file on demand; don't load it wholesale into context.

## Rules

- **Self-contained only.** No external requests, no CDN, no fonts, no backend — the page
  must work from `file://`.
- **Stable anchors.** Kebab-case `data-anchor` ids that persist across iterations; the
  snippet is the fallback locator, not the primary.
- **Copy-paste transport.** No server, no POST, no polling. The user clicks **Done** to
  copy the JSON and paste it when ready — the loop is async.
- **Two voices, paired.** A round that answers notes quotes them verbatim in a
  `.ev.user` row and pairs each with its `.ev.agent` answer by `data-thread`. A note is
  carried forward or explicitly retired — never dropped, never paraphrased.
- **Simple format.** One line per annotation in the pasted JSON, no noise, severity only
  when it adds signal.
- **Code/context is truth.** If the artifact is stale vs the current context, say so
  rather than silently resolving against outdated data.
- **Inspect before opening** if the HTML source isn't trusted — it runs in the browser
  as a local file.
