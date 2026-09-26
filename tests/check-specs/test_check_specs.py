#!/usr/bin/env python3
"""
test_check_specs.py — behaviour tests for the shipped check-specs.sh.

The script turns "is every locked doc still true?" into "which commits have
landed since the commit this doc names?". It ships inside the fs binary and is
read back out of it, so it may not know any one project's layout: with no
argument it measures the whole project, and --covered is how a caller that does
know the repo asks a narrower question.

Every case below runs against a throwaway git repo of three commits — src/,
then docs/, then src/ again — so the answers are real commits rather than a
stubbed history. The refusal cases are the point:

  1. a project with no docs directory exits 0 — absence is not staleness
  2. a docs directory holding no files exits 0
  3. a doc locked at HEAD passes
  4. a doc locked behind a commit fails, naming the commits that moved past it
  5. --covered narrows that to the paths named, in both directions
  6. --covered naming a path that is not here fails rather than passing
     vacuously on a pathspec that matches nothing
  7. a doc with no specs:locked marker fails
  8. a doc locked at a sha that is no commit in the repo fails
  9. a doc locked at a commit that is not an ancestor of HEAD fails
 10. one stale doc among several fails the run, and names the stale one only

Usage: python3 tests/check-specs/test_check_specs.py     (run from anywhere)
Exit: 0 all pass, 1 any fail. Prints one line per failure, prefixed FAIL.
"""

import os
import pathlib
import shutil
import subprocess
import sys
import tempfile

SCRIPT = pathlib.Path(__file__).resolve().parents[2] / "src/flagship/defaults/scripts/check-specs.sh"

PASS, FAILS = 0, []


def check(name, cond, detail=""):
    global PASS
    if cond:
        PASS += 1
        print(f"  ok   {name}")
    else:
        FAILS.append(name)
        print(f"FAIL {name}: {detail}")


def git(repo, *args):
    e = {
        **os.environ,
        "GIT_AUTHOR_NAME": "t", "GIT_AUTHOR_EMAIL": "t@e",
        "GIT_COMMITTER_NAME": "t", "GIT_COMMITTER_EMAIL": "t@e",
        "GIT_CONFIG_GLOBAL": os.devnull, "GIT_CONFIG_SYSTEM": os.devnull,
    }
    return subprocess.run(["git", *args], cwd=repo, capture_output=True,
                          text=True, env=e, check=True).stdout.strip()


def write(repo, rel, text):
    p = pathlib.Path(repo) / rel
    p.parent.mkdir(parents=True, exist_ok=True)
    p.write_text(text)
    return p


def commit(repo, message):
    git(repo, "add", "-A")
    git(repo, "commit", "-m", message)
    return git(repo, "rev-parse", "HEAD")


def lock(repo, doc, sha, body=""):
    """Write a doc whose marker names sha — the shape lock.sh writes."""
    write(repo, doc, f"<!-- specs:locked:{sha} 2026-01-01 type=spec -->\n\n{body}")


def run(repo, *args):
    """Run the script with the throwaway repo as cwd, as a gate step would."""
    e = {**os.environ, "GIT_CONFIG_GLOBAL": os.devnull, "GIT_CONFIG_SYSTEM": os.devnull}
    p = subprocess.run(["bash", str(SCRIPT), *args], cwd=repo,
                       capture_output=True, text=True, env=e)
    return p.returncode, p.stdout + p.stderr


def new_repo():
    """src/, then docs/, then src/ again — so a covered path can move twice and
    an uncovered one can sit still."""
    repo = tempfile.mkdtemp(prefix="check-specs-")
    git(repo, "init", "-q", "-b", "main")
    write(repo, "src/code.go", "package main\n")
    a = commit(repo, "add src")
    write(repo, "docs/note.md", "hi\n")
    b = commit(repo, "add docs")
    write(repo, "src/more.go", "package main\n")
    c = commit(repo, "add src again")
    return repo, a, b, c


