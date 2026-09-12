#!/usr/bin/env python3
"""canvas-worker.py — run the canvas feedback loop on a WORKER agent in a herdr pane.

Why this exists
---------------
`canvas.py wait` blocks whoever runs it, and reading notes, editing sections, building,
verifying and replying costs a lot of context. Run that in the main session and the user
is blocked for minutes while their context is spent on the loop. So the loop runs in its
own pane, on its own agent, with its own context — and the main session stays free to
talk to the user.

Usage
-----
    canvas-worker.py start  <topic> [--root DIR] [--kind pi] [--port N] [--name NAME]
    canvas-worker.py ensure <topic> [--root DIR] [--kind pi] [--port N] [--name NAME]
    canvas-worker.py stop   <topic> [--root DIR] [--now] [--wait SECONDS]
    canvas-worker.py stop   --all [--root DIR] [--now]
    canvas-worker.py list   [--root DIR]
    canvas-worker.py status <topic> [--root DIR]
    canvas-worker.py card   <topic> [--root DIR] [--print]

`stop` is graceful by default: it writes <topic>/STOP, which the worker checks before
each wait, so an in-flight round finishes instead of being killed mid-edit. It then waits
for that round to end (bounded by `--wait`, default 60s) and closes the pane, so a stop
leaves nothing behind. `--now` closes the pane immediately — only do that when nothing is
in flight. A stop that was left half-done (pane open, STOP set, nobody listening) is
recovered by `ensure`, which starts a worker only when none is parked.

Requires herdr: HERDR_ENV=1 and the herdr CLI. Outside herdr, this exits with the
instruction to work the loop inline instead.
"""

import argparse
import json
import os
import re
import shutil
import subprocess
import sys
import time
from datetime import datetime, timezone
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))
from canvas import (DEFAULT_ROOT, DEFAULT_PORT, TOPIC_RE, read_daemon, is_up,  # noqa: E402
                    load_history, pending_notes, read_parked, read_registry,
                    read_send, unsent_notes)

SKILL_DIR = Path(__file__).resolve().parent.parent
CARD_TEMPLATE = SKILL_DIR / "references" / "worker-card.md"
ROUND_TEMPLATE = SKILL_DIR / "references" / "round-card.md"
COORDINATOR_TEMPLATE = SKILL_DIR / "references" / "coordinator-card.md"
WORKER_FILE = "worker.json"


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


def worker_path(root, topic):
    return root / topic / WORKER_FILE


def load_worker(root, topic):
    try:
        return json.loads(worker_path(root, topic).read_text(encoding="utf-8"))
    except (OSError, ValueError):
        return None


def save_worker(root, topic, data):
    p = worker_path(root, topic)
    p.parent.mkdir(parents=True, exist_ok=True)
    p.write_text(json.dumps(data, indent=2) + "\n", encoding="utf-8")


def herdr(*args, check=True):
    """Run the herdr CLI and return its parsed JSON result."""
    exe = shutil.which("herdr")
    if not exe:
        raise SystemExit("canvas-worker: herdr CLI not found on PATH")
    p = subprocess.run([exe] + list(args), capture_output=True, text=True)
    if check and p.returncode != 0:
        raise SystemExit("canvas-worker: herdr %s failed: %s"
                         % (" ".join(args[:2]), (p.stderr or p.stdout).strip()[:300]))
    try:
        return json.loads(p.stdout)
    except ValueError:
        return {"raw": p.stdout.strip()}


# Agent states that mean no round is in flight. start/stop/ensure must agree on this, or
# one of them replaces a worker another one is still counting on.
# "done" is a real terminal state: an agent that finished its turn but is not idle.
# Without it, `stop` waits out its whole grace period and leaves the pane open.
IDLE_STATES = (None, "idle", "done", "exited", "unknown", "stopped")


def agent_status(agent_name):
    """herdr's status for an agent name — 'unknown' when it cannot be read at all."""
    res = herdr("agent", "get", agent_name, check=False).get("result") or {}
    agent = res.get("agent") or {}
    return agent.get("agent_status") or res.get("agent_status") or "unknown"


def agent_record(agent_name):
    """The agent herdr knows under this name, or None.

    Unlike agent_status, this tells "no such agent" apart from "cannot read it" — which is
    what an idempotent `coordinator start` needs: a live coordinator must be recognised
    BEFORE a pane is split for a second one.
    """
    return (herdr("agent", "get", agent_name, check=False).get("result") or {}).get("agent")


