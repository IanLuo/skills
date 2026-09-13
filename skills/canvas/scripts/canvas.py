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

    GET    /                     index of topics
    GET    /health               {ok, port, root, mermaid, canvases}
    GET    /t/<topic>            built shell
    GET    /c/<topic>            content partial (hot-swap source)
    GET    /v/<topic>            {v, sections, pending, unsent, sent, sweep} — poll this
    GET    /a/<topic>            annotations
    GET    /h/<topic>            history events, oldest first
    POST   /a/<topic>            upsert one annotation
    POST   /a/<topic>/ack        {"ids":[...]} → mark resolved
    DELETE /a/<topic>?id=<id>    delete one annotation
    POST   /a/<topic>/send       the user pressed Send — the sweep wakes the coordinator
    GET    /mermaid.js           vendored mermaid (404 if not vendored)

CLI:

    canvas.py serve [--root DIR] [--port N]      run in the foreground
    canvas.py start [--root DIR] [--port N] [--open TOPIC]
    canvas.py stop  [--root DIR]
    canvas.py status [--root DIR]
    canvas.py pending <topic> [--root DIR] [--json]
    canvas.py send <topic>                       simulate the page's Send button
    canvas.py ack <topic> [--ids c1,c2 | --all]
    canvas.py say <topic> "<one line>"           record the agent's reply on the topic
    canvas.py flag <topic> --ids c1,c2 [--note "why"]    escalate notes to the coordinator
    canvas.py versions <topic>                   list content snapshots (undo points)
    canvas.py restore <topic> --to last|<fragment>       put a snapshot back
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
WRITE_COMMANDS = ("say", "flag", "ack", "restore")


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


# The daemon runs whatever code it was started with, so a daemon left running after an
# upgrade silently lacks the routes this file has. The revision is reported on /health and
# /topics so that mismatch is loud instead of mysterious.
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


def load_feedback(root, topic):
    raw = read_text(topic_paths(root, topic)["feedback"])
    if not raw:
        return {"topic": topic, "updated": now_iso(), "annotations": []}
    try:
        data = json.loads(raw)
    except ValueError:
        return {"topic": topic, "updated": now_iso(), "annotations": []}
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
# send signal instead, and the daemon's sweep notices it and wakes the coordinator. The
# signal is sticky: a send that arrives while no agent can be woken stays on disk and is
# served by the next sweep, so it is never lost.

# herdr states that mean "not in a round", so the sweep may wake this agent. Kept next to
# the wake path and imported by canvas-worker.py so the two cannot drift.
IDLE_STATES = (None, "idle", "done", "exited", "unknown", "stopped")


def send_path(root, topic):
    return topic_paths(root, topic)["dir"] / "send.json"


def stop_path(root, topic):
    return topic_paths(root, topic)["dir"] / "STOP"


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
    carries. It deliberately does NOT wake anyone — the daemon's sweep is the single wake
    path, so one Send can never become two rounds. Returns the count."""
    notes = unsent_notes(root, topic) or pending_notes(root, topic)
    write_send(root, topic, {"ts": now_iso(), "count": len(notes), "consumed_at": None})
    append_history(root, topic, {"kind": "send", "count": len(notes)})
    return len(notes)


def pending_notes(root, topic):
    return [a for a in load_feedback(root, topic)["annotations"] if not a.get("resolved")]


def unsent_notes(root, topic):
    """Notes written since the last send — what the Send button is offering."""
    send = read_send(root, topic)
    sent_at = (send or {}).get("ts") or ""
    return [a for a in pending_notes(root, topic) if a.get("ts", "") > sent_at]


def unread_notes(root, topic):
    """Unresolved notes that were already sent but never handled.

    The message a missing or short-lived watcher drops. A send used to be consumed on
    DELIVERY, so if the waiter then died the note was stranded forever with nothing to
    re-trigger anyone. These re-trigger a waiter instead, which is what makes feedback
    survive a dead worker, a restart, or a topic with no watcher at all.

    Flagged notes are excluded: they await a decision no worker may make, so including
    them would re-trigger a round every time and never resolve.
    """
    send = read_send(root, topic)
    sent_at = (send or {}).get("ts") or ""
    if not sent_at:
        return []
    return [a for a in pending_notes(root, topic)
            if not a.get("flagged") and a.get("ts", "") <= sent_at
            and a.get("attempts", 0) < MAX_ROUND_ATTEMPTS]


# ── dashboard: one view of every topic, from files ───────────────────────

def read_coordinator_record(root):
    """`.coordinator.json`, written by canvas-worker.py `coordinator start`. This is the only
    wake target: it is the record the live architecture actually produces. Repeating it
    verbatim, not guessing liveness — whether the pane still exists is herdr's answer."""
    try:
        return json.loads((root / ".coordinator.json").read_text(encoding="utf-8"))
    except (OSError, ValueError):
        return None


