<!-- design:locked:8352a90 2026-09-16 -->
# Design system — canvas chrome

Read this before changing `chrome.css`, `chrome.js` chrome markup, or any canvas visual token.
Locked: re-run design-task to change it, do not edit around it.

## Scope

- Applies to: the canvas chrome — tokens, chrome controls, content primitives (`chrome.css`), and the shell markup.
- Does NOT govern: canvas *content* (nodes/sections the session writes), mermaid diagram internals beyond the theme variables below, or the `annotate` skill's own copy of the old palette.
- Consumers: `canvas` skill (build + chrome), any dev-task touching canvas visuals.
- N/A: product/PRD inputs — the brief came from the user directly (see Goals).

## Goals — accepted brief

- User's words: "more creative visual, make the point emphasised, non-important elements dimmed, background calm, highlights obvious, colour pattern harmonious, steady, mature, cold, calm", plus "a colour scheme selector that can switch", "don't put too many colours on page".
- Mechanism per goal:
  - point emphasised → 3-step tone ladder + one `.key` block per section
  - non-important dimmed → `.dim` lowers *tone*, never legibility
  - background calm → surfaces within 8% luminance of the background
  - highlights obvious → exactly ONE saturated accent per scheme
  - few colours → two chromatic roles only: accent + critical (was 3 + accent)
  - steady/mature → one type scale, two elevations, no gradients, no glow
- Accepted scheme default: `slate`. `graphite` is one click away and is the strongest "obvious highlight". `paper` = **newsprint**: paper-white stock, print-black ink, hairline rules, a deep print-blue accent, and a press red reserved for critical — two hues, no collision, so a highlight is still a colour rather than only a tone.
- N/A: brand constraints beyond the user's brief — none supplied.

## Accepted references

- N/A: no external reference products were supplied or used. Direction came from the brief and measured contrast.

## Palette tokens

- Token file: `references/chrome.css` — `:root` = slate (default), plus `[data-scheme="graphite"]` and `[data-scheme="paper"]`.
- Scheme is **explicit, not OS-driven**: the reader's choice wins; `?scheme=` overrides for screenshots.
- 11 tokens per scheme: `--bg --surface --surface-2 --line --ink --ink-2 --ink-3 --accent --on-accent --crit --on-crit`.
- Derived, not per-scheme: `--sug: var(--ink-3)`, `--imp: var(--accent)`.
- Accent lightness is chosen per scheme so it stands out against **its own** field — slate's accent is a deep teal, graphite's a bright cyan. This asymmetry is deliberate, not drift.

| token | slate (default) | graphite | paper |
|---|---|---|---|
| `--bg` | `#eef1f4` | `#0d1014` | `#f6f4ee` |
| `--surface` | `#f8fafb` | `#14181e` | `#fcfaf5` |
| `--surface-2` | `#e7ecf0` | `#12161b` | `#efece2` |
| `--line` | `#d3dae1` | `#262d37` | `#b8b1a0` |
| `--ink` | `#0f141a` | `#e7ecf1` | `#100e0a` |
| `--ink-2` | `#46505c` | `#a8b2bf` | `#3a352d` |
| `--ink-3` | `#606975` | `#77828f` | `#665f52` |
| `--accent` | `#0e6f8e` | `#58c8e3` | `#1b3f6b` |
| `--on-accent` | `#ffffff` | `#0b0e12` | `#f8f6f1` |
| `--crit` | `#b3261e` | `#ff6b5e` | `#a3231a` |
| `--on-crit` | `#ffffff` | `#160b09` | `#ffffff` |
| `color-scheme` | light | dark | light (newsprint) |

## Contrast evidence

- Method: WCAG 2.1 relative-luminance ratio, computed (not eyeballed) from the shipped token values.
- Thresholds: body/meta text ≥ 4.5:1, primary ink ≥ 10:1, non-text boundaries ≥ 1.15:1, sunken/raised surfaces ≥ 1.03:1 from bg.
- Result: 14/14 pairs pass in all three schemes.

