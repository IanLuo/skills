<!-- specs:locked:1e463ba 2026-09-16 type=architecture -->

## Link contract
- **upstream** (this doc relies on): none
- **referrers** (must cite this when they change): skills/canvas/SKILL.md, skills/canvas/references/manual.md, skills/canvas/references/communication-contract.md, skills/canvas/references/graphics.md

# Architecture — canvas
Scope: the `canvas` skill and its runtime — the page server, the CLI, and the topic files. Not the
skills repo. The decisions below are what the code cannot tell you: the constraint that forced
each choice and the alternative that lost.

Obeys `../skill-man/references/doc-format.md`: bullets, fixed headers, explicit `N/A`.

## Load-bearing structure

Six decisions. Changing any one invalidates large parts of `scripts/` and the round loop.

1. **The daemon is a page server, not a dispatcher.**
   - Choice: `canvas.py serve` serves the shell, hot-swaps only the sections whose hash changed,
     and writes each annotation to `feedback.json` as it is typed. It owns no scheduling.
   - Constraint: the page must stay live and a note must reach disk without the user retyping it
     into a chat box. That needs a local HTTP origin and nothing else.
   - Rejected: a scheduler. It was tried twice and removed twice — see decision 2.

2. **No scheduler, no coordinator, no worker.**
   - Choice: a round runs in the session the user is talking to, when the user asks.
   - Constraint, measured: 4 live topics held **76 notes; all 76 were eventually resolved, but 24
     (32%) had to be escalated as "an agent on this loop cannot do this"**. **21 of those 24 were
     the same wall**: the user annotated an element in the chrome, and the round contract let a
     content-only worker touch `content.html` while `chrome.css`/`chrome.js` stayed locked. The
     autonomy layer's own role split turned a third of the user's feedback into a re-routing
     job for the user — the note was answered in the end, but not by the loop that collected it.
   - The measurement that killed its predecessor: a parked waiter as the correctness primitive —
     **18 of 21 wakes reached nobody**, and `listening=1` was once reported against a heartbeat
     11 438 s old.
   - Rejected: keep autonomy and fix the chrome wall. Smaller change, but it keeps three roles,
     two task cards and a lease in order to deliver a round that the talking session could have
     run in the same turn.
   - Consequence: a round costs the talking session's context. That is the accepted trade — and
     the reason the round steps stay short.

3. **One writer per topic, by convention.**
   - Choice: the session owns `content.html` for the duration of a round.
   - Constraint: `content.html` has no merge, and with no always-on component there is nothing
     that could enforce or expire a claim.
   - Rejected: a lease file (`writer.json`, TTL ≈ round timeout). It existed to serialise
     dispatched workers; with one possible writer it can only go stale and lie.

4. **Files are the state; the daemon holds none.**
   - Choice: content, feedback, send, history and versions are files; the daemon re-reads them
     per request and rebuilds its view from scratch.
   - Constraint: a restart must not change what the page says, and the file-only commands must
     work when no daemon is running — that is the recovery path.
   - Rejected: in-memory topic state. It would make `pending`, `ack`, `say` and `list` unusable
     exactly when the server is down.

5. **Ports are resolved, never guessed.**
   - Choice: `<root>/.daemon.json` `{port, pid, root, started}`, a fixed default (7391) probing
     upward, and `start`/`open` always printing the authoritative URL.
   - Constraint: the user's tab is bound to one origin, so a restart must prefer the previous port
     or it orphans the open page.
   - Rejected: trusting the URL an earlier command printed — it goes stale across restarts.

6. **Roots are explicit; there is no registry.**
   - Choice: `--root` names a root; `canvas.py list --root A --root B` reads several.
   - Constraint: a root's own directory must be the only source of truth. Shared state is state
     that can go stale without anyone noticing.
   - Rejected: the `~/.agents/canvas/roots.json` registry — dropped after it reported roots whose
     daemons were long gone. Two projects therefore means two daemons and two ports; that is the
     whole cost of the choice.

## Layers & ownership

Four layers. Dependencies point one way: session → CLI → files, and page → server → files.

| layer | owns | never |
|---|---|---|
| Files (`<project>/.agents/canvas/<topic>/`) | the record — content, feedback, send, history, versions | — |
| Server (`canvas.py serve`) | socket, static shell, annotation writes, section hashes | edits `content.html` |
| CLI (`canvas.py`, `build-canvas.py`, `verify-canvas.py`) | every human/agent action; file-only fallback when the server is down | depends on server memory |
| Session | reasoning, content authorship, the round | assumes anything wakes it |

- **Boundary rule:** nothing depends on the server to be correct. A dead server costs a live
  page and any note typed while it was down; the notes already on disk and every CLI command are
  unaffected.
- The page is **not** a layer — it is the server's projection, and it renders the same files from
  `file://` with no server at all.

## Repo tree

Scope is the skill, not the repo. One line per part:

- `SKILL.md` — the operating contract: purpose, the content contract, the round loop, the rules.
- `scripts/canvas.py` — the page server **and** every file command, one binary.
- `scripts/build-canvas.py` — content → shell. `scripts/verify-canvas.py` — headless render check.
- `references/` — `manual.md`, `graphics.md`, `design-system.md`, `communication-contract.md`, this doc.
- `assets/` — vendored mermaid (gitignored).
- Outside the skill: `<project>/.agents/canvas/<topic>/` (the record) and `<root>/.daemon.json`.

## Cross-cutting concerns

- **Logging — nothing happens silently.** There are no autonomous decisions left to log. The
  daemon records what it is told (note written, note resolved, note deleted, content changed,
  Send raised) into the topic's `history.jsonl`, and `say` adds the session's line. Rounds are
  *derived* from that stream, never numbered.
- **Error model — two vocabularies, both already in use.** CLI exit codes (`0` ok · `1` refuse)
  for humans and agents; HTTP `4xx`/`5xx` **with a reason string** for the page. A refusal names
  its cause; never a bare failure.
- **State — files are the state, server memory is a cache.** A restart must not change observable
  behaviour.
- **Trust boundary — 127.0.0.1 and an explicit root, nothing else.** No tokens, no auth. The
  daemon serves one root, named at start, and is not a general web server.

## Assumptions and edges

- **Smallest input that must succeed:** one root, one topic, one note → `canvas.py pending`
  prints it, and the page shows it as waiting.
- **Smallest input that must be refused:** a write command naming a topic with no directory →
  refused with the known topics listed, nothing created.
- **Assumed, not verified:** a session exists to run rounds (absent it, notes wait on disk by
  design); the user reads the page rather than chat; headless Chrome is present for
  `verify-canvas.py` (`--chrome PATH` otherwise).
- **N/A:** multi-writer merge — there is one writer per topic, and no mechanism to arbitrate two.

## Pointers

- `references/communication-contract.md` — what the page must achieve (locked).
- `references/manual.md` — operating rules and recovery playbooks.
- `references/design-system.md` — locked visuals (grep `design:locked:` on disk for its rev; do
  not restate the sha here — a duplicated sha is a cross-reference that drifts).
- `references/graphics.md` — content vocabulary and the view ladder.
- **N/A:** no ADRs, no `CONVENTIONS.md` in this repo.

Last reviewed: 2026-09-16 · canvas /specs