def parse_ts(s):
    try:
        return datetime.strptime(str(s), "%Y-%m-%dT%H:%M:%SZ").replace(tzinfo=timezone.utc).timestamp()
    except (ValueError, TypeError):
        return None


def send_latency(events):
    """Seconds from a Send to the next agent reply — the interval that stayed invisible while
    a canvas was unowned. Most recent one, or None if a round has not been answered yet."""
    last, waiting = None, None
    for e in events:
        if e.get("kind") == "send":
            waiting = parse_ts(e.get("ts"))
        elif e.get("kind") == "agent" and waiting:
            done = parse_ts(e.get("ts"))
            if done:
                last = int(done - waiting)
            waiting = None
    return last


def topic_summary(root, topic):
    """Everything the dashboard shows about one topic, derived from files — the daemon adds
    no memory of its own, so a restart cannot change what the page says."""
    content = read_text(topic_paths(root, topic)["content"])
    history = load_history(root, topic)
    notes = load_feedback(root, topic)["annotations"]
    send = read_send(root, topic) or {}
    replies = [e for e in history if e.get("kind") == "agent"]
    return {
        "topic": topic,
        "url": "/t/" + topic,
        "has_content": bool(content),
        "sections": len(section_hashes(content)) if content else 0,
        "notes": len(notes),
        "pending": len([a for a in notes if not a.get("resolved")]),
        "flagged": len([a for a in notes if a.get("flagged") and not a.get("resolved")]),
        "unsent": len(unsent_notes(root, topic)),
        "sent_at": send.get("ts"),
        "consumed_at": send.get("consumed_at"),
        "queued": bool(send.get("ts") and not send.get("consumed_at")),
        "stop_requested": stop_path(root, topic).exists(),
        # Liveness, derived from files: is there a coordinator to wake, and is the sweep on?
        "coordinator": bool(read_coordinator_record(root)),
        "sweep": SWEEP_SECONDS,
        "rounds": len(replies),
        "last_reply": replies[-1].get("ts") if replies else None,
        "latency_s": send_latency(history),
    }


def run_action(argv, timeout=120):
    """Run one of the skill's own CLIs and hand back its real result. Paths go through as a
    list, never a shell string; the caller sees the exit code and both streams."""
    try:
        p = subprocess.run([sys.executable] + [str(a) for a in argv],
                           capture_output=True, text=True, timeout=timeout)
    except subprocess.TimeoutExpired:
        return {"ok": False, "code": None, "out": "", "err": "timed out after %ss" % timeout}
    except OSError as e:
        return {"ok": False, "code": None, "out": "", "err": str(e)}
    return {"ok": p.returncode == 0, "code": p.returncode,
            "out": (p.stdout or "").strip()[-4000:], "err": (p.stderr or "").strip()[-2000:]}


# ── waking the coordinator ────────────────────────────────────────────────
#
# Who does the work is decided by file state, not by who is parked. The daemon already
# knows which topics have unread work, so it wakes the root's coordinator with herdr. Best
# effort on purpose: no coordinator or no herdr leaves the Send on disk, which the sweep
# retries and the page says out loud.

