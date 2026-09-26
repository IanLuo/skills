#!/usr/bin/env python3
"""
test_check_binary_fresh.py — behaviour tests for bin/check-binary-fresh.sh.

The script answers one question: is the fs about to certify this work the build
of the tree it belongs to? It is bound as a project-scope exit check for this
repo, so what it must not do is fire in a project that is not fs's own source,
and what it must not do is pass a binary it cannot vouch for.

Every case runs against throwaway git repos and a stand-in binary — a shell
script that answers `version` with a stamp — so the answers are real revisions
and real file states rather than a stubbed history. The refusal cases are the
point:

  1. a binary that is the build of its tree, unchanged since, passes
  2. a commit touching src/flagship after the build fails, naming the file
  3. an edit to src/flagship that was never committed fails
  4. a commit touching only skills/ passes — unrelated work does not stale a build
  5. a build stamped dirty fails: it cannot be shown to be the tree's content
  6. a build whose revision is "unknown" fails
  7. a binary too old to answer `version` fails, naming the rebuild
  8. a binary in another checkout passes while the tree it runs in has moved
     (the worktree case: a worker's unmerged change is not the cap's binary's
     business, and refusing there would have no remedy to offer)
  9. a revision that is no commit in the tree fails
 10. a directory that is no checkout of this repo refuses rather than passing
     vacuously — it cannot evaluate, and a gate that cannot evaluate refuses
 11. no binary named, or a path that is not executable, is exit 2

Usage: python3 tests/binary-fresh/test_check_binary_fresh.py   (from anywhere)
Exit: 0 all pass, 1 any fail. Prints one line per failure, prefixed FAIL.
"""

import os
import pathlib
import stat
import subprocess
import sys
import tempfile

SCRIPT = pathlib.Path(__file__).resolve().parents[2] / "bin/check-binary-fresh.sh"

PASS, FAILS = 0, []
TMP = tempfile.mkdtemp(prefix="binary-fresh-test-")

# The stand-in binary: `fs version` prints the envelope fs prints, with the stamp
# the test gives it. Anything else is an unknown command, which is what a real
# fs that predates the stamp does.
FAKE = """#!/usr/bin/env bash
if [ "$1" = "version" ]; then
  printf '{\\n  "ok": true,\\n  "data": {\\n    "revision": "%s",\\n    "dirty": "%s",\\n    "path": "%s"\\n  }\\n}\\n'
  exit 0
fi
if [ "$1" = "--help" ]; then
  echo "usage: fs <command>" >&2
  exit 0
fi
echo "fs: unknown command $1" >&2
exit 2
"""


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


def commit(repo, message):
    git(repo, "add", "-A")
    git(repo, "commit", "-q", "-m", message)
    return git(repo, "rev-parse", "HEAD")


def new_repo(name):
    """A throwaway checkout of this repo's shape: flake.nix + src/flagship."""
    repo = pathlib.Path(TMP) / name
    repo.mkdir(parents=True)
    git(repo, "init", "-q", "-b", "main")
    write(repo, "flake.nix", "{ }\n")
    write(repo, "src/flagship/main.go", "package main\n")
    write(repo, "skills/thing/SKILL.md", "# thing\n")
    commit(repo, "first")
    return repo


def fake_binary(repo, rel, rev, dirty, own=None):
    """Install the stand-in binary at repo/rel, stamped rev/dirty."""
    p = pathlib.Path(repo) / rel
    p.parent.mkdir(parents=True, exist_ok=True)
    p.write_text(FAKE % (rev, dirty, own if own is not None else str(p)))
    p.chmod(p.stat().st_mode | stat.S_IXUSR | stat.S_IXGRP | stat.S_IXOTH)
    return p


def run(binary=None, cwd=None):
    args = ["bash", str(SCRIPT)] + ([str(binary)] if binary else [])
    env = {**os.environ}
    env.pop("FS_BIN", None)
    p = subprocess.run(args, cwd=cwd or TMP, capture_output=True, text=True, env=env)
    return p.returncode, p.stderr + p.stdout


