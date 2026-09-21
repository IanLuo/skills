#!/usr/bin/env python3
"""
test_playbooks.py — the shipped playbooks are a contract, so they are checked
like one.

fs ships 14 playbooks in src/flagship/defaults/kb. They are the protocols a
dispatch is measured against, and they are data: a typo in a phase or a step
kind is a protocol that refuses at the worst moment, and a `use:` naming a skill
this repo does not ship is a hint that sends a worker looking for nothing. Both
have happened — the cap's standing rules carried a closed list of skill names
that had gone stale, naming a skill this repo does not ship, and the list was
deleted rather than corrected. This test is what keeps the replacement honest.

What it checks, per playbook:

  1. The set is exactly the 14 the schema ships — no file added or dropped
     without this list moving with it.
  2. Each file parses, its `name` is its file name, its phase is one of
     cap/entry/work/exit, and its `order`, when given, is an integer.
  3. Every step's kind is one the phase allows — the same table fs enforces, so
     a playbook that passes here is one `fs kb add` would accept.
  4. `use`, `do` and `include` steps carry a body: each one names something, and
     a nameless one is a step with no content that reads as if it had some.
  5. Every `applies_when` term is `k` or `k=v` — the only two forms the matcher
     understands. A term may match a derived tag: `type=parallel` and
     `engine=herdr` are how a playbook selects itself.
  6. Every `use:` step names a skill that exists — checked against the SKILL.md
     on disk, because that is what a worker would go and read.
  7. Every `skills/<name>/...` path named anywhere in a step body resolves, so
     the stale-list failure has a mechanical detector rather than a promise.
  8. Every `fs <subcommand>` in an executed step (`check`/`do`) is a subcommand
     fs actually has, read from the one place that decides: the command specs in
     src/flagship/cmd/args.go.
  9. The `worker` playbook declares no `applies_when` — an empty one matches
     every dispatch, and every worker must be told to write to the node it was
     given — and carries that rule.

A `use:` body is prose: the skill name is the first word, up to the em-dash or
the end of the line. Skill names are read from the skills tree rather than a
list in this file, so adding a skill needs no edit here.

Prose in a `say:` step is not executed and is not scanned for subcommands — a
sentence may mention `fs` and `git` in the same breath. What IS scanned for
skills is any step body, because the stale list that motivated this test lived
in exactly such prose, and `skills/<name>/...` is unambiguous there.

Usage: python3 tests/playbooks/test_playbooks.py     (run from anywhere)
Exit: 0 all pass, 1 any fail. Prints one line per failure, prefixed FAIL.
"""

import pathlib
import re
import sys

REPO = pathlib.Path(__file__).resolve().parents[2]
KB = REPO / "src/flagship/defaults/kb"
SKILLS = REPO / "skills"
ARGS_GO = REPO / "src/flagship/cmd/args.go"

# The shipped set. Kept here as well as in the KB so a playbook that disappears
# is a failure and not a quietly smaller loop.
SHIPPED = [
    "cap",
    "herdr",
    "worker",
    "code-entry",
    "code-exit",
    "plan",
    "development",
    "tdd",
    "review",
    "research",
    "parallel-entry",
    "parallel-exit",
    "integration-entry",
    "integration-exit",
]

# allowedKinds in src/flagship/internal/knowledge/knowledge.go. `include` is
# legal in every phase and is spliced away before anything runs.
ALLOWED = {
    "cap": {"say"},
    "entry": {"check", "ask", "say"},
    "work": {"say", "use"},
    "exit": {"check", "ask", "do", "say"},
}
PHASES = ["cap", "entry", "work", "exit"]
NEEDS_BODY = {"use", "do", "include"}

PASS, FAILS = 0, []


def check(name, cond, detail=""):
    global PASS
    if cond:
        PASS += 1
        print(f"  ok   {name}")
    else:
        FAILS.append(name)
        print(f"FAIL {name}: {detail}")


def parse(path):
    """Parse one playbook the way the Go parser does: top-level `key:` lines,
    `- kind: body` steps, `#` comments dropped, blank lines ignored."""
    playbook = {"steps": []}
    key = None
    for raw in path.read_text().splitlines():
        line = raw.split("#", 1)[0].rstrip() if raw.lstrip().startswith("#") else raw
        if line.lstrip().startswith("#"):
            continue
        if not line.strip():
            continue
        if line.startswith("  - "):
            kind, _, body = line[4:].partition(":")
            playbook["steps"].append((kind.strip(), body.strip()))
        elif line.startswith("  "):
            if key:  # a continuation of a folded value; nothing here uses one
                playbook[key] += " " + line.strip()
        else:
            key, _, value = line.partition(":")
            key, value = key.strip(), value.strip()
            if key == "steps":
                key = None
            else:
                playbook[key] = value
    return playbook


def one_line(text):
    """A value with an inline comment stripped: `applies_when: code  # why`."""
    return text.split("#", 1)[0].strip()


def fs_subcommands():
    """Every subcommand fs has, from the specs that enforce the flag lists."""
    src = ARGS_GO.read_text()
    seen = set()
    for key in re.findall(r'^\t"([a-z-]+(?: [a-z-]+)?)":\s*\{', src, re.M):
        seen.add(key)
    return seen


