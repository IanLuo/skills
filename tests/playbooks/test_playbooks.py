#!/usr/bin/env python3
"""
test_playbooks.py — the shipped playbooks are a contract, so they are checked
like one.

fs ships the playbooks listed in SHIPPED, in src/flagship/defaults/kb. They are
the protocols a
dispatch is measured against, and they are data: a typo in a section or a step
kind is a protocol that refuses at the worst moment, and a `use:` naming a skill
this repo does not ship is a hint that sends a worker looking for nothing. Both
have happened — the cap's standing rules carried a closed list of skill names
that had gone stale, naming a skill this repo does not ship, and the list was
deleted rather than corrected. This test is what keeps the replacement honest.

One file is one participant: the moment is a section (`cap:`, `entry:`, `work:`,
`exit:`), not a field, so a playbook that takes part in two moments declares two
sections in the same file. What it checks, per playbook:

  1. The set is exactly the names SHIPPED lists — no file added or dropped
     without this list moving with it.
  2. Each file parses, its `name` is its file name, it declares at least one
     section, and its `order`, when given, is an integer.
  3. Every step's kind is one the section's moment allows — the same table fs
     enforces, so a playbook that passes here is one `fs kb add` would accept.
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

 10. No executed step (`check`/`do`) in a shipped playbook names a repo-relative
     path. A playbook lives in the binary and runs in every project, so
     `bin/...` or `tests/...` resolves in exactly one of them and exits 127 in
     the rest. `batch.yaml` ran `bin/check-parallel.sh` and `integration.yaml`
     ran `tests/run.sh`, and each could only ever pass here.

 11. Every script an executed step runs through `"$FS_BIN" script <name>` is one
     the binary ships, in src/flagship/defaults/scripts. The scripts are read
     out of the binary — nothing is written to disk — so a name that is not in
     it is a step that runs nothing at all, and `$FS_SCRIPTS` is refused
     outright: where the old directory survives, a step that named it would run
     the stale copy and pass.

 12. No executed step names a bare `fs`. The binary is installed into ~/.fs and
     is not on PATH, so `fs reviews "$FS_NODE"` exits 127 — which is what the
     review gate did on its one live firing, and what left a shipped gate
     unrunnable while every test around it passed. `"$FS_BIN"` is the running
     binary's own path, exported by both env builders, and it is how a step
     addresses fs. Rule 8 reads the subcommand out of that form as well as out
     of a bare `fs`, so moving a step onto the exported path does not quietly
     stop checking the subcommand it names.

Rules 8, 10, 11 and 12 are the same question from four sides — can this step run at
all in a project that is not this one? — and rule 10 is the one the other two
could not have caught: a script check that resolves against a repo path is a
name fs has no way to know is wrong.

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
SCRIPTS = REPO / "src/flagship/defaults/scripts"
SKILLS = REPO / "skills"
ARGS_GO = REPO / "src/flagship/cmd/args.go"

# Directories that exist inside this repo and nowhere else. A shipped playbook
# naming one of them is a gate that can only ever pass where it was written.
REPO_LOCAL = re.compile(
    r"(?<![\w.$/-])(?:" + "|".join(["bin", "tests", "scripts", "src", "internal", "cmd"]) + r")/[\w./-]+"
    r"|(?<![\w.$-])\./[\w./-]+"
)

# How a step names the binary: bare (which resolves nowhere, since fs is
# installed into ~/.fs and not into PATH), or through $FS_BIN, the running
# binary's own path that both env builders export. The lookbehind rejects a
# token glued to a path or a variable name, so `$FS_BIN` is not read as a bare
# `fs` and neither is `.../fs`.
FS_INVOCATION = re.compile(r'(?:(?<![\w$./-])fs|"\$FS_BIN"|\$FS_BIN)\s+([a-z-]+)(?:\s+([a-z-]+))?')
BARE_FS = re.compile(r"(?<![\w$./-])fs\s+([a-z-]+)")

# How a step runs a script the binary ships: `"$FS_BIN" script <name>`, the name
# being the next token. There is no directory to address — the script is read
# out of the binary — so this is the only form, and the word after it is the
# name rather than a subcommand's second word.
SCRIPT_INVOCATION = re.compile(r'(?:(?<![\w$./-])fs|"\$FS_BIN"|\$FS_BIN)\s+script\s+([\w.-]+)')

# The address that no longer exists. fs used to write the scripts to ~/.fs/scripts
# and export $FS_SCRIPTS, so a step named one as a path; a step that reverted to
# that form would find it on any machine the operator step has not reached yet,
# run the stale copy, and pass — a gate quietly testing the wrong code.
STALE_SCRIPTS = re.compile(r"\$FS_SCRIPTS")

# The shipped set. Kept here as well as in the KB so a playbook that disappears
# is a failure and not a quietly smaller loop.
SHIPPED = [
    "cap",
    "herdr",
    "worker",
    "code",
    "plan",
    "development",
    "tdd",
    "review",
    "independent-review",
    "research",
    "batch",
    "member",
    "integration",
    "spec",
]

# allowedKinds in src/flagship/internal/knowledge/knowledge.go. `include` is
# legal in every moment and is spliced away before anything runs.
ALLOWED = {
    "cap": {"say"},
    "entry": {"check", "ask", "say"},
    "work": {"say", "use"},
    "exit": {"check", "ask", "do", "say"},
}
MOMENTS = ["cap", "entry", "work", "exit"]
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
    `- kind: body` steps under a moment section, `#` comments dropped, blank
    lines ignored."""
    playbook = {}
    moment = None
    for raw in path.read_text().splitlines():
        line = raw.split("#", 1)[0].rstrip() if raw.lstrip().startswith("#") else raw
        if line.lstrip().startswith("#"):
            continue
        if not line.strip():
            continue
        if line.startswith("  - "):
            kind, _, body = line[4:].partition(":")
            if moment is not None:
                playbook.setdefault(moment, []).append((kind.strip(), body.strip()))
        elif line.startswith("  "):
            continue  # a continuation of a folded value; nothing here uses one
        else:
            key, _, value = line.partition(":")
            key, value = key.strip(), value.strip()
            if key in MOMENTS:
                moment = key
                playbook.setdefault(key, [])
            else:
                moment = None
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


