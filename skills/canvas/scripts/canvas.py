#!/usr/bin/env python3
"""canvas.py — the canvas daemon.

Serves a topic's canvas over http://127.0.0.1 so the page can (a) poll for new
content and hot-swap only the sections the agent changed, and (b) POST the user's
annotations straight to disk. Without this daemon the built .html still works from
file:// — it just falls back to reload-to-update and copy-paste feedback.

Layout (--root, default .agents/canvas) — one DIRECTORY per topic, so everything a
session produced lives together:

    <topic>/content.html     section-keyed content the agent edits each round
    <topic>/index.html       built shell (chrome + content inlined) — file:// fallback
    <topic>/feedback.json    annotations, written by this daemon
    <topic>/history.jsonl    append-only record of the conversation on this topic
    .daemon.json             {port, pid, root, started}

Routes:

    GET    /                     a plain index of topic links — not the dashboard
    GET    /health               {ok, port, root, mermaid, canvases}
    GET    /t/<topic>            built shell
    GET    /c/<topic>            content partial (hot-swap source)
    GET    /v/<topic>            {v, sections, pending, unsent} — poll this
    GET    /a/<topic>            annotations
    GET    /h/<topic>            history events, oldest first
    POST   /a/<topic>            upsert one annotation
    POST   /a/<topic>/ack        {"ids":[...]} → mark resolved
    DELETE /a/<topic>?id=<id>    delete one annotation
    POST   /a/<topic>/send       the user pressed Send — the batch is ready to collect
    GET    /mermaid.js           vendored mermaid (404 if not vendored)

CLI:

    canvas.py serve [--root DIR] [--port N]      run in the foreground
    canvas.py start [--root DIR] [--port N]
    canvas.py stop  [--root DIR]
    canvas.py status [--root DIR]
    canvas.py list [--root DIR] [--root DIR ...]   every topic and what is waiting
    canvas.py pending <topic> [--root DIR] [--json]
    canvas.py show <topic> [sN ...]               one section, or the section index
    canvas.py send <topic>                       simulate the page's Send button
    canvas.py ack <topic> [--ids c1,c2 | --all]
    canvas.py say <topic> "<one line>"           record the agent's reply on the topic
    canvas.py flag <topic> --ids c1,c2 [--note "why"]    escalate notes the user must decide
    canvas.py versions <topic>                   list content snapshots (undo points)
    canvas.py restore <topic> --to last|<fragment>       put a snapshot back
    canvas.py drop <topic> [--force]                     end a topic and remove it
    canvas.py open <topic>
"""

import argparse
import hashlib
import json
import os
import re
import shutil
import socket
import subprocess
import sys
import threading
import time
from datetime import datetime, timezone
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
from urllib.parse import urlparse, parse_qs

SKILL_DIR = Path(__file__).resolve().parent.parent
MERMAID_ASSET = SKILL_DIR / "assets" / "mermaid.min.js"

DEFAULT_ROOT = ".agents/canvas"
DEFAULT_PORT = 7391  # 8787/8080/5173/3000 are routinely taken by dev servers
TOPIC_RE = re.compile(r"^[a-z0-9][a-z0-9-]{0,63}$")
SECTION_RE = re.compile(r'<section\b[^>]*\bdata-section="([^"]+)"', re.I)
SEVERITIES = ("suggestion", "important", "critical")


# Commands that WRITE into a topic. For these, a topic that has no directory is almost
# always a typo — `say start worker hi` parses as topic="start" + text "worker hi", so the
# tool must not silently create and write to a topic nobody meant.
WRITE_COMMANDS = ("say", "flag", "ack", "restore", "drop")


def require_known_topic(root, topic, cmd):
    if root is None or topic is None or cmd not in WRITE_COMMANDS:
        return
    root = Path(root)
    if not (root / topic).is_dir():
        known = ", ".join(list_canvases(root)) or "none"
        raise SystemExit("canvas: no canvas named %r under %s (have: %s).\n"
                         "        a topic is a lowercase slug — 'start worker' must be "
                         "'start-worker'." % (topic, root, known))


def require_topic(topic):
    """One choke point for every subcommand. Without it, `say start worker hi` splits into
    topic="start" + text=("worker","hi") and silently writes to the WRONG topic — as bad as
    a crash, and harder to notice."""
    import re as _re
    if topic is None or TOPIC_RE.match(topic):
        return
    guess = _re.sub(r"[^a-z0-9-]+", "-", str(topic).lower()).strip("-")
    hint = "  did you mean %r?" % guess if TOPIC_RE.match(guess or "") else ""
    raise SystemExit("bad topic %r — topics are lowercase slugs matching "
                     "^[a-z0-9][a-z0-9-]{0,63}$ (no spaces).%s" % (topic, hint))


def now_iso():
    return datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")


def sha(text):
    return hashlib.sha256(text.encode("utf-8")).hexdigest()[:16]


# The daemon runs whatever code it was started with, so a daemon left running after an upgrade
# silently lacks the routes this file has. /health reports the revision, so a mismatch is loud
# instead of mysterious.
SCRIPT_REV = None  # set below, once Path is imported


def script_rev():
    global SCRIPT_REV
    if SCRIPT_REV is None:
        SCRIPT_REV = sha(Path(__file__).read_text(encoding="utf-8"))
    return SCRIPT_REV


# ── topic files ───────────────────────────────────────────────────────────

def topic_paths(root, topic):
    d = root / topic
    return {
        "dir": d,
        "shell": d / "index.html",
        "content": d / "content.html",
        "feedback": d / "feedback.json",
        "history": d / "history.jsonl",
    }


def read_text(path, default=""):
    try:
        return path.read_text(encoding="utf-8")
    except (FileNotFoundError, NotADirectoryError):
        return default


