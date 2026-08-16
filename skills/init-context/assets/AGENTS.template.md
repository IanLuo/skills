# AGENTS.md — <project-name>

## Intent

<≤3 lines: the product goal the agent must not satisfice past. One sentence is fine.>

## How to run / build / test

```bash
# Build
<build command>

# Run
<run command>

# Test
<test command>

# Lint / type-check
<lint command>
```

Each command verified before writing — if impractical to run, note why explicitly.

## Hot invariants

- <never X, because Y>
- <always Z, because W>
- <do not touch A unless B>

Only the frozen "because" rules a fresh agent would silently break — a handful, not a catalog. Longer conventions live in `CONVENTIONS.md`.

## Architecture elevator

<5 lines: layers, ownership, boundaries. A one-level repo tree is enough. Deep rationale (why these choices) lives in ARCHITECTURE.md or docs/adr/.>

```
src/          — <one-line purpose>
tests/        — <one-line purpose>
docs/         — <one-line purpose>
```

## Deeper docs

<List locked docs that exist on disk — one line each. Find with:
`grep -rl '<!-- specs:locked:\|<!-- design:locked:' *.md`. Omit this section if zero docs exist.>

- `<path>` — `<one-line purpose>`
