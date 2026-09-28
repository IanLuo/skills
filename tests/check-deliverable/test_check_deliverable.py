#!/usr/bin/env python3
"""
test_check_deliverable.py — behaviour tests for the shipped check-deliverable.sh.

The script lives in src/flagship/defaults/scripts/ because it ships inside the fs
binary, which reads the body back out of itself and runs it: a playbook
addresses it as `"$FS_BIN" script check-deliverable.sh`, and nothing is written
to disk. This test runs it against real repositories in a temp directory, with
the tree it is asked about as the cwd.

The defect it answers: a card asked for "the commit sha" as evidence, a worker's
node named one, and fs close passed — while 15 changed files sat uncommitted in
the working tree and HEAD had not moved. A card is a request, not a gate.

So what is asserted here is the gate's bite:

  1. a commit past the base exits 0 — the ordinary pass
  2. HEAD still at the base exits 1, naming both revisions and the count of
     changed paths: the defect above, exactly
  3. a base that is not a commit in this tree exits 1, naming both
  4. a tree that is not a git repository stands down at 0 and says so
  5. an empty base — a dispatch prepared with none recorded — stands down at 0
  6. no arguments at all is a usage error at 2
  7. a card's declared paths, committed and clean, exit 0
  8. a path the card declares and the change leaves dirty exits 1, naming the
     path and the card
  9. a change that commits nothing on the card's declared paths exits 1, naming
     the card — "a commit exists" is not "the deliverable reached the tree"
 10. a card that is not in the tree but is in the checkout it belongs to is read
     from there: a batch member's worktree does not carry an uncommitted card
     from the cap's own checkout
 11. a card that is in neither exits 1, naming the card
 12. the "## Files" convention is read as check-parallel.sh reads it: fenced
     blocks and comments are skipped, a trailing annotation is dropped
 13. a card declaring nothing is refused rather than passed vacuously

Usage: python3 tests/check-deliverable/test_check_deliverable.py   (from anywhere)
Exit: 0 all pass, 1 any fail. Prints one line per failure, prefixed FAIL.
"""

import pathlib
import subprocess
import sys
import tempfile

SCRIPT = pathlib.Path(__file__).resolve().parents[2] / "src/flagship/defaults/scripts/check-deliverable.sh"

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


def git(cwd, *args):
    return subprocess.run(
        ["git", *args], cwd=cwd, capture_output=True, text=True, check=True
    ).stdout.strip()


def repo(path: pathlib.Path, seed="seed.txt"):
    """A git repository with one commit, whose revision is returned."""
    path.mkdir(parents=True, exist_ok=True)
    git(path, "init", "-q", "-b", "main")
    git(path, "config", "user.email", "fs@test.invalid")
    git(path, "config", "user.name", "fs test")
    write(path / seed, "seed\n")
    git(path, "add", seed)
    git(path, "commit", "-q", "-m", "seed")
    return git(path, "rev-parse", "HEAD")


def commit(cwd, name, text=None):
    write(pathlib.Path(cwd) / name, text if text is not None else name + "\n")
    git(cwd, "add", name)
    git(cwd, "commit", "-q", "-m", name)
    return git(cwd, "rev-parse", "HEAD")


def run(*args, cwd):
    return subprocess.run(
        ["bash", str(SCRIPT), *[str(a) for a in args]],
        cwd=cwd, capture_output=True, text=True,
    )


def card(path, body):
    return write(path, "# Task: sample\n\n## Files\n\n" + body + "\n")


