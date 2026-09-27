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
 11. every pass says what it established, and says what it did not: a marker
     being current is not the doc still describing the code. A doc locked by the
     design regime is reported by that wording as carrying no marker at all,
     which is the scope the script states rather than a defect it hides.
 12. the pass under --covered names the covered paths as what it measured, since
     a commit can move past the marker without touching them
 13. an empty element in a covered list is a usage error rather than a pass, and
     --covered "" still widens to the whole project
 14. a range git refuses to evaluate fails the run, rather than reading as an
     empty range and passing
 15. the exit-code list in the header covers every arm exit 1 answers for
 16. git's exit status and git's output are different answers: a warning on a
     range that is genuinely empty does not count as commits that moved
 17. a list entry is trimmed before it is judged or used, so `--covered 'src,
     docs'` measures src and docs

Case 11 is the one that is not about a status: the script's exit code is the
same either way, and the reading a caller takes from green is the thing that was
wrong. Cases 12-14 are all that same misreading one layer in — a green taken for
more than it established, up to a green over a range that never ran. Cases 15-17
ask the neighbouring questions: whether the list a reader takes the exit code
from is complete, whether a warning is a commit, and whether a spaced list names
the paths it looks like it names.

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


def header_prose():
    """The script's header comment read as prose: the comment markers and the
    line wrapping removed, so a case can assert a phrase the header states
    rather than whichever fragment a re-wrap left on one line."""
    header = SCRIPT.read_text().split("set -uo")[0]
    return " ".join(line.lstrip("#").strip() for line in header.splitlines())


