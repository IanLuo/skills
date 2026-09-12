# Graphics vocabulary

Read this when writing canvas content, or when a diagram does not show up.

## The rule that actually saves tokens

Measured, same 4-node flow, ≈4 chars/token:

| encoding | chars | ≈tokens |
|---|---|---|
| mermaid source | 89 | **~22** |
| JSON spec for a bespoke renderer | 212 | ~53 |
| hand-authored inline SVG | 1282 | ~320 |

But the bigger lever is not the format — it is **not re-emitting sections you did not
change**. A 5-section canvas rewritten wholesale is ~1000 tokens; editing one section is
~200. Over ten rounds that is 10k vs 2k tokens. So:

- Touch only the sections whose content actually changed. Every round.
- Keep sections small (roughly 10–60 lines) so a rewrite is cheap.
- Unchanged sections keep their hash and are not even re-rendered, so the user's
  scroll position, marks, and focus survive.

**Markup golf is a trap.** Rendering a block from data instead of writing tags saves only
11–15% (measured: 3 cards 123→104 tokens, a 4-row table 155→138), because JSON braces,
quotes and keys cost nearly what the tags cost. Do not spend effort there — spend it on
emitting fewer sections.

## Primitives (HTML/CSS — cheap, and what the user can point at)

Options side by side. ~2× prose tokens, but the user compares instead of parsing:

```html
<div class="cards">
  <div class="card" data-anchor="opt-a"><p class="kicker">Option A</p><h3>Vendor mermaid</h3><p>3.4 MB once; ~22 tokens per diagram after.</p></div>
  <div class="card" data-anchor="opt-b"><p class="kicker">Option B</p><h3>Own a renderer</h3><p>~400 lines to maintain.</p></div>
</div>
```

Trade-off table:

```html
<table class="compare">
  <tr><th></th><th>cost</th><th>risk</th></tr>
  <tr><td>mermaid</td><td>~22 tok</td><td>syntax error → blank</td></tr>
</table>
```

Pipeline, no library needed:

```html
<div class="flow">
  <span class="step" data-anchor="st-1">click</span><span class="arr">→</span>
  <span class="step" data-anchor="st-2">poll</span><span class="arr">→</span>
  <span class="step" data-anchor="st-3">swap</span>
</div>
```

## Emphasis (chrome-provided — use these instead of inventing styling)

- `.key` — the one thing that matters in a section. Renders an accent left-rule. **At most one per section.**
- `.dim` — lowers tone without losing legibility (stays ≥ 4.5:1). Never for text the reader needs; to de-emphasise non-text, change the surface/line token instead.
- Tone ladder: `--ink` (the point) → `--ink-2` (support) → `--ink-3` (metadata). Hierarchy by weight, not by illegibility.

**Never hardcode a colour in content.** The page ships three schemes (`slate` default, `graphite`, `paper`, switchable at the top of every page) and a literal hex or a fixed light colour will break at least one of them. Use the tokens — or better, no colour at all. The palette, type scale, spacing and radii are locked in
[references/design-system.md](design-system.md); read it before adding a visual token.

Also available: `.tree` (file/call trees, `white-space: pre`), `.bars`/`.bar`/`.track`/`.fill`
(magnitudes), `.note` (a callout), `figure` + `figcaption`, `.card`, `.meta-row`, `.kicker`,
`.eyebrow`, `.mono`.


## Readability contract (what keeps a canvas worth reading)

A canvas accumulates: every round adds, nothing is removed, so by round ten it is a wall and
the live question is lost in it. Three rules fix that, and the first is not optional.

- **Mark every section's status**: `data-status="settled" | "new" | "open"` on the `<section>`.
  `settled` **folds** (chrome does it — the reader can still open it), `new` is flagged in the
  accent, `open` is the working state. The reader should be able to answer *"what changed since
  I last looked?"* by scanning pills alone, without reading a word.
- **Smallest view that makes the point** (this is show-me's principle, and it is the cure for
  wall-of-text): pseudocode for logic, a tree for structure, mermaid for flow, a diff for what
  changed, a table or cards for options. Prose is the last resort, not the default.
- **One idea per section, and keep it short.** If a section answers two questions, split it. If
  a settled section has grown long, fold it rather than trim it — the history has value, the
  screen does not have room. Prefer `.key` (one per section) to mark the point and `.dim` to
  push the rest back.

