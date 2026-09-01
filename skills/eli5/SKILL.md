---
name: eli5
description: Explain complex topics in simple terms, calibrated to the target audience (children, managers, engineers, parents, designers). Adjusts vocabulary, analogies, tone, depth, and framing to match who's asking. Supports visual HTML with diagrams (published as artifact or saved locally). Use when asked to "ELI5", "explain to [audience]", "simplify", or "break this down". Do NOT use for code documentation (use specs), detailed technical writing, or when the user wants the full complexity preserved.
metadata:
  audience: personal
  domain: general
---

# eli5

Explain complex topics in simple, audience-calibrated terms with optional visual diagrams.

## When to use

- User explicitly asks: "ELI5 [topic]", "explain like I'm five", "explain to my manager", "simplify this for a designer"
- User mentions an audience: "how would I explain X to my mom", "break this down for a 10-year-old"
- Translating across expertise boundaries: technical→business, code→UX impact, error→layperson

## Calibration dimensions

Detect the target audience from context (explicit mentions or question framing), then adjust:

| Dimension | Children (5-12) | Non-technical adults | Technical peers | Experts |
|---|---|---|---|---|
| **Vocabulary** | No jargon; define every term | Remove/explain jargon | Keep domain terms, no acronym expansion | Full terminology, assume shared knowledge |
| **Analogies** | Everyday objects (toys, food, playground) | Common experiences (driving, shopping, home) | Industry patterns (CI/CD→assembly line) | Precise analogues (B-tree→sorted array with pointers) |
| **Tone** | Playful, encouraging | Conversational, respectful | Professional, peer-level | Terse, assumes context |
| **Depth** | One core concept only | Surface-level with one layer down | Mechanisms visible, edge cases noted | Nuanced, tradeoffs explicit |
| **Framing** | What it does, why it matters | Business impact, risk/benefit | Implementation approach, constraints | Correctness proofs, performance characteristics |

**Default when audience is unclear**: Non-technical adult (conversational tone, explained jargon, common-experience analogies, surface-level depth).

## Output format

Choose based on complexity and user preference:

### 1. Text-only (simple concepts, <3 steps)
- Inline explanations in conversation
- Optional ASCII diagrams for simple flows
- Use for: "what's a database index" → one analogy + one-sentence mechanism

### 2. Visual HTML (multi-step processes, relationships, state changes)
- Full HTML page with numbered steps + inline SVG diagrams
- Self-contained (no external dependencies)
- Use for: "how does git merge work", "explain our auth flow", "what happens when I click submit"

**Publishing mode (auto-detected):**
- **Artifact available**: publish as artifact → hosted URL, shareable
- **Artifact unavailable**: save to local file `/tmp/eli5-{slug}.html` → auto-open in browser

### Visual HTML structure

```html
<!doctype html>
<html>
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>Topic for [Audience]</title>
  <style>
    /* Light/dark theme support */
    :root {
      --bg: #ffffff;
      --text: #1a1a1a;
      --accent: #0066cc;
      --border: #e0e0e0;
      --code-bg: #f5f5f5;
    }
    @media (prefers-color-scheme: dark) {
      :root:not([data-theme="light"]) {
        --bg: #1a1a1a;
        --text: #e0e0e0;
        --accent: #4da6ff;
        --border: #333333;
        --code-bg: #2a2a2a;
      }
    }
    :root[data-theme="dark"] {
      --bg: #1a1a1a;
      --text: #e0e0e0;
      --accent: #4da6ff;
      --border: #333333;
      --code-bg: #2a2a2a;
    }
    body {
      font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', sans-serif;
      background: var(--bg);
      color: var(--text);
      line-height: 1.6;
      max-width: 800px;
      margin: 0 auto;
      padding: 2rem;
    }
    h1 { font-size: 2rem; margin: 0 0 0.5rem; }
    h2 { font-size: 1.5rem; margin: 2rem 0 1rem; border-top: 1px solid var(--border); padding-top: 1rem; }
    .hook { font-size: 1.1rem; color: var(--accent); margin-bottom: 2rem; }
    .step { margin-bottom: 2rem; }
    svg { max-width: 100%; height: auto; margin: 1rem 0; }
    .why { background: var(--code-bg); padding: 1rem; border-radius: 8px; border-left: 4px solid var(--accent); }
  </style>
</head>
<body>
  <h1>Topic explained for [Audience]</h1>
  <p class="hook">One-sentence hook connecting to their world</p>

  <div class="step">
    <h2>Step 1: [verb phrase]</h2>
    <p>What happens, using their vocabulary</p>
    <svg width="600" height="200" viewBox="0 0 600 200">
      <!-- Inline SVG diagram -->
    </svg>
  </div>

  <div class="why">
    <h2>Why this matters to you</h2>
    <p>Tie to their goals/concerns</p>
  </div>
</body>
</html>
```

