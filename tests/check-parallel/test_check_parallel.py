#!/usr/bin/env python3
"""
test_check_parallel.py — behaviour tests for bin/check-parallel.sh.

The script turns "is this batch safe to run in parallel?" into a set
intersection over the cards' declared file sets. Its refusal cases are the point,
so each is asserted here:

  1. disjoint cards exit 0
  2. a path declared by two cards exits 1, naming the path
  3. a card with a "## Files" section listing nothing is refused
  4. a card with no "## Files" section at all is refused
  5. naming no cards exits 2 (a check with no input must not pass)
  6. a fenced block is parsed; trailing comments and annotations are dropped
  7. one card naming the same path twice is not an overlap with itself

Usage: python3 tests/check-parallel/test_check_parallel.py     (run from anywhere)
Exit: 0 all pass, 1 any fail. Prints one line per failure, prefixed FAIL.
"""

import pathlib
import subprocess
import sys
import tempfile

SCRIPT = pathlib.Path(__file__).resolve().parents[2] / "bin/check-parallel.sh"

PASS, FAILS = 0, []


def check(name, cond, detail=""):
    global PASS
    if cond:
        PASS += 1
        print(f"  ok   {name}")
    else:
        FAILS.append(name)
        print(f"FAIL {name}: {detail}")


def write(path: pathlib.Path, text=""):
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(text)
    return path


def run(*cards):
    p = subprocess.run(
        ["bash", str(SCRIPT), *[str(c) for c in cards]],
        capture_output=True, text=True,
    )
    return p.returncode, p.stdout + p.stderr


def main():
    tmp = pathlib.Path(tempfile.mkdtemp(prefix="check-parallel-"))
    try:
        a = write(tmp / "a.md", "# A\n\n## Files\nsrc/cmd/main.go\ninternal/registry/registry.go\n")
        b = write(tmp / "b.md", "# B\n\n## Files\n```\ninternal/knowledge/knowledge.go\n```\n")
        c = write(tmp / "c.md", "# C\n\n## Files\ninternal/registry/registry.go\n")
        empty = write(tmp / "empty.md", "# Empty\n\n## Files\n\n## Notes\nnothing\n")
        nofiles = write(tmp / "nofiles.md", "# No files\n\n## Goal\ndo the thing\n")
        annotated = write(
            tmp / "annotated.md",
            "# Annotated\n\n## Files\nsrc/one.go        the first file\nsrc/two.go # inline comment\n",
        )
        selfsame = write(tmp / "selfsame.md", "# Self\n\n## Files\nsrc/one.go\nsrc/one.go\n")

        code, out = run(a, b)
        check("disjoint cards exit 0", code == 0, f"exit {code}: {out}")

        code, out = run(a, c)
        check("overlapping cards exit 1", code == 1, f"exit {code}: {out}")
        check("the overlap names the path", "internal/registry/registry.go" in out, out)
        check("the overlap names both cards", "a.md" in out and "c.md" in out, out)

        code, out = run(empty)
        check("an empty file list is refused", code != 0, f"exit {code}: {out}")
        check("the refusal names the card", "empty.md" in out, out)

        code, out = run(nofiles)
        check("a card with no Files section is refused", code != 0, f"exit {code}: {out}")
        check("the refusal names the convention", "## Files" in out, out)

        code, out = run()
        check("naming no cards exits 2", code == 2, f"exit {code}: {out}")

        code, out = run(annotated, b)
        check("comments and annotations are dropped", code == 0, f"exit {code}: {out}")

        code, out = run(selfsame)
        check("a card listing one path twice is not an overlap", code == 0, f"exit {code}: {out}")
    finally:
        import shutil

        shutil.rmtree(tmp, ignore_errors=True)

    print()
    if FAILS:
        print(f"{len(FAILS)} failed, {PASS} passed")
        return 1
    print(f"{PASS} passed")
    return 0


if __name__ == "__main__":
    sys.exit(main())