def section_hashes(content):
    """Hash each top-level <section data-section="..."> block.

    Plain string scanning, not a parser: sections must not nest. If the split is
    imperfect the worst case is the page replacing a section it did not need to.
    """
    out = {}
    marks = [(m.start(), m.group(1)) for m in SECTION_RE.finditer(content)]
    for i, (start, sid) in enumerate(marks):
        end = marks[i + 1][0] if i + 1 < len(marks) else len(content)
        out[sid] = sha(content[start:end])
    return out


def anchor_sections(content):
    """{anchor: section id} for the anchors written in the markup.

    Not every anchor a note can sit on is in the markup: a `data-render` shape and a mermaid
    diagram build theirs in the browser (`<container>.<slug of the item>`), so those are resolved
    by `section_for`'s prefix rule instead of by re-implementing chrome.js's slug here. The rule
    used to be duplicated in Python; it is not worth two implementations that can drift.
    """
    out = {}
    for m in re.finditer(r'<section\b[^>]*\bdata-section="([^"]+)"(.*?)(?=<section\b|$)',
                         content, re.S | re.I):
        sec, body = m.group(1), m.group(2)
        for a in re.findall(r'data-anchor="([^"]+)"', body):
            out.setdefault(a, sec)
    return out


def states_of(note, index):
    """Every state a note is in — a note can be both flagged and an orphan.

    `resolved` (the exact anchor is in the markup), `derived` (only its container is: a
    `data-render` row or a mermaid node, which the browser builds and which may since have been
    deleted), `orphan` (neither — the element is gone), `flagged` (awaits the user). Returning a
    list is why `--json` no longer has to pick one: collapsing FLAGGED+ORPHAN into "flagged" hid
    the orphan.
    """
    anchor = note["anchor"]
    out = []
    if anchor not in index:
        out.append("derived" if anchor.split(".", 1)[0] in index else "orphan")
    if note.get("flagged"):
        out.append("flagged")
    return out


def section_for(anchor, index):
    """The section a note lives in, or None when the anchor is nowhere in the page.

    `vs-annotate.the-artifact` and `fig-owners.D` are not in the markup — they are built in the
    browser from the container's own data — so a miss falls back to the container before the
    first dot. Anything that resolves to neither is an orphan: the element is gone.
    """
    if anchor in index:
        return index[anchor]
    head = anchor.split(".", 1)[0]
    return index.get(head)


def split_sections(content):
    """[(section id, raw html)] in document order. A section is the hot-swap unit and the unit a
    note points at, so this is how a round reads one section instead of the whole file."""
    out = []
    for m in re.finditer(r'<section\b(?=[^>]*\bdata-section="([^"]+)")[^>]*>', content, re.I):
        nxt = re.search(r'<section\b', content[m.end():], re.I)
        end = m.end() + nxt.start() if nxt else len(content)
        out.append((m.group(1), content[m.start():end].rstrip() + "\n"))
    return out


def fresh_sections(root, topic):
    """Sections whose content changed since the user last pressed Send.

    The daemon already records this (`content` events carry changed/added/removed), so the page
    can point at what a round just changed without the content carrying any round language. It is
    derived, never authored: the agent writes about the subject, the chrome says what is new.
    """
    # Order, not time. Timestamps are second-granular, so a Send and the content change it
    # triggers routinely share a second and a `ts >` comparison drops the very change it is
    # looking for. The log is append-only, so the last `send` event is the boundary.
    events = load_history(root, topic)
    boundary = None
    for i, e in enumerate(events):
        if e.get("kind") == "send":
            boundary = i
    if boundary is None:
        return []
    fresh = set()
    for e in events[boundary + 1:]:
        if e.get("kind") != "content" or e.get("baseline"):
            # A baseline event means "the snapshot was missing, so we cannot say what changed".
            # Counting it would light up every section on the page.
            continue
        fresh.update(e.get("changed") or [])
        fresh.update(e.get("added") or [])
        fresh.difference_update(e.get("removed") or [])
    return sorted(fresh)


def batch_notes(root, topic):
    """(batch, held): the notes a round may work, and the ones held back.

    A Send is the batch boundary. Notes written after it are the user still typing, and a round
    that answers them gets un-answered by their next keystroke (an edit re-posts the note with
    `resolved: false`). A topic with no Send has nothing to gate against, so everything
    unresolved is the batch — otherwise a fresh canvas would look empty until the first Send.
    """
    unresolved = pending_notes(root, topic)
    sent_at = (read_send(root, topic) or {}).get("ts") or ""
    if not sent_at:
        return unresolved, []
    return ([a for a in unresolved if a.get("ts", "") <= sent_at],
            [a for a in unresolved if a.get("ts", "") > sent_at])


def norm(text):
    """Lowercase, punctuation to hyphens — for comparing a snippet against its anchor only.
    Deliberately NOT chrome.js's slug(): the derivation rule is not duplicated in this file."""
    return "-".join(w for w in re.split(r"[^a-z0-9]+", str(text).lower()) if w)


def needs_snippet(note):
    """The snippet earns its place only when it tells you something the anchor does not — it
    exists to identify an opaque element, not to restate `claim-a` as "Claim A."."""
    snip = norm(note.get("snippet") or "")
    last = note["anchor"].split(".")[-1]
    return bool(snip) and last not in snip and snip not in last


def load_feedback(root, topic):
    """The notes. Fails CLOSED on a corrupt file.

    This returns the user's own words, and every caller that writes goes through
    `save_feedback`, which replaces the file whole. Treating an unparseable file as "no notes"
    was therefore the one path in the skill that could discard every note silently — a single
    truncated write and the next ack would make it true. The contract's R9 says a note is never
    discarded, so a corrupt file stops the command instead.
    """
    path = topic_paths(root, topic)["feedback"]
    raw = read_text(path)
    if not raw:
        return {"topic": topic, "updated": now_iso(), "annotations": []}
    try:
        data = json.loads(raw)
    except ValueError as e:
        raise SystemExit(
            "canvas: %s is not valid JSON (%s).\n"
            "        Refusing to continue: reading it as empty and saving would discard every "
            "note in it.\n"
            "        Inspect or move it aside, then retry." % (path, e))
    if not isinstance(data, dict):
        raise SystemExit("canvas: %s is not an object — refusing to overwrite it" % path)
    data.setdefault("topic", topic)
    data.setdefault("annotations", [])
    return data