def git_stderr(repo, *args):
    """What git wrote to stderr, for the cases where the reading is a warning
    rather than an exit status."""
    e = {**os.environ, "GIT_CONFIG_GLOBAL": os.devnull, "GIT_CONFIG_SYSTEM": os.devnull}
    return subprocess.run(["git", *args], cwd=repo, capture_output=True,
                          text=True, env=e).stderr


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
    check("and says nothing was checked", "nothing was checked" in out, out)
    shutil.rmtree(repo)

    # 2 — the directory exists and holds nothing.
    repo, a, b, c = new_repo()
    (pathlib.Path(repo) / "specs").mkdir()
    code, out = run(repo, "--dir", "specs")
    check("a docs directory holding no files passes", code == 0, f"exit {code}: {out}")
    check("and says nothing was checked", "nothing was checked" in out, out)
    shutil.rmtree(repo)

    # 3 — locked at HEAD.
    repo, a, b, c = new_repo()
    lock(repo, ".agents/specs/a.md", c)
    code, out = run(repo)
    check("a doc locked at HEAD passes", code == 0, f"exit {code}: {out}")
    # The green is the misreading this covers: a caller that reads only the
    # status takes it as "the doc still describes the code".
    check("a pass says what it established", "every marker current" in out, out)
    check("and says what it did not", "does not say a doc still describes the code" in out, out)
    check("and counts what it read", "1 locked doc," in out, out)
    check("and names the whole project as what it measured",
          "no commit has landed on the project since" in out, out)
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

    # 11 — the two boundaries the header states, so the statement cannot drift
    #      from the code: a design-locked doc is not a specs lock, and a
    #      directory of plain references fails while a directory of current
    #      specs passes.
    repo, a, b, c = new_repo()
    lock(repo, ".agents/specs/a.md", c)
    lock(repo, ".agents/specs/b.md", c)
    write(repo, "specs/design-system.md", f"<!-- design:locked:{c} 2026-01-01 -->\n")
    code, out = run(repo, "--dir", "specs")
    check("a design-locked doc fails as an unlocked one", code == 1, f"exit {code}: {out}")
    check("under the wording the header states",
          "specs/design-system.md carries no specs:locked marker" in out, out)
    code, out = run(repo, "--dir", ".agents/specs")
    check("two current docs pass together", code == 0, f"exit {code}: {out}")
    check("and the pass counts both", "2 locked docs," in out, out)
    shutil.rmtree(repo)

    # 12 — the pass under --covered says which paths it measured. A commit can
    #      move past the marker and still miss them, so "no commit has moved past
    #      the one a marker names" was true only for the whole project. Here the
    #      marker sits at b and c has landed on src; --covered docs is current.
    repo, a, b, c = new_repo()
    lock(repo, ".agents/specs/a.md", b)
    code, out = run(repo, "--covered", "docs")
    check("a pass under --covered passes", code == 0, f"exit {code}: {out}")
    check("and names the covered paths as what it measured",
          "no commit has landed on the covered paths (docs) since" in out, out)
    check("not the whole history",
          "no commit has moved past the one a marker names" not in out, out)
    shutil.rmtree(repo)

    # 13 — an empty element in a covered list. The loop written to catch a path
    #      that is not here cannot see it: the empty string appends nothing to
    #      `missing`. It then reached git log as an empty pathspec, which is a
    #      fatal, which 2>/dev/null made indistinguishable from an empty range —
    #      so the doc was reported current with a commit past it.
    repo, a, b, c = new_repo()
    lock(repo, ".agents/specs/a.md", a)
    # The whitespace-only entry is the same refusal one step on: an entry is
    # trimmed before it is judged, so a space is as empty as nothing.
    for decl in (",src", "src,,", " ,src"):
        code, out = run(repo, "--covered", decl)
        check(f"--covered {decl!r} refuses", code != 0, f"exit {code}: {out}")
        check(f"--covered {decl!r} names the declaration", decl in out, out)
        check(f"--covered {decl!r} does not report the doc current",
              "every marker current" not in out, out)
    code, out = run(repo, "--covered", "")
    check("--covered '' still widens to the whole project", code == 1,
          f"exit {code}: {out}")
    check("and says so", "the project has moved since" in out, out)
    code, out = run(repo, "--covered", "src")
    check("a correct --covered still reports", code == 1, f"exit {code}: {out}")
    check("and names the paths it measured", "src has moved since" in out, out)
    shutil.rmtree(repo)

    # 14 — a range git refuses to evaluate is not an empty range. The pathspec
    #      here exists, so it clears the loop above, and git rejects it as
    #      invalid magic: without the status check that failure was swallowed.
    repo, a, b, c = new_repo()
    lock(repo, ".agents/specs/a.md", a)
    write(repo, ":(badmagic)x", "not a doc\n")
    code, out = run(repo, "--covered", ":(badmagic)x")
    check("a range git refuses fails the run", code == 1, f"exit {code}: {out}")
    check("and says the doc could not be measured", "could not be measured" in out, out)
    check("and shows why rather than swallowing it",
          "Invalid pathspec magic" in out, out)
    check("and does not report the doc current", "every marker current" not in out, out)
    shutil.rmtree(repo)

    # 15 — the header's exit-code list is what the reader of a gate step takes,
    #      and exit 1 answers for a range that could not be measured as well as
    #      for a doc that is behind. The two have different remedies — nothing
    #      is behind in the first — so the list has to name both, or it names
    #      the wrong remedy for one of them.
    prose = header_prose()
    check("the exit-code list covers a range that could not be measured",
          "could not be measured" in prose, "the exit list does not name it")
    check("and still names the doc that is behind", "behind" in prose, prose)

    # 16 — a range git refuses and a range git measures as empty are different
    #      answers, and so is a warning. This range is genuinely empty; git
    #      exits 0 and writes to stderr only because a tag carries the marker's
    #      short sha as its name, which makes that refname ambiguous. Merged
    #      with 2>&1 the warning landed where the commits go, and the doc was
    #      reported behind on the strength of a line that is not a commit.
    repo, a, b, c = new_repo()
    # lock.sh writes `git rev-parse --short HEAD`, so the marker names a short
    # sha — which is also what a bare token in a range can be read as a refname.
    short = git(repo, "rev-parse", "--short", b)
    lock(repo, ".agents/specs/a.md", short)
    git(repo, "tag", short, b)
    check("the refname really is ambiguous",
          "ambiguous" in git_stderr(repo, "log", "--oneline", f"{short}..HEAD", "--", "docs"),
          "git did not warn, so this case proves nothing")
    code, out = run(repo, "--covered", "docs")
    check("a warning on a clean range does not fail the run", code == 0,
          f"exit {code}: {out}")
    check("and the doc is reported current", "every marker current" in out, out)
    check("and the warning is not reported as commits moved",
          "ambiguous" not in out, out)
    shutil.rmtree(repo)

    # 17 — a list entry is trimmed before it is judged or used. Comma plus space
    #      is the natural spelling of the documented "comma-separated", and
    #      lock.sh trims its contract entries the same way; untrimmed, ` docs`
    #      named a path that is not here and refused honest work.
    repo, a, b, c = new_repo()
    lock(repo, ".agents/specs/a.md", b)
    code, out = run(repo, "--covered", "src, docs")
    check("a spaced list measures both paths it names", code == 1, f"exit {code}: {out}")
    check("and reds on the one that moved", "src,docs has moved since" in out, out)
    check("and not on a path that does not exist",
          "does not exist here" not in out, out)
    lock(repo, ".agents/specs/a.md", c)
    code, out = run(repo, "--covered", "src, docs")
    check("and greens when neither moved", code == 0, f"exit {code}: {out}")
    check("naming the paths it measured", "the covered paths (src,docs)" in out, out)
    shutil.rmtree(repo)

    print()
    if FAILS:
        print(f"{len(FAILS)} failed, {PASS} passed")
        return 1
    print(f"{PASS} passed")
    return 0


if __name__ == "__main__":
    sys.exit(main())