### SVG diagram guidelines

- Keep viewBox width ≤800, height ≤600
- Use CSS variables for colors:
  ```svg
  <rect fill="var(--accent)" stroke="var(--border)" />
  <text fill="var(--text)">Label</text>
  ```
- Label everything — no unlabeled arrows
- Simple shapes: `<rect>`, `<circle>`, `<line>`, `<path>`, `<text>`
- Text size 14-16px for readability
- Add `<title>` for accessibility:
  ```svg
  <svg><title>Diagram showing X process</title>...</svg>
  ```

### How to publish

**If Artifact tool is available:**
```
1. Write HTML to temporary file (use Write tool)
2. Call Artifact tool with file_path
3. Return the artifact URL to user
```

**If Artifact tool unavailable:**
```
1. Write HTML to /tmp/eli5-{slug}.html (slug = topic name, lowercase-hyphen)
2. Auto-open: `open /tmp/eli5-{slug}.html` (macOS) or `xdg-open` (Linux)
3. Tell user: "Saved to /tmp/eli5-{slug}.html and opened in browser"
```

**Detection:**
- Try calling Artifact tool first
- If it errors with "need a claude.ai login", fall back to local file mode
- Don't ask the user which mode — auto-detect and proceed

## Examples by audience

**Children (5-12)**: "ELI5 what a database is"
> A database is like a magical filing cabinet. Each drawer has cards with information — like your name, your favorite color, your birthday. When you want to find something, you tell the cabinet what you're looking for ("show me everyone who likes blue!") and it finds all the right cards super fast. Your apps use these cabinets to remember things about you.

**Manager**: "Explain this deploy error to my boss"
> The deploy failed because the staging environment's certificate expired yesterday. Think of it like a security badge for a building — ours expired, so the system won't let the new code enter. We need IT to renew the certificate (2-hour turnaround), then we can retry the deploy. No data loss, no customer impact, just a 2-hour delay.

**Designer**: "Simplify this API response structure for the UX team"
> The API returns a list of "cards", each with a title, image URL, and action. If `action.type` is "link", show a → icon and open the URL when tapped. If it's "modal", show a ⓘ icon and display `action.content` in a popup. Missing `action`? The card is read-only, no tap target needed.

**Engineer**: "Explain how our rate limiter works"
> Token-bucket algorithm: each user gets 100 tokens, refilled at 10/second. Each API call consumes 1 token. If bucket is empty, request gets 429. Bucket state is in Redis with 60s TTL (implicit reset for idle users). Config: `RATE_LIMIT_BURST=100`, `RATE_LIMIT_REFILL_RATE=10`.

## Rules

- **Detect before responding**: scan the prompt for audience clues (age, role, "my [relative/colleague]", technical terms used/avoided).
- **Choose format wisely**: text for simple concepts, visual HTML for multi-step processes or when relationships/flow are hard to describe in prose.
- **One core concept per explanation**: if the topic is large, break it into "Part 1: [foundational concept]" and offer to continue.
- **Analogies must map correctly**: the analogy should match the mechanism, not just the vibes. A database is not "like a brain" (too vague); it's "like a filing cabinet with an index" (findable, structured).
- **No condescension**: simple ≠ childish for adults. Use respectful language even when simplifying maximally.
- **Admit unknowns**: if you don't have enough context to explain (e.g. "our auth flow" but you haven't read the code), say so and offer to read the relevant files first.
- **Auto-detect publishing mode**: try Artifact first, fall back to local file silently if unavailable. Don't ask the user which mode — just make it work.
- **Self-contained HTML only**: all CSS/JS/SVG inline. No external dependencies, no CDN links (except Google Fonts if needed).

## When NOT to use this skill

- User wants full technical depth preserved (use your default technical voice)
- Writing API documentation, architecture docs, or specs (use `specs` skill)
- Code comments or docstrings (those are peer-to-peer, not simplified)
- The user is correcting your explanation (they're teaching you, not asking for simplification)