def agent_state(root, topic):
    """(worker.json, herdr status) for the recorded worker; (None, None) if never started."""
    info = load_worker(root, topic)
    return (info, agent_status(info.get("agent_name", ""))) if info else (None, None)


def wait_for_idle(agent_name, timeout):
    """Poll until the agent's turn ends; return the last status seen.

    A worker parked in `wait` reads as 'working' (a tool call is in flight) — STOP makes
    the daemon return that call immediately, so the turn ends within a second or two. A
    worker mid-round is also 'working' until it finishes, which is what grace has to mean.
    """
    deadline = time.time() + max(0, timeout)
    while True:
        status = agent_status(agent_name)
        if status in IDLE_STATES or time.time() >= deadline:
            return status
        time.sleep(1.0)


def listening_count(root, topic, port):
    """Waiters parked in `canvas.py wait` right now — the page's 'agent listening' signal."""
    if not port:
        return 0
    try:
        import urllib.request
        with urllib.request.urlopen("http://127.0.0.1:%d/v/%s" % (port, topic), timeout=5) as r:
            return json.loads(r.read().decode("utf-8")).get("listening") or 0
    except Exception:
        return 0


def require_herdr():
    if os.environ.get("HERDR_ENV") != "1":
        raise SystemExit(
            "canvas-worker: not inside a herdr session (HERDR_ENV != 1).\n"
            "Work the loop inline instead: canvas.py wait <topic> --root <root>, then edit,\n"
            "build-canvas.py, verify-canvas.py, say, ack.")


def write_card(root, topic, port):
    daemon_url = "http://127.0.0.1:%d" % port
    text = CARD_TEMPLATE.read_text(encoding="utf-8")
    for key, value in (
        ("topic", topic),
        ("root", str(root.resolve())),
        ("skill", str(SKILL_DIR)),
        ("port", str(port)),
        ("daemon_url", daemon_url),
    ):
        text = text.replace("{{%s}}" % key, value)
    path = root / topic / "WORKER.md"
    path.write_text(text, encoding="utf-8")
    return path


def write_round_card(root, topic, port):
    """The temp worker's card: one round, then exit. Its context dies with its pane."""
    daemon_url = "http://127.0.0.1:%d" % port
    text = ROUND_TEMPLATE.read_text(encoding="utf-8")
    for key, value in (
        ("topic", topic),
        ("root", str(root.resolve())),
        ("skill", str(SKILL_DIR)),
        ("port", str(port)),
        ("daemon_url", daemon_url),
    ):
        text = text.replace("{{%s}}" % key, value)
    path = root / topic / "ROUND.md"
    path.write_text(text, encoding="utf-8")
    return path


def daemon_port(root, override):
    if override:
        return override, True
    info = read_daemon(root)
    if info and is_up(info.get("port", 0)):
        return info["port"], True
    return DEFAULT_PORT, False


def cmd_card(args):
    root = Path(args.root)
    port, up = daemon_port(root, args.port)
    path = write_card(root, topic=args.topic, port=port)
    print(path)
    if args.print_card:
        print(path.read_text(encoding="utf-8"))
    return 0


