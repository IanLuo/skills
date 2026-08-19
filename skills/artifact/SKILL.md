---
name: artifact
description: Render a decision-heavy response — options to choose between, a plan to review, a list the user must react to — as an annotatable HTML page they can point at, then resolve pasted feedback back to the exact data it points at. Reach for this whenever the user needs to give feedback or make a choice on concrete content, or when the user explicitly asks for it. Feedback travels by copy-paste — no server. Do NOT use for single-answer responses (plain text suffices), charts or dashboards (use dataviz), writing specs (use specs), or design files (use design-task).
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

The AI writes ONLY the content; a build script inlines the static chrome so the
artifact is ONE self-contained file (works from `file://`, which treats each page as a
unique origin and blocks `<link>`/`<script src>` to sibling files).

1. Take the data from the current context or the AI's response. Write ONLY the body
   content to a temp file, e.g. `.agents/artifacts/<name>.content.html` — the data with
   a **stable `data-anchor="<kebab-id>"`** on every element the user might comment on
   (ids must NOT change between iterations; pasted feedback resolves by id).
2. Inline the chrome: run this skill's build script
   `python3 scripts/build-artifact.py <name> <name>.content.html` — it merges
   `references/chrome.css` + `references/chrome.js` + the template + your content into
   one self-contained `.agents/artifacts/<name>.html`. Delete the temp content file.
3. Open it in the browser: `open <path>` (macOS) or `xdg-open <path>` (Linux). Tell the
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
