#!/usr/bin/env python3
"""
test_lock.py — behaviour tests for the specs skill's lock.sh.

lock.sh writes two things into a doc: the marker that names the commit the doc
was locked against, and the Link contract that names what it relies on and what
must cite it. For a doc whose file is untracked, the contract is the *only*
record of that dependency graph — there is no git history to recover it from.

Three rotations destroyed the contract of five specs by running `--force` with
no contract flags: an absent flag was rendered as the literal value "none", and
the write was unconditional, so a real referrer list became indistinguishable
from a doc that genuinely has none. Each case below is the property that was
missing.

Every refusal case asserts the same three things: the script exits non-zero,
its message names what was wrong, and the target is byte-identical afterwards
with nothing left beside it. The last case asserts a correct invocation still
writes the contract it was given, so a refusal cannot pass by refusing always.

Usage: python3 tests/lock/test_lock.py     (run from anywhere)
Exit: 0 all pass, 1 any fail. Prints one line per failure, prefixed FAIL.
"""

import hashlib
import os
import pathlib
import shutil
import subprocess
import sys
import tempfile

SCRIPT = pathlib.Path(__file__).resolve().parents[2] / "skills/specs/scripts/lock.sh"

PASS, FAILS = 0, []
DOC = "specs/thing.md"

# The shape lock.sh writes: marker, blank, contract, blank, body.
LOCKED = """<!-- specs:locked:abc1234 2026-01-01 type=spec -->

## Link contract
- **upstream** (this doc relies on): none
- **referrers** (must cite this when they change): specs/other.md

# Thing

The body.
"""

# The contract line a locked doc records. A doc carrying someone else's marker
# has none, so those cases assert the marker survived instead.
CONTRACT_REFERRERS = "- **referrers** (must cite this when they change): specs/other.md"

# A marker lock.sh did not write, in the position both regimes write one in.
DESIGN_LOCKED = "<!-- design:locked:640b994 2026-09-28 -->\n\n# Design\n"
NO_TYPE_LOCKED = "<!-- specs:locked:640b994 2026-09-28 -->\n\n# AG\n"


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


def digest(path):
    return hashlib.sha256(pathlib.Path(path).read_bytes()).hexdigest()


def listing(path):
    return sorted(os.listdir(pathlib.Path(path).parent))


def run(repo, *args):
    """Run lock.sh with the throwaway repo as cwd, as the specs skill does."""
    e = {**os.environ, "GIT_CONFIG_GLOBAL": os.devnull, "GIT_CONFIG_SYSTEM": os.devnull}
    p = subprocess.run(["bash", str(SCRIPT), *args], cwd=repo,
                       capture_output=True, text=True, env=e)
    return p.returncode, p.stdout + p.stderr, p.stderr


def new_repo():
    """A repo with one commit, a locked doc, and the doc its contract cites."""
    repo = tempfile.mkdtemp(prefix="lock-")
    git(repo, "init", "-q", "-b", "main")
    write(repo, "src/a.go", "package main\n")
    write(repo, "specs/other.md", "# Other\n")
    write(repo, DOC, LOCKED)
    git(repo, "add", "-A")
    git(repo, "commit", "-q", "-m", "add src and specs")
    return repo, pathlib.Path(repo) / DOC


def refusal(name, repo, doc, args, expect_in, contains=CONTRACT_REFERRERS):
    """One refusal case: non-zero, says what is wrong on stderr, changes nothing."""
    before_sha, before_files = digest(doc), listing(doc)
    code, out, err = run(repo, DOC, "--type", "spec", *args)
    check(f"{name} refuses", code != 0, f"exit 0, output: {out}")
    check(f"{name} names what is wrong", expect_in in err, f"expected {expect_in!r} in {err!r}")
    check(f"{name} leaves the doc byte-identical",
          digest(doc) == before_sha, "the doc was rewritten")
    check(f"{name} leaves nothing beside the doc",
          listing(doc) == before_files, f"{listing(doc)} != {before_files}")
    if contains:
        check(f"{name} keeps the recorded contract", contains in doc.read_text(),
              doc.read_text())