def skill_names():
    return {p.name for p in SKILLS.iterdir() if (p / "SKILL.md").is_file()}


def steps_of(playbook, kind):
    return [body for k, body in playbook["steps"] if k == kind]


def skill_of(body):
    """The skill a `use:` step names: the first word, before any em-dash."""
    head = re.split(r"\s+[—-]\s+|—", body, maxsplit=1)[0]
    return head.strip().split()[0] if head.strip() else ""


def main():
    subs = fs_subcommands()
    skills = skill_names()

    check("the command specs are readable", len(subs) > 10, f"found {sorted(subs)}")
    check("the skills tree is readable", len(skills) > 5, f"found {sorted(skills)}")

    found = sorted(p.stem for p in KB.glob("*.yaml"))
    check("the shipped set is exactly the 14", found == sorted(SHIPPED),
          f"on disk: {found}; expected: {sorted(SHIPPED)}")

    for name in sorted(SHIPPED):
        path = KB / f"{name}.yaml"
        if not path.is_file():
            check(f"{name}: file exists", False, f"{path} is missing")
            continue

        pb = parse(path)
        check(f"{name}: declares its own name", pb.get("name") == name,
              f"name: {pb.get('name')!r}")

        phase = pb.get("phase", "")
        check(f"{name}: phase is one of {'/'.join(PHASES)}", phase in ALLOWED,
              f"phase: {phase!r}")

        order = pb.get("order")
        if order is not None:
            check(f"{name}: order is an integer", re.fullmatch(r"-?\d+", one_line(order)) is not None,
                  f"order: {order!r}")

        steps = pb["steps"]
        check(f"{name}: declares at least one step", bool(steps), "no steps")

        if phase in ALLOWED:
            for i, (kind, body) in enumerate(steps, start=1):
                check(f"{name}: step {i} kind {kind!r} is allowed in phase {phase}",
                      kind in ALLOWED[phase] | {"include"},
                      f"allowed kinds: {', '.join(sorted(ALLOWED[phase] | {'include'}))}")
                if kind in NEEDS_BODY:
                    check(f"{name}: step {i} ({kind}) carries a body", bool(body),
                          f"a {kind} step names what it is for")

            if "applies_when" in pb:
                terms = one_line(pb["applies_when"]).split()
                check(f"{name}: applies_when is not empty when declared", bool(terms),
                      "an empty applies_when belongs on a playbook that omits the key")
                # A term MAY match a derived tag — `type=parallel` and
                # `engine=herdr` are how a playbook selects itself by task type
                # and by engine. What may not restate a derived tag is a
                # *declared* --tag, which is a dispatch-time rule, not a schema
                # one.
                for term in terms:
                    check(f"{name}: tag {term!r} is a k or k=v term",
                          re.fullmatch(r"[A-Za-z0-9_.-]+(=[^\s]*)?", term) is not None,
                          f"{term!r} is not a tag term")

            for i, body in enumerate(steps_of(pb, "use"), start=1):
                skill = skill_of(body)
                check(f"{name}: use {i} names a skill that exists ({skill})",
                      skill in skills,
                      f"{skill!r} has no {SKILLS / skill / 'SKILL.md'}")

        for path_ref in re.findall(r"skills/([A-Za-z0-9_.-]+)/", " ".join(b for _, b in steps)):
            check(f"{name}: the path skills/{path_ref}/ resolves",
                  path_ref in skills,
                  f"{path_ref!r} is not a skill this repo ships")

        for kind in ("check", "do"):
            for i, body in enumerate(steps_of(pb, kind), start=1):
                for sub in fs_invocations(body):
                    check(f"{name}: {kind} {i} calls a subcommand fs has (fs {sub})",
                          sub in subs,
                          f"fs {sub} is not a subcommand; the specs in args.go have "
                          f"{', '.join(sorted(subs))}")

    # The worker's rules are the one playbook every dispatch carries: an empty
    # applies_when is what makes that true, and the node rule is the reason it
    # had to be a playbook rather than a line in the brief renderer.
    worker = parse(KB / "worker.yaml")
    check("worker: phase is work", worker.get("phase") == "work", f"phase: {worker.get('phase')!r}")
    check("worker: declares no applies_when, so every dispatch carries it",
          "applies_when" not in worker, f"applies_when: {worker.get('applies_when')!r}")
    says = " ".join(steps_of(worker, "say"))
    check("worker: tells the worker to use the node in its brief",
          "named in your brief" in says, says)
    check("worker: tells the worker never to invent a node",
          "never invent one" in says, says)

    print()
    if FAILS:
        print(f"{len(FAILS)} failed, {PASS} passed")
        return 1
    print(f"{PASS} passed")
    return 0


def fs_invocations(body):
    """The fs subcommands a shell body runs. Two words where the specs have a
    two-word subcommand (`task get`, `kb prompt`), one otherwise."""
    found = []
    for m in re.finditer(r"\bfs ([a-z-]+)(?: ([a-z-]+))?", body):
        first, second = m.group(1), m.group(2)
        found.append(f"{first} {second}" if second and not second.startswith("-") else first)
    return found


if __name__ == "__main__":
    sys.exit(main())
