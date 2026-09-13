#!/usr/bin/env python3
"""Wake target — the daemon must wake the root's COORDINATOR, never a per-topic worker record.

Characterizes the failure measured 2026-09-12: the wake path resolved a per-topic `worker.json`
that only the rejected per-topic watcher ever wrote, so 18 of 21 wakes ended "no worker
recorded" while `.coordinator.json` named a live pane. Locked decision:
`skills/canvas/references/architecture.md` § Load-bearing structure (4).

Run: python3 tests/canvas/test_wake_target.py
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
    """A herdr that records its argv and succeeds, so we assert WHO was woken, not how."""
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


def new_topic(root, topic="probe"):
    d = root / topic
    d.mkdir(parents=True)
    (d / "content.html").write_text('<section data-section="s1">x</section>', encoding="utf-8")
    return d


def woken(log):
    if not log.exists():
        return []
    return [json.loads(line) for line in log.read_text(encoding="utf-8").splitlines() if line.strip()]


def main():
    with tempfile.TemporaryDirectory() as td:
        tmp = Path(td)
        log = fake_herdr(tmp)
        root = tmp / "root"
        root.mkdir()
        new_topic(root)

        # 1 · the coordinator recorded in .coordinator.json is the target
        (root / ".coordinator.json").write_text(json.dumps(
            {"agent_name": "canvas-coord-probe", "pane_id": "w1:p9", "kind": "pi"}), encoding="utf-8")
        res = canvas.notify_worker(root, "probe", 2)
        check("coordinator is woken", res.get("ok") is True and res.get("agent") == "canvas-coord-probe", res)
        argv = woken(log)
        check("herdr got one prompt", len(argv) == 1 and argv[0][:3] == ["agent", "prompt", "canvas-coord-probe"], argv)
        check("prompt says dispatch a round", bool(argv) and " round " in " " + " ".join(argv[0]) + " ", argv)

        # 2 · the orphaned per-topic worker.json must NOT satisfy the wake path
        (root / ".coordinator.json").unlink()
        (root / "probe" / "worker.json").write_text(json.dumps(
            {"agent_name": "canvas-probe-r1", "pane_id": "w1:p1"}), encoding="utf-8")
        res = canvas.notify_worker(root, "probe", 1)
        check("orphaned worker.json does not wake", res.get("ok") is False, res)
        check("and the reason names the coordinator",
              res.get("why") == "no coordinator recorded", res)
        check("no extra herdr prompt was sent", len(woken(log)) == 1, woken(log))

        # 3 · a coordinator record with only a pane id still resolves
        (root / ".coordinator.json").write_text(json.dumps({"pane_id": "w2:p7"}), encoding="utf-8")
        res = canvas.notify_worker(root, "probe", 1)
        check("pane id is the fallback target", res.get("ok") is True and res.get("agent") == "w2:p7", res)

        # 4 · no record at all → an explicit reason, never a silent failure
        (root / ".coordinator.json").unlink()
        res = canvas.notify_worker(root, "probe", 1)
        check("absent record gives a reason", res.get("why") == "no coordinator recorded", res)

    print("\n%s" % ("All wake-target tests passed." if not FAILED else "FAILED: %s" % ", ".join(FAILED)))
    return 1 if FAILED else 0


if __name__ == "__main__":
    sys.exit(main())
