#!/usr/bin/env python3
"""
query.py — search the librarian library without loading every entry into context.

Search is greedy over lightweight signals: index.md row text + each hit entry's
frontmatter title/tags/category (NOT the full body — frontmatter is small). Results are
ranked by a simple keyword-overlap score so token-cheap.

.QUERY  "<question>"  (argv[1], the first positional arg)

.INPUT --root <dir>  library root (default $LIBRARY_ROOT or ~/Documents/librarian).
.INPUT --limit N     max rows to print (default 10).

.OUTPUT stdout: lines of `<absolute path>\t<title>\t<score>\t<confidence>`
        best first. Empty output (exit 0) means the library has no matching entry —
        the orchestrator should fall through to research.

The query is intentionally dumb and dependency-free: lowercase substring + token
overlap. It is fast on hundreds of entries and good enough for routing. If the library
scales to thousands of entries, replace the inner ranking with a search index, not this
loop (see the plan's "Open question").
"""

import argparse
import os
import re
import sys
from pathlib import Path

LIBRARY_DEFAULT = Path.home() / "Documents" / "librarian"
FRONTMATTER_RE = re.compile(r"^---\n(.*?)\n---", re.DOTALL)


def parse_frontmatter(text: str) -> dict:
    """Pull a few fields from YAML frontmatter without a YAML dependency."""
    fm = {}
    m = FRONTMATTER_RE.search(text)
    if not m:
        return fm
    body = m.group(1)
    for line in body.splitlines():
        if line.startswith("title:"):
            fm["title"] = _strip(line.split(":", 1)[1].strip())
        elif line.startswith("category:"):
            fm["category"] = line.split(":", 1)[1].strip()
        elif line.startswith("confidence:"):
            fm["confidence"] = line.split(":", 1)[1].strip()
        elif line.startswith("depth:"):
            fm["depth"] = line.split(":", 1)[1].strip()
        elif line.startswith("tags:"):
            fm["tags"] = _split_brackets(line.split(":", 1)[1].strip())
    return fm


def _strip(s):
    return s.strip().strip('"').strip("'")


def _split_brackets(s):
    s = s.strip()
    if s.startswith("[") and s.endswith("]"):
        s = s[1:-1]
    return [t.strip().strip('"').strip("'") for t in s.split(",") if t.strip()]


def score(question_tokens: list, entry_fm: dict, path_text: str) -> int:
    """Token-overlap score across title, tags, category, and the index path text."""
    hay = " ".join(
        [entry_fm.get("title", ""), entry_fm.get("category", "")]
        + entry_fm.get("tags", [])
    ).lower()
    path_text = path_text.lower()
    s = 0
    for t in question_tokens:
        if t in hay:
            s += 2
        if t in path_text:
            s += 1
    return s


def main():
    ap = argparse.ArgumentParser(description="Query the librarian library.")
    ap.add_argument("question", nargs="?", help="the question to match against the library")
    ap.add_argument("--root", default=os.environ.get("LIBRARY_ROOT", str(LIBRARY_DEFAULT)))
    ap.add_argument("--limit", type=int, default=10)
    args = ap.parse_args()

    if not args.question:
        sys.exit("query: a question (positional arg) is required")

    library = Path(args.root) / "library"
    if not library.exists():
        # Empty/null result signals the orchestrator to research.
        sys.exit(0)

    # Walk every .md entry (skipping index.md). Parse frontmatter only.
    hits = []
    q_tokens = [t for t in re.split(r"\W+", args.question.lower()) if len(t) > 2]
    for entry in sorted(library.rglob("*.md")):
        if entry.name == "index.md" or entry.parent == library:
            continue
        text = entry.read_text(errors="replace")
        fm = parse_frontmatter(text)
        rel_path = str(entry.relative_to(library))
        s = score(q_tokens, fm, rel_path)
        if s > 0:
            hits.append((s, entry, fm))

    hits.sort(key=lambda h: h[0], reverse=True)
    for s, entry, fm in hits[: args.limit]:
        title = fm.get("title", entry.stem)
        conf = fm.get("confidence", "?")
        print("\t".join([str(entry), title, str(s), conf]))


if __name__ == "__main__":
    main()