def main():
    check("the script is readable", SCRIPT.is_file(), f"{SCRIPT} is missing")

    # 1 — no docs directory at all.
    repo, a, b, c = new_repo()
    code, out = run(repo, "--dir", ".agents/specs")
    check("a project with no docs directory passes", code == 0, f"exit {code}: {out}")
    shutil.rmtree(repo)

    # 2 — the directory exists and holds nothing.
    repo, a, b, c = new_repo()
    (pathlib.Path(repo) / "specs").mkdir()
    code, out = run(repo, "--dir", "specs")
    check("a docs directory holding no files passes", code == 0, f"exit {code}: {out}")
    shutil.rmtree(repo)

    # 3 — locked at HEAD.
    repo, a, b, c = new_repo()
    lock(repo, ".agents/specs/a.md", c)
    code, out = run(repo)
    check("a doc locked at HEAD passes", code == 0, f"exit {code}: {out}")
    shutil.rmtree(repo)

    # 4 — locked behind commits, and with no --covered the whole project counts.
    repo, a, b, c = new_repo()
    lock(repo, ".agents/specs/a.md", a)
    code, out = run(repo)
    check("a doc behind HEAD fails", code == 1, f"exit {code}: {out}")
    check("the failure names the doc and every commit that moved past it",
          "a.md" in out and "add docs" in out and "add src again" in out, out)
    check("and says what it measured", "the project has moved since" in out, out)
    shutil.rmtree(repo)

    # 5 — --covered is the only way to narrow it, and it cuts both ways.
    repo, a, b, c = new_repo()
    lock(repo, ".agents/specs/a.md", b)
    code, out = run(repo, "--covered", "docs")
    check("--covered to a path that did not move passes", code == 0, f"exit {code}: {out}")
    code, out = run(repo, "--covered", "src")
    check("--covered to a path that did move fails", code == 1, f"exit {code}: {out}")
    check("and the failure names only the covered path's commit",
          "add src again" in out and "add docs" not in out, out)
    shutil.rmtree(repo)

    # 7 — no marker.
    repo, a, b, c = new_repo()
    write(repo, ".agents/specs/a.md", "# a doc with no lock\n")
    code, out = run(repo)
    check("a doc with no lock marker fails", code == 1, f"exit {code}: {out}")
    check("the failure says the marker is missing", "no specs:locked marker" in out, out)
    shutil.rmtree(repo)

    # 8 — a sha that is not a commit here.
    repo, a, b, c = new_repo()
    lock(repo, ".agents/specs/a.md", "deadbee")
    code, out = run(repo)
    check("a doc locked at an unknown sha fails", code == 1, f"exit {code}: {out}")
    check("the failure says the sha is not a commit", "not a commit" in out, out)
    shutil.rmtree(repo)

    # 9 — a commit that is not an ancestor of HEAD.
    repo, a, b, c = new_repo()
    git(repo, "checkout", "-q", "-b", "side", a)
    write(repo, "src/side.go", "package main\n")
    side = commit(repo, "side work")
    git(repo, "checkout", "-q", "main")
    lock(repo, ".agents/specs/a.md", side)
    code, out = run(repo)
    check("a doc locked off-branch fails", code == 1, f"exit {code}: {out}")
    check("the failure says the sha is not an ancestor", "not an ancestor" in out, out)
    shutil.rmtree(repo)

    # 10 — one stale doc among several.
    repo, a, b, c = new_repo()
    lock(repo, ".agents/specs/stale.md", a)
    lock(repo, ".agents/specs/current.md", c)
    code, out = run(repo)
    check("one stale doc fails the run", code == 1, f"exit {code}: {out}")
    check("the stale doc is named and the current one is not",
          "stale.md" in out and "current.md" not in out, out)
    shutil.rmtree(repo)

    print()
    if FAILS:
        print(f"{len(FAILS)} failed, {PASS} passed")
        return 1
    print(f"{PASS} passed")
    return 0


if __name__ == "__main__":
    sys.exit(main())