| pair | threshold | slate | graphite | paper (newsprint) |
|---|---|---|---|---|
| body ink / bg | 10.00:1 | 16.32:1 | 16.04:1 | 17.53:1 |
| support / bg | 4.50:1 | 7.23:1 | 8.89:1 | 11.06:1 |
| dim / bg | 4.50:1 | 4.91:1 | 4.88:1 | 5.74:1 |
| support / surface-2 | 4.50:1 | 6.89:1 | 8.46:1 | 10.29:1 |
| dim / surface-2 | 4.50:1 | 4.68:1 | 4.65:1 | 5.35:1 |
| accent / bg | 4.50:1 | 5.03:1 | 9.79:1 | 9.69:1 |
| accent / surface | 4.50:1 | 5.45:1 | 9.15:1 | 10.22:1 |
| accent / surface-2 | 4.50:1 | 4.79:1 | 9.33:1 | 9.02:1 |
| text on accent | 4.50:1 | 5.70:1 | 9.93:1 | 9.87:1 |
| critical / bg | 4.50:1 | 5.77:1 | 6.83:1 | 6.79:1 |
| text on critical | 4.50:1 | 6.54:1 | 6.92:1 | 7.47:1 |
| line / bg | 1.15:1 | 1.24:1 | 1.37:1 | 1.94:1 |
| surface / bg | 1.03:1 | 1.08:1 | 1.07:1 | 1.05:1 |
| surface-2 / bg | 1.03:1 | 1.05:1 | 1.05:1 | 1.07:1 |

- **Defects this check found in the previous chrome** (both were shipped): inline `code` text at 3.20:1, and the Send button label on its fill at 3.29:1 — both below the 4.5:1 small-text floor.
- Grey-on-grey risk: `--ink-3` is used for 11–12px metadata, so it is held to the 4.5:1 *text* threshold, not the 3:1 large-text one. That is why the "dim" tier is tone-separated rather than contrast-poor.

## Emphasis mechanism

- `.key` — 2px accent left-rule marking the one thing that matters in a section. At most one per section, and **the only accent rule the system permits**.
- `.note` — a callout is a **tone change, not a decoration**: `--surface-2` fill, radius 6px, no rule and no accent wash.
- `.dim` — drops to `--ink-3` (still ≥ 4.5:1). Never applied to text the reader needs; for non-text de-emphasis use `--line` / `--surface-2` instead.
- Tone ladder: `--ink` 16.3:1 → `--ink-2` 7.2:1 → `--ink-3` 4.9:1 (slate). Hierarchy by weight, not by illegibility.
- Severity spends no extra hue: suggestion = tone step, important = accent, critical = `--crit`.
- Decoration budget (user rule, 2026-09-12): one accent rule per section at most; no other left/right bars, no accent washes, no gradients. Dots and pills (listening, count badge, severity) are status, not decoration.

## Typography

- Families: mono = `"IBM Plex Mono", ui-monospace, Menlo, monospace`; sans = `"IBM Plex Sans", system-ui, -apple-system, "Segoe UI", Roboto, sans-serif`. Self-contained — no webfonts, no CDN.
- Roles: mono for structure (headings, labels, metadata, numbers); sans for prose.
- Scale: h1 `2rem` / h2 `1.3rem` / h3 `.9rem` (uppercase, letter-spaced) / body `.95rem`, `1.6` line-height / small `.83rem` / chrome `.62–.78rem`.
- Weights: 400 body, 500/600 headings. No italics in chrome; italics only inside content.
- Scheme variant — **newsprint masthead**: under `[data-scheme="paper"]`, `h1` and `h2` use `Georgia, "Iowan Old Style", "Times New Roman", Times, serif`. Body stays sans (serif body at 15px costs more screen readability than it buys). Recorded here because it is a deliberate exception to "mono for structure".
- Headings use `text-wrap: balance`; long-form measure capped at `52rem`.

## Spacing / radius / elevation

- Spacing steps: 4 · 8 · 12 · 16 · 24 · 40 · 64 px (0.25/0.5/0.75/1/1.5/2.5/4 rem).
- Section rhythm: `2.4rem` between content sections, `1.7rem` padding-top with a `1px var(--line)` divider.
- Radius: `4px` inline/code · `6px` card/inline-block · `8px` panel/dialog · `999px` pills (Send, Conversation, listening, status, scheme group).
- Bottom bar: one fixed row aligned to the 52rem content column (`#bottombar`) — status left, `#controls` right; nothing is pinned to a window corner.
- Elevation: exactly two — `0 4px 12px rgba(11,16,28,.14)` controls, `0 12px 32px rgba(11,16,28,.28)` panels/dialog. No elevation on content blocks.
- N/A: motion tokens — only one transition exists (`.flash` 1.6s anchor highlight); no others are permitted without re-opening this doc.

## Keybindings (chrome, shared by every topic)

