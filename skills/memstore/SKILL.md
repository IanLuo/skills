---
name: memstore
description: Store and recall memories in a local memstore database, so a decision, lesson, or fact survives past one session or repo. Use when the user says "remember this", "store this decision", "what did we decide about X", "have we done this before", "search memory", "recall", "save this lesson", or "check the memory store". Covers when to put, when to search, lexical search limits, and correction without overwrite. Do NOT use for the fs task board (flagship), for research libraries (librarian), or for a project's own docs (specs, init-context).
metadata:
  audience: general
  domain: memory
compatibility: Requires the memstore CLI on PATH (install from the memstore repo with nix profile install .#default). Store path is $MEMSTORE_DB, or ~/.memstore/memory.db.
---

# memstore

A local memory store. One SQLite file holds every memory. A memory is a short record with a
kind, an optional scope, and a title or text. It is read back by word match.

## Check the install first

```bash
command -v memstore && memstore stats
```

If `memstore` is missing, say so and stop. Do not fall back to memory and do not write a
file by hand.

## When to put

Put a memory when a later session will need it:

- A decision and its reason: `-kind decision`.
- A lesson from a failure: `-kind lesson`.
- A fact that is hard to re-derive: `-kind fact`.

Do not put: secrets, credentials, personal data, chat chatter, or anything already in a
locked doc or the repo. Point to the doc instead.

```bash
memstore put -kind decision -scope <project> -title "short title" "one or two sentences: what was decided and why"
```

Use a scope that names the project, such as `-scope myproject`. A title helps a later
reader scan results.

## When to search

Search before a decision that may have past context. Search by the words an entry would
use.

```bash
memstore search -k 5 retry backoff
memstore recent -k 10 <scope>
```

Search is **lexical**. It matches stems of the words you give. It has no search by meaning.
A query with zero shared words returns `[]`. That is a limit, not an error. Retry with the
terms the entry would contain: a full name and its abbreviation, a synonym, or an inflected
form.

## Read the result as a record

- Cite each memory by its `Id` and `Ts`: "memory 5123, 2026-10-19".
- Mark it as "from memory, dated". Do not call it verified.
- A locked doc or the current code wins over an old memory. Say when they disagree.
- A memory can be stale or wrong. A decision in memory was true when it was written.

## Correct, do not overwrite

Content is immutable. Only status and deletion change after a put.

- To correct a memory: put a new one, then close the old one, pointing to the new `Id`.

```bash
memstore put -kind decision -scope <project> "corrected: ..."
memstore close <old-id> <new-id>
```

- `forget` hides a memory and cannot be undone by any verb. Run it only when the user asks.

## Link related memories

```bash
memstore link <from-id> <rel> <to-id>
```

Only `superseded-by` and `found-by` are shown on search results. Other relations are stored
but not shown. `close <old-id> <new-id>` already writes `superseded-by`, so use `link` only
for a `found-by` edge or a relation you can name plainly.

## Stop rule

Stop after the put or the search you were asked for. Report the `Id` you wrote, or the hits
you found and how you read them. Do not write extra memories on your own.

## Verbs

`memstore --help` prints the full list. The verbs are `put`, `get`, `search`, `close`,
`forget`, `recent`, `link`, `stats`, and `serve`. `serve` starts a read-only view on
127.0.0.1. Use `memstore serve` only when the user wants to browse.
