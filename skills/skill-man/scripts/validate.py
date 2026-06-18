#!/usr/bin/env python3
"""
validate.py — validate every skill in this repo's skills/ directory.

This is the SINGLE SOURCE OF TRUTH for the skill frontmatter spec in this repo.
The rules live in the SPEC dict below; skill-spec.md and SKILL.md are cheatsheets
that restate what this enforces. To upgrade the spec, edit SPEC (and the provenance
constants), then re-run tests/run.sh + the upstream-conformance check.

Provenance — the canonical spec lives at:
    SPEC_SOURCE        https://agentskills.io/specification
Validator reference (the rules below are synced from):
    SPEC_VALIDATOR     https://github.com/anthropics/skills/blob/main/skills/skill-creator/scripts/quick_validate.py
    SPEC_PINNED_REF    57546260929473d4e0d1c1bb75297be2fdfa1949  (anthropics/skills commit rules were last synced from)
    SPEC_REVIEWED      2026-06

YAML parsing: PyYAML is the primary parser (matches the official validator
exactly — handles block scalars, lists, quoted values). A stdlib-only fallback
handles the common subset (single-line scalars + one-level nested maps) so the
script still runs where PyYAML is absent, but with a clear limitation message.

Usage:
    python3 validate.py [skills_dir]

If skills_dir is omitted, it is resolved relative to this script's location
(skills/ at the repo root), so the script works whether run from the repo or via
the deployed symlink at ~/.agents/skills/skill-man/scripts/validate.py.

Exit code: 0 if every skill passes, 1 if any skill fails (warnings do not fail).
"""

import os
import re
import sys

# ── Provenance ────────────────────────────────────────────────────────────
SPEC_SOURCE = "https://agentskills.io/specification"
SPEC_VALIDATOR = (
    "https://github.com/anthropics/skills/blob/main/skills/skill-creator/"
    "scripts/quick_validate.py"
)
SPEC_PINNED_REF = "57546260929473d4e0d1c1bb75297be2fdfa1949"  # anthropics/skills main, synced 2026-06
SPEC_REVIEWED = "2026-06"

# ── Spec (the single source of truth) ─────────────────────────────────────
SPEC = {
    "allowed_keys": {"name", "description", "license", "allowed-tools", "metadata", "compatibility"},
    "max_name": 64,
    "max_description": 1024,
    "max_compatibility": 500,
    "soft_body_line_limit": 500,  # warn, not fail (official spec guidance, not validator-enforced)
    "name_regex": re.compile(r"^[a-z0-9-]+$"),
}

# Whether PyYAML is available (primary parser).
try:
    import yaml as _yaml  # noqa: N812
    _HAVE_YAML = True
except ImportError:
    _HAVE_YAML = False


def repo_root_from_script() -> str:
    """Resolve repo root by following this script's symlink chain.

    Script lives at <repo>/skills/skill-man/scripts/validate.py.
    Asserts the resolved root actually contains a skills/ dir, so a relocated
    script fails loudly instead of scanning the wrong place.
    """
    script = os.path.realpath(__file__)  # follows symlinks + parent-dir symlinks
    scripts_dir = os.path.dirname(script)
    root = os.path.dirname(os.path.dirname(os.path.dirname(scripts_dir)))
    if not os.path.isdir(os.path.join(root, "skills")):
        raise SystemExit(
            f"validate: could not locate repo root (expected skills/<name>/scripts/ layout). "
            f"Resolved to {root!r} which has no skills/ dir."
        )
    return root


# ── Frontmatter parsing ───────────────────────────────────────────────────

def parse_frontmatter(text: str):
    """Return (frontmatter_dict_or_None, error_str_or_None, used_yaml_bool).

    PyYAML first (correct for block scalars, lists, quotes); stdlib fallback for
    the common single-line + one-level-nested-map subset when PyYAML is absent.
    """
    if not text.startswith("---"):
        return None, "No YAML frontmatter found (must start with '---')", _HAVE_YAML
    m = re.match(r"^---\n(.*?)\n---\s*\n?", text, re.DOTALL)
    if not m:
        return None, "Invalid frontmatter format (missing closing '---')", _HAVE_YAML

    if _HAVE_YAML:
        try:
            fm = _yaml.safe_load(m.group(1))
        except _yaml.YAMLError as e:
            return None, f"Invalid YAML in frontmatter: {e}", True
        if not isinstance(fm, dict):
            return None, "Frontmatter must be a YAML mapping", True
        return fm, None, True

    fm, err = _parse_frontmatter_subset(m.group(1))
    return fm, err, False