def save_feedback(root, topic, data):
    data["updated"] = now_iso()
    path = topic_paths(root, topic)["feedback"]
    path.parent.mkdir(parents=True, exist_ok=True)
    tmp = path.with_suffix(".json.tmp")
    tmp.write_text(json.dumps(data, indent=2, ensure_ascii=False) + "\n", encoding="utf-8")
    os.replace(tmp, path)


def list_canvases(root):
    if not root.is_dir():
        return []
    return sorted(d.name for d in root.iterdir()
                  if d.is_dir() and TOPIC_RE.match(d.name) and (d / "content.html").is_file())


# ── history ───────────────────────────────────────────────────────────────

def append_history(root, topic, event):
    """Append-only log of the conversation on this topic. Never rewritten, so a
    partial write cannot lose earlier rounds."""
    path = topic_paths(root, topic)["history"]
    path.parent.mkdir(parents=True, exist_ok=True)
    event = dict(event)
    event.setdefault("ts", now_iso())
    with open(str(path), "a", encoding="utf-8") as f:
        f.write(json.dumps(event, ensure_ascii=False) + "\n")


def load_history(root, topic):
    out = []
    for line in read_text(topic_paths(root, topic)["history"]).splitlines():
        line = line.strip()
        if not line:
            continue
        try:
            out.append(json.loads(line))
        except ValueError:
            continue
    return out


# ── delivery: the send signal ─────────────────────────────────────────────
#
# The user should never have to retype their notes into a chat box. The page raises a
# send signal instead; the notes themselves are already on disk, written the moment they
# were typed, so a send only means "this batch is ready" and can never be lost.

def send_path(root, topic):
    return topic_paths(root, topic)["dir"] / "send.json"


def read_send(root, topic):
    try:
        return json.loads(send_path(root, topic).read_text(encoding="utf-8"))
    except (OSError, ValueError):
        return None


def write_send(root, topic, data):
    path = send_path(root, topic)
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(json.dumps(data, indent=2) + "\n", encoding="utf-8")


def record_send(root, topic):
    """The page's Send button, as state: mark the batch sent and say how many notes it
    carries. Send writes state and nothing else — a session reading `pending` collects it,
    so one Send can never start two rounds. Returns the count."""
    notes = unsent_notes(root, topic) or pending_notes(root, topic)
    write_send(root, topic, {"ts": now_iso(), "count": len(notes)})
    append_history(root, topic, {"kind": "send", "count": len(notes)})
    return len(notes)


def pending_notes(root, topic):
    return [a for a in load_feedback(root, topic)["annotations"] if not a.get("resolved")]


def unsent_notes(root, topic):
    """Notes written since the last send — what the Send button is offering."""
    send = read_send(root, topic)
    sent_at = (send or {}).get("ts") or ""
    return [a for a in pending_notes(root, topic) if a.get("ts", "") > sent_at]


# ── content changes: the hot-swap source, and what can be undone ──────────

def note_content_change(root, topic, content):
    """Log which sections changed, and snapshot the content so a round can be undone.
    Only fires when the content actually differs from the last logged version (/v is
    polled every second, so this must be idempotent)."""
    v = sha(content)
    sections = section_hashes(content)
    prior = None
    for event in load_history(root, topic):
        if event.get("kind") == "content":
            prior = event
    if prior and prior.get("v") == v:
        # No change — but if the latest content event has no snapshot ON DISK (it predates the
        # feature, the file was pruned, or the daemon was upgraded under a topic that already
        # had an event), back-fill the baseline now. Without this there is nothing to undo TO,
        # which is exactly when it is needed.
        snap = prior.get("snapshot")
        if not snap or not (versions_dir(root, topic) / snap).is_file():
            append_history(root, topic, {
                "kind": "content", "v": v, "sections": sections,
                "snapshot": snapshot(root, topic, content, v),
                "changed": [], "added": sorted(sections), "removed": [], "baseline": True,
            })
        return
    before = (prior or {}).get("sections") or {}
    snap = snapshot(root, topic, content, v)
    append_history(root, topic, {
        "kind": "content", "v": v, "sections": sections, "snapshot": snap,
        "changed": sorted(k for k in sections if k in before and before[k] != sections[k]),
        "added": sorted(k for k in sections if k not in before),
        "removed": sorted(k for k in before if k not in sections),
    })


# ── undo: content snapshots ───────────────────────────────────────────────
# A worker can write the wrong thing, and there is no VCS under .agents/. Without a
# snapshot the only recovery is retyping. Cheap insurance: keep the last KEEP versions.

KEEP_SNAPSHOTS = 20

def versions_dir(root, topic):
    return topic_paths(root, topic)["dir"] / "versions"


def snapshot(root, topic, content, v):
    d = versions_dir(root, topic)
    d.mkdir(parents=True, exist_ok=True)
    existing = sorted(p.name for p in d.glob("*.html"))
    name = "%03d-%s.html" % (len(existing) + 1, v)
    (d / name).write_text(content, encoding="utf-8")
    pruned = sorted(d.glob("*.html"))[:-KEEP_SNAPSHOTS]
    for old in pruned:
        old.unlink(missing_ok=True)
    if pruned:
        print("canvas: pruned %d old snapshot(s) from %s/versions — the newest %d are kept"
              % (len(pruned), topic, KEEP_SNAPSHOTS))
    return name