- N/A: no composer keybindings — the composer was removed in Rev 6. The only text input left in
  the chrome is the annotation dialog's textarea, where Enter saves the note.
- `Escape` — closes the **innermost** open surface, in this precedence:
  1. the annotation dialog (`#form`), but only while its comment is empty — closing it would drop the anchor the note is attached to;
  2. otherwise the Conversation panel (`#histpanel`), whose draft and history survive a hide.
- Rule: **no keybinding may discard typed text.** That is why (1) refuses and (2) does not.
- N/A: vim-style or configurable bindings — deliberate, so one canvas behaves like every other.

## Component inventory and states

| component | selector | states covered |
|---|---|---|
| masthead | `#content > section:first-child` | centred as a group (eyebrow + title + meta row); bottom rule retained |
| scheme switcher | `#schemes button` | rest, hover, active `.on`, `:focus-visible` |
| Send | `#send` | rest, hover, `:disabled` (nothing unsent), "Sent ✓" |
| Conversation | `#convo`, `#history` | **vertical** tab (`writing-mode: vertical-rl`) on the **left** edge, vertically centred; hidden while `#histpanel` is open (it follows the panel in the DOM so the sibling rule can fire); rest, hover |
| listening pill | `#listening` | `agent away`, `.on` = `agent listening` |
| bottom bar | `#bottombar` | one docked surface (--surface + 1px --line + 12px top radius, no new elevation); never overlaps at any width |
| status group | `#statusrow` | `#listening` + `#status` adjacent, left; `#err` on its own line below; `#controls` right |
| section status | `#content .secpill.settled|.new|.open`, `.secfold` | every section may carry `data-status`; `settled` folds (heading becomes the summary, anchors preserved), `new` wears the accent, `open` is hairline-only. Tone-only styling: no new tokens |
| needs-your-decision | `#convo #convonotice` (outer/left edge), `#histbody .ev .answered` button |, `#histbody .ev.needs`, `.ev.flag` | badge on the Conversation tab driven by `/v.flagged`; the flagged note row AND the session's escalation text highlighted in `--crit`. Sibling of the rotated tab, so the count is never sideways |
| state dots | `#statusrow ::before` | ONE vocabulary: hollow `○` idle, filled `●` active, `●` in `--crit` for a warning. Same glyph/size/margin for both pills — colour carries the meaning, not the shape (a triangle for warn was removed) |
| status pill | `#status` | live / static / delivery-state strings; state dot `○` rest / `●` `.on` / `▲` `.warn` |
| error line | `#err` | empty (`display:none`), error (crit border) |
| annotation dialog | `#form` | `[hidden]`, open — **centred** (`top/left 50%` + translate), width `min(34rem, 92vw)` |
| finished feedback | `#list details`, `summary` | folded by default (`7 finished`), unfolds in place |
| severity dot + level buttons | `#list .dot`, `#levels .lvl` | suggestion, important, critical, `.active` |
| anchor marks | `[data-anchor]` | rest, hover, `.marked`, `.marked-done` |
| count badge | `.badge` | pending count; suppressed inside `<pre>` and `<svg>` |
| ~~composer~~ | — | **removed in Rev 6. `#composer`, `#req`, `#reqadd` no longer exist.** Read-and-point: a requirement is a note on the element it concerns |
| emphasis | `.key`, `.dim` | static |
| cards | `.cards`, `.card`, `.kicker` | static, hover (anchor) |
| compare table | `.compare` | static; header row uppercase mono |
| bars | `.bars`, `.bar`, `.track`, `.fill` | static, hover (anchor) |
| flow | `.flow`, `.step`, `.arr` | static, hover (anchor) |
| tree block | `.tree` | static, preformatted |
| callout | `.note` | static (accent rule + 5% wash) |
| figure/caption | `figure`, `.figcap` | static |
| diagram block | `.mermaid`, `.mermaid-failed` | rendered, failed (crit dashed + reason line) |
| spec error | `.spec-error` | bad JSON / unknown `data-render` kind |
| Conversation panel | `#histpanel`, `.round`, `.rhead`, `.ev` | hidden/open, closes on outside tap, `overscroll-behavior: contain` so its scroll never moves the page; current round, user/agent/meta rows, resolved rows |
| content text | `h1–h3`, `p`, `ul/ol`, `a`, `code`, `hr` | rest, hover (`a`), focus (`a`) |
| N/A | — | no form inputs beyond the annotation textarea; no tables beyond `.compare` |

## Verification evidence