# ── the change must be committed ──────────────────────────────────────────
with tempfile.TemporaryDirectory() as tmp:
    tmp = pathlib.Path(tmp)
    tree = tmp / "tree"
    base = repo(tree)

    # 1. the ordinary pass: HEAD has moved past the base.
    head = commit(tree, "work.txt")
    p = run(base, cwd=tree)
    check(
        "a commit past the base exits 0",
        p.returncode == 0,
        f"exit {p.returncode}: {p.stderr.strip() or p.stdout.strip()}",
    )
    check(
        "the pass names what it measured",
        head[:7] in p.stdout or base[:7] in p.stdout,
        f"stdout = {p.stdout.strip()!r}",
    )

    # 2. the defect: HEAD at the base, changed files uncommitted.
    tree2 = tmp / "tree2"
    base2 = repo(tree2)
    write(tree2 / "work.txt", "uncommitted\n")
    git(tree2, "add", "work.txt")
    p = run(base2, cwd=tree2)
    check("HEAD still at the base exits 1", p.returncode == 1, f"exit {p.returncode}")
    check(
        "the refusal names both revisions and the changed count",
        base2[:7] in p.stderr and "1" in p.stderr and "commit" in p.stderr,
        f"stderr = {p.stderr.strip()!r}",
    )

    # 3. a base this tree does not know.
    p = run("0" * 40, cwd=tree)
    check("a base that is not a commit here exits 1", p.returncode == 1, f"exit {p.returncode}")
    check(
        "the refusal names the base it could not read",
        ("0" * 40) in p.stderr and "not a commit" in p.stderr,
        f"stderr = {p.stderr.strip()!r}",
    )

    # 4. not a repository at all.
    plain = tmp / "plain"
    plain.mkdir()
    p = run("abc1234", cwd=plain)
    check("a non-repository tree stands down at 0", p.returncode == 0, f"exit {p.returncode}: {p.stderr.strip()}")
    check(
        "the stand-down says why",
        "not a git repository" in p.stdout + p.stderr,
        f"output = {(p.stdout + p.stderr).strip()!r}",
    )

    # 5. no base recorded.
    p = run("", cwd=tree)
    check("an empty base stands down at 0", p.returncode == 0, f"exit {p.returncode}: {p.stderr.strip()}")
    check(
        "that stand-down says why",
        "no revision was recorded" in p.stdout + p.stderr,
        f"output = {(p.stdout + p.stderr).strip()!r}",
    )

    # 6. no arguments.
    p = run(cwd=tree)
    check("no arguments is a usage error at 2", p.returncode == 2, f"exit {p.returncode}")

    # ── the paths the cards declare ────────────────────────────────────────
    tree3 = tmp / "tree3"
    base3 = repo(tree3)
    c = card(tmp / "card.md", "src/a.go")
    write(tree3 / "src/a.go", "package a\n")
    commit(tree3, "src/a.go")

    # 7. declared, committed, clean.
    p = run(base3, c, cwd=tree3)
    check("a card's committed paths exit 0", p.returncode == 0, f"exit {p.returncode}: {p.stderr.strip()}")

    # 8. declared and left dirty.
    write(tree3 / "src/a.go", "package a // dirty\n")
    p = run(base3, c, cwd=tree3)
    check("a declared path left dirty exits 1", p.returncode == 1, f"exit {p.returncode}")
    check(
        "the refusal names the path and the card",
        "src/a.go" in p.stderr and "card.md" in p.stderr,
        f"stderr = {p.stderr.strip()!r}",
    )
    git(tree3, "checkout", "--", "src/a.go")

    # 9. the change commits nothing on the declared paths.
    commit(tree3, "unrelated.txt")
    c2 = card(tmp / "card2.md", "src/b.go")
    p = run(base3, c2, cwd=tree3)
    check("a card whose paths no commit touches exits 1", p.returncode == 1, f"exit {p.returncode}")
    check(
        "that refusal names the card",
        "card2.md" in p.stderr,
        f"stderr = {p.stderr.strip()!r}",
    )

    # 10. the card is in the cap's checkout, not the member's worktree.
    main_co = tmp / "main"
    repo(main_co)
    c3 = card(main_co / "cards" / "card3.md", "src/a.go")
    worktree = tmp / "wt"
    git(main_co, "worktree", "add", "-q", "-b", "member", str(worktree))
    write(worktree / "src/a.go", "package a\n")
    git(worktree, "add", "src/a.go")
    git(worktree, "commit", "-q", "-m", "the member's work")
    mbase = git(worktree, "rev-parse", "HEAD~1")
    p = run(mbase, "cards/card3.md", cwd=worktree)
    check(
        "a card absent from the worktree is read from the checkout it belongs to",
        p.returncode == 0,
        f"exit {p.returncode}: {p.stderr.strip()}",
    )

    # 11. a card in neither.
    p = run(mbase, "cards/nowhere.md", cwd=worktree)
    check("a card that is nowhere exits 1", p.returncode == 1, f"exit {p.returncode}")
    check(
        "that refusal names the card",
        "nowhere.md" in p.stderr,
        f"stderr = {p.stderr.strip()!r}",
    )

    # 12. the "## Files" convention, as check-parallel.sh reads it.
    c4 = card(
        tmp / "card4.md",
        "```\nignored/inside/fence.go\n```\nsrc/a.go   # touched for the seam\n# a whole-line comment",
    )
    p = run(base3, c4, cwd=tree3)
    check(
        "fences and comments are skipped, annotations dropped",
        p.returncode == 0,
        f"exit {p.returncode}: {p.stderr.strip()}",
    )

    # 13. a card declaring nothing.
    c5 = card(tmp / "card5.md", "# nothing here")
    p = run(base3, c5, cwd=tree3)
    check("a card declaring no files exits 1", p.returncode == 1, f"exit {p.returncode}")
    check(
        "that refusal says the card declares nothing",
        "declares no files" in p.stderr,
        f"stderr = {p.stderr.strip()!r}",
    )

print()
if FAILS:
    print(f"FAILED: {len(FAILS)} of {PASS + len(FAILS)}: {', '.join(FAILS)}")
    sys.exit(1)
print(f"all {PASS} checks passed")