Anti-pattern this exists to prevent: ten sections of prose, all the same weight, no way to tell
a decision made in round 2 from the question you are being asked right now.

## Data-rendered shapes (optional, for repeated blocks)

For shapes that repeat, write data instead of tags and let the chrome render it:

```html
<div data-render="compare" data-anchor="cmp-1">
  <script type="application/json">{"head":["fork","cost"],"rows":[["transport","1 process"]]}</script>
</div>
```

Kinds: `compare` (`{head, rows}`), `cards` (`{cols:[{k,h,p}]}`), `bars` (`{max, items:[{l,v,t}]}`),
`flow` (`{steps:[{t}]}`).

Every item may carry `"a":"<anchor>"` to pin its id. **Without `a`, the anchor is derived
from the item's own data** — a compare row from its first cell, a card from its kicker/heading,
and so on. Derived anchors are the reason to use this at all: they cannot drift, so they
cannot silently orphan the user's notes. Use `a` only to preserve an anchor that already has
annotations on it.

The saving is 11–15%, not the third it looks like. Choose `data-render` for correctness and
consistency; choose raw HTML when the layout is bespoke and no shape fits.

`build-canvas.py` validates content before writing the shell: duplicate anchors, nested
sections, malformed spec JSON and unknown kinds all fail the build rather than appearing as
a broken page.

Use inline `<svg>` only for small bespoke marks — under ~6 elements. Above that it is
~320 tokens and hand-placed coordinates, and mermaid wins.

## Diagrams (mermaid)

```html
<pre class="mermaid" data-anchor="fig-1">flowchart LR
  U[User] -->|click| P[Page]
  P -->|poll| D[Daemon]</pre>
```

Pick by question shape:

| question | diagram |
|---|---|
| what leads to what / which branch | `flowchart LR` / `TD` |
| who says what, in what order | `sequenceDiagram` |
| what states, what transitions | `stateDiagram-v2` |
| how data is shaped / related | `erDiagram` |
| when things happen over time | `gantt` |

Do **not** use mermaid for side-by-side options, layout mockups, or numeric comparison —
that is what `.cards`, plain HTML, and `.compare`/`.bars` are for.

Mermaid renders only in live mode, or when the shell was built with `--inline-mermaid`.
No `assets/mermaid.min.js` vendored means diagrams display as their source text — check
with `curl -s -o /dev/null -w '%{http_code}' http://127.0.0.1:<port>/mermaid.js` (200 = ok).
A syntax error leaves the source text in place rather than breaking the page, so if you see
source text, the diagram is malformed — fix the syntax.

## The annotation contract

- **Stable anchors.** Every element the user might disagree with carries
  `data-anchor="<kebab-id>"`. Anchors must not change between rounds: the user's
  annotations are keyed by them, and renaming one silently orphans their feedback.
- **Granularity.** Anchor the claim, option, row, node, or code block — not the wrapper
  around it. Anchoring a whole section produces vague feedback ("this section is wrong").
  Do not nest anchors deeper than two levels.
- **Mermaid nodes are automatic.** Each rendered node gets `<figure>.<node>`, so the user
  can point at "the Daemon box" instead of "that diagram". Do not hand-write those ids.
- **Every anchor should be something you would defend.** If nothing would change when the
  user disagreed, the anchor is noise.

## Section hygiene

- `#content`'s top-level children are `<section data-section="sN">`. Do not nest sections —
  the daemon hashes by scanning top-level boundaries, and nesting causes full replaces.
- One idea per section. If a section answers two questions, split it.
- Removing a section deletes its anchors, so the user's annotations on it stop showing.
  Say that in chat ("dropped §4, your 2 notes on it went with it") rather than dropping
  it silently.

## Failure modes

| symptom | cause |
|---|---|
| whole page flashes on every update | sections nested, or anchors/hashes churning |
| diagram shows as source text | mermaid not vendored, or the diagram has a syntax error |
| a note of the user's disappeared | its anchor was renamed or its section deleted |
| a block shows a red dashed box | bad spec JSON or an unknown `data-render` kind — `build-canvas.py` now rejects both at build time |
| a diagram shows a red dashed box | mermaid refused that diagram; the source is shown below the message |
| edits don't appear | daemon down, or the content file is not `<topic>.content.html` |
| "out of sync with the daemon" in the status line | the page kept annotating while the daemon was down; those notes are local only |