# ── HTTP ──────────────────────────────────────────────────────────────────

class Handler(BaseHTTPRequestHandler):
    server_version = "canvas/1.0"
    protocol_version = "HTTP/1.1"
    root = Path(DEFAULT_ROOT)

    def log_message(self, *args):
        pass  # the daemon is a background service; keep the terminal quiet

    # -- helpers ----------------------------------------------------------
    def send(self, code, body, ctype="application/json", cache="no-store"):
        if isinstance(body, str):
            body = body.encode("utf-8")
        self.send_response(code)
        self.send_header("Content-Type", ctype)
        self.send_header("Content-Length", str(len(body)))
        self.send_header("Cache-Control", cache)
        self.end_headers()
        if not getattr(self, "head_only", False):
            self.wfile.write(body)

    def send_json(self, code, obj):
        self.send(code, json.dumps(obj, ensure_ascii=False), "application/json")

    def read_json(self):
        try:
            n = int(self.headers.get("Content-Length") or 0)
            return json.loads(self.rfile.read(n).decode("utf-8")) if n else {}
        except (ValueError, OSError):
            return {}

    def topic(self, parts):
        if len(parts) < 2 or not TOPIC_RE.match(parts[1]):
            return None
        return parts[1]

    # -- routes -----------------------------------------------------------
    def do_HEAD(self):
        # HTTP/1.1 clients (and the canvas page's mermaid probe) HEAD before GET;
        # BaseHTTPRequestHandler answers 501 unless this exists.
        self.head_only = True
        try:
            self.do_GET()
        finally:
            self.head_only = False

    def do_GET(self):
        u = urlparse(self.path)
        parts = [p for p in u.path.split("/") if p]
        path = u.path

        if path == "/health":
            return self.send_json(200, {
                "ok": True, "port": self.server.server_address[1],
                "root": str(self.root.resolve()), "mermaid": MERMAID_ASSET.is_file(),
                "canvases": list_canvases(self.root), "time": now_iso(),
                "rev": script_rev(),
            })

        if path == "/mermaid.js":
            if not MERMAID_ASSET.is_file():
                return self.send(404, "// mermaid not vendored", "application/javascript")
            return self.send(200, MERMAID_ASSET.read_bytes(), "application/javascript",
                             cache="public, max-age=31536000, immutable")

        if path in ("/", "/dashboard"):
            # No dashboard. It existed to start and stop a coordinator and to show the state of a
            # dispatch loop; neither exists. Its two remaining jobs are `canvas.py list` and
            # `build-canvas.py <topic> --new`, so all that is worth serving at the origin is where
            # the topics are.
            links = "".join('<li><a href="/t/%s">%s</a></li>' % (t, t)
                            for t in list_canvases(self.root)) \
                or "<li>(no topics yet)</li>"
            return self.send(200,
                             "<!doctype html><meta charset=utf-8><title>canvas</title>"
                             "<h1>canvas</h1><ul>%s</ul>"
                             "<p><small>every topic and what is waiting: "
                             "<code>canvas.py list</code></small></p>" % links,
                             "text/html; charset=utf-8")

        if parts and parts[0] == "t":
            topic = self.topic(parts)
            if not topic:
                return self.send_json(404, {"error": "bad topic"})
            body = read_text(topic_paths(self.root, topic)["shell"])
            if not body:
                return self.send(404, "<h1>no such canvas</h1>", "text/html; charset=utf-8")
            return self.send(200, body, "text/html; charset=utf-8")

        if parts and parts[0] == "c":
            topic = self.topic(parts)
            if not topic:
                return self.send_json(404, {"error": "bad topic"})
            return self.send(200, read_text(topic_paths(self.root, topic)["content"]),
                             "text/html; charset=utf-8")

        if parts and parts[0] == "v":
            topic = self.topic(parts)
            if not topic:
                return self.send_json(404, {"error": "bad topic"})
            content = read_text(topic_paths(self.root, topic)["content"])
            note_content_change(self.root, topic, content)
            send = read_send(self.root, topic)
            return self.send_json(200, {
                "v": sha(content), "sections": section_hashes(content),
                "pending": len(pending_notes(self.root, topic)),
                "unsent": len(unsent_notes(self.root, topic)),
                # flagged notes await a HUMAN decision, so the page must not report them
                # as work in progress.
                "flagged": len([a for a in pending_notes(self.root, topic) if a.get("flagged")]),
                # Delivery state, so the page can say the notes were COLLECTED. Nothing
                # consumes a send automatically — a session reads `pending`.
                "last_send_ts": (send or {}).get("ts"),
                "send_count": (send or {}).get("count"),
                # What a round just changed, so the page can mark it. See fresh_sections.
                "fresh": fresh_sections(self.root, topic),
            })

        if parts and parts[0] == "h":
            topic = self.topic(parts)
            if not topic:
                return self.send_json(404, {"error": "bad topic"})
            return self.send_json(200, {"topic": topic, "events": load_history(self.root, topic)})

        if parts and parts[0] == "a":
            topic = self.topic(parts)
            if not topic:
                return self.send_json(404, {"error": "bad topic"})
            return self.send_json(200, load_feedback(self.root, topic))

        return self.send_json(404, {"error": "not found"})

    def post_daemon_stop(self):
        """Stop the daemon itself. Answer first, then leave: the reply has to get out before
        the socket goes, or the page reports a failure for a stop that worked."""
        def bye():
            time.sleep(0.25)
            info = read_daemon(self.root)
            if info:
                stop_record(self.root, info)
            self.server.shutdown()
        threading.Thread(target=bye, daemon=True).start()
        return self.send_json(200, {
            "ok": True, "out": "daemon stopping — this page has no data source after this",
            "cmd": "python3 %s/scripts/canvas.py start --root %s" % (SKILL_DIR, self.root)})

    def do_POST(self):
        u = urlparse(self.path)
        parts = [p for p in u.path.split("/") if p]
        body = self.read_json()

        if parts[:2] == ["daemon", "stop"]:
            return self.post_daemon_stop()
        if not parts or parts[0] != "a":
            return self.send_json(404, {"error": "not found"})
        topic = self.topic(parts)
        if not topic:
            return self.send_json(404, {"error": "bad topic"})

        data = load_feedback(self.root, topic)

        if len(parts) > 2 and parts[2] == "send":
            # Send writes state and stops there. A session collects it by reading
            # `pending`; nothing else should ever start a round.
            count = record_send(self.root, topic)
            return self.send_json(200, {"sent": count})

        if len(parts) > 2 and parts[2] == "ack":
            ids = set(body.get("ids") or [])
            for a in data["annotations"]:
                if a.get("id") in ids:
                    a["resolved"] = True
            save_feedback(self.root, topic, data)
            if ids:
                append_history(self.root, topic, {"kind": "resolve", "ids": sorted(ids)})
            return self.send_json(200, data)

        anchor = str(body.get("anchor") or "").strip()
        comment = str(body.get("comment") or "").strip()
        if not anchor or not comment:
            return self.send_json(400, {"error": "anchor and comment are required"})

        severity = body.get("severity") if body.get("severity") in SEVERITIES else "suggestion"
        aid = str(body.get("id") or "").strip()
        entry = {
            "id": aid,
            "anchor": anchor,
            "snippet": str(body.get("snippet") or "")[:120],
            "comment": comment,
            "severity": severity,
            "resolved": False,
            "ts": now_iso(),
        }

        if aid:
            for i, a in enumerate(data["annotations"]):
                if a.get("id") == aid:
                    entry["ts"] = a.get("ts") or entry["ts"]
                    data["annotations"][i] = entry
                    break
            else:
                data["annotations"].append(entry)
        else:
            entry["id"] = "c%d" % int(time.time() * 1000)
            data["annotations"].append(entry)

        append_history(self.root, topic, {
            "kind": "user", "id": entry["id"], "anchor": anchor,
            "comment": comment, "severity": severity})

        save_feedback(self.root, topic, data)
        return self.send_json(200, data)

    def do_DELETE(self):
        u = urlparse(self.path)
        parts = [p for p in u.path.split("/") if p]
        topic = self.topic(parts) if parts and parts[0] == "a" else None
        if not topic:
            return self.send_json(404, {"error": "bad topic"})
        aid = (parse_qs(u.query).get("id") or [""])[0]
        data = load_feedback(self.root, topic)
        gone = [a for a in data["annotations"] if a.get("id") == aid]
        data["annotations"] = [a for a in data["annotations"] if a.get("id") != aid]
        save_feedback(self.root, topic, data)
        if gone:
            append_history(self.root, topic, {
                "kind": "delete", "id": aid, "anchor": gone[0].get("anchor"),
                "comment": gone[0].get("comment")})
        return self.send_json(200, data)


