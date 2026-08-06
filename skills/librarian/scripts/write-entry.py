#!/usr/bin/env python3
"""
write-entry.py — persist a researched topic into the librarian library.

Writes one markdown entry with YAML frontmatter under
library/<category>/<slug>.md and updates the index. The model hands it
accepted findings + a synthesis; the script owns the on-disk format so
query.py can rely on it.

.INPUT --topic "<title>"
.INPUT --category <category>    e.g. technology, science. ^[a-z0-9-]+$
.INPUT --tags a,b,c            optional
.INPUT --findings <file>        JSON list: [{"angle","body","sources":[]}, ...]
.INPUT --confidence low|medium|high   the model's read of the batch.
.INPUT --synthesis "<text>"    one paragraph synthesis (required for real runs).
.INPUT --open-questions a,b    optional.
.INPUT --root <dir>            library root (default $LIBRARY_ROOT or ~/Documents/librarian).
.INPUT --today <YYYY-MM-DD>    overrides today's date (deterministic tests).

.OUTPUT stdout: the absolute path of the written entry.
"""

import argparse
import json
import os
import re
import sys
from pathlib import Path

LIBRARY_DEFAULT = Path.home() / "Documents" / "librarian"


def slugify(title: str) -> str:
    s = title.lower().strip()
    s = re.sub(r"[^a-z0-9]+", "-", s)
    return re.sub(r"-+", "-", s).strip("-") or "untitled"


def _today():
    from datetime import date
    return date.today().isoformat()


def _q(s):
    return '"%s"' % str(s).replace('"', '\\"')


def build_body(topic, findings, synthesis, open_qs) -> str:
    lines = [f"# {topic}", "", "## Key findings"]
    for f in findings:
        lines.append(f"\n**Angle:** {f.get('angle', f.get('id', ''))}")
        lines.append(str(f.get("body", "- _(no body provided)_")).strip())
    lines += ["", "## Synthesis", synthesis.strip(), ""]
    if open_qs:
        lines += ["## Open questions"] + [f"- {q}" for q in open_qs] + [""]
    return "\n".join(lines).rstrip() + "\n"


def index_update(index_path: Path, category: str, slug: str, title: str):
    """Insert/replace the row for this entry; rebuild Categories + Index from rows."""
    text = index_path.read_text() if index_path.exists() else ""
    rel = f"{category}/{slug}.md"
    row_re = re.compile(r"^- \[([^\]]+)\]\(([^)]+)\) `([a-z0-9-]+)`\s*$", re.MULTILINE)
    rows = {m.group(2): (m.group(1), m.group(3)) for m in row_re.finditer(text)}
    rows[rel] = (title, category)
    cats = sorted({c for _, c in rows.values()})
    pre = re.split(r"^## Categories\b", text, maxsplit=1, flags=re.MULTILINE)[0].rstrip()
    out = [(pre + "\n") if pre else "# Librarian library index\n"]
    out += ["\n## Categories\n"] + [f"- {c}\n" for c in cats]
    out += ["\n## Index\n"]
    out += [f"- [{t}]({r}) `{c}`\n" for r, (t, c) in
            sorted(rows.items(), key=lambda kv: (kv[1][1], kv[0]))]
    index_path.write_text("".join(out) + "\n")


def main():
    ap = argparse.ArgumentParser(description="Write a librarian library entry.")
    ap.add_argument("--topic", required=True)
    ap.add_argument("--category", required=True)
    ap.add_argument("--tags", default="")
    ap.add_argument("--findings", required=True,
                    help="JSON list of {angle, body, sources:[{url,title,accessed}]}")
    ap.add_argument("--confidence", choices=["low", "medium", "high"], default="high")
    ap.add_argument("--synthesis", default="")
    ap.add_argument("--open-questions", default="")
    ap.add_argument("--root", default=str(os.environ.get("LIBRARY_ROOT", LIBRARY_DEFAULT)))
    ap.add_argument("--today", default=None)
    args = ap.parse_args()

    if not re.match(r"^[a-z0-9-]+$", args.category):
        sys.exit("write-entry: --category must match ^[a-z0-9-]+$")

    if not args.synthesis:
        sys.exit("write-entry: --synthesis is required — synthesize the findings yourself")

    root = Path(args.root)
    library = root / "library"
    library.mkdir(parents=True, exist_ok=True)
    index_path = library / "index.md"
    if not index_path.exists():
        index_path.write_text(
            "# Librarian library index\n\n## Categories\n\n<!-- categories appended below -->\n\n"
            "## Index\n\n<!-- index rows appended below -->\n")

    with open(args.findings) as f:
        findings = json.load(f)

    tags = [t.strip() for t in args.tags.split(",") if t.strip()]
    today = args.today or os.environ.get("LIBRARIAN_TODAY", _today())
    slug = slugify(args.topic)
    entry_dir = library / args.category
    entry_dir.mkdir(parents=True, exist_ok=True)
    entry_path = entry_dir / f"{slug}.md"

    sources = []
    for fr in findings:
        for s in fr.get("sources", []) or []:
            if s not in sources:
                sources.append(s)

    fm = ["---",
          f"title: {_q(args.topic)}",
          f"category: {args.category}",
          f"tags: [{', '.join(_q(t) for t in tags)}]"]
    if sources:
        fm.append("sources:")
        for s in sources:
            fm.append(f"  - url: {_q(s.get('url', ''))}")
            fm.append(f"    title: {_q(s.get('title', ''))}")
            fm.append(f"    accessed: {s.get('accessed', '')}")
    else:
        fm.append("sources: []")
    fm += [f"confidence: {args.confidence}",
           f"researched_at: {today}",
           "---\n"]
    body = build_body(args.topic, findings, args.synthesis,
                      [q.strip() for q in args.open_questions.split("|")
                       if q.strip()] if args.open_questions else None)
    entry_path.write_text("\n".join(fm) + body)
    index_update(index_path, args.category, slug, args.topic)
    print(str(entry_path))


if __name__ == "__main__":
    main()