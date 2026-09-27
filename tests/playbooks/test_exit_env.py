#!/usr/bin/env python3
"""
test_exit_env.py — a close's exit environment is stated in three places, and
this is what holds them together.

`exitEnv` in src/flagship/internal/dispatch/close.go is the set every exit step
sees as `FS_*`, and two docs enumerate it for a reader: the closing-out table in
skills/flagship/SKILL.md and the paragraph in
skills/flagship/references/fs-advanced.md. Nothing read either against the
slice. That is how `FS_BRANCH` reached close.go and neither doc until a gap was
filed — the only check that existed was a person diffing the three by hand, and
a hand is what the gap was filed against.

So the three are read as text and compared, both ways: every name exitEnv
returns is named in both docs, and neither doc names one it does not. The test
is over the NAMES, not their order or their prose, because the names are what a
step types and what a doc has to be findable by.

This lives under tests/playbooks/ rather than beside cmd/kb_test.go, where the
review that found the hole suggested it, for one reason: it reads files outside
the Go module, and the nix build copies only `src/flagship` into a hermetic
sandbox and runs `go test ./...` there (flake.nix, `src = ./src/flagship;`). A
Go test reaching ../../../skills would fail that check phase, which is the build
`./bin/build-project.sh flagship` runs. The docs are text here and reachable;
the suite this file joins is the repo's integration gate.

Usage: python3 tests/playbooks/test_exit_env.py     (run from anywhere)
Exit: 0 all pass, 1 any fail. Prints one line per failure, prefixed FAIL.
"""

import pathlib
import re
import sys

REPO = pathlib.Path(__file__).resolve().parents[2]
CLOSE_GO = REPO / "src/flagship/internal/dispatch/close.go"
SKILL_MD = REPO / "skills/flagship/SKILL.md"
ADVANCED_MD = REPO / "skills/flagship/references/fs-advanced.md"

# The heading whose table enumerates the set. Scoped to the section rather than
# to the whole file because SKILL.md has a second table — the entry gate's env —
# and its names are a different set: FS_GOAL is exported there and not at close.
SKILL_SECTION = "### Closing out"

# The sentence that introduces the set in fs-advanced.md, whose paragraph runs
# to the next blank line. The same paragraph opens on the entry gate's env, so
# the slice starts at this marker rather than at the paragraph. The gap between
# two of its words is spelled `\s+` because the prose is wrapped — the marker
# straddles a line break, and a literal space in it would find nothing.
ADVANCED_MARKER = r"`fs close`'s exit steps\s+get a different set:"

PASS, FAILS = 0, []


def check(name, cond, detail=""):
    global PASS
    if cond:
        PASS += 1
        print(f"  ok   {name}")
    else:
        FAILS.append(name)
        print(f"FAIL {name}: {detail}")


def exit_env_names():
    """Every name exitEnv exports, read out of its returned slice: the quoted
    `FS_*=` literals between its signature and the closing brace."""
    lines = CLOSE_GO.read_text().splitlines()
    start = next((i for i, l in enumerate(lines) if l.startswith("func exitEnv(")), None)
    if start is None:
        return set()
    body = []
    for line in lines[start + 1:]:
        if line == "}":
            break
        body.append(line)
    return set(re.findall(r'"(FS_[A-Z0-9_]+)=', "\n".join(body)))


def skill_md_names():
    """The names in the closing-out table's first column, which is how SKILL.md
    spells each variable: a table row whose leading cell is a backticked name."""
    lines = SKILL_MD.read_text().splitlines()
    start = next((i for i, l in enumerate(lines) if l.strip() == SKILL_SECTION), None)
    if start is None:
        return set()
    names = set()
    for line in lines[start + 1:]:
        if line.startswith("#"):
            break
        m = re.match(r"^\|\s*`(FS_[A-Z0-9_]+)`\s*\|", line)
        if m:
            names.add(m.group(1))
    return names


def advanced_md_names():
    """The names in fs-advanced.md's exit-env paragraph, which is prose rather
    than a table: the names are the backticked FS_* tokens the paragraph uses."""
    text = ADVANCED_MD.read_text()
    found = re.search(ADVANCED_MARKER, text)
    if found is None:
        return set()
    para = text[found.end():].split("\n\n", 1)[0]
    return set(re.findall(r"\b(FS_[A-Z0-9_]+)\b", para))


def compare(doc, names, env):
    """One doc against exitEnv, both directions, in one failure line: a name
    exitEnv exports and a doc does not mention is the FS_BRANCH hole, and a
    name a doc mentions that exitEnv does not export is a step told about a
    variable that will never be in its environment."""
    missing = sorted(env - names)
    extra = sorted(names - env)
    check(f"{doc} names exactly the exit environment",
          not missing and not extra,
          f"exitEnv exports but {doc} does not name: {missing}; "
          f"{doc} names but exitEnv does not export: {extra}")


def main():
    env = exit_env_names()
    check("exitEnv's returned slice is readable", len(env) > 10,
          f"found {sorted(env)} in {CLOSE_GO}")

    skill = skill_md_names()
    check(f"{SKILL_MD.name} still has its {SKILL_SECTION!r} table", bool(skill),
          f"no backticked FS_* row after that heading in {SKILL_MD}")
    compare(SKILL_MD.name, skill, env)

    advanced = advanced_md_names()
    check(f"{ADVANCED_MD.name} still has its exit-env paragraph", bool(advanced),
          f"{ADVANCED_MARKER!r} is gone from {ADVANCED_MD}")
    compare(ADVANCED_MD.name, advanced, env)

    print()
    if FAILS:
        print(f"{len(FAILS)} failed, {PASS} passed")
        return 1
    print(f"{PASS} passed")
    return 0


if __name__ == "__main__":
    sys.exit(main())