class Server(ThreadingHTTPServer):
    daemon_threads = True
    allow_reuse_address = True


# ── CLI ───────────────────────────────────────────────────────────────────

def port_free(port):
    # SO_REUSEADDR matters: without it a port is unbindable for the TIME_WAIT after a
    # restart, so `stop` then `start` would silently move the daemon and orphan the
    # user's open tab (round-1 bug).
    with socket.socket() as s:
        s.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
        try:
            s.bind(("127.0.0.1", port))
            return True
        except OSError:
            return False


def pick_port(preferred, tries=10):
    """Prefer the port already in use (a restart must not move the origin), then
    the requested one, then the next free ones."""
    for port in dict.fromkeys([preferred]):
        if port and port_free(port):
            return port
    for port in range(DEFAULT_PORT, DEFAULT_PORT + tries):
        if port_free(port):
            return port
    raise SystemExit("canvas: no free port near %d" % DEFAULT_PORT)


def daemon_file(root):
    return root / ".daemon.json"


def pid_alive(pid):
    try:
        os.kill(int(pid), 0)
        return True
    except (OSError, TypeError, ValueError):
        return False


def read_daemon(root):
    try:
        return json.loads(daemon_file(root).read_text())
    except (OSError, ValueError):
        return None


def stop_record(root, info):
    """Keep the daemon record, with `pid` cleared once the process is actually gone.

    The record is kept because the user's tab is bound to that origin and a deliberate restart is
    exactly when it must not move — `start` prefers the recorded port, so dropping the file here
    made the port jump and orphan the open page.

    Called only once the process is gone — `cmd_stop` waits for it and refuses to clear the pid
    otherwise, because a daemon that survives the signal (a refused kill, or one wedged
    mid-request) would hold the port with no record naming it: `stop` could never kill it and
    `start` reported "already running" for a process it could not identify. Seen for real after
    macOS revoked filesystem access under a running daemon.
    """
    info["pid"] = None
    try:
        daemon_file(root).write_text(json.dumps(info, indent=2) + "\n")
    except OSError:
        pass


def wait_gone(pid, seconds=2.0):
    """SIGTERM is asynchronous: the process is still alive microseconds later, so asking once
    right after the signal always says "still running". Wait for it, briefly."""
    deadline = time.time() + seconds
    while time.time() < deadline and pid_alive(pid):
        time.sleep(0.05)
    return not pid_alive(pid)


def is_up(port):
    with socket.socket() as s:
        s.settimeout(0.3)
        return s.connect_ex(("127.0.0.1", port)) == 0


def opener():
    for cmd in ("open", "xdg-open"):
        if shutil.which(cmd):
            return cmd
    return None