def cmd_start(args):
    require_herdr()
    root = Path(args.root)
    if not TOPIC_RE.match(args.topic):
        raise SystemExit("canvas-worker: bad topic %r" % args.topic)
    if not (root / args.topic / "content.html").is_file():
        raise SystemExit("canvas-worker: no canvas at %s/%s (build it first)"
                         % (args.root, args.topic))

    existing, status = agent_state(root, args.topic)
    if existing:
        if status not in IDLE_STATES:
            raise SystemExit(
                "canvas-worker: a worker is live for %s (%s, pane %s, status=%s).\n"
                "Stop it first: canvas-worker.py stop %s --now"
                % (args.topic, existing.get("agent_name"), existing.get("pane_id"), status,
                   args.topic))
        # A gracefully stopped worker leaves an idle agent in the pane it created, so close
        # that pane and carry on rather than forcing the caller to run --now as well.
        if existing.get("pane_id"):
            herdr("pane", "close", existing["pane_id"], check=False)
        worker_path(root, args.topic).unlink(missing_ok=True)

    port, up = daemon_port(root, args.port)
    if not up:
        raise SystemExit("canvas-worker: daemon is not running. Start it first:\n"
                         "  python3 %s/scripts/canvas.py start --root %s --port %d"
                         % (SKILL_DIR, args.root, port))

    # One watcher per topic. worker.json only records the LAST one, so a differently-named
    # second watcher could park on the same topic: each Send then wakes one of them (the
    # daemon consumes it once), the other starves, and a re-triggered batch makes both work.
    live = []
    for a in (herdr("agent", "list", check=False).get("result") or {}).get("agents", []):
        n = a.get("name") or ""
        if n == "canvas-" + args.topic and a.get("agent_status") not in IDLE_STATES:
            live.append(n)
    if live:
        raise SystemExit("canvas-worker: %s is already watching %s (status=%s). One watcher "
                         "per topic — stop it first, or use `round` for a single round."
                         % (", ".join(live), args.topic, agent_status(live[0])))

    card = write_card(root, args.topic, port)
    (root / args.topic / "STOP").unlink(missing_ok=True)

    # split → start → prompt. Parse ids from the JSON, never guess.
    split = herdr("pane", "split", "--current", "--direction", "right",
                  "--cwd", os.getcwd(), "--no-focus")
    pane = (split.get("result", {}).get("pane", {}) or {}).get("pane_id")
    if not pane:
        raise SystemExit("canvas-worker: could not read the new pane id from: %s"
                         % json.dumps(split)[:300])

    name = args.name or ("canvas-" + args.topic)[:40]
    kind_args = [] if args.kind == "pi" else ["--"]
    if args.kind == "claude":
        kind_args = ["--", "--permission-mode", "auto"]
    elif args.kind == "pi":
        kind_args = []
    started = herdr("agent", "start", name, "--kind", args.kind, "--pane", pane, *kind_args)

    prompt = ("Read %s and follow it exactly. You own the feedback loop for canvas "
              "\"%s\" from now on." % (card, args.topic))
    herdr("agent", "prompt", name, prompt, check=False)

    save_worker(root, args.topic, {
        "topic": args.topic, "pane_id": pane, "agent_name": name, "kind": args.kind,
        "card": str(card), "port": port, "started": now_iso(),
        "url": "http://127.0.0.1:%d/t/%s" % (port, args.topic),
    })
    print("worker   %s (%s) in pane %s" % (name, args.kind, pane))
    print("card     %s" % card)
    print("canvas   http://127.0.0.1:%d/t/%s" % (port, args.topic))
    print("check    python3 %s/scripts/canvas-worker.py status %s --root %s"
          % (SKILL_DIR, args.topic, args.root))
    return 0


def cmd_list(args):
    """One table for every topic on this machine: the root you name, or every root in the
    registry (~/.agents/canvas/roots.json) when you name none. Topics live with the project
    they document, so each group says which project that is.

    The point is that `ps` and `kill` are never needed to answer "is anyone listening, and
    how do I close this" — state that lives in a process nobody can see is the failure this
    reports on."""
    if args.root:
        roots = [Path(args.root)]
    else:
        roots = [Path(e["root"]) for e in read_registry() if e.get("root")] or [Path(DEFAULT_ROOT)]

    groups = []
    for root in roots:
        # An explicit --port is a request, not a fact: check it, or the table lies about a
        # daemon that is not there.
        port = args.port or (read_daemon(root) or {}).get("port") or DEFAULT_PORT
        up = is_up(port)
        rows = []
        if root.is_dir():
            topics = sorted(p.name for p in root.iterdir()
                            if p.is_dir() and TOPIC_RE.match(p.name)
                            and not p.name.endswith("-verify"))
            for t in topics:
                info, status = agent_state(root, t)
                stop = (root / t / "STOP").exists()
                listening = listening_count(root, t, port) if up else 0
                parked = read_parked(root, t)
                pending = len(pending_notes(root, t))
                unsent = len(unsent_notes(root, t))
                send = read_send(root, t) or {}
                rounds = sum(1 for e in load_history(root, t) if e.get("kind") == "agent")
                # A durable marker beats memory: STOP outranks everything, then a connected
                # waiter, then the heartbeat that outlives a killed waiter and a restarted
                # daemon (shown as parked* so the weaker claim is visible).
                if stop:
                    state = "stopped"
                elif listening:
                    state = "parked"
                elif parked:
                    state = "parked*"
                elif not info:
                    state = "no worker"
                elif status in IDLE_STATES:
                    state = "idle"
                else:
                    state = status
                notes = "%d pending" % pending if pending else "clear"
                if unsent:
                    notes += " · %d unsent" % unsent
                if send.get("ts") and not send.get("consumed_at"):
                    notes += " · queued, unread"
                rows.append((t, (info or {}).get("agent_name", "—"), state,
                             (info or {}).get("pane_id", "—"), notes,
                             (send.get("ts") or "—")[11:16] if send.get("ts") else "—",
                             str(rounds)))
        groups.append((root, port, up, rows))

    head = ("topic", "worker", "state", "pane", "notes", "send", "rounds")
    all_rows = [r for _, _, _, rows in groups for r in rows]
    width = [max(len(str(r[i])) for r in [head] + all_rows) for i in range(len(head))]
    line = lambda cells: "  ".join(str(c).ljust(width[i]) for i, c in enumerate(cells)).rstrip()
    for root, port, up, rows in groups:
        print("root     %s  %s" % (root, ("daemon up port=%d" % port) if up else "no daemon"))
        if not root.is_dir():
            print("         registered, but that directory is gone")
            continue
        if not rows:
            print("         no canvases")
            continue
        print(line(head))
        for r in rows:
            print(line(r))

    print("\nstate    parked = a waiter is connected now · parked* = a heartbeat on disk")
    close = "python3 %s/scripts/canvas-worker.py" % SKILL_DIR
    print("close one   %s stop <topic> --root <root>" % close)
    print("close all   %s stop --all --root <root>" % close)
    print("daemon      python3 %s/scripts/canvas.py stop --root <root>" % SKILL_DIR)
    return 0


