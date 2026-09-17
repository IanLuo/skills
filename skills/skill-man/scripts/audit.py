#!/usr/bin/env python3
"""
audit.py — the mechanizable half of a skill evaluation (see references/evaluation.md).

validate.py answers "does it satisfy the spec?". This answers the quality questions that
are cheap and deterministic:

  Tier A2  reference integrity — every referenced path resolves, and no resource is orphaned
  Tier C1  body size          — SKILL.md against the soft target and the spec limit

Everything else in evaluation.md is agent judgement. Do NOT add spec rules here: the spec
lives in validate.py's SPEC dict, which is the single source of truth for it.

Checks
  1. refs     Broken / ambiguous path references in any .md inside the skill dir.
              The ambiguous form is `<other-skill>/references/x.md`: the harness resolves
              relative paths against the skill's own dir, so that form works only if the
              reader assumes skills-root-relative — it breaks. Correct forms:
                  references/x.md                   (this skill)
                  ../other-skill/references/x.md    (a sibling)
                  skills/<name>/references/x.md     (skills root, pinned at repo root)

              Severity, because only you know what the skill owns:
                AMBIGUOUS, or `../` escaping the skills dir   → fails the run
                MISSING in SKILL.md (the load path)           → fails the run
                MISSING in references/*.md                    → warning: reference files often
                  quote another project's paths (a catalog entry, a spec example), which
                  are not ours to resolve. Ignore those deliberately.
              Markdown link targets are audited; link display text is not (text is a
              label, the target is the reference).
  2. orphans  Resource files under references/ scripts/ assets/ cited nowhere in the skill.
              A file may legitimately be cited from a script or a sibling reference file,
              not only SKILL.md, so the whole skill dir is searched.
  3. budget   SKILL.md line count (soft target, spec warning line).

Usage
    python3 audit.py [skill ...]           # named skills, or all when omitted
    python3 audit.py --quiet               # only skills with findings
    python3 audit.py --skills-dir PATH     # audit another tree (fixtures, tests)

Exit code: 0 = clean. 1 = an ambiguous reference, or a missing reference in SKILL.md.
Orphans, body size, and missing paths in reference files are warnings and never fail.
2 = no such skill.
"""

import re
import sys
import pathlib

BODY_SOFT = 200   # personal target: past this, push detail into references/
BODY_SPEC = 500   # spec guidance; validate.py warns here
RESOURCE_DIRS = ("references", "scripts", "assets")

# Path-ish tokens that mention a resource dir. Deliberately narrow: it will not match
# `bin/deploy-skills.sh`, `tests/run.sh`, or prose. The `$`/`@`/`:` guards drop shell
# variables ($S/assets/x), makes-targets and URLs.
REF_RE = re.compile(
    r"(?<![\w$@./:-])"
    r"((?:(?:\.\./)+)?[\w.-]+/(?:references|scripts|assets)/[\w./-]+"
    r"|(?:references|scripts|assets)/[\w./-]+)"
)
# A markdown link: keep the target, drop the display text (the text is a label).
MD_LINK_RE = re.compile(r"\[([^\]]*)\]\(([^)]+)\)")


def auditable_tokens(text: str):
    """Reference-ish paths from `text`, excluding markdown link display text."""
    return set(REF_RE.findall(MD_LINK_RE.sub(lambda m: " " + m.group(2) + " ", text)))


# ── Check 1: reference integrity ──────────────────────────────────────────
def check_refs(skill, repo_root, names):
    problems, resolved_count = [], 0
    for md in sorted(skill.rglob("*.md")):
        rel_md = md.relative_to(skill)
        in_load_path = rel_md.name == "SKILL.md"
        for token in sorted(auditable_tokens(md.read_text())):
            first = token.split("/")[0]

            if token.startswith("skills/"):
                target, how = repo_root / token, "repo root"
            elif token.startswith("../"):
                # `../` is spent from the skill dir; the most it may reach is a sibling, so
                # the target has to stay inside the skills dir (skill.parent).
                target, how = (skill / token).resolve(), "skill dir"
                if not str(target).startswith(str(skill.parent)):
                    problems.append(
                        (rel_md, token,
                         "escapes the skills dir — every `../` is spent from the skill dir, so "
                         "one level reaches a sibling; drop the extra `../`",
                         "../" + "/".join(token.split("/")[2:]) if token.count("../") > 1
                         else None, True)
                    )
                    continue
            elif first in names:
                problems.append(
                    (rel_md, token,
                     "ambiguous form — read as skill-dir relative, so it resolves only if "
                     "you happen to be at the skills root",
                     _fix_hint(token, skill, names), True)
                )
                continue
            elif first in RESOURCE_DIRS:
                target, how = skill / token, "skill dir"
            else:
                continue  # project-relative (e.g. .agents/canvas/) — not ours to check

            if target.exists():
                resolved_count += 1
            else:
                problems.append(
                    (rel_md, token,
                     f"missing (checked against {how})"
                     + ("" if in_load_path else " — ignore if this quotes another project"),
                     None, in_load_path)
                )
    return problems, resolved_count