def cmd_serve(args):
    root = Path(args.root)
    root.mkdir(parents=True, exist_ok=True)
    port = args.port if args.strict_port else pick_port(args.port)
    Handler.root = root
    server = Server(("127.0.0.1", port), Handler)
    daemon_file(root).write_text(json.dumps(
        {"port": port, "pid": os.getpid(), "root": str(root.resolve()), "started": now_iso()},
        indent=2) + "\n")
    print("canvas: serving %s on http://127.0.0.1:%d" % (root.resolve(), port), flush=True)
    try:
        server.serve_forever()
    except KeyboardInterrupt:
        pass
    finally:
        server.server_close()
    return 0


def cmd_start(args):
    root = Path(args.root)
    root.mkdir(parents=True, exist_ok=True)
    existing = read_daemon(root)
    if existing and is_up(existing.get("port", 0)):
        print("canvas: already running on port %d (pid %s)" % (existing["port"], existing.get("pid")))
        return 0
    # Restart on the previous port if we can — the user's tab is bound to that origin.
    port = pick_port((existing or {}).get("port") or args.port)
    cmd = [sys.executable, str(Path(__file__).resolve()), "serve",
           "--root", str(root), "--port", str(port), "--strict-port"]
    subprocess.Popen(cmd, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL,
                     start_new_session=True)
    for _ in range(50):
        if is_up(port):
            break
        time.sleep(0.1)
    else:
        print("canvas: daemon did not come up on port %d" % port, file=sys.stderr)
        return 1
    # Deliberately opens nothing: starting the daemon must not spawn a browser tab. The page
    # is live and hot-swaps its own sections, so a tab opened once stays correct — including
    # across a restart, because `stop` keeps the port. `canvas.py open <topic>` is the one
    # place that opens, and it is the user's call.
    print("http://127.0.0.1:%d/" % port)
    return 0


def cmd_stop(args):
    root = Path(args.root)
    info = read_daemon(root)
    if not info:
        print("canvas: no daemon recorded for %s" % root)
        return 0
    pid = info.get("pid")
    if pid:
        try:
            os.kill(pid, 15)
        except (ProcessLookupError, PermissionError, KeyError, OSError):
            pass
        if not wait_gone(pid):
            print("canvas: pid %s did not exit — it is still holding port %s.\n"
                  "        Kill it by hand, then retry; the record still names it."
                  % (pid, info.get("port")), file=sys.stderr)
            return 1
    stop_record(root, info)
    print("canvas: stopped (port %s kept for the next start)" % info.get("port"))
    return 0


def cmd_status(args):
    root = Path(args.root)
    info = read_daemon(root)
    if not info or not is_up(info.get("port", 0)):
        print("down")
        return 1
    print("up port=%s pid=%s root=%s canvases=%s"
          % (info["port"], info.get("pid"), info.get("root"), ",".join(list_canvases(root)) or "-"))
    return 0


def cmd_ack(args):
    root = Path(args.root)
    data = load_feedback(root, args.topic)
    ids = set(a["id"] for a in data["annotations"] if not a.get("resolved")) \
        if args.all else set(x for x in args.ids.split(",") if x)
    for a in data["annotations"]:
        if a.get("id") in ids:
            a["resolved"] = True
    save_feedback(root, args.topic, data)
    print("canvas: resolved %d annotation(s) on %s" % (len(ids), args.topic))
    return 0


def cmd_flag(args):
    """Mark notes as needing a decision the session must not make itself.

    Without this a later round cannot tell an escalated note from an unhandled one, and
    re-attempts work an earlier round correctly refused.
    """
    root = Path(args.root)
    data = load_feedback(root, args.topic)
    ids = set(x for x in args.ids.split(",") if x)
    hit = 0
    for a in data["annotations"]:
        if a.get("id") in ids:
            a["flagged"] = True
            a["flag_note"] = args.note
            hit += 1
    save_feedback(root, args.topic, data)
    append_history(root, args.topic, {"kind": "flag", "ids": sorted(ids), "note": args.note})
    print("canvas: flagged %d note(s) as needing a decision" % hit)
    return 0


def cmd_versions(args):
    root = Path(args.root)
    d = versions_dir(root, args.topic)
    files = sorted(d.glob("*.html")) if d.is_dir() else []
    if not files:
        print("canvas: no snapshots for %s" % args.topic)
        return 1
    cur = sha(read_text(topic_paths(root, args.topic)["content"]))
    for f in files:
        print("  %s%s" % (f.name, "   <- current" if f.name.endswith(cur + ".html") else ""))
    return 0


def cmd_restore(args):
    """Put an earlier snapshot back as content.html. Does not rebuild — run
    build-canvas.py after. `--to last` means the most recent state that DIFFERS from
    what is on disk now (the useful undo), not the newest snapshot, which is the current
    state by definition."""
    root = Path(args.root)
    d = versions_dir(root, args.topic)
    files = sorted(d.glob("*.html"))
    if not files:
        raise SystemExit("canvas: no snapshots for %s" % args.topic)
    content_path = topic_paths(root, args.topic)["content"]
    current = read_text(content_path)
    match = None
    if args.to == "last":
        for f in reversed(files):
            if f.read_text(encoding="utf-8") != current:
                match = f
                break
        if not match:
            raise SystemExit("canvas: nothing to undo — every snapshot matches the current "
                             "content")
    else:
        for f in files:
            if args.to in f.name:
                match = f
    if not match:
        raise SystemExit("canvas: no snapshot matching %r (see: canvas.py versions %s)"
                         % (args.to, args.topic))
    if current:
        (d / ("%03d-%s.undone.html" % (len(files) + 1, sha(current)))).write_text(
            current, encoding="utf-8")
    content_path.write_text(match.read_text(encoding="utf-8"), encoding="utf-8")
    print("canvas: restored %s over content.html (the replaced content was snapshotted)" % match.name)
    print("        now run: build-canvas.py %s --root %s" % (args.topic, args.root))
    return 0