def cmd_stop(args):
    root = Path(args.root)
    if getattr(args, "all", False):
        return stop_all(args, root)
    if not args.topic:
        raise SystemExit("canvas-worker: give a topic, or --all to stop every worker")
    if not TOPIC_RE.match(args.topic):
        raise SystemExit("canvas-worker: bad topic %r" % args.topic)
    return stop_one(args, root, args.topic)


def stop_all(args, root):
    """Close every topic that has a worker recorded, reporting each one so a slow --wait on a
    mid-round worker reads as progress rather than a hang."""
    topics = [p.name for p in sorted(root.iterdir())
              if p.is_dir() and TOPIC_RE.match(p.name) and load_worker(root, p.name)]
    if not topics:
        print("canvas-worker: no workers recorded under %s" % root)
        return 0
    rc = 0
    for t in topics:
        print("-- %s" % t)
        sub = argparse.Namespace(topic=t, root=args.root, now=args.now, wait=args.wait, all=False)
        rc = stop_one(sub, root, t) or rc
    return rc


def stop_one(args, root, topic):
    info = load_worker(root, topic)
    if not info:
        print("canvas-worker: no worker recorded for %s" % topic)
        return 0
    pane = info.get("pane_id")
    if args.now:
        if pane:
            herdr("pane", "close", pane, check=False)
        (root / topic / "STOP").unlink(missing_ok=True)
        worker_path(root, topic).unlink(missing_ok=True)
        print("canvas-worker: closed pane %s immediately (an in-flight round may be half done)" % pane)
        return 0
    stop = root / topic / "STOP"
    stop.write_text("stop\n", encoding="utf-8")
    print("canvas-worker: stop requested — %s exits at the end of its current round" % info.get("agent_name"))

    # Requesting a stop is only half the job: the worker exits, but the pane, the record and
    # the STOP flag stay behind. That is exactly how a canvas ends up with nobody listening —
    # and how a later worker is blocked by a flag nobody remembers writing. Finish it here.
    status = wait_for_idle(info.get("agent_name", ""), args.wait)
    if status not in IDLE_STATES:
        print("canvas-worker: %s is still %s after %ds — pane %s left open, STOP set" %
              (info.get("agent_name"), status, args.wait, pane))
        print("             re-run with --now to close it now (kills a live round)")
        return 1
    if pane:
        herdr("pane", "close", pane, check=False)
    stop.unlink(missing_ok=True)
    worker_path(root, topic).unlink(missing_ok=True)
    print("canvas-worker: round finished — closed pane %s, cleared STOP and worker.json" % pane)
    return 0