def coordinator_is_idle(root):
    """True unless herdr says the recorded coordinator is mid-round.

    Locked decision: wake only when work is unread AND the coordinator is idle — a prompt
    sent into a running round queues, and dispatching twice for one note is the storm this
    design exists to prevent (architecture.md, load-bearing structure 3). An unreadable
    status counts as idle on purpose: refusing to wake on a status we cannot read would be a
    silent outage, the worse failure.
    """
    coord = read_coordinator_record(root) or {}
    name = coord.get("agent_name")
    exe = shutil.which("herdr")
    if not name or not exe:
        return True                       # notify_worker reports the missing coordinator
    try:
        p = subprocess.run([exe, "agent", "get", name], capture_output=True, text=True,
                           timeout=15)
        if p.returncode != 0:
            return True
        res = json.loads(p.stdout).get("result") or {}
    except (subprocess.TimeoutExpired, OSError, ValueError):
        return True
    agent = res.get("agent") or {}
    return (agent.get("agent_status") or res.get("agent_status")) in IDLE_STATES


def notify_worker(root, topic, count):
    coord = read_coordinator_record(root) or {}
    # The target is the root's COORDINATOR. It used to be a per-topic worker.json — a record
    # only the rejected per-topic watcher ever wrote, so the push read a file nothing produced
    # and 18 of 21 wakes reached nobody. Locked: architecture.md, load-bearing structure 4.
    name = coord.get("agent_name") or coord.get("pane_id")
    if not name:
        return {"ok": False, "why": "no coordinator recorded"}
    exe = shutil.which("herdr")
    if not exe:
        return {"ok": False, "why": "herdr not on PATH"}
    prompt = ("Notes arrived on canvas %r (%d). Dispatch one round: "
              "canvas-worker.py round %s --root %s --wait. Do not edit content yourself."
              % (topic, count, topic, root))
    try:
        p = subprocess.run([exe, "agent", "prompt", name, prompt],
                           capture_output=True, text=True, timeout=20)
    except subprocess.TimeoutExpired:
        return {"ok": False, "why": "herdr prompt timed out"}
    except OSError as e:
        return {"ok": False, "why": str(e)}
    return {"ok": p.returncode == 0, "agent": name,
            "why": None if p.returncode == 0 else (p.stderr or p.stdout).strip()[:200]}


def wake_and_log(root, topic, count):
    """Wake in the background so a Send never waits on herdr, and log the outcome either way."""
    res = notify_worker(root, topic, count)
    try:
        append_history(root, topic, {"kind": "wake", "ok": bool(res.get("ok")),
                                     "why": res.get("why") or "",
                                     "agent": res.get("agent") or ""})
    except OSError:
        pass
    return res


# ── the sweep: the daemon is what notices work ────────────────
#
# There is no waiter and no park. The daemon is the only always-on component and already
# holds the state this decision needs (work unread, coordinator idle, target known), so it
# sweeps for unread work and wakes the coordinator. This is the single wake path: the Send
# route writes state and nothing else.
# Locked: architecture.md, load-bearing structure (2), (3) and (4).

SWEEP_LAST = {}            # (root, topic) -> last wake ts, so one nudge per interval
SWEEP_WAKES = []           # wake ts, for the global cap
SWEEP_MIN_INTERVAL = 60    # seconds between wakes for one topic
SWEEP_MAX_WAKES = 5        # wakes per SWEEP_WINDOW, however many topics are waiting
SWEEP_WINDOW = 600         # seconds
SWEEP_SECONDS = 0          # the interval this daemon was started with; 0 = off


def work_by_topic(root):
    """Every topic with work the daemon should dispatch: notes that were sent and never
    resolved (the retry path), or a freshly raised Send — which re-arms even notes that hit
    the retry cap, because pressing Send again is the user's way to say "try once more"."""
    out = []
    for topic in list_canvases(root):
        unread = unread_notes(root, topic)
        send = read_send(root, topic) or {}
        raised = bool(send and not send.get("consumed_at"))
        pending = [a for a in pending_notes(root, topic) if not a.get("flagged")]
        if unread:
            out.append((topic, len(unread)))
        elif raised and pending:
            out.append((topic, send.get("count") or len(pending)))
    return out