def cmd_say(args):
    """Record the agent's own line on the topic, so the page shows both sides of the
    conversation and chat can stay quiet."""
    append_history(Path(args.root), args.topic, {"kind": "agent", "text": " ".join(args.text)})
    print("canvas: said on %s" % args.topic)
    return 0


def cmd_open(args):
    """The one place a browser tab is opened. Call it once per topic: the page is live and
    hot-swaps, so opening again is a second tab showing the same thing."""
    root = Path(args.root)
    info = read_daemon(root)
    if not info or not is_up(info.get("port", 0)):
        print("canvas: daemon is down — run: canvas.py start --root %s" % args.root,
              file=sys.stderr)
        return 1
    url = "http://127.0.0.1:%d/t/%s" % (info["port"], args.topic)
    print(url)
    if opener():
        subprocess.run([opener(), url], check=False)
    return 0


def cmd_send(args):
    """Simulate the page's Send button: raise the signal for a session to collect."""
    count = record_send(Path(args.root), args.topic)
    print("canvas: send raised with %d note(s)" % count)
    return 0


def cmd_show(args):
    """The read side of a round. `pending` names the sections a batch touches; this reads just
    those, so a note in one section costs that section instead of the whole page. With no section
    named it prints the index — id, size, anchors — which is enough to choose without reading."""
    root = Path(args.root)
    content = read_text(topic_paths(root, args.topic)["content"])
    sections = split_sections(content)

    if not args.sections:
        if not sections:
            print("canvas: %s has no sections" % args.topic)
            return 0
        per = {}
        for a, sec in anchor_sections(content).items():
            per.setdefault(sec, []).append(a)
        for sid, text in sections:
            anchors = per.get(sid, [])
            print("%-6s %5d chars  %2d anchors  %s"
                  % (sid, len(text), len(anchors), ", ".join(anchors[:3])
                     + (" …" if len(anchors) > 3 else "")))
        print("--- read one or more: canvas.py show %s %s"
              % (args.topic, " ".join(s for s, _ in sections[:2])))
        return 0

    by_id = dict(sections)
    missing = [s for s in args.sections if s not in by_id]
    if missing:
        raise SystemExit("canvas: no section %s on %s (have: %s)"
                         % (", ".join(missing), args.topic, ", ".join(by_id) or "none"))
    for s in args.sections:
        print(by_id[s])
    return 0


