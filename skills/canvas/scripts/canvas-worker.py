#!/usr/bin/env python3
"""canvas-worker.py — dispatch canvas rounds to an agent in a herdr pane, and own the
coordinator that the daemon wakes.

Why this exists
---------------
Reading notes, editing sections, building, verifying and replying costs a lot of context. Run
that in the main session and the user blocks for minutes while their context is spent on the
loop. So a round runs in its own pane, on a disposable agent — and the main session stays free
to talk to the user.

Usage
-----
    canvas-worker.py coordinator start|stop|status [--root DIR] [--kind pi] [--name NAME]
    canvas-worker.py round <topic> [--root DIR] [--wait] [--kind pi] [--timeout SECONDS]
    canvas-worker.py list  [--root DIR ...]

`round` splits a pane, hands the new agent <topic>/ROUND.md, waits for the round to finish and
closes the pane, so its context dies with it. `coordinator start` is idempotent: a live
coordinator is reported, and a root with none gets one — the daemon then wakes that agent (see
references/architecture.md, decisions 3 and 4).

Requires herdr: HERDR_ENV=1 and the herdr CLI. Outside herdr it exits with the instruction to
work the loop inline instead.
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
from canvas import (DEFAULT_ROOT, DEFAULT_PORT, TOPIC_RE, IDLE_STATES, read_daemon,  # noqa: E402
                    is_up, load_history, pending_notes, read_send, unsent_notes)

SKILL_DIR = Path(__file__).resolve().parent.parent
ROUND_TEMPLATE = SKILL_DIR / "references" / "round-card.md"
COORDINATOR_TEMPLATE = SKILL_DIR / "references" / "coordinator-card.md"


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


def wait_for_idle(agent_name, timeout):
    """Poll until the agent's turn ends; return the last status seen.

    A worker mid-round reads as 'working' until the round finishes, which is what grace has
    to mean; an idle agent has ended its turn and can be closed. A stop request cannot
    interrupt a round already running — it only stops the next one.
    """
    deadline = time.time() + max(0, timeout)
    while True:
        status = agent_status(agent_name)
        if status in IDLE_STATES or time.time() >= deadline:
            return status
        time.sleep(1.0)


def require_herdr():
    if os.environ.get("HERDR_ENV") != "1":
        raise SystemExit(
            "canvas-worker: not inside a herdr session (HERDR_ENV != 1).\n"
            "Run one round inline instead: canvas.py pending <topic> --root <root>, then edit,\n"
            "build-canvas.py, verify-canvas.py, say, ack.")


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


def cmd_list(args):
    """One table for every topic of the roots you name: topic, what is waiting, rounds.
    Topics live with the project they document, so each group says which project that is.

    `--root` is repeatable; with none, it lists the root here. *Locked, not yet built:*
    `references/architecture.md` decision 1 replaces this with one daemon serving every
    registered project, which would let the no-argument form list all of them.

    The point is that `ps` and `kill` are never needed to answer "is a coordinator recorded,
    and what is waiting" — state that lives in a process nobody can see is the failure this
    reports on."""
    # Tolerate both parser shapes: --root may arrive as one string or as a list
    # (repeatable). Two agents edited this function concurrently and the mismatch
    # surfaced as a TypeError rather than a wrong listing.
    raw = args.root
    if raw is None:
        roots = [Path(DEFAULT_ROOT)]
    elif isinstance(raw, (list, tuple)):
        roots = [Path(r) for r in raw]
    else:
        roots = [Path(raw)]

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
                pending = len(pending_notes(root, t))
                unsent = len(unsent_notes(root, t))
                send = read_send(root, t) or {}
                rounds = sum(1 for e in load_history(root, t) if e.get("kind") == "agent")
                notes = "%d pending" % pending if pending else "clear"
                if unsent:
                    notes += " · %d unsent" % unsent
                if send.get("ts") and not send.get("consumed_at"):
                    notes += " · queued, unread"
                rows.append((t, notes,
                             (send.get("ts") or "—")[11:16] if send.get("ts") else "—",
                             str(rounds)))
        groups.append((root, port, up, rows))

    head = ("topic", "notes", "send", "rounds")
    all_rows = [r for _, _, _, rows in groups for r in rows]
    width = [max(len(str(r[i])) for r in [head] + all_rows) for i in range(len(head))]
    line = lambda cells: "  ".join(str(c).ljust(width[i]) for i, c in enumerate(cells)).rstrip()
    for root, port, up, rows in groups:
        coord = (load_coordinator(root) or {}).get("agent_name")
        print("root     %s  %s  %s" % (root,
                                       ("daemon up port=%d" % port) if up else "no daemon",
                                       "coordinator %s" % coord if coord else "no coordinator"))
        if not root.is_dir():
            print("         registered, but that directory is gone")
            continue
        if not rows:
            print("         no canvases")
            continue
        print(line(head))
        for r in rows:
            print(line(r))

    print("\narmed    the sweep wakes the agent in .coordinator.json when a topic has unread notes")
    close = "python3 %s/scripts/canvas-worker.py" % SKILL_DIR
    print("coordinator %s coordinator status --root <root>" % close)
    print("daemon      python3 %s/scripts/canvas.py stop --root <root>" % SKILL_DIR)
    return 0


def project_label(root, maxlen=20):
    """A short, herdr-safe name for the project a canvas root belongs to.

    herdr's agent namespace is GLOBAL across the workspace, so a fixed name like
    "canvas-coordinator" meant only one project could have a coordinator at a time —
    starting a second failed with `agent_name_taken`. The root is normally
    <project>/.agents/canvas, so the project name is two levels up.
    """
    import re as _re
    parts = list(Path(root).resolve().parts)
    if len(parts) >= 2 and parts[-1] == "canvas" and parts[-2] == ".agents":
        parts = parts[:-2]
    label = _re.sub(r"[^a-z0-9]+", "-", (parts[-1] if parts else "canvas").lower()).strip("-")
    return (label or "canvas")[:maxlen]


def agent_name_for(root, kind, topic=None):
    """Namespaced agent name. kind: "coord" or "round". Always keeps the -r<N> suffix.

    32 is herdr's hard cap (``invalid_agent_name`` above it), and it counts the suffix —
    truncating to 40 made every topic longer than ~18 chars undispatchable.
    """
    limit = 32
    label = project_label(root)
    if kind == "coord":
        return ("canvas-coord-" + label)[:limit]
    suffix = "-r%d" % (int(time.time()) % 100000)
    base = "canvas-%s-%s" % (label, topic or "topic")
    return (base[:limit - len(suffix)] + suffix)[:limit]


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
    name = args.name or agent_name_for(root, "coord")
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

    name = agent_name_for(root, "round", topic)
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

    sp = sub.add_parser("list")
    sp.add_argument("--root", action="append", default=None, metavar="DIR",
                    help="a root to include, repeatable (default: .agents/canvas here)")
    sp.add_argument("--port", type=int, default=None)
    sp.set_defaults(func=cmd_list)

    args = p.parse_args(argv)
    require_topic(getattr(args, "topic", None))
    return args.func(args)


if __name__ == "__main__":
    sys.exit(main())