def sweep_once(root, now=None, interval=None):
    """One pass. Returns the wakes it made; silent when the rate limits hold or the
    coordinator is busy, because this runs every few seconds and an unanswered wake must not
    write a failure line each time.

    A successful wake counts the attempt and consumes the send (`take_work`), so a note that
    was handed over is not handed over again, and one nobody resolved retries exactly
    MAX_ROUND_ATTEMPTS times before it is `stuck`."""
    root = Path(root)
    now = time.time() if now is None else now
    interval = SWEEP_MIN_INTERVAL if interval is None else interval
    global SWEEP_WAKES
    SWEEP_WAKES = [t for t in SWEEP_WAKES if now - t < SWEEP_WINDOW]
    if (root / ".COORDINATOR_STOP").exists():
        return []                          # a stop was requested; waking again would fight it
    woken = []
    for topic, count in work_by_topic(root):
        key = (str(root.resolve()), topic)
        if now - SWEEP_LAST.get(key, 0) < interval:
            continue
        if len(SWEEP_WAKES) >= SWEEP_MAX_WAKES:
            break                          # cap the noise: five unanswered wakes is enough
        if not coordinator_is_idle(root):
            break                          # a round is in flight — wake after it, not into it
        SWEEP_LAST[key] = now
        SWEEP_WAKES.append(now)
        res = wake_and_log(root, topic, count)
        if res.get("ok"):
            take_work(root, topic)         # count the attempt, consume the send
        woken.append(res)
    return woken


def sweep_loop(root, seconds):
    while True:
        try:
            sweep_once(root)
        except Exception:
            pass                           # a sweep must never take the server down
        time.sleep(max(0.5, seconds))


def start_sweep(root, seconds):
    """Start the sweep thread and make its interval observable on /health."""
    global SWEEP_SECONDS
    SWEEP_SECONDS = float(seconds or 0)
    if SWEEP_SECONDS <= 0:
        return None
    t = threading.Thread(target=sweep_loop, args=(Path(root), SWEEP_SECONDS), daemon=True)
    t.start()
    return t


def take_work(root, topic):
    """Hand over a topic's batch: count the attempt, consume the send. The one place it
    happens, so the wake path and the page cannot drift apart."""
    unread = unread_notes(root, topic)
    if unread:
        data = load_feedback(root, topic)
        ids = set(a["id"] for a in unread)
        for a in data["annotations"]:
            if a.get("id") in ids:
                a["attempts"] = a.get("attempts", 0) + 1
        save_feedback(root, topic, data)
    send = read_send(root, topic)
    if send and not send.get("consumed_at"):
        send["consumed_at"] = now_iso()
        write_send(root, topic, send)
    return unread or pending_notes(root, topic)


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

# A note that a round fails to resolve (neither acked nor flagged) would otherwise re-trigger
# a fresh round forever — an endless agent loop, which is worse than the silent drop it was
# meant to fix. Two tries, then it is `stuck` and the page says so.
MAX_ROUND_ATTEMPTS = 2


def stuck_notes(root, topic):
    send = read_send(root, topic)
    sent_at = (send or {}).get("ts") or ""
    return [a for a in pending_notes(root, topic)
            if not a.get("flagged") and a.get("ts", "") <= sent_at
            and a.get("attempts", 0) >= MAX_ROUND_ATTEMPTS]


def versions_dir(root, topic):
    return topic_paths(root, topic)["dir"] / "versions"


