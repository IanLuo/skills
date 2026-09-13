#!/usr/bin/env python3
"""The sweep — the daemon, not a parked waiter, is what notices work.

Locked decision: `skills/canvas/references/architecture.md` § Load-bearing structure (2) and (3):
the daemon sweeps every 2 s and wakes the coordinator when unread work exists; the park stays
only as a latency optimisation. This test pins the three properties that matter:

  * it wakes when work is waiting and nobody is parked
  * it stays silent when a park already has the work (or nothing is waiting)
  * it is rate-limited, so an unanswered wake cannot fill history.jsonl

Run: python3 tests/canvas/test_sweep.py
"""

import importlib.util
import json
import os
import stat
import sys
import tempfile
from pathlib import Path

REPO = Path(__file__).resolve().parents[2]
CANVAS = REPO / "skills" / "canvas" / "scripts" / "canvas.py"

spec = importlib.util.spec_from_file_location("canvas", CANVAS)
canvas = importlib.util.module_from_spec(spec)
spec.loader.exec_module(canvas)

FAILED = []


def check(name, cond, detail=""):
    print("%s %s%s" % ("PASS" if cond else "FAIL", name, ("  — " + str(detail)) if detail and not cond else ""))
    if not cond:
        FAILED.append(name)


def fake_herdr(tmp):
    bindir = tmp / "bin"
    bindir.mkdir()
    log = tmp / "herdr-argv.jsonl"
    exe = bindir / "herdr"
    exe.write_text(
        "#!%s\nimport json, sys\nopen(%r, 'a').write(json.dumps(sys.argv[1:]) + '\\n')\n"
        % (sys.executable, str(log)),
        encoding="utf-8",
    )
    exe.chmod(exe.stat().st_mode | stat.S_IEXEC)
    os.environ["PATH"] = str(bindir) + os.pathsep + os.environ.get("PATH", "")
    return log


def prompts(log):
    if not log.exists():
        return 0
    return len([l for l in log.read_text(encoding="utf-8").splitlines() if l.strip()])


def topic_with_send(root, topic, *, consumed=False, notes=1):
    d = root / topic
    d.mkdir(parents=True, exist_ok=True)
    (d / "content.html").write_text('<section data-section="s1">x</section>', encoding="utf-8")
    (d / "feedback.json").write_text(json.dumps({"annotations": [
        {"id": "c%d" % i, "anchor": "s1", "comment": "change this", "severity": "suggestion",
         "resolved": False, "ts": "2026-09-12T10:00:00Z", "attempts": 0}
        for i in range(notes)
    ]}), encoding="utf-8")
    canvas.write_send(root, topic, {
        "ts": "2026-09-12T10:00:00Z", "count": notes,
        "consumed_at": "2026-09-12T10:00:05Z" if consumed else None,
    })
    return d


def main():
    with tempfile.TemporaryDirectory() as td:
        tmp = Path(td)
        log = fake_herdr(tmp)
        root = tmp / "root"
        root.mkdir()
        (root / ".coordinator.json").write_text(json.dumps(
            {"agent_name": "canvas-coord-probe", "pane_id": "w1:p9"}), encoding="utf-8")
        topic_with_send(root, "probe")

        # 1 · work waiting, nobody parked → wake, once
        canvas.SWEEP_LAST.clear()
        canvas.SWEEP_WAKES.clear()
        woken = canvas.sweep_once(root, now=1000.0)
        check("sweep wakes when work is waiting", len(woken) == 1 and woken[0].get("ok") is True, woken)
        check("herdr was prompted once", prompts(log) == 1, prompts(log))
        check("the wake is logged on the page",
              any(e.get("kind") == "wake" and e.get("ok") for e in canvas.load_history(root, "probe")),
              canvas.load_history(root, "probe"))

        # 2 · rate limit: a second sweep a moment later is silent, but one a minute later is not
        check("second sweep within the minute is silent", canvas.sweep_once(root, now=1001.0) == [])
        check("still silent at 59s", canvas.sweep_once(root, now=1059.0) == [])
        check("wakes again after the interval", len(canvas.sweep_once(root, now=1061.0)) == 1)

        # 3 · a park already has the work → the sweep must stay out of the way
        canvas.SWEEP_LAST.clear()
        canvas.SWEEP_WAKES.clear()
        (root / "probe" / "parked.json").write_text(
            json.dumps({"ts": "2026-09-12T10:00:00Z", "pid": os.getpid()}), encoding="utf-8")
        import datetime
        fresh = datetime.datetime.now(datetime.timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")
        (root / "probe" / "parked.json").write_text(
            json.dumps({"ts": fresh, "pid": os.getpid()}), encoding="utf-8")
        before = prompts(log)
        check("a parked waiter suppresses the sweep", canvas.sweep_once(root, now=2000.0) == [])
        check("and no prompt was sent", prompts(log) == before)
        (root / "probe" / "parked.json").unlink()

        # 4 · nothing waiting → no wake, no log line
        canvas.SWEEP_LAST.clear()
        canvas.SWEEP_WAKES.clear()
        empty = tmp / "empty"
        empty.mkdir()
        topic_with_send(empty, "quiet", consumed=True)
        (empty / "quiet" / "feedback.json").write_text(json.dumps({"annotations": []}), encoding="utf-8")
        check("no work → no wake", canvas.sweep_once(empty, now=3000.0) == [])
        check("no work → no history line",
              not [e for e in canvas.load_history(empty, "quiet") if e.get("kind") == "wake"])

        # 5 · global cap: an unanswered wake cannot fill the log forever
        canvas.SWEEP_LAST.clear()
        canvas.SWEEP_WAKES.clear()
        scattered = tmp / "scattered"
        scattered.mkdir()
        (scattered / ".coordinator.json").write_text(json.dumps({"pane_id": "w9:p1"}), encoding="utf-8")
        for i in range(8):
            topic_with_send(scattered, "t%d" % i)
        total = 0
        for step in range(8):
            total += len(canvas.sweep_once(scattered, now=5000.0 + step * 61))
        check("the global cap holds wakes to %d per 10 min" % canvas.SWEEP_MAX_WAKES,
              total <= canvas.SWEEP_MAX_WAKES, total)

    print("\n%s" % ("All sweep tests passed." if not FAILED else "FAILED: %s" % ", ".join(FAILED)))
    return 1 if FAILED else 0


if __name__ == "__main__":
    sys.exit(main())
