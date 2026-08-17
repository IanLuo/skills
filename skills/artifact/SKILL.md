---
name: artifact
description: Render a long AI response that asks for the user's input as an interactive HTML page the user can annotate, then resolve pasted feedback back to the exact data it points at. Use ONLY when the response solicits user input AND is long enough to need annotation, OR when the user explicitly asks for it. Feedback travels by copy-paste — no server. Do NOT use for short answers or quick reads (plain text suffices), charts or dashboards (use dataviz), specs (use specs), or design files (use design-task).
metadata:
  audience: personal
  domain: agent-orchestration
compatibility: Opens a local HTML file in the browser (open / xdg-open). The page runs entirely client-side on file:// — no server, no backend.
---

# artifact

Render what's in context (or the AI's direct response) as an interactive HTML page the
user can annotate, then resolve the feedback back to the exact data it points at. The
feedback loop is copy-paste — no server, no POST.

## When to use

Generate an artifact ONLY when:

- **The response asks for the user's input** (a decision, feedback, a choice) **AND the
  text is long** enough that pointing at the exact spot beats re-stating it, OR
- **The user explicitly asks** for an artifact.

Otherwise — short answers, quick reads, or responses that don't solicit input — reply
in plain text. The artifact's whole value is *pointing at the exact place*; if there's
nothing to point at or it's short, plain text is cheaper and quieter.

## Generate

1. Take the data from the current context or the AI's response. Render it as
   `artifact.html` in the project's agent-data dir: `.agents/artifacts/<name>.html`.
   The file is the CONTENT + the small chrome markup — it does NOT inline the CSS/JS.
2. **Copy the static chrome once per project** (if not already there):
   `cp` `chrome.css` + `chrome.js` from this skill's `references/` into
   `.agents/artifacts/`. They are shared by every artifact in the project — never
   regenerate them.
3. Write `artifact.html` following
   [references/artifact-template.html](references/artifact-template.html):
   - `<link rel="stylesheet" href="chrome.css">` in the head, `<script src="chrome.js">`
     at the end (relative links work on `file://`).
   - The chrome markup verbatim (Done button, `raw`, `status`, the `#form` with the
     three level buttons).
   - Your content in place of the placeholder — with a **stable
     `data-anchor="<kebab-id>"`** on every element the user might comment on. Anchor
     ids must NOT change between iterations; the pasted feedback resolves by id.
4. Open it in the browser: `open <path>` (macOS) or `xdg-open <path>` (Linux). Tell the
   user it's open, that every element is clickable to annotate, and the Done button
   copies the feedback for pasting back.

## Resolve feedback

The user pastes the feedback into chat — one annotation per line, compact and readable:

```text
<data-anchor> (severity): <comment> — "<short snippet…>"
```

(A "raw" link on the page copies the JSON form instead; same fields.)

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
- **Simple format.** One line per annotation in the pasted JSON, no noise, severity only
  when it adds signal.
- **Code/context is truth.** If the artifact is stale vs the current context, say so
  rather than silently resolving against outdated data.
- **Inspect before opening** if the HTML source isn't trusted — it runs in the browser
  as a local file.
