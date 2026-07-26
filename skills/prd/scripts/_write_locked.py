#!/usr/bin/env python3
"""
_write_locked.py — reads an (optional) existing prd doc, strips the old lock marker +
## Link contract block, and re-emits a fresh marker + contract + the preserved body.

Called only by lock.sh. Kept in Python over awk because the marker/contract parsing is
the fragile correctness surface — a script owns it so downstream tools can grep the
exact marker shape. Token-cheap: not read into context at runtime.
"""
import argparse

MARKER_RE = "<!-- prd:locked:{sha} {date} type={typed} -->"
CONTRACT_HEADER = "## Link contract"


def split_list(s: str) -> str:
    parts = [p.strip() for p in s.replace("|", ",").split(",") if p.strip()]
    return ", ".join(parts) if parts else "none"


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--doc", required=True)
    ap.add_argument("--sha", required=True)
    ap.add_argument("--date", required=True)
    ap.add_argument("--type", required=True)
    ap.add_argument("--upstream", default="")
    ap.add_argument("--referrers", default="")
    args = ap.parse_args()

    try:
        text = open(args.doc).read()
    except FileNotFoundError:
        text = ""

    # Strip the old marker (first line if it matches) and the old contract block.
    lines = text.splitlines()
    i = 0
    if lines and lines[0].startswith("<!-- prd:locked:"):
        i = 1
        if i < len(lines) and lines[i].strip() == "":
            i += 1
    while i < len(lines):
        if lines[i].strip() == CONTRACT_HEADER:
            i += 1
            while i < len(lines) and lines[i].startswith("- "):
                i += 1
            if i < len(lines) and lines[i].strip() == "":
                i += 1
            break
        break  # no contract header at this offset -> stop stripping

    body_lines = lines[i:]
    while body_lines and body_lines[0].strip() == "":
        body_lines.pop(0)

    marker = MARKER_RE.format(sha=args.sha, date=args.date, typed=args.type)
    contract = [
        CONTRACT_HEADER,
        f"- **upstream** (this doc relies on): {split_list(args.upstream)}",
        f"- **referrers** (must cite this when they change): {split_list(args.referrers)}",
    ]

    out = [marker, ""] + contract + [""]
    if body_lines:
        out += body_lines
    with open(args.doc, "w") as f:
        f.write("\n".join(out).rstrip() + "\n")


if __name__ == "__main__":
    main()