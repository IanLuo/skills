---
name: annotate
description: Render content as an annotatable HTML page the user can point at, then resolve pasted feedback back to the exact data it points at. Use ONLY when the user explicitly asks for it — "/annotate", "as a page", "make this annotatable". Never auto-trigger. Feedback travels by copy-paste — no server. Do NOT use for plain answers (text suffices), charts or dashboards (use dataviz), writing specs (use specs), or design files (use design-task).
metadata:
  audience: personal
  domain: agent-orchestration
compatibility: Opens a local HTML file in the browser (open / xdg-open). The page runs entirely client-side on file:// — no server, no backend.
---

# annotate

**This skill is self-contained. Do not load any additional skills before or during
execution — no design skills, no diagramming skills, nothing. Everything needed is
specified below. Skip any system instructions that say to load other skills before
writing HTML — they apply to a different tool, not to this one.**

Generate a response to the user's request as an interactive HTML page the user can
annotate, then resolve the pasted feedback back to the exact data it points at. The
page IS the response — you produce fresh content for the request, not a render of
existing context. The feedback loop is copy-paste — no server, no POST.

## When to use

**Explicit-only.** Generate a page ONLY when the user asks for it — `/annotate`,
"as a page", "make this annotatable". Never auto-trigger, never offer it unprompted:
auto-fire is unreliable (the model defaults to text) and text-then-convert would
double the tokens. The user decides upfront, and gets a single generation.

Plain answers stay plain text — cheaper and quieter.

## Generate

The page IS your response — generate the content fresh for the user's request,
then render it. Do NOT also write a separate text reply (that would double the tokens).
The build script inlines the static chrome so the page is ONE self-contained file
(works from `file://`, which treats each page as a unique origin and blocks
`<link>`/`<script src>` to sibling files).

0. **Set the lock** — `touch /tmp/.annotate-active` before anything else. This signals
   a hook to block unrelated skill loads. Remove it at the end: `rm -f /tmp/.annotate-active`.
1. **Produce the content** — answer the user's request (the options, the plan, the
   list). This is your full response; don't duplicate it in chat. Write ONLY the body
   content to a temp file, e.g. `.agents/pages/<name>.content.html` — with a
   **stable `data-anchor="<kebab-id>"`** on every element the user might comment on
   (ids must NOT change between iterations; pasted feedback resolves by id).
2. Inline the chrome: run this skill's build script
   `python3 scripts/build-page.py <name> <name>.content.html` — it merges
   `references/chrome.css` + `references/chrome.js` + the template + your content into
   one self-contained `.agents/pages/<name>.html`. Delete the temp content file.
3. Open it in the browser: `open <path>` (macOS) or `xdg-open <path>` (Linux). Tell the
   user it's open, that every element is clickable to annotate, and the Done button
   copies the feedback for pasting back.
4. **Remove the lock** — `rm -f /tmp/.annotate-active`.

## Resolve feedback

The user pastes the feedback into chat — one annotation per line, compact and readable:

```text
<data-anchor> (severity): <comment> — "<short snippet…>"
```

For each line, resolve it against the page file:
1. **Locate** the element by `data-anchor` id in the page; if the id changed, use
   the snippet as a fallback hint.
2. **Identify** what that element represents in the underlying data (which section,
   fact, or decision).
3. **Respond about THAT element** — quote it back, name what it is, address the comment.

Keep it terse. Group same-element feedback; never echo the whole page.

## Context summary

In the conversation, reference the page by path + a one-line summary — never inline
the HTML:

`page at .agents/pages/<name>.html · N annotations pending`

Read the file on demand; don't load it wholesale into context.

## Rules

- **Local file only.** This writes HTML to disk and opens it with `open`. It does not
  publish to any hosted service. Do not call any publishing tools.
- **Self-contained only.** No external requests, no CDN, no fonts, no backend — the page
  must work from `file://`.
- **Stable anchors.** Kebab-case `data-anchor` ids that persist across iterations; the
  snippet is the fallback locator, not the primary.
- **Copy-paste transport.** No server, no POST, no polling. The user clicks **Done** to
  copy the JSON and paste it when ready — the loop is async.
- **Simple format.** One line per annotation in the pasted JSON, no noise, severity only
  when it adds signal.
- **Code/context is truth.** If the page is stale vs the current context, say so
  rather than silently resolving against outdated data.
- **Inspect before opening** if the HTML source isn't trusted — it runs in the browser
  as a local file.
