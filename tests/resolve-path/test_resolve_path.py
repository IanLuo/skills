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
  6. a symlink above the last component resolves too, and one whose target
     leaves the link's own directory is where the textual fold and the kernel
     answer different directories — the shipped answer is asserted, so the
     choice the resolver's comment records is pinned by a test
  7. two spellings of one directory come back identical
  8. a path whose directory does not exist exits 1 with nothing on stdout,
     so the caller refuses rather than matching nothing and passing
  9. no argument, an empty argument and two arguments exit 2
 10. a symlink that leads back to a symlink exits 1 rather than spinning:
     `readlink` reads a target, it does not resolve one, so nothing underneath
     refuses on the script's behalf and the loop would never end. A teardown step
     that hangs is a close that never returns, so this is checked with a timeout —
     the failure it is guarding against looks exactly like a test that stopped.
 11. a chain longer than the bound exits 1 too, and a short one still resolves,
     so the bound is a bound and not a refusal of everything.
 12. the last component can be a REFERENCE to a directory rather than its name:
     `/x/.` and `/x/y/..` both name `/x`, and `cd //x && pwd -P` answers `//x`.
     None of the three is a spelling git prints, and each one goes in and comes
     back out unchanged — a match against git's listing that finds nothing, which
     a caller reads as "already gone" rather than as the miss it is. That is the
     leak this closes: a branch left behind, exit 0, nothing said.
 13. one directory has many spellings and every one of them comes back as the
     same single string — the caller compares that against `git worktree list
     --porcelain`, so a second answer is a worktree it is told is not
     registered. Asserted as a property over combinations rather than as a list
     of the spellings someone thought of, because folding a trailing slash can
     expose a `/.` and folding a `/.` can expose a `//`, so the interesting
     cases are the compositions — and together with idempotence, since an answer
     with nothing left to fold is one resolving again cannot change.

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


