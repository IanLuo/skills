# Library format

Read this when you need the exact entry schema or index layout. Entries are written by
`scripts/write-entry.py`, not by hand — this documents what that script produces so
full-body search (`rg`) and you can rely on it.

## TOC

1. Library root
2. Entry frontmatter
3. Entry body
4. `index.md` layout

## 1. Library root

The library lives at `~/Documents/librarian/library/` (override with `$LIBRARY_ROOT`
or the `--root` flag on any script). It is **outside** this repo so moving or
reorganizing the repo never orphans the knowledge store.

```
<root>/library/
├── index.md
├── technology/<slug>.md
├── science/<slug>.md
└── ...
```

Bootstrap: `scripts/init-library.sh [--root <dir>]` creates the tree + empty `index.md`
idempotently. Run it if the library is missing.

## 2. Entry frontmatter

One entry per topic. `<slug>` is the title in lowercase-hyphen-case.

```yaml
---
title: "Pod networking in Kubernetes"
category: technology              # ^[a-z0-9-]+$ — the folder name under library/
tags: ["kubernetes", "networking", "cni"]   # searched alongside title + body
sources:
  - url: "https://kubernetes.io/docs/..."
    title: "K8s Docs — Networking"
    accessed: 2026-07-08
confidence: high                  # high | medium | low — see rubric.md
researched_at: 2026-07-08
---
```

- `sources` is the deduped union of all findings' sources.
- `category` must match `^[a-z0-9-]+$`; it names a folder and becomes a first-class
  query dimension.

## 3. Entry body

```markdown
# <title>

## Key findings

**Angle:** <sub-question 1>
- <concrete fact, with a number/version when available>
- <concrete fact>

**Angle:** <sub-question 2>
- ...

## Synthesis
<one paragraph tying the findings together; passed via --synthesis or minimal-inferred>

## Open questions
- <named gap — especially under confidence: low>
```

## 4. `index.md` layout

Maintained by `write-entry.py`. Do not hand-edit unless reorganizing — queries search
the entry files directly with `rg`, not this index.

```markdown
# Librarian library index

<preamble>

## Categories
- science
- technology

## Index
- [Photosynthesis](science/photosynthesis.md) `science`
- [CNI basics](technology/cni-basics.md) `technology`
```

`write-entry.py` rebuilds the Categories + Index paragraphs from parsed rows each run,
so duplicate rows/categories cannot accumulate. Rewriting an existing topic overwrites
its row in place.