def cmd_ensure(args):
    """Make sure ONE worker is parked for the topic — the recovery command.

    `start` refuses while a live worker exists: right for a human making a choice, wrong for
    recovery, because the caller cannot tell "already parked" from "stopped and nobody
    noticed". ensure is safe to run at any time and repeatedly: parked → no-op; dead,
    stopped or half-stopped → clear the leftovers and start one.
    """
    root = Path(args.root)
    info, status = agent_state(root, args.topic)
    if info and not (root / args.topic / "STOP").exists():
        port, up = daemon_port(root, info.get("port"))
        if up and listening_count(root, args.topic, port):
            print("canvas-worker: already parked (%s, pane %s) — nothing to do"
                  % (info.get("agent_name"), info.get("pane_id")))
            return 0
        if status not in IDLE_STATES:
            print("canvas-worker: %s is alive (pane %s, status=%s) but not parked — mid-round"
                  % (info.get("agent_name"), info.get("pane_id"), status))
            print("             or between rounds. Not starting a second worker; if it stays")
            print("             that way: canvas-worker.py stop %s --now, then ensure again"
                  % args.topic)
            return 0
    return cmd_start(args)


def cmd_status(args):
    root = Path(args.root)
    info = load_worker(root, args.topic)
    if not info:
        print("no worker recorded for %s" % args.topic)
        return 1
    stop = (root / args.topic / "STOP").exists()
    status = agent_status(info.get("agent_name", ""))
    port, up = daemon_port(root, info.get("port"))
    listening = listening_count(root, args.topic, port) if up else None
    print("worker   %s (%s) pane %s" % (info.get("agent_name"), info.get("kind"), info.get("pane_id")))
    print("status   %s%s" % (status, "  (stop requested)" if stop else ""))
    print("parked   %s" % ("yes — waiting for a Send" if listening else "no (idle, between rounds, or starting up)"))
    print("since    %s" % info.get("started"))
    if not listening:
        # The recovery path, printed where someone looking at a dead canvas will see it.
        print("recover  python3 %s/scripts/canvas-worker.py ensure %s --root %s"
              % (SKILL_DIR, args.topic, args.root))
    return 0


def coordinator_path(root):
    return root / ".coordinator.json"


def write_coordinator_card(root, port):
    daemon_url = "http://127.0.0.1:%d" % port
    text = COORDINATOR_TEMPLATE.read_text(encoding="utf-8")
    for key, value in (("root", str(root.resolve())), ("skill", str(SKILL_DIR)),
                       ("port", str(port)), ("daemon_url", daemon_url)):
        text = text.replace("{{%s}}" % key, value)
    path = root / "COORDINATOR.md"
    path.write_text(text, encoding="utf-8")
    return path


def load_coordinator(root):
    try:
        return json.loads(coordinator_path(root).read_text(encoding="utf-8"))
    except (OSError, ValueError):
        return None