- Contrast: 42/42 computed ratios pass (see table above) — regenerated from the shipped tokens, not transcribed.
- Layout/render: `verify-canvas.py` passes at 1280x900 — no chrome overlap, no element outside the viewport, no horizontal overflow, JS inlined intact, all `data-render` specs drawn, no render errors.
- Visual: screenshots reviewed at **1280x900** for all three schemes, from `.agents/canvas/canvas-theme/shots/{slate,graphite,paper}-desktop.png`. Confirmed rendering, hierarchy, switcher state, and disabled-Send state.
- User approval: the four decisions (default `slate`; keep all three schemes; switcher at page top; chrome-only scope) were approved explicitly.
- User approval (rev 5, canvas-skill topic): the bottom-bar reorganisation was requested directly ("group them properly", "input can move into history view, rename history to conversation", "status … needs to be more obvious", controls "at center of the page, anchor right side") and approved ("yes, do it. if need /design-task to update design, do it").
- Regression rule: any change to the token values above must re-run the contrast check and `verify-canvas.py --viewport 1280x900`.

## Known gaps

- **Phone-width rendering is UNVERIFIED.** Headless Chrome ignores `--window-size` for the layout viewport (minimum ≈ 500–756px), so no true sub-500px render was produced; the `≤640px` breakpoint is unexercised. `verify-canvas.py` now warns when this happens instead of reporting a silent pass. A crop taken at 390px was mistaken for a phone render and deleted.
- The visual review was done from static screenshots, not interaction — hover/focus states are styled but were not exercised by a pointer.
- `annotate` still carries the pre-lock palette (`#d8731f` accent, `#2f63c9` blue). Two chrome stylesheets now exist; folding annotate into this system is **not** done and is not covered by this lock.
- The accent-as-`code`-colour choice means a code chip is accent-coloured on `--surface-2`; it passes at 4.67:1 (slate) with the least margin of any pair. Darkening `--surface-2` further will break it.

Last reviewed: 2026-09-16 · canvas design task
Rev 6 (2026-09-16): **the composer is removed**, on the user's direction. A canvas is read-and-point,
so a requirement becomes a note on the element it concerns — the same mechanism as every other note.
The free-text box was the one affordance that made a canvas feel like a chat client, and so like
`/annotate`. Chrome loses `#composer` / `#req` / `#reqadd` and their keybindings; `snippetOf()` also
stops reading mermaid's injected `<style>` element as a note's snippet (a note on a diagram box stored
CSS instead of the box label). No token changed, so the contrast table is unchanged. Applied in
`references/canvas-template.html` + `chrome.css` + `chrome.js`, then the topic rebuilt and
`verify-canvas.py` re-run.
Rev 5 (same day): bottom chrome reorganised on the user's direction — one `#bottombar` (fixed, aligned to the 52rem content column) holding `#status` as a state pill (dot ○/●/▲) on the left and `#controls` as a single right-aligned row (dashboard · listening · Conversation · Send); the requirement composer moved from the bottom-left corner into the foot of the Conversation panel, its textarea growing to 6rem then scrolling; the panel reserves a bottom strip so the bar stays clickable under an open drawer; `History` renamed `Conversation` in the button, the panel head and the jump-to-composer row. No token changed, so the contrast table is unchanged; `verify-canvas.py` re-run on both topics (no overlap, all chrome in viewport — headless laid out at 756x469, see Known gaps) and the file:// fallback probed (composer reachable, no JS errors). Applied in `references/canvas-template.html` + `chrome.css` + `chrome.js`, then every topic rebuilt.
Rev 4 (same day): `paper` finalised from the round worker's own spec (#f6f4ee stock, #100e0a print-black, #1b3f6b print-blue, #b8b1a0 rules) after independent re-measurement matched its reported ratios; `--crit` set to press red #a3231a; newsprint masthead (h1/h2 serif) adopted from the worker's open question. Rev 3 (superseded) had made the accent ink-black.
Rev 3 (same day): `paper` re-specced as **newsprint** on user feedback ("news paper, not notebook paper"; background should feel like plain paper). Warm stock #f8f6f1, near-black warm ink, accent becomes ink black so the scheme contains ONE hue — the press red, reserved for critical. Key stays `paper` so saved preferences do not fall back to slate.
Rev 2 (same day): user feedback — callout decoration removed, `.key` rule 3px→2px and declared the only accent rule, dialog centred and widened from 18rem→34rem, finished feedback folded by default, history panel closes on outside tap and contains its scrolling.