def main():
    check("the script is readable", SCRIPT.is_file(), f"{SCRIPT} is missing")

    # 1. the build of its tree, unchanged since
    repo = new_repo("fresh")
    rev = git(repo, "rev-parse", "HEAD")
    bin = fake_binary(repo, "bin/fake-fs", rev, "false")
    write(repo, "skills/thing/SKILL.md", "# thing, edited but not committed\n")
    code, out = run(bin, repo)
    check("a fresh binary passes", code == 0, f"exit {code}: {out}")
    check("and says which rev and tree", rev[:7] in out and str(repo) in out, out)

    # 2. a commit touching src/flagship after the build
    binary = fake_binary(repo, "bin/fake-fs", rev, "false")
    write(repo, "src/flagship/main.go", "package main // the fix under test\n")
    commit(repo, "edit the source")
    code, out = run(binary, repo)
    check("a commit past the build fails", code == 1, f"exit {code}: {out}")
    check("and names the file that moved", "src/flagship/main.go" in out, out)
    check("and names the rebuild", "build-project.sh" in out, out)

    # 3. an uncommitted edit to src/flagship
    rev2 = git(repo, "rev-parse", "HEAD")
    binary = fake_binary(repo, "bin/fake-fs", rev2, "false")
    write(repo, "src/flagship/main.go", "package main // edited\n")
    code, out = run(binary, repo)
    check("an uncommitted source edit fails", code == 1, f"exit {code}: {out}")
    git(repo, "checkout", "--", "src/flagship/main.go")

    # 4. a commit touching only skills/ — no churn from unrelated work
    binary = fake_binary(repo, "bin/fake-fs", rev2, "false")
    write(repo, "skills/thing/SKILL.md", "# thing, changed\n")
    commit(repo, "docs only")
    code, out = run(binary, repo)
    check("a docs-only commit does not stale the build", code == 0, f"exit {code}: {out}")

    # 5. a build from a dirty tree
    head = git(repo, "rev-parse", "HEAD")
    binary = fake_binary(repo, "bin/fake-fs", head, "true")
    code, out = run(binary, repo)
    check("a dirty-stamped build fails", code == 1, f"exit {code}: {out}")
    check("and says why it cannot be judged", "uncommitted" in out, out)

    # 6. a revision the build cannot name
    binary = fake_binary(repo, "bin/fake-fs", "unknown", "unknown")
    code, out = run(binary, repo)
    check("an unknown revision fails", code == 1, f"exit {code}: {out}")

    # 7. a binary too old to answer
    old = fake_binary(repo, "bin/old-fs", "", "")
    old.write_text("#!/usr/bin/env bash\necho 'fs: unknown command \"version\"' >&2\nexit 2\n")
    old.chmod(old.stat().st_mode | stat.S_IXUSR)
    code, out = run(old, repo)
    check("a build that cannot say what it is fails", code == 1, f"exit {code}: {out}")
    check("and names the rebuild", "build-project.sh" in out, out)

    # 8. the worktree case: the binary belongs to a checkout that is fresh, while
    # the tree it is run in has moved. That is a worker's worktree, and the
    # binary that certifies the cap's tree is not wrong for not containing an
    # unmerged change.
    main = new_repo("main")
    main_rev = git(main, "rev-parse", "HEAD")
    main_bin = fake_binary(main, "bin/fake-fs", main_rev, "false")
    work = pathlib.Path(TMP) / "worktree"
    work.mkdir()
    git(TMP, "clone", "-q", str(main), str(work))
    write(work, "src/flagship/main.go", "package main // the worker's change\n")
    commit(work, "the worker's change")
    code, out = run(main_bin, work)
    check("a worker's unmerged change does not refuse the main build", code == 0,
          f"exit {code}: {out}")

    # 9. a revision that is no commit in the tree
    other = new_repo("other")
    other_rev = git(other, "rev-parse", "HEAD")
    binary = fake_binary(repo, "bin/fake-fs", other_rev, "false")
    code, out = run(binary, repo)
    check("a revision from another repo fails", code == 1, f"exit {code}: {out}")

    # 10. no checkout anywhere: the script cannot evaluate, so it refuses
    scratch = pathlib.Path(TMP) / "scratch"
    scratch.mkdir()
    loose = fake_binary(scratch, "fake-fs", "abcdef1", "false")
    code, out = run(loose, scratch)
    check("a tree that is no checkout refuses rather than passing", code == 1,
          f"exit {code}: {out}")
    check("and says there is nothing to measure against", "no tree" in out, out)

    # 11. misuse
    code, out = run(None, TMP)
    check("no binary named is exit 2", code == 2, f"exit {code}: {out}")
    code, out = run(pathlib.Path(TMP) / "does-not-exist", TMP)
    check("a path that is not executable is exit 2", code == 2, f"exit {code}: {out}")

    print()
    if FAILS:
        print(f"{PASS} passed, {len(FAILS)} failed: {', '.join(FAILS)}")
        return 1
    print(f"all {PASS} checks passed")
    return 0


if __name__ == "__main__":
    sys.exit(main())