def age_of(path):
    """How long since anything in this topic was written. A temp topic gone quiet is the one to
    drop — this is the number that says so."""
    try:
        newest = max((f.stat().st_mtime for f in Path(path).rglob("*") if f.is_file()),
                     default=None)
    except OSError:
        return "-"
    if newest is None:
        return "-"
    secs = max(0, time.time() - newest)
    for unit, div in (("d", 86400), ("h", 3600), ("m", 60)):
        if secs >= div:
            return "%d%s" % (secs // div, unit)
    return "%ds" % int(secs)


def cmd_drop(args):
    """End a topic and remove it.

    A canvas is one problem, small and short-lived: it exists to make that problem clear, not to
    be kept. Dropping is therefore the ordinary ending, not a destructive exception — but it
    still refuses while notes are unresolved, because those are the user's own words and nothing
    else holds a copy.
    """
    root = Path(args.root)
    d = topic_paths(root, args.topic)["dir"]
    notes = load_feedback(root, args.topic)["annotations"]
    pending = [a for a in notes if not a.get("resolved")]
    if pending and not args.force:
        raise SystemExit(
            "canvas: %s has %d unresolved note(s) — resolve them, or drop --force to discard:\n"
            "        %s" % (args.topic, len(pending),
                          ", ".join(sorted({a["anchor"] for a in pending})[:8])))
    content = read_text(topic_paths(root, args.topic)["content"])
    rounds = len([e for e in load_history(root, args.topic) if e.get("kind") == "agent"])
    size = sum(f.stat().st_size for f in d.rglob("*") if f.is_file())
    # MOVED, not deleted. Dropping is meant to be ordinary — a topic per problem, generated and
    # dropped all day — and an ordinary action must not be able to destroy the user's own words.
    # `--purge` is the only thing that deletes. (Learned by deleting a live topic with this
    # command while "testing" it: the guard below only fires on unresolved notes.)
    if args.purge:
        shutil.rmtree(d)
        where = "purged"
    else:
        trash = root / ".dropped" / ("%s-%s" % (args.topic, time.strftime("%Y%m%dT%H%M%S")))
        n = 2
        while trash.exists():          # two drops in the same second used to nest one inside the
            trash = trash.with_name("%s-%d" % (trash.name, n))   # other, so the printed path was wrong
            n += 1
        trash.parent.mkdir(parents=True, exist_ok=True)
        shutil.move(str(d), str(trash))
        where = "recoverable at %s  (mv it back to resume)" % trash
    print("canvas: dropped %s — %d section(s), %d note(s), %d round(s), %.1f KB · %s"
          % (args.topic, len(split_sections(content)), len(notes), rounds, size / 1024.0, where))
    return 0


def cmd_list(args):
    """Every topic under each root, and what is waiting there. Read straight from files,
    so it works with no daemon running."""
    for raw in (args.root or [DEFAULT_ROOT]):
        root = Path(raw)
        topics = list_canvases(root)
        if not topics:
            print("%s: no topics" % root)
        else:
            print("%s" % root)
        for topic in topics:
            notes = load_feedback(root, topic)["annotations"]
            pending = [a for a in notes if not a.get("resolved")]
            flagged = [a for a in pending if a.get("flagged")]
            send = read_send(root, topic) or {}
            print("  %-24s pending=%d unsent=%d flagged=%d idle=%-4s"
                  % (topic, len(pending), len(unsent_notes(root, topic)), len(flagged),
                     age_of(root / topic)))
        trash = root / ".dropped"
        if trash.is_dir() and any(trash.iterdir()):
            print("  (%d dropped topic(s) recoverable under %s)" % (len(list(trash.iterdir())), trash))
    return 0


def cmd_pending(args):
    """The round's whole input: one line per note with its id (so `ack`/`flag` can act), the
    section it lives in (so only that section is read), and its state."""
    root = Path(args.root)
    content = read_text(topic_paths(root, args.topic)["content"])
    index = anchor_sections(content)
    batch, held = batch_notes(root, args.topic)

    def rec(a):
        r = {k: v for k, v in a.items() if k != "resolved"}   # always False here
        r["section"] = section_for(a["anchor"], index)
        r["state"] = states_of(a, index)
        r["in_batch"] = a in batch
        return r

    if args.json:
        print(json.dumps([rec(a) for a in batch + held], indent=2, ensure_ascii=False))
        return 0

    if not batch and not held:
        print("canvas: nothing pending on %s" % args.topic)
        return 0

    def line(a, held=False):
        sec = section_for(a["anchor"], index)
        state = [s.upper() for s in states_of(a, index)]
        if held:
            state.append("HELD")
        tag = "%s·%s" % (a["id"], a["severity"]) + (" " + " ".join(state) if state else "")
        snip = '  — "%s"' % (a.get("snippet") or "")[:60] if needs_snippet(a) else ""
        return "%-6s %-30s %-24s %s%s" % (sec or "(gone)", a["anchor"], tag, a["comment"], snip)

    for a in sorted(batch, key=lambda a: (section_for(a["anchor"], index) or "~", a["anchor"])):
        print(line(a))
    flagged = [a for a in batch if a.get("flagged")]
    orphan = [a for a in batch if not section_for(a["anchor"], index)]
    print("--- %d in this batch · %d flagged (yours to decide) · %d orphan"
          % (len(batch), len(flagged), len(orphan)))
    if held:
        print("--- %d held back — typed after the last Send; press Send to include them:"
              % len(held))
        for a in held:
            print(line(a, held=True))
    sections = sorted({section_for(a["anchor"], index) for a in batch} - {None})
    if sections:
        print("--- sections to touch: %s" % ", ".join(sections))
    return 0


def main(argv=None):
    p = argparse.ArgumentParser(prog="canvas.py", description="canvas daemon")
    sub = p.add_subparsers(dest="cmd", required=True)

    def common(sp):
        sp.add_argument("--root", default=DEFAULT_ROOT, help="canvas dir (default %s)" % DEFAULT_ROOT)
        return sp

    sp = common(sub.add_parser("serve"))
    sp.add_argument("--port", type=int, default=DEFAULT_PORT)
    sp.add_argument("--strict-port", action="store_true")
    sp.set_defaults(func=cmd_serve)

    sp = common(sub.add_parser("start"))
    sp.add_argument("--port", type=int, default=DEFAULT_PORT)
    sp.set_defaults(func=cmd_start)

    common(sub.add_parser("stop")).set_defaults(func=cmd_stop)
    common(sub.add_parser("status")).set_defaults(func=cmd_status)

    sp = common(sub.add_parser("flag"))
    sp.add_argument("topic")
    sp.add_argument("--ids", required=True, help="comma-separated annotation ids")
    sp.add_argument("--note", default="", help="why it needs a decision")
    sp.set_defaults(func=cmd_flag)

    sp = common(sub.add_parser("versions"))
    sp.add_argument("topic")
    sp.set_defaults(func=cmd_versions)

    sp = common(sub.add_parser("restore"))
    sp.add_argument("topic")
    sp.add_argument("--to", required=True, help="snapshot name fragment, or 'last'")
    sp.add_argument("--force", action="store_true",
                    help="(accepted for compatibility; the replaced content is always kept)")
    sp.set_defaults(func=cmd_restore)

    sp = common(sub.add_parser("say"))
    sp.add_argument("topic")
    sp.add_argument("text", nargs="+")
    sp.set_defaults(func=cmd_say)

    sp = common(sub.add_parser("ack"))
    sp.add_argument("topic")
    sp.add_argument("--ids", default="", help="comma-separated annotation ids")
    sp.add_argument("--all", action="store_true", help="resolve every pending annotation")
    sp.set_defaults(func=cmd_ack)

    sp = common(sub.add_parser("open"))
    sp.add_argument("topic")
    sp.set_defaults(func=cmd_open)

    sp = common(sub.add_parser("pending"))
    sp.add_argument("topic")
    sp.add_argument("--json", action="store_true")
    sp.set_defaults(func=cmd_pending)

    sp = common(sub.add_parser("send"))
    sp.add_argument("topic")
    sp.set_defaults(func=cmd_send)

    sp = common(sub.add_parser("show"))
    sp.add_argument("topic")
    sp.add_argument("sections", nargs="*", help="section ids; omit to list them")
    sp.set_defaults(func=cmd_show)

    sp = common(sub.add_parser("drop"))
    sp.add_argument("topic")
    sp.add_argument("--force", action="store_true",
                    help="discard unresolved notes too")
    sp.add_argument("--purge", action="store_true",
                    help="delete instead of moving to .dropped/")
    sp.set_defaults(func=cmd_drop)

    sp = sub.add_parser("list", help="every topic and what is waiting (no daemon needed)")
    sp.add_argument("--root", action="append", default=None,
                    help="canvas dir; repeatable (default %s)" % DEFAULT_ROOT)
    sp.set_defaults(func=cmd_list)

    args = p.parse_args(argv)
    require_topic(getattr(args, "topic", None))
    require_known_topic(getattr(args, "root", None),
                        getattr(args, "topic", None),
                        getattr(args, "cmd", None))
    return args.func(args)


if __name__ == "__main__":
    sys.exit(main())
