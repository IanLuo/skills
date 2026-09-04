---
name: ax
description: "Use `ax` instead of curl/WebFetch for HTTP requests, web scraping, and data extraction. Covers fetch, discover, and extract modes with token-efficient output. Load when you need to fetch a URL, scrape a page, extract structured data, or read web docs."
metadata:
  audience: personal
  domain: tools
---

# ax — HTTP fetch, discover, extract

`ax` replaces `curl` and `WebFetch`. Use it for all HTTP requests and web scraping.

## Fetch mode (curl replacement)

```bash
# JSON API — returns structured envelope {status, ok, ms, headers, body}
ax https://api.example.com/users

# POST with data
ax https://api.example.com/users -X POST -d '{"name":"foo"}' -H 'content-type: application/json'

# Auth
ax https://api.example.com/private -H 'authorization: Bearer TOKEN'
ax https://api.example.com/private -u user:pass

# Body only (raw stdout, like curl default)
ax https://example.com --body

# Save to file; -f makes HTTP errors exit 22 (file untouched on error)
ax https://example.com/file.zip -o out.zip -f
```

Curl flags that work: `-X`, `-H`, `-d`, `-u`, `-I` (HEAD), `-o`, `-k`, `-m <secs>`, `-f`.
No-ops (accepted, ignored): `-L`, `-i`, `-s`, `-S`, `--compressed`.

Fetch mode never caches. Downloads cap at 20MB / 30s by default (`--max-bytes`, `-m`).

## Discover mode (unknown page structure)

```bash
# What's on this page? Tag signatures with counts
ax https://example.com --outline

# Where is this text?
ax https://example.com --locate "Sign up"

# How many matches?
ax https://example.com '.item' --count
```

Parse-mode responses are cached ~2min — probing is free.

## Extract mode (CSS selectors + structured output)

```bash
# Structured rows (TSV by default — 40% fewer tokens than JSON)
ax https://example.com '.item > a' --row 'title=, href=@href'

# Table extraction
ax https://example.com 'table.stats' --table

# Text/attribute/HTML of matches
ax https://example.com '.title' --text
ax https://example.com 'a.nav' --attr href

# Page content as markdown (for reading docs)
ax https://docs.example.com/guide --md

# Filter rows
ax https://example.com 'table' --table --where 'stars >= 30000'
```

## Token budget control

```bash
# Cap output to ~800 tokens (cuts at item boundaries, never silent)
ax https://example.com '.item' --text --budget 800

# Pagination: truncation notes give the exact --offset to continue
ax https://example.com '.item' --text --limit 20 --offset 40

# JSON envelope with pagination state
ax https://example.com '.item' --row 'title=' --json-envelope
# → {data, meta} — continue only while state=more, stop on complete/past_end

# Full output (no cap)
ax https://example.com '.item' --text --all
```

## Workflow: scraping an unknown page

1. `ax URL --outline` — see the page structure
2. `ax URL --locate "target text"` — find the right selector
3. `ax URL 'selector' --count` — verify match count
4. `ax URL 'selector' --row 'col1=sel1, col2=sel2@attr'` — extract

## Key differences from curl

| | curl | ax |
|---|---|---|
| JSON APIs | raw body, pipe to `jq` | parsed JSON in envelope |
| HTML pages | raw dump | `--md`, `--outline`, CSS selectors |
| Token cost | unbounded | `--budget`, `--limit`, TSV default |
| Errors | silent with `-s` | never silent, always announced |
| Caching | none | ~2min parse-mode cache |

## Gotchas

- `--body` for raw body on stdout (curl-like behavior)
- `--data-raw` never interprets `@` as file (unlike `-d`)
- Default timeout 30s — use `-m <secs>` for slow sites
- Credential-bearing requests (`-H auth`, `-u`) are never cached