def cmd_coordinator(args):
    """ONE persistent agent per root: it listens on every topic and dispatches each batch to a
    disposable worker. N topics no longer means N parkers."""
    root = Path(args.root)
    action = args.action

    if action == "status":
        info = load_coordinator(root)
        if not info:
            print("no coordinator for %s" % root)
            return 1
        status = agent_status(info.get("agent_name", ""))
        print("coordinator %s (%s) pane %s" % (info.get("agent_name"), info.get("kind"),
                                              info.get("pane_id")))
        print("status   %s%s" % (status, "  (stop requested)"
                                 if (root / ".COORDINATOR_STOP").exists() else ""))
        print("since    %s" % info.get("started"))
        return 0

    if action == "stop":
        (root / ".COORDINATOR_STOP").write_text("stop\n", encoding="utf-8")
        info = load_coordinator(root)
        if not info:
            print("coordinator: stop flag written (no coordinator was recorded)")
            return 0
        if args.now:
            herdr("pane", "close", info.get("pane_id"), check=False)
            (root / ".COORDINATOR_STOP").unlink(missing_ok=True)
            coordinator_path(root).unlink(missing_ok=True)
            print("coordinator: closed pane %s immediately" % info.get("pane_id"))
            return 0
        status = wait_for_idle(info.get("agent_name", ""), args.wait)
        if status in IDLE_STATES:
            herdr("pane", "close", info.get("pane_id"), check=False)
            (root / ".COORDINATOR_STOP").unlink(missing_ok=True)
            coordinator_path(root).unlink(missing_ok=True)
            print("coordinator: exited (%s) — pane closed, STOP cleared" % status)
        else:
            print("coordinator: still %s after %ss — pane %s left open, STOP set"
                  % (status, args.wait, info.get("pane_id")))
        return 0

    # start
    require_herdr()
    name = (args.name or "canvas-coordinator")[:40]
    # Already running is SUCCESS, not an error: the caller asked for a coordinator to be up
    # and one is. This used to exit 1, so a second click on the dashboard's start button read
    # as a failure; when the record was missing it leaked herdr's raw `agent_name_taken` and
    # left the freshly split pane behind.
    existing = load_coordinator(root)
    if existing and agent_record(existing.get("agent_name") or ""):
        print("coordinator: already running — %s (%s) in pane %s, since %s"
              % (existing.get("agent_name"), existing.get("kind"), existing.get("pane_id"),
                 existing.get("started")))
        print("check    canvas-worker.py coordinator status --root %s" % args.root)
        return 0
    taken = agent_record(name)
    if taken:
        print("coordinator: already running — herdr already has an agent named %r (pane %s)."
              % (name, taken.get("pane_id")))
        print("           It may belong to another root; nothing was started.")
        return 0
    port, up = daemon_port(root, args.port)
    if not up:
        raise SystemExit("canvas-worker: daemon is not running. Start it first:\n"
                         "  python3 %s/scripts/canvas.py start --root %s --port %d"
                         % (SKILL_DIR, args.root, port))
    (root / ".COORDINATOR_STOP").unlink(missing_ok=True)
    card = write_coordinator_card(root, port)
    split = herdr("pane", "split", "--current", "--direction", args.direction,
                  "--cwd", os.getcwd(), "--no-focus")
    pane = (split.get("result", {}).get("pane", {}) or {}).get("pane_id")
    if not pane:
        raise SystemExit("canvas-worker: could not read the new pane id from: %s"
                         % json.dumps(split)[:300])
    kind_args = ["--", "--permission-mode", "auto"] if args.kind == "claude" else []
    herdr("agent", "start", name, "--kind", args.kind, "--pane", pane, *kind_args)
    herdr("agent", "prompt", name,
          "Read %s and follow it exactly. You coordinate every canvas topic under %s; you "
          "never edit content yourself." % (card, root), check=False)
    coordinator_path(root).write_text(json.dumps({
        "root": str(root.resolve()), "pane_id": pane, "agent_name": name, "kind": args.kind,
        "card": str(card), "started": now_iso()}, indent=2) + "\n", encoding="utf-8")
    print("coordinator %s (%s) in pane %s" % (name, args.kind, pane))
    print("card     %s" % card)
    print("listens  every topic under %s" % root)
    return 0


def cmd_round(args):
    """One round on a TEMP worker: spawn a pane, hand it ROUND.md, wait, close the pane.

    Called by the watcher on every Send. The worker's context is thrown away with the pane,
    so no agent accumulates a session's worth of file reads, build output and DOM dumps —
    which is the whole reason the watcher/worker split exists.
    """
    require_herdr()
    root = Path(args.root)
    topic = args.topic
    if not TOPIC_RE.match(topic):
        raise SystemExit("canvas-worker: bad topic %r" % topic)
    if not (root / topic / "content.html").is_file():
        raise SystemExit("canvas-worker: no canvas at %s/%s" % (args.root, topic))

    port, up = daemon_port(root, args.port)
    if not up:
        raise SystemExit("canvas-worker: daemon is not running. Start it first:\n"
                         "  python3 %s/scripts/canvas.py start --root %s --port %d"
                         % (SKILL_DIR, args.root, port))

    card = write_round_card(root, topic, port)

    # Same split → start → prompt shape as `start`, but the agent is disposable.
    split = herdr("pane", "split", "--current", "--direction", args.direction,
                  "--cwd", os.getcwd(), "--no-focus")
    pane = (split.get("result", {}).get("pane", {}) or {}).get("pane_id")
    if not pane:
        raise SystemExit("canvas-worker: could not read the new pane id from: %s"
                         % json.dumps(split)[:300])

    name = ("canvas-%s-r%d" % (topic, int(time.time()) % 100000))[:40]
    kind_args = ["--", "--permission-mode", "auto"] if args.kind == "claude" else []
    herdr("agent", "start", name, "--kind", args.kind, "--pane", pane, *kind_args)
    herdr("agent", "prompt", name,
          "Read %s and do exactly one round, then exit. Do not wait for more work." % card,
          check=False)

    with open(str(root / topic / "rounds.jsonl"), "a", encoding="utf-8") as fh:
        fh.write(json.dumps({"ts": now_iso(), "agent": name, "pane": pane,
                             "kind": args.kind, "waited": bool(args.wait)}) + "\n")

    if not args.wait:
        print("round    %s (%s) in pane %s — not waited for" % (name, args.kind, pane))
        print("card     %s" % card)
        return 0

    t0 = time.time()
    # The prompt takes a moment to land. Polling immediately sees the agent still idle and
    # closes the pane before the round begins — that bug shipped once, silently (0s
    # 'finished', empty history). So: observe it START before waiting for it to finish.
    saw_working = False
    while time.time() - t0 < 30:
        if agent_status(name) == "working":
            saw_working = True
            break
        time.sleep(1)
    if saw_working:
        status = wait_for_idle(name, args.timeout)
    else:
        time.sleep(10)          # never went to work: give it the benefit of the doubt
        status = agent_status(name)
    herdr("pane", "close", pane, check=False)
    print("round    %s finished (%s%s) in %.0fs — pane closed, context discarded"
          % (name, status, "" if saw_working else ", never reported working",
             time.time() - t0))
    return 0