def run(*args, timeout=20):
    """A check that hangs must fail the suite, not stop it. A timeout comes back
    as no exit code at all, which no assertion here accepts."""
    try:
        p = subprocess.run(["bash", str(SCRIPT), *args], capture_output=True,
                           text=True, timeout=timeout)
    except subprocess.TimeoutExpired:
        return None, "", f"did not return within {timeout}s"
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

    # 6 — a link above the last component, which is the one place the fold and
    # the kernel are not the same question. A link pointing INTO the directory
    # it sits in, as `up` does above, is a link the two folds agree about. A
    # link pointing OUT of it is not: `..` pops the component before it in the
    # STRING, so it pops the LINK rather than the directory the link names, and
    # `beside/l/../sibling` answers `beside/sibling` where the kernel follows
    # `l` to `away` first and reaches the sibling of `away`'s parent. Two
    # different directories. The textual answer is the shipped one, by choice,
    # and both are asserted below: the resolver's own comment is where the
    # choice is written down, and until this case existed nothing failed if a
    # later swap to a physical fold changed the answer here.
    above = root / "above"
    (above / "inner").mkdir(parents=True)
    (root / "up").symlink_to(target)
    code, out, err = run(str(root / "up" / ".." / "target"))
    check("a symlink above the last component resolves",
          code == 0 and out == str(real / "target"), f"exit {code}: {out!r} {err!r}")

    beside = root / "beside"
    beside.mkdir()
    (root / "away").mkdir()
    (root / "sibling").mkdir()
    os.symlink("../away", str(beside / "l"))
    spelled = str(beside / "l" / ".." / "sibling")
    code, out, err = run(spelled)
    check("a link above the last component that leaves its own directory "
          "answers the textual fold",
          code == 0 and out == str(real / "beside" / "sibling"),
          f"exit {code}: {out!r} want {str(real / 'beside' / 'sibling')!r} {err!r}")
    check("and the kernel answers a different directory, so the two folds are "
          "told apart by this case",
          os.path.realpath(spelled) == str(real / "sibling"),
          f"realpath {os.path.realpath(spelled)!r} want {str(real / 'sibling')!r}")

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

    # 10 — a link that leads back to a link never terminates. This is the case
    # that hung: `readlink` reads the target instead of resolving it, so no
    # syscall underneath ever returns ELOOP and the loop has nothing to stop it.
    cyc_a, cyc_b = root / "cycle-a", root / "cycle-b"
    cyc_a.symlink_to(cyc_b)
    cyc_b.symlink_to(cyc_a)
    code, out, err = run(str(cyc_a), timeout=10)
    check("a cyclic symlink exits 1", code == 1, f"exit {code}: {out!r} {err!r}")
    check("a cyclic symlink says so rather than hanging", "never ends" in err, f"{err!r}")

    self_link = root / "self"
    self_link.symlink_to(self_link)
    code, out, err = run(str(self_link), timeout=10)
    check("a symlink to itself exits 1", code == 1, f"exit {code}: {out!r} {err!r}")

    # 11 — the bound is a bound: a chain past it refuses like any other path it
    # cannot resolve, and a chain under it still resolves to the real directory.
    short = root / "short-target"
    short.mkdir()
    prev = short
    for i in range(5):
        nxt = root / f"short-{i}"
        nxt.symlink_to(prev)
        prev = nxt
    code, out, err = run(str(prev))
    check("a chain under the bound still resolves",
          code == 0 and out == str(real / "short-target"), f"exit {code}: {out!r} {err!r}")

    long_target = root / "long-target"
    long_target.mkdir()
    prev = long_target
    for i in range(45):
        nxt = root / f"long-{i}"
        nxt.symlink_to(prev)
        prev = nxt
    code, out, err = run(str(prev), timeout=10)
    check("a chain past the bound exits 1", code == 1, f"exit {code}: {out!r} {err!r}")

    # 12 — references to a directory rather than a name for one. Every one of
    # these names the same directory as `plain`, and every one has to come back
    # as `plain`: a caller matches the result against `git worktree list
    # --porcelain`, so a spelling that comes back unchanged is a worktree the
    # caller is told is not registered — and a branch it leaves behind, silently.
    (root / "refs" / "inner").mkdir(parents=True)
    for label, spelled, want in [
        ("a trailing /.",               str(plain) + "/.",                          str(real / "plain")),
        ("two trailing /.",             str(plain) + "/./.",                        str(real / "plain")),
        ("a trailing /..",              str(root / "refs" / "inner") + "/..",       str(real / "refs")),
        ("a /.. after a . in the middle", str(root / "refs") + "/./inner/..",       str(real / "refs")),
        ("a doubled leading slash",     "/" + str(real / "plain"),                  str(real / "plain")),
        ("a doubled leading slash on a link", "/" + str(link),                      str(real / "target")),
    ]:
        code, out, err = run(spelled)
        check(f"{label} resolves to the directory it names",
              code == 0 and out == want, f"exit {code}: {out!r} want {str(want)!r} {err!r}")

    # The root is the one path with no last component to append, so the format
    # that builds every other answer would print it as `//`.
    for label, spelled in [("the root", "/"), ("a doubled root", "//"),
                           ("a tripled root", "///"),
                           ("the root as /.", "/."), ("the root as /..", "/.."),
                           ("the root as //.", "//.")]:
        code, out, err = run(spelled)
        check(f"{label} resolves to one slash",
              code == 0 and out == "/", f"exit {code}: {out!r} {err!r}")

    # 13 — one directory has many spellings, and the contract every caller rests
    # on is that they all come back as ONE string: the caller compares this
    # against `git worktree list --porcelain`, so a second answer is a worktree
    # the caller is told is not registered. That is asserted here as a property
    # over combinations rather than as a list of the spellings someone thought
    # of — folding a trailing slash can expose a `/.`, and folding a `/.` can
    # expose a `//`, so the interesting cases are the compositions.
    #
    # The same property covers idempotence: the answer must be a spelling with
    # nothing left to fold, so resolving it again has to change nothing.
    prop = root / "prop"
    (prop / "sub" / "inner").mkdir(parents=True)
    prop_link = root / "prop-link"
    prop_link.symlink_to(prop)

    #
    # Every ref in these sets names `prop` itself — a `/sub/..` in the middle
    # comes back, and `inner/..` alone does not: it leaves you in `prop/sub`,
    # a different directory with its own one spelling. A ref that leaves is not
    # wrong, it is a spelling of somewhere else, and asserting it here would be
    # asserting something false about the resolver.
    spellings = []
    for base in (str(prop), str(prop_link)):
        for lead in ("", "/"):
            for mid in ("", "/./", "//", "/sub/..", "/sub/inner/../..", "/sub/../."):
                for tail in ("", "/", "/.", "/./", "//."):
                    spellings.append(lead + base + mid + tail)

    answers, problems = {}, []
    for spelled in spellings:
        code, out, err = run(spelled)
        if code != 0:
            problems.append(f"{spelled!r} -> exit {code}: {err!r}")
            continue
        answers.setdefault(out, []).append(spelled)
        again_code, again, _ = run(out)
        if again_code != 0 or again != out:
            problems.append(f"{spelled!r} -> {out!r}, and resolving that gave {again!r}")

    check("every spelling of one directory comes back as one string",
          len(answers) == 1,
          f"{len(answers)} distinct answers: {sorted(answers)[:3]}")
    check("and it is that directory",
          list(answers) == [str(real / "prop")], f"{list(answers)}")
    check("resolving an answer again changes nothing",
          not problems, f"{len(problems)} of {len(spellings)}: {problems[:4]}")

    print()
    if FAILS:
        print(f"{len(FAILS)} failed, {PASS} passed")
        return 1
    print(f"{PASS} passed")
    return 0


if __name__ == "__main__":
    sys.exit(main())