def _parse_frontmatter_subset(fm_text: str):
    """Stdlib fallback. Handles single-line `key: value` + one-level nested maps.

    Limitations vs PyYAML: no block scalars (`|`, `|-`, `>`, `>-`), no block
    sequences (`- item`), no flow collections. If a value uses one of these,
    returns a clear error pointing the author at PyYAML rather than an opaque
    parse failure.
    """
    fm = {}
    current_top = None
    for raw_line in fm_text.splitlines():
        if not raw_line.strip() or raw_line.lstrip().startswith("#"):
            continue
        if not raw_line[0].isspace():
            current_top = None
            if ":" not in raw_line:
                return None, f"Invalid frontmatter line (no ':'): {raw_line!r}"
            key, _, val = raw_line.partition(":")
            key = key.strip()
            val = _strip_comment(val).strip()
            # Block-scalar indicators require PyYAML.
            if val in ("|", "|-", ">", ">-", "|+", ">+"):
                return None, (
                    f"block-scalar indicator {val!r} for {key!r} requires PyYAML "
                    f"(pip install pyyaml), which is not available"
                )
            if val == "":
                current_top = key
                fm[key] = {}
            else:
                fm[key] = _scalar(val)
        else:
            if current_top is None:
                return None, (
                    f"unexpected indented line {raw_line!r}; the stdlib fallback parser "
                    f"cannot handle block scalars/lists — install PyYAML"
                )
            if not isinstance(fm[current_top], dict):
                fm[current_top] = {}
            line = raw_line.strip()
            if line.startswith("- "):
                return None, (
                    f"block sequence under {current_top!r} requires PyYAML "
                    f"(the stdlib fallback cannot parse YAML lists)"
                )
            if ":" not in line:
                return None, f"Invalid nested line: {raw_line!r}"
            key, _, val = line.partition(":")
            fm[current_top][key.strip()] = _scalar(_strip_comment(val).strip())
    return fm, None


def _scalar(val: str):
    """Strip surrounding quotes from a scalar value."""
    if len(val) >= 2 and val[0] == val[-1] and val[0] in ("'", '"'):
        return val[1:-1]
    return val


def _strip_comment(val: str) -> str:
    """Strip an unquoted trailing YAML comment (' # ...') from a scalar value."""
    in_single = in_double = False
    for i, ch in enumerate(val):
        if ch == "'" and not in_double:
            in_single = not in_single
        elif ch == '"' and not in_single:
            in_double = not in_double
        elif ch == "#" and not in_single and not in_double and i > 0 and val[i - 1] in (" ", "\t"):
            return val[:i]
    return val


# ── Per-skill validation ──────────────────────────────────────────────────

def validate_skill(skill_dir: str):
    """Return (ok: bool, messages: list[str])."""
    skill_md = os.path.join(skill_dir, "SKILL.md")
    name = os.path.basename(skill_dir)
    msgs = []

    if not os.path.isfile(skill_md):
        msgs.append(f"✖ {name}: SKILL.md not found")
        return False, msgs

    try:
        content = open(skill_md, encoding="utf-8").read()
    except OSError as e:
        msgs.append(f"✖ {name}: cannot read SKILL.md: {e}")
        return False, msgs

    fm, err, used_yaml = parse_frontmatter(content)
    if err:
        msgs.append(f"✖ {name}: {err}")
        if not used_yaml:
            msgs.append(f"  ({name}: parsed with stdlib fallback; install PyYAML for full YAML support)")
        return False, msgs

    ok = True
    allowed = SPEC["allowed_keys"]
    unexpected = set(fm.keys()) - allowed
    if unexpected:
        msgs.append(
            f"✖ {name}: unexpected frontmatter key(s): {', '.join(sorted(unexpected))}. "
            f"Allowed: {', '.join(sorted(allowed))}"
        )
        ok = False

    ok = _check_name(fm, name, msgs) and ok
    ok = _check_description(fm, name, msgs) and ok
    ok = _check_compat(fm, name, msgs) and ok

    # Body length warning (progressive-disclosure budget — spec guidance, not validator-enforced).
    close = re.search(r"\n---\s*\n", content)
    body_text = content[close.end():] if close else content
    body_lines = len(body_text.splitlines())
    if body_lines > SPEC["soft_body_line_limit"]:
        msgs.append(
            f"⚠ {name}: body is {body_lines} lines (> {SPEC['soft_body_line_limit']}); "
            f"consider moving detail into references/"
        )

    if ok and not any(m.startswith("⚠") for m in msgs):
        msgs.append(f"✓ {name}")
    elif ok:
        msgs.insert(0, f"✓ {name} (with warnings)")
    return ok, msgs


