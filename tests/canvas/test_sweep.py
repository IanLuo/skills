#!/usr/bin/env python3
"""The sweep — the daemon, not a parked waiter, is what notices work.

Locked decision: `skills/canvas/references/architecture.md` § Load-bearing structure (2), (3)
and (5): there is no park any more; the daemon sweeps every 2 s and wakes the coordinator,
only when the coordinator is idle, and one Send can only ever produce one round. This test
pins the properties that matter:

  * a Send writes state and wakes nobody — the sweep is the single wake path
  * it wakes when work is waiting and the coordinator is idle
  * it stays silent while a round is in flight, or when nothing is waiting
  * a successful wake consumes the send and counts the attempt
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


class Herdr:
    """A herdr that answers `agent get` with a status we control, and records every
    `agent prompt` so the test can assert WHO was woken and how many times."""

    def __init__(self, tmp, status="idle"):
        bindir = tmp / "bin"
        bindir.mkdir()
        self.log = tmp / "herdr-argv.jsonl"
        self.status = tmp / "herdr-status"
        self.status.write_text(status, encoding="utf-8")
        exe = bindir / "herdr"
        exe.write_text(
            "#!%s\n"
            "import json, sys\n"
            "argv = sys.argv[1:]\n"
            "if argv[:2] == ['agent', 'get']:\n"
            "    st = open(%r).read().strip() or 'idle'\n"
            "    print(json.dumps({'result': {'agent': {'agent_status': st}}}))\n"
            "else:\n"
            "    open(%r, 'a').write(json.dumps(argv) + '\\n')\n"
            % (sys.executable, str(self.status), str(self.log)),
            encoding="utf-8",
        )
        exe.chmod(exe.stat().st_mode | stat.S_IEXEC)
        os.environ["PATH"] = str(bindir) + os.pathsep + os.environ.get("PATH", "")

    def busy(self, status="working"):
        self.status.write_text(status, encoding="utf-8")

    def prompts(self):
        if not self.log.exists():
            return []
        return [json.loads(l) for l in self.log.read_text(encoding="utf-8").splitlines() if l.strip()]


def topic(root, name, notes=1, resolved=False, ts="2026-09-12T10:00:00Z"):
    """A topic with content and `notes` unresolved annotations, but no Send yet."""
    d = root / name
    d.mkdir(parents=True, exist_ok=True)
    (d / "content.html").write_text('<section data-section="s1">x</section>', encoding="utf-8")
    (d / "feedback.json").write_text(json.dumps({"annotations": [
        {"id": "c%d" % i, "anchor": "s1", "comment": "change this", "severity": "suggestion",
         "resolved": resolved, "ts": ts, "attempts": 0}
        for i in range(notes)
    ]}), encoding="utf-8")
    return d


def coordinator(root, name="canvas-coord-probe"):
    (root / ".coordinator.json").write_text(json.dumps(
        {"agent_name": name, "pane_id": "w1:p9"}), encoding="utf-8")


def wakes(root, name):
    return [e for e in canvas.load_history(root, name) if e.get("kind") == "wake"]


def main():
    with tempfile.TemporaryDirectory() as td:
        tmp = Path(td)
        herdr = Herdr(tmp)
        root = tmp / "root"
        root.mkdir()
        coordinator(root)
        topic(root, "probe")

        # 1 · the Send path wakes nobody: the daemon's sweep is the single wake path, so one
        #     Send can never produce two rounds.
        canvas.SWEEP_LAST.clear()
        canvas.SWEEP_WAKES.clear()
        count = canvas.record_send(root, "probe")
        check("the Send carries the note", count == 1, count)
        check("a Send alone prompts nobody", herdr.prompts() == [], herdr.prompts())
        check("a Send alone writes no wake line", wakes(root, "probe") == [], wakes(root, "probe"))

        # 2 · one sweep wakes the coordinator, once
        woken = canvas.sweep_once(root, now=1000.0)
        check("the sweep wakes when work is waiting", len(woken) == 1 and woken[0].get("ok") is True, woken)
        check("the coordinator was prompted once", len(herdr.prompts()) == 1, herdr.prompts())
        check("the prompt says dispatch a round",
              bool(herdr.prompts()) and " round " in " " + " ".join(herdr.prompts()[0]) + " ",
              herdr.prompts())

        # 3 · the wake hands the batch over: consume the send, count the attempt. Without this
        #     a resolved topic keeps looking like unread work and wakes forever.
        send = canvas.read_send(root, "probe") or {}
        check("a successful wake consumes the send", bool(send.get("consumed_at")), send)
        attempts = [a.get("attempts", 0) for a in canvas.load_feedback(root, "probe")["annotations"]]
        check("a successful wake counts the attempt", attempts and min(attempts) >= 1, attempts)

        # 4 · rate limit: a second sweep a moment later is silent, but one a minute later is not
        check("second sweep within the minute is silent", canvas.sweep_once(root, now=1001.0) == [])
        check("still silent at 59s", canvas.sweep_once(root, now=1059.0) == [])
        check("wakes again after the interval", len(canvas.sweep_once(root, now=1061.0)) == 1)

        # 5 · resolved work is not work: the sweep stops
        canvas.SWEEP_LAST.clear()
        canvas.SWEEP_WAKES.clear()
        done = tmp / "done"
        done.mkdir()
        coordinator(done)
        topic(done, "wrapped", resolved=True)
        canvas.record_send(done, "wrapped")
        check("resolved notes do not wake", canvas.sweep_once(done, now=2000.0) == [])

        # 6 · a coordinator mid-round must not be woken — a prompt sent into a running round
        #     queues, and dispatching twice for one note is the storm this design prevents.
        busy = tmp / "busy"
        busy.mkdir()
        coordinator(busy)
        topic(busy, "waiting")
        canvas.record_send(busy, "waiting")
        canvas.SWEEP_LAST.clear()
        canvas.SWEEP_WAKES.clear()
        herdr.busy("working")
        before = len(herdr.prompts())
        check("a busy coordinator is not woken", canvas.sweep_once(busy, now=3000.0) == [])
        check("and no prompt went out while it was busy", len(herdr.prompts()) == before)
        herdr.busy("idle")
        check("it is woken once it is idle", len(canvas.sweep_once(busy, now=3001.0)) == 1)

        # 7 · a stop request suppresses the sweep instead of fighting it
        canvas.SWEEP_LAST.clear()
        canvas.SWEEP_WAKES.clear()
        (busy / ".COORDINATOR_STOP").write_text("stop\n", encoding="utf-8")
        topic(busy, "second")
        canvas.record_send(busy, "second")
        check("a stop request suppresses the sweep", canvas.sweep_once(busy, now=4000.0) == [])
        (busy / ".COORDINATOR_STOP").unlink()

        # 8 · nothing waiting → no wake, no log line
        canvas.SWEEP_LAST.clear()
        canvas.SWEEP_WAKES.clear()
        empty = tmp / "empty"
        empty.mkdir()
        coordinator(empty)
        topic(empty, "quiet", notes=0)
        check("no work → no wake", canvas.sweep_once(empty, now=5000.0) == [])
        check("no work → no history line", wakes(empty, "quiet") == [])

        # 9 · global cap: an unanswered wake cannot fill the log forever
        canvas.SWEEP_LAST.clear()
        canvas.SWEEP_WAKES.clear()
        herdr.busy("idle")
        scattered = tmp / "scattered"
        scattered.mkdir()
        coordinator(scattered)
        for i in range(8):
            topic(scattered, "t%d" % i)
            canvas.record_send(scattered, "t%d" % i)
        total = 0
        for step in range(8):
            total += len(canvas.sweep_once(scattered, now=6000.0 + step * 61))
        check("the global cap holds wakes to %d per 10 min" % canvas.SWEEP_MAX_WAKES,
              total <= canvas.SWEEP_MAX_WAKES, total)

    print("\n%s" % ("All sweep tests passed." if not FAILED else "FAILED: %s" % ", ".join(FAILED)))
    return 1 if FAILED else 0


if __name__ == "__main__":
    sys.exit(main())
