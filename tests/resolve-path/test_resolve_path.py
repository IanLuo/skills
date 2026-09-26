#!/usr/bin/env python3
"""
test_resolve_path.py — behaviour tests for the shipped resolve-path.sh.

The script answers one question: what is this path's single spelling — the one
git prints in `git worktree list --porcelain`? A teardown step matches a
recorded checkout path against that listing, and a match that finds nothing
reads as "already gone", so a spelling the resolver misses is not an error the
step reports; it is a step that exits 0 over a worktree still there.

Every case is built on a real filesystem rather than stubbed, because the whole
subject is what the filesystem says about two names for one directory. The
refusals are the point:

  1. a path with no symlink anywhere comes back as itself
  2. a symlinked final component resolves to its target
  3. a dangling symlink still resolves — the case the branch step needs, where
     the checkout is gone and the link is all that is left
  4. a relative link target is joined to the directory the link sits in, which
     is what /tmp on macOS looks like (private/tmp) and what a bare `/` join
     gets wrong
  5. a trailing slash resolves the same as no trailing slash — `-L` on it asks
     about the directory, not the link, and skips the resolution entirely
  6. a symlink above the last component resolves too
  7. two spellings of one directory come back identical
  8. a path whose directory does not exist exits 1 with nothing on stdout,
     so the caller refuses rather than matching nothing and passing
  9. no argument, an empty argument and two arguments exit 2

Usage: python3 tests/resolve-path/test_resolve_path.py     (run from anywhere)
Exit: 0 all pass, 1 any fail. Prints one line per failure, prefixed FAIL.
"""

import os
import pathlib
import subprocess
import sys
import tempfile

SCRIPT = pathlib.Path(__file__).resolve().parents[2] / "src/flagship/defaults/scripts/resolve-path.sh"

PASS, FAILS = 0, []


def check(name, cond, detail=""):
    global PASS
    if cond:
        PASS += 1
        print(f"  ok   {name}")
    else:
        FAILS.append(name)
        print(f"FAIL {name}: {detail}")


def run(*args):
    p = subprocess.run(["bash", str(SCRIPT), *args], capture_output=True, text=True)
    return p.returncode, p.stdout.strip(), p.stderr.strip()


def scratch():
    """A real directory for the real filesystem questions, and its resolved
    spelling — /var/folders/... is itself reached through a link on macOS, so
    the expected answers are derived rather than written down."""
    root = pathlib.Path(tempfile.mkdtemp(prefix="resolve-path-"))
    return root, pathlib.Path(os.path.realpath(root))


def main():
    check("the script is readable", SCRIPT.is_file(), f"{SCRIPT} is missing")

    root, real = scratch()

    # 1 — nothing to resolve.
    plain = root / "plain"
    plain.mkdir()
    code, out, err = run(str(plain))
    check("a path with no symlink resolves to itself",
          code == 0 and out == str(real / "plain"), f"exit {code}: {out!r} {err!r}")

    # 2 — the final component is a link.
    target = root / "target"
    target.mkdir()
    link = root / "link"
    link.symlink_to(target)
    code, out, err = run(str(link))
    check("a symlinked final component resolves to its target",
          code == 0 and out == str(real / "target"), f"exit {code}: {out!r} {err!r}")

    # 3 — the link is all that is left.
    dangling = root / "dangling"
    dangling.symlink_to(root / "vanished")
    code, out, err = run(str(dangling))
    check("a dangling symlink still resolves",
          code == 0 and out == str(real / "vanished"), f"exit {code}: {out!r} {err!r}")

    # 4 — a relative target, which is what /tmp is: the join must be to the
    # directory the link sits in, not to a bare slash.
    os.symlink("target", str(root / "relative"))
    code, out, err = run(str(root / "relative"))
    check("a relative link target is joined to the link's own directory",
          code == 0 and out == str(real / "target"), f"exit {code}: {out!r} {err!r}")

    # 5 — the trailing slash, which hides the link from -L.
    code, out, err = run(str(link) + "/")
    check("a trailing slash resolves the same as without it",
          code == 0 and out == str(real / "target"), f"exit {code}: {out!r} {err!r}")

    # 6 — a link above the last component.
    above = root / "above"
    (above / "inner").mkdir(parents=True)
    (root / "up").symlink_to(target)
    code, out, err = run(str(root / "up" / ".." / "target"))
    check("a symlink above the last component resolves",
          code == 0 and out == str(real / "target"), f"exit {code}: {out!r} {err!r}")

    # 7 — the property the steps actually depend on.
    _, a, _ = run(str(link))
    _, b, _ = run(str(link) + "/")
    _, c, _ = run(str(target))
    check("every spelling of one directory comes back identical",
          a == b == c, f"{a!r} {b!r} {c!r}")

    # 8 — nothing to match against, so the caller must be told.
    code, out, err = run(str(root / "no-such-parent" / "child"))
    check("an unresolvable path exits 1", code == 1, f"exit {code}: {out!r} {err!r}")
    check("an unresolvable path prints nothing on stdout", out == "", f"{out!r}")

    # 9 — the command line.
    for args, what in [((), "no argument"), (("",), "an empty argument"),
                       (("/tmp", "/tmp"), "two arguments")]:
        code, _, err = run(*args)
        check(f"{what} exits 2", code == 2, f"exit {code}: {err!r}")

    print()
    if FAILS:
        print(f"{len(FAILS)} failed, {PASS} passed")
        return 1
    print(f"{PASS} passed")
    return 0


if __name__ == "__main__":
    sys.exit(main())