def shipped_scripts():
    """The scripts the binary embeds, which fs runs out of the binary."""
    return {p.name for p in SCRIPTS.glob("*.sh")}


def repo_local(body):
    """Path-shaped tokens in a step body that resolve inside this repo only.

    `"$FS_BIN" script <name>` is deliberately not one: the name is resolved
    against the embedded set instead, and the binary's own path is what the
    lookbehind rejects a bare `fs` for.
    """
    return sorted({m.group(0) for m in REPO_LOCAL.finditer(body)})


def scripts_named(body):
    """The script names a body runs out of the binary, through `script <name>`."""
    return sorted(set(SCRIPT_INVOCATION.findall(body)))


def two_word_subs(subs):
    """The first words of the specs that have a two-word subcommand — `task`,
    `kb`, `cap`, `project`. A spec with one word is one word whatever follows
    it: `script check-specs.sh` names the subcommand `script` and then a script,
    and reading it as `script check-specs` would invent a subcommand."""
    return {s.split()[0] for s in subs if " " in s}


def all_steps(playbook):
    """Every step in a playbook, across its sections, in canonical moment order."""
    steps = []
    for moment in MOMENTS:
        steps.extend(playbook.get(moment, []))
    return steps


def steps_of(playbook, kind):
    return [body for k, body in all_steps(playbook) if k == kind]


def skill_of(body):
    """The skill a `use:` step names: the first word, before any em-dash."""
    head = re.split(r"\s+[—-]\s+|—", body, maxsplit=1)[0]
    return head.strip().split()[0] if head.strip() else ""


