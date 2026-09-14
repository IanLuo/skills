#!/usr/bin/env python3
"""
test_audit.py — behaviour tests for skill-man's audit.py (the mechanized half of
references/evaluation.md).

Builds throwaway skills trees in a temp dir and asserts audit.py's exit code and reported
severity for each reference-integrity rule. The rules under test:

  1. `references/x.md` resolves against the skill's own dir
  2. `../other/references/x.md` resolves a sibling
  3. `<other>/references/x.md` is AMBIGUOUS — always a failure, with a fix hint
  4. a missing path in SKILL.md fails (SKILL.md is the load path)
  5. a missing path in references/*.md only warns (reference files quote other projects)
  6. markdown link display text is ignored; the link target is what counts
  7. an uncited resource file warns (cited from a script still counts as cited)
  8. body size warns past the soft target
  9. quoted foreign paths in reference files do not fail the run

Usage: python3 tests/audit/test_audit.py     (run from anywhere)
Exit: 0 all pass, 1 any fail. Prints one line per failure, prefixed FAIL.
"""

import pathlib
import shutil
import subprocess
import sys
import tempfile

AUDIT = pathlib.Path(__file__).resolve().parents[2] / "skills/skill-man/scripts/audit.py"

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


def run(skills_dir: pathlib.Path, *args):
    """Run audit.py against a tree. Returns (exit_code, stdout)."""
    p = subprocess.run(
        [sys.executable, str(AUDIT), "--skills-dir", str(skills_dir), *args],
        capture_output=True, text=True,
    )
    return p.returncode, p.stdout + p.stderr


def body(extra="", pad=0):
    """A minimal valid SKILL.md; `pad` lines of filler to exercise the size warning."""
    return (
        "---\nname: {name}\ndescription: A test skill.\n---\n\n# {name}\n\n"
        + extra + "\n" + ("padding line\n" * pad)
    )


def build(root: pathlib.Path, name, extra="", resources=(), pad=0):
    """Create skills/<name>/SKILL.md plus any (relative_path, content) resources."""
    skill = root / "skills" / name
    write(skill / "SKILL.md", body(extra, pad).format(name=name))
    for rel, content in resources:
        write(skill / rel, content)
    return skill


def main():
    tmp = pathlib.Path(tempfile.mkdtemp(prefix="audit-test-"))
    try:
        # ── 1 + 2: correct forms resolve ──────────────────────────────────
        root = tmp / "good"
        build(root, "alpha",
              extra="Read `references/guide.md` before starting.",
              resources=[("references/guide.md", "guide")])
        build(root, "beta",
              extra="House format: `../alpha/references/guide.md`.")
        code, out = run(root / "skills")
        check("own-dir reference resolves", code == 0, f"exit={code}\n{out}")
        check("sibling reference resolves", "missing" not in out, out)

        # ── 3: ambiguous form fails, with a fix hint ──────────────────────
        root = tmp / "ambiguous"
        build(root, "alpha", resources=[("references/guide.md", "guide")])
        build(root, "beta", extra="House format: `alpha/references/guide.md`.")
        code, out = run(root / "skills", "beta")
        check("ambiguous form fails the run", code == 1, f"exit={code}\n{out}")
        check("ambiguous form explains the fix", "../alpha/references/guide.md" in out,
              f"fix hint missing:\n{out}")

        # ── 4: missing in SKILL.md fails ──────────────────────────────────
        root = tmp / "missing-load-path"
        build(root, "gamma", extra="Read `references/typo.md` first.")
        code, out = run(root / "skills", "gamma")
        check("missing ref in SKILL.md fails", code == 1, f"exit={code}\n{out}")

        # ── 5 + 9: missing in a reference file only warns ─────────────────
        root = tmp / "missing-reference-file"
        build(root, "delta",
              extra="Read `references/notes.md`.",
              resources=[("references/notes.md",
                          "Upstream ships `scripts/foreign_tool.py` for this.")])
        code, out = run(root / "skills", "delta")
        check("missing ref in reference file does not fail", code == 0, f"exit={code}\n{out}")
        check("quoted foreign path is reported as a warning", "⚠" in out and "foreign_tool" in out,
              out)

        # ── escaping ../ forms fail, with the corrected path ──────────────
        root = tmp / "escape"
        build(root, "alpha", resources=[("references/guide.md", "guide")])
        build(root, "beta",
              extra="House format: `../alpha/references/guide.md`.",
              resources=[("references/deep.md", "See `../../alpha/references/guide.md`.")])
        code, out = run(root / "skills", "beta")
        check("extra ../ is flagged as escaping", code == 1 and "escapes the skills dir" in out,
              f"exit={code}\n{out}")
        check("escaping form offers the one-level fix", "use: ../alpha/references/guide.md" in out,
              out)
        root = tmp / "escape-ok"
        build(root, "alpha", resources=[("references/guide.md", "guide")])
        build(root, "beta", resources=[("references/deep.md", "See `../alpha/references/guide.md`.")])
        code, out = run(root / "skills", "beta")
        check("sibling ref works from a nested reference file", code == 0, f"exit={code}\n{out}")

        # ── 6: link display text ignored, target audited ──────────────────
        root = tmp / "links"
        build(root, "alpha", resources=[("references/guide.md", "guide")])
        build(root, "eps",
              extra="House format: [`alpha/references/guide.md`](../alpha/references/guide.md).")
        code, out = run(root / "skills", "eps")
        check("link display text is ignored", code == 0, f"exit={code}\n{out}")
        build(root, "zeta",
              extra="House format: [label](../alpha/references/GONE.md).")
        code, out = run(root / "skills", "zeta")
        check("link target is audited", code == 1, f"exit={code}\n{out}")

        # ── 7: orphans ────────────────────────────────────────────────────
        root = tmp / "orphans"
        build(root, "eta",
              extra="Read `references/used.md`.",
              resources=[("references/used.md", "x"), ("references/unused.md", "y")])
        code, out = run(root / "skills", "eta")
        check("uncited resource warns", code == 0 and "unused.md" in out, f"exit={code}\n{out}")
        check("cited resource is not flagged orphan", "references/used.md —" not in out, out)

        # resource cited only from a script still counts as cited
        root = tmp / "orphan-from-script"
        build(root, "theta",
              resources=[("references/tmpl.html", "<html>"),
                         ("scripts/build.py", "TEMPLATE = 'tmpl.html'\n")])
        code, out = run(root / "skills", "theta")
        check("resource cited from a script is not an orphan", "tmpl.html —" not in out, out)

        # ── 8: body size ──────────────────────────────────────────────────
        root = tmp / "size"
        build(root, "iota", pad=260)
        code, out = run(root / "skills", "iota")
        check("oversized body warns without failing", code == 0 and "lines" in out,
              f"exit={code}\n{out}")
        check("oversized body names the soft target", "soft target" in out, out)

        # ── usage errors ──────────────────────────────────────────────────
        code, _ = run(tmp / "does-not-exist")
        check("missing skills dir exits 2", code == 2, f"exit={code}")
        root = tmp / "usage"
        build(root, "kappa")
        code, _ = run(root / "skills", "nope")
        check("unknown skill exits 2", code == 2, f"exit={code}")
    finally:
        shutil.rmtree(tmp, ignore_errors=True)

    print(f"\n  {PASS} passed, {len(FAILS)} failed")
    if FAILS:
        print(f"FAIL summary: {', '.join(FAILS)}")
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