def snapshot(root, topic, content, v):
    d = versions_dir(root, topic)
    d.mkdir(parents=True, exist_ok=True)
    existing = sorted(p.name for p in d.glob("*.html"))
    name = "%03d-%s.html" % (len(existing) + 1, v)
    (d / name).write_text(content, encoding="utf-8")
    for old in sorted(d.glob("*.html"))[:-KEEP_SNAPSHOTS]:
        old.unlink(missing_ok=True)
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
                # 0 = the sweep is off (tests, or a deliberately silent daemon).
                "sweep": SWEEP_SECONDS,
            })

        if path == "/mermaid.js":
            if not MERMAID_ASSET.is_file():
                return self.send(404, "// mermaid not vendored", "application/javascript")
            return self.send(200, MERMAID_ASSET.read_bytes(), "application/javascript",
                             cache="public, max-age=31536000, immutable")

        if path in ("/", "/dashboard"):
            body = read_text(SKILL_DIR / "dashboard.html")
            if not body:
                body = ("<!doctype html><meta charset=utf-8><h1>canvas</h1>"
                        "<p>dashboard.html is missing from %s</p>" % SKILL_DIR)
            return self.send(200, body, "text/html; charset=utf-8")

        if path == "/topics":
            info = read_daemon(self.root) or {}
            try:
                disk = sha(Path(__file__).read_text(encoding="utf-8"))
            except OSError:
                disk = None
            return self.send_json(200, {
                "rev": script_rev(),
                # The daemon was started from an older canvas.py: every route this page uses
                # may be missing. Worth saying out loud — it cost a debugging round today.
                "stale": bool(disk and disk != script_rev()),
                "root": str(self.root.resolve()),
                "port": self.server.server_address[1],
                "pid": info.get("pid"),
                "started": info.get("started"),
                "topics": [topic_summary(self.root, t) for t in list_canvases(self.root)],
            })

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
                "unread": len(unread_notes(self.root, topic)),
                "stuck": len(stuck_notes(self.root, topic)),
                # flagged notes await a HUMAN decision and are never dispatched, so the
                # page must not report them as work in progress.
                "flagged": len([a for a in pending_notes(self.root, topic) if a.get("flagged")]),
                "sent": bool(send and not send.get("consumed_at")),
                # Delivery state, so the page can say the notes were COLLECTED. Without
                # this the user presses Send again because nothing appears to happen.
                "last_send_ts": (send or {}).get("ts"),
                "send_consumed_at": (send or {}).get("consumed_at"),
                "send_count": (send or {}).get("count"),
                "stop_requested": stop_path(self.root, topic).exists(),
                # Liveness: the daemon notices work by itself and wakes the coordinator, so
                # the page's question is "is the sweep armed and is there a coordinator to
                # wake", never "is someone parked".
                "sweep": SWEEP_SECONDS,
                "coordinator": bool(read_coordinator_record(self.root)),
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

    # -- dashboard actions: manage topics and processes ----------------------
    def post_topics(self, parts, body):
        """Manage a topic from the dashboard. Every branch runs one of the skill's own CLIs
        and hands back its real output — no shell strings, nothing the CLI cannot do."""
        root = self.root
        scripts = SKILL_DIR / "scripts"
        if len(parts) == 1:                        # POST /topics {topic: slug} → create
            new = str(body.get("topic") or "").strip()
            if not TOPIC_RE.match(new):
                return self.send_json(400, {"error": "topic must match ^[a-z0-9][a-z0-9-]{0,63}$"})
            if topic_paths(root, new)["content"].is_file():
                return self.send_json(409, {"error": "%s already exists" % new})
            return self.send_json(200, run_action(
                [scripts / "build-canvas.py", new, "--root", root, "--new"], 60))

        topic = self.topic(parts)
        if not topic:
            return self.send_json(404, {"error": "bad topic"})
        if not topic_paths(root, topic)["content"].is_file():
            return self.send_json(404, {"error": "no canvas called %s" % topic})
        action = "/".join(parts[2:])
        if action == "rebuild":
            return self.send_json(200, run_action(
                [scripts / "build-canvas.py", topic, "--root", root], 120))
        return self.send_json(404, {"error": "unknown action %r" % action})

    def post_daemon_stop(self):
        """Stop the daemon itself. Answer first, then leave: the reply has to get out before
        the socket goes, or the page reports a failure for a stop that worked."""
        def bye():
            time.sleep(0.25)
            try:
                daemon_file(self.root).unlink()
            except OSError:
                pass
            self.server.shutdown()
        threading.Thread(target=bye, daemon=True).start()
        return self.send_json(200, {
            "ok": True, "out": "daemon stopping — this page has no data source after this",
            "cmd": "python3 %s/scripts/canvas.py start --root %s" % (SKILL_DIR, self.root)})

    def do_POST(self):
        u = urlparse(self.path)
        parts = [p for p in u.path.split("/") if p]
        body = self.read_json()

        if parts and parts[0] == "topics":
            return self.post_topics(parts, body)
        if parts[:2] == ["daemon", "stop"]:
            return self.post_daemon_stop()
        # One coordinator per root: exactly two root-level commands, and no per-topic worker
        # plumbing. The coordinator is the agent the daemon wakes; nothing registers it here.
        if parts[:2] == ["coordinator", "start"]:
            return self.send_json(200, run_action(
                [SKILL_DIR / "scripts" / "canvas-worker.py", "coordinator", "start",
                 "--root", str(self.root)], timeout=180))
        if parts[:2] == ["coordinator", "stop"]:
            return self.send_json(200, run_action(
                [SKILL_DIR / "scripts" / "canvas-worker.py", "coordinator", "stop",
                 "--root", str(self.root), "--wait", "5"], timeout=180))

        if not parts or parts[0] != "a":
            return self.send_json(404, {"error": "not found"})
        topic = self.topic(parts)
        if not topic:
            return self.send_json(404, {"error": "bad topic"})

        data = load_feedback(self.root, topic)

        if len(parts) > 2 and parts[2] == "send":
            # The Send writes state and stops there. Waking is the daemon's job, on the
            # sweep — a second wake path here is how one Send became two rounds.
            count = record_send(self.root, topic)
            return self.send_json(200, {"sent": count, "wake": "sweep"})

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
    start_sweep(root, args.sweep)
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
    url = "http://127.0.0.1:%d/%s" % (port, ("t/" + args.open_topic) if args.open_topic else "")
    print(url.rstrip("/"))
    if args.open_topic and opener():
        subprocess.run([opener(), url], check=False)
    return 0