def main():
    check("the script is readable", SCRIPT.is_file(), f"{SCRIPT} is missing")

    # 1 — the destructive call: --force with no contract flags. This is the
    #     exact command that erased five specs' contracts, three times.
    repo, doc = new_repo()
    refusal("a bare --force (missing --upstream)", repo, doc, ["--force", "--referrers", "none"],
            "--upstream")
    shutil.rmtree(repo)

    # 2 — the other half of the same call.
    repo, doc = new_repo()
    refusal("--force with a missing --referrers", repo, doc, ["--force", "--upstream", "none"],
            "--referrers")
    shutil.rmtree(repo)

    # 3 — a supplied value that is not a doc: the sha shape that was written.
    repo, doc = new_repo()
    refusal("a --upstream that is a sha", repo, doc,
            ["--force", "--upstream", "6120814", "--referrers", "none"], "6120814")
    shutil.rmtree(repo)

    # 4 — a supplied value that names no file.
    repo, doc = new_repo()
    refusal("a --referrers that names nothing here", repo, doc,
            ["--force", "--upstream", "none", "--referrers", "no-such-doc.md"], "no-such-doc.md")
    shutil.rmtree(repo)

    # 5 — a correct rotation still writes the contract it was given: a refusal
    #     that always refuses proves nothing.
    repo, doc = new_repo()
    code, out, _ = run(repo, DOC, "--type", "spec", "--force",
                        "--upstream", "none", "--referrers", "specs/other.md")
    head = git(repo, "rev-parse", "--short", "HEAD")
    text = doc.read_text()
    check("a correct rotation is written", code == 0, f"exit {code}: {out}")
    check("its marker names HEAD", text.startswith(f"<!-- specs:locked:{head} "), text[:80])
    check("it keeps the upstream it was given",
          "- **upstream** (this doc relies on): none" in text, text)
    check("it keeps the referrers it was given",
          "- **referrers** (must cite this when they change): specs/other.md" in text, text)
    check("the body survives the rotation", "# Thing" in text and "The body." in text, text)
    shutil.rmtree(repo)

    # 6 — `none` is still how a doc says it has no upstream: an explicit none
    #     must not be refused along with the absent flag.
    repo, doc = new_repo()
    code, out, _ = run(repo, DOC, "--type", "spec", "--force",
                        "--upstream", "none", "--referrers", "none")
    check("an explicit none is accepted", code == 0, f"exit {code}: {out}")
    check("and is written as the whole list",
          "- **upstream** (this doc relies on): none" in doc.read_text(), doc.read_text())
    shutil.rmtree(repo)

    # 7 — a linked doc that exists is accepted, so the check is not a blanket
    #     refusal of every value.
    repo, doc = new_repo()
    code, out, _ = run(repo, DOC, "--type", "spec", "--force",
                        "--upstream", "specs/other.md", "--referrers", "none")
    check("a doc-relative upstream is accepted", code == 0, f"exit {code}: {out}")
    check("and lands in the contract",
          "- **upstream** (this doc relies on): specs/other.md" in doc.read_text(),
          doc.read_text())
    shutil.rmtree(repo)

    # 8 — "none" beside a real entry says two things at once, and it would be
    #     written as a contract line that looks like a real one.
    repo, doc = new_repo()
    refusal("a --upstream that mixes none with a doc", repo, doc,
            ["--force", "--upstream", "none,specs/other.md", "--referrers", "none"], "none")
    shutil.rmtree(repo)

    # 9 — the shape the specs skill writes: a basename resolved against the
    #     doc's own directory, with the caller in a different one.
    repo, _ = new_repo()
    write(repo, ".agents/specs/other.md", "# Other\n")
    doc2 = write(repo, ".agents/specs/thing.md", LOCKED.replace("specs/thing.md", ".agents/specs/thing.md"))
    code, out, _ = run(repo, ".agents/specs/thing.md", "--type", "spec", "--force",
                       "--upstream", "other.md", "--referrers", "none")
    check("a doc-relative basename is accepted", code == 0, f"exit {code}: {out}")
    check("and lands in the contract",
          "- **upstream** (this doc relies on): other.md" in doc2.read_text(), doc2.read_text())
    shutil.rmtree(repo)

    # 10 — a lock this script did not write. Another regime's marker is not an
    #      unlocked doc: the write is unconditional, so a marker the script does
    #      not recognise is one it would destroy. Reproduced before the fix —
    #      the design freeze was gone and the exit was 0.
    repo, doc3 = new_repo()
    doc3.write_text(DESIGN_LOCKED)
    refusal("another regime's marker", repo, doc3,
            ["--upstream", "none", "--referrers", "none"], "design:locked", contains=None)
    shutil.rmtree(repo)

    # 11 — the shape README.md documents, which carries no type= and so is not a
    #      marker this script can parse. Unparseable is a refusal too: the
    #      filename is the same, the marker is not.
    repo, doc4 = new_repo()
    doc4.write_text(NO_TYPE_LOCKED)
    refusal("a marker with no type=", repo, doc4,
            ["--upstream", "none", "--referrers", "none"], "specs:locked:640b994", contains=None)
    shutil.rmtree(repo)

    # 12 — and --force is the documented way past it, so the refusal is not a
    #      blanket refusal of a doc that carries someone else's marker.
    repo, doc5 = new_repo()
    doc5.write_text(DESIGN_LOCKED)
    code, out, _ = run(repo, DOC, "--type", "spec", "--force",
                       "--upstream", "none", "--referrers", "none")
    head = git(repo, "rev-parse", "--short", "HEAD")
    check("--force replaces a marker it did not write", code == 0, f"exit {code}: {out}")
    check("and writes its own",
          doc5.read_text().startswith(f"<!-- specs:locked:{head} "), doc5.read_text()[:80])
    shutil.rmtree(repo)

    # 13 — a directory is not a doc. It used to reach the write and leave a
    #      staged <dir>.body beside it, which is not a refusal.
    repo, _ = new_repo()
    adir = pathlib.Path(repo) / "specs/adir"
    adir.mkdir()
    before_files = sorted(os.listdir(adir.parent))
    code, out, err = run(repo, "specs/adir", "--type", "spec",
                         "--upstream", "none", "--referrers", "none")
    check("a directory is refused", code != 0, f"exit 0: {out}")
    check("and named as not a file", "specs/adir" in err, err)
    check("and leaves nothing beside it",
          sorted(os.listdir(adir.parent)) == before_files, sorted(os.listdir(adir.parent)))
    check("and is still a directory", adir.is_dir())
    shutil.rmtree(repo)

    print()
    if FAILS:
        print(f"{len(FAILS)} failed, {PASS} passed")
        return 1
    print(f"{PASS} passed")
    return 0


if __name__ == "__main__":
    sys.exit(main())