def main(argv=None):
    p = argparse.ArgumentParser(prog="canvas-worker.py")
    sub = p.add_subparsers(dest="cmd", required=True)

    def common(sp):
        sp.add_argument("topic")
        sp.add_argument("--root", default=DEFAULT_ROOT)
        sp.add_argument("--port", type=int, default=None)
        return sp

    sp = common(sub.add_parser("start"))
    sp.add_argument("--kind", default="pi", help="agent kind (pi, claude, codex, …)")
    sp.add_argument("--name", default=None, help="herdr agent name (default canvas-<topic>)")
    sp.set_defaults(func=cmd_start)

    sp = sub.add_parser("coordinator")
    sp.add_argument("action", choices=("start", "stop", "status"))
    sp.add_argument("--root", default=DEFAULT_ROOT)
    sp.add_argument("--port", type=int, default=None)
    sp.add_argument("--kind", default="pi")
    sp.add_argument("--name", default=None)
    sp.add_argument("--direction", default="down")
    sp.add_argument("--now", action="store_true")
    sp.add_argument("--wait", type=float, default=60.0)
    sp.set_defaults(func=cmd_coordinator)

    sp = common(sub.add_parser("round"))
    sp.add_argument("--kind", default="pi", help="agent kind for the temp round worker")
    sp.add_argument("--direction", default="down",
                    help="where to split the temp pane (down keeps the watcher visible)")
    sp.add_argument("--wait", action="store_true",
                    help="wait for the round to finish, then close its pane")
    sp.add_argument("--timeout", type=int, default=900, help="seconds to wait for the round")
    sp.set_defaults(func=cmd_round)

    sp = sub.add_parser("stop")
    sp.add_argument("topic", nargs="?", default=None)
    sp.add_argument("--root", default=DEFAULT_ROOT)
    sp.add_argument("--port", type=int, default=None)
    sp.add_argument("--now", action="store_true", help="close the pane now instead of next round")
    sp.add_argument("--all", action="store_true", help="stop every topic that has a worker")
    sp.add_argument("--wait", type=float, default=60.0,
                    help="seconds to let an in-flight round finish before reporting (default 60)")
    sp.set_defaults(func=cmd_stop)

    sp = common(sub.add_parser("ensure"))
    sp.add_argument("--kind", default="pi", help="agent kind (pi, claude, codex, …)")
    sp.add_argument("--name", default=None, help="herdr agent name (default canvas-<topic>)")
    sp.set_defaults(func=cmd_ensure)

    sp = common(sub.add_parser("status"))
    sp.set_defaults(func=cmd_status)

    sp = sub.add_parser("list")
    sp.add_argument("--root", default=None,
                    help="one root (default: every root in the registry)")
    sp.add_argument("--port", type=int, default=None)
    sp.set_defaults(func=cmd_list)

    sp = common(sub.add_parser("card"))
    sp.add_argument("--print", dest="print_card", action="store_true")
    sp.set_defaults(func=cmd_card)

    args = p.parse_args(argv)
    require_topic(getattr(args, "topic", None))
    return args.func(args)


if __name__ == "__main__":
    sys.exit(main())