def main():
    subs = fs_subcommands()
    two_word = two_word_subs(subs)
    skills = skill_names()
    scripts = shipped_scripts()

    check("the command specs are readable", len(subs) > 10, f"found {sorted(subs)}")
    check("the skills tree is readable", len(skills) > 5, f"found {sorted(skills)}")
    check("the shipped script set is readable", bool(scripts), f"found {sorted(scripts)}")

    found = sorted(p.stem for p in KB.glob("*.yaml"))
    check("the shipped set is exactly the names SHIPPED lists", found == sorted(SHIPPED),
          f"on disk: {found}; expected: {sorted(SHIPPED)}")

    for name in sorted(SHIPPED):
        path = KB / f"{name}.yaml"
        if not path.is_file():
            check(f"{name}: file exists", False, f"{path} is missing")
            continue

        pb = parse(path)
        check(f"{name}: declares its own name", pb.get("name") == name,
              f"name: {pb.get('name')!r}")

        sections = [m for m in MOMENTS if m in pb]
        check(f"{name}: declares at least one section", bool(sections),
              "no section declared")

        order = pb.get("order")
        if order is not None:
            check(f"{name}: order is an integer", re.fullmatch(r"-?\d+", one_line(order)) is not None,
                  f"order: {order!r}")

        # engine: and agent: are optional; when present each is one non-empty
        # token — a name, not an expression.
        for field in ("engine", "agent"):
            if field in pb:
                check(f"{name}: {field} is a single non-empty token",
                      re.fullmatch(r"[A-Za-z0-9_.-]+", one_line(pb[field])) is not None,
                      f"{field}: {pb[field]!r}")

        # agent_args: is optional; when present it is a comma-separated list of
        # non-empty terms on the field's own line. Deliberately not held to the
        # single-token shape above — an agent's own command line is not a name,
        # so `--add-dir /tmp/x` is legal. A block list lands here as an empty
        # value and fails, which is the point: fs's parser is line-oriented and
        # drops one in silence.
        if "agent_args" in pb:
            terms = [t for t in (s.strip() for s in one_line(pb["agent_args"]).split(",")) if t]
            check(f"{name}: agent_args names at least one comma-separated term",
                  bool(terms),
                  f"agent_args: {pb['agent_args']!r} — write `agent_args: one, two` on the "
                  f"field's own line; a YAML block list is dropped in silence")

        steps = all_steps(pb)
        check(f"{name}: declares at least one step", bool(steps), "no steps")

        for moment in sections:
            for i, (kind, body) in enumerate(pb[moment], start=1):
                check(f"{name}: {moment} step {i} kind {kind!r} is allowed",
                      kind in ALLOWED[moment] | {"include"},
                      f"allowed kinds: {', '.join(sorted(ALLOWED[moment] | {'include'}))}")
                if kind in NEEDS_BODY:
                    check(f"{name}: {moment} step {i} ({kind}) carries a body", bool(body),
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
                for sub in fs_invocations(body, two_word):
                    check(f"{name}: {kind} {i} calls a subcommand fs has (fs {sub})",
                          sub in subs,
                          f"fs {sub} is not a subcommand; the specs in args.go have "
                          f"{', '.join(sorted(subs))}")
                for call in bare_fs(body):
                    check(f"{name}: {kind} {i} names fs by its own path ({call})", False,
                          f"`{call}` resolves only where fs is installed, and it is not "
                          f"installed into PATH; address the running binary as \"$FS_BIN\"")
                for ref in repo_local(body):
                    check(f"{name}: {kind} {i} names no repo-relative path ({ref})", False,
                          f"{ref} resolves in this repo only, and this playbook runs in "
                          f"every project; run a shipped script as \"$FS_BIN\" script <name>")
                if STALE_SCRIPTS.search(body):
                    check(f"{name}: {kind} {i} names no $FS_SCRIPTS ({body})", False,
                          "fs writes no scripts directory; run a shipped script as "
                          "\"$FS_BIN\" script <name>. Where the old directory survives, this "
                          "runs the stale copy and passes")
                for script in scripts_named(body):
                    check(f"{name}: {kind} {i} runs a script the binary ships ({script})",
                          script in scripts,
                          f"{SCRIPTS / script} does not exist, so \"$FS_BIN\" script {script} "
                          f"runs nothing at all")

    # The worker's rules are the one playbook every dispatch carries: an empty
    # applies_when is what makes that true, and the node rule is the reason it
    # had to be a playbook rather than a line in the brief renderer.
    worker = parse(KB / "worker.yaml")
    check("worker: serves work", "work" in worker, f"sections: {sorted(worker)}")
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


def bare_fs(body):
    """The fs subcommands a body runs without a path — `fs reviews ...`, which
    resolves only where fs happens to be installed. `"$FS_BIN" reviews ...` is
    the portable form and is deliberately not one."""
    return [f"fs {m.group(1)}" for m in BARE_FS.finditer(body)]


def fs_invocations(body, two_word):
    """The fs subcommands a shell body runs. The binary is named either bare
    (`fs`) or through `"$FS_BIN"`, the running binary's own path; both are read,
    so rule 8 keeps checking what a step asks fs to do whichever way it asks.
    Two words where the specs have a two-word subcommand (`task get`,
    `kb prompt`), one otherwise — the second word of `script check-specs.sh` is
    a script name, not a subcommand's second word."""
    found = []
    for m in FS_INVOCATION.finditer(body):
        first, second = m.group(1), m.group(2)
        found.append(
            f"{first} {second}"
            if second and first in two_word and not second.startswith("-")
            else first
        )
    return found


if __name__ == "__main__":
    sys.exit(main())