def _check_name(fm, name, msgs) -> bool:
    if "name" not in fm:
        msgs.append(f"✖ {name}: missing 'name' in frontmatter")
        return False
    nm = fm["name"]
    if not isinstance(nm, str):
        msgs.append(f"✖ {name}: 'name' must be a string")
        return False
    nm = nm.strip()
    if not SPEC["name_regex"].match(nm):
        msgs.append(f"✖ {name}: name '{nm}' must be lowercase letters, digits, hyphens only")
        return False
    if nm.startswith("-") or nm.endswith("-") or "--" in nm:
        msgs.append(f"✖ {name}: name '{nm}' cannot start/end with hyphen or contain '--'")
        return False
    if len(nm) > SPEC["max_name"]:
        msgs.append(f"✖ {name}: name too long ({len(nm)} > {SPEC['max_name']})")
        return False
    if nm != name:
        msgs.append(f"✖ {name}: name '{nm}' must equal folder name '{name}'")
        return False
    return True


def _check_description(fm, name, msgs) -> bool:
    if "description" not in fm:
        msgs.append(f"✖ {name}: missing 'description' in frontmatter")
        return False
    desc = fm["description"]
    if not isinstance(desc, str):
        msgs.append(f"✖ {name}: 'description' must be a string")
        return False
    desc = desc.strip()
    ok = True
    if "<" in desc or ">" in desc:
        msgs.append(f"✖ {name}: description cannot contain '<' or '>'")
        ok = False
    if len(desc) > SPEC["max_description"]:
        msgs.append(f"✖ {name}: description too long ({len(desc)} > {SPEC['max_description']})")
        ok = False
    if "TODO" in desc:
        msgs.append(f"⚠ {name}: description still contains TODO — fill it in before deploying")
    return ok


def _check_compat(fm, name, msgs) -> bool:
    if "compatibility" not in fm:
        return True  # optional; most skills omit it
    compat = fm["compatibility"]
    if not isinstance(compat, str):
        msgs.append(f"✖ {name}: 'compatibility' must be a string")
        return False
    if len(compat) > SPEC["max_compatibility"]:
        msgs.append(
            f"✖ {name}: compatibility too long ({len(compat)} > {SPEC['max_compatibility']})"
        )
        return False
    return True


# ── Main ──────────────────────────────────────────────────────────────────

def main():
    arg = sys.argv[1] if len(sys.argv) > 1 else os.path.join(repo_root_from_script(), "skills")

    # If the arg points at a single skill dir (contains SKILL.md), validate just it.
    if os.path.isfile(os.path.join(arg, "SKILL.md")):
        if not _HAVE_YAML:
            print("validate: PyYAML not found — using stdlib fallback (no block-scalar/list support).", file=sys.stderr)
        ok, msgs = validate_skill(os.path.abspath(arg))
        for line in msgs:
            print(line)
        return 0 if ok else 1

    skills_dir = arg
    if not os.path.isdir(skills_dir):
        print(f"validate: skills directory not found: {skills_dir}", file=sys.stderr)
        return 1

    if not _HAVE_YAML:
        print("validate: PyYAML not found — using stdlib fallback (no block-scalar/list support).", file=sys.stderr)

    skill_dirs = sorted(
        os.path.join(skills_dir, d)
        for d in os.listdir(skills_dir)
        if not d.startswith(".") and os.path.isdir(os.path.join(skills_dir, d))
    )
    if not skill_dirs:
        print(f"validate: no skills found in {skills_dir}")
        return 0

    all_ok = True
    for sd in skill_dirs:
        ok, msgs = validate_skill(sd)
        if not ok:
            all_ok = False
        for line in msgs:
            print(line)
    print()
    print("All skills valid." if all_ok else "Some skills failed validation.")
    return 0 if all_ok else 1


if __name__ == "__main__":
    sys.exit(main())