def _fix_hint(token, skill, names):
    first = token.split("/")[0]
    if first == skill.name:
        return "references/… — drop the skill-name prefix, you are already in that dir"
    if first in names:
        return f"../{token} — resolve the sibling from this skill's dir"
    return None


# ── Check 2: orphan resources ─────────────────────────────────────────────
def check_orphans(skill):
    text = "\n".join(p.read_text(errors="ignore") for p in skill.rglob("*") if p.is_file())
    orphans = []
    for rd in RESOURCE_DIRS:
        base = skill / rd
        for f in sorted(base.rglob("*")) if base.is_dir() else []:
            if f.is_file() and not f.name.startswith(".") and f.name not in text:
                orphans.append(f.relative_to(skill))
    return orphans


# ── Check 3: body budget ──────────────────────────────────────────────────
def check_budget(skill):
    lines = len((skill / "SKILL.md").read_text().splitlines())
    if lines > BODY_SPEC:
        return lines, f"over the spec guidance ({BODY_SPEC}) — validate.py warns here"
    if lines > BODY_SOFT:
        return lines, f"over the soft target ({BODY_SOFT}) — move detail into references/"
    return lines, None


# ── Report ────────────────────────────────────────────────────────────────
def audit(skill, repo_root, names, quiet):
    problems, ref_count = check_refs(skill, repo_root, names)
    orphans = check_orphans(skill)
    lines, note = check_budget(skill)

    if quiet and not problems and not orphans and not note:
        return 0, []

    out = [f"── {skill.name} ──", "  Tier A2 · reference integrity"]
    if problems:
        for rel_md, token, why, fix, hard in problems:
            out.append(f"    {'✖' if hard else '⚠'} {rel_md}: `{token}` — {why}")
            if fix:
                out.append(f"        use: {fix}")
    else:
        out.append(f"    ✓ {ref_count} reference(s) resolve")

    out.append("  Tier C1 · orphan resources")
    out += [f"    ⚠ {o} — cited nowhere in the skill" for o in orphans] or ["    ✓ none"]

    out.append("  Tier C1 · body size")
    out.append(
        f"    {'⚠' if note else '✓'} {lines} lines"
        + (f" — {note}" if note else f" (soft {BODY_SOFT}, spec {BODY_SPEC})")
    )
    return (1 if any(p[4] for p in problems) else 0), out


def main(argv):
    default_dir = pathlib.Path(__file__).resolve().parent.parent.parent
    skills_dir, quiet, args = default_dir, False, list(argv)

    if "--skills-dir" in args:
        i = args.index("--skills-dir")
        if i + 1 >= len(args):
            print("✖ --skills-dir needs a path", file=sys.stderr)
            return 2
        skills_dir = pathlib.Path(args[i + 1]).resolve()
        del args[i:i + 2]
    if "--quiet" in args:
        quiet = True
        args.remove("--quiet")

    if not skills_dir.is_dir():
        print(f"✖ no such skills dir: {skills_dir}", file=sys.stderr)
        return 2

    repo_root = skills_dir.parent
    wanted = set(args)
    names = {d.name for d in skills_dir.iterdir() if d.is_dir()}

    targets = [s for s in sorted(skills_dir.iterdir())
               if s.is_dir() and (s / "SKILL.md").exists() and (not wanted or s.name in wanted)]
    if wanted and not targets:
        print(f"✖ no such skill: {', '.join(sorted(wanted))}", file=sys.stderr)
        return 2

    failed = 0
    for s in targets:
        code, report = audit(s, repo_root, names, quiet)
        failed |= code
        if report:
            print("\n".join(report))

    print()
    if failed:
        print("✖ Tier A2 failing — fix the references before judging anything else")
    else:
        print(f"✓ Tier A2 clean across {len(targets)} skill(s)")
    return 1 if failed else 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