def cmd_stop(args):
    root = Path(args.root)
    info = read_daemon(root)
    if not info:
        print("canvas: no daemon recorded for %s" % root)
        return 0
    try:
        os.kill(info["pid"], 15)
    except (ProcessLookupError, PermissionError, KeyError):
        pass
    try:
        daemon_file(root).unlink()
    except OSError:
        pass
    print("canvas: stopped (pid %s, port %s)" % (info.get("pid"), info.get("port")))
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
    """Mark notes as needing a decision the worker must not make itself.

    Without this a fresh worker cannot tell an escalated note from an unhandled one, and
    re-attempts work its predecessor correctly refused.
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
    root = Path(args.root)
    info = read_daemon(root)
    if not info or not is_up(info.get("port", 0)):
        print("canvas: daemon is down — run: canvas.py start --root %s --open %s"
              % (args.root, args.topic), file=sys.stderr)
        return 1
    url = "http://127.0.0.1:%d/t/%s" % (info["port"], args.topic)
    print(url)
    if opener():
        subprocess.run([opener(), url], check=False)
    return 0


def cmd_send(args):
    """Simulate the page's Send button: raise the signal, let the sweep do the waking."""
    count = record_send(Path(args.root), args.topic)
    print("canvas: send raised with %d note(s)" % count)
    return 0


def cmd_pending(args):
    root = Path(args.root)
    data = load_feedback(root, args.topic)
    pending = [a for a in data["annotations"] if not a.get("resolved")]
    if args.json:
        print(json.dumps(pending, indent=2, ensure_ascii=False))
        return 0
    if not pending:
        print("canvas: no pending annotations on %s" % args.topic)
        return 0
    for a in pending:
        print("%s (%s): %s — \"%s\"" % (a["anchor"], a["severity"], a["comment"], a.get("snippet", "")))
    print("--- %d pending" % len(pending))
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
    sp.add_argument("--sweep", type=float, default=2.0,
                    help="seconds between sweeps for unread work; 0 disables (default 2)")
    sp.set_defaults(func=cmd_serve)

    sp = common(sub.add_parser("start"))
    sp.add_argument("--port", type=int, default=DEFAULT_PORT)
    sp.add_argument("--open", dest="open_topic", default=None, metavar="TOPIC")
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

    args = p.parse_args(argv)
    require_topic(getattr(args, "topic", None))
    require_known_topic(getattr(args, "root", None),
                        getattr(args, "topic", None),
                        getattr(args, "cmd", None))
    return args.func(args)


if __name__ == "__main__":
    sys.exit(main())
