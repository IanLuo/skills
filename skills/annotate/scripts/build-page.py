#!/usr/bin/env python3
"""build-page.py — inline chrome.css + chrome.js + content into a single
self-contained HTML page.

The AI writes only the CONTENT (the data + data-anchor elements). This script merges
it with the static chrome into ONE file, so the page works from file:// (which
treats each page as a unique origin and BLOCKS <link>/<script src> to sibling files).

Usage:
  build-page.py <name> <content-file>

Reads:  this skill's references/page-template.html, chrome.css, chrome.js
        <content-file> (the AI's rendered body content)
Writes: .agents/pages/<name>.html  (single self-contained file)
"""

import sys
import pathlib

name = sys.argv[1]
content_file = pathlib.Path(sys.argv[2])

skill_dir = pathlib.Path(__file__).resolve().parent.parent
css = (skill_dir / "references" / "chrome.css").read_text()
js = (skill_dir / "references" / "chrome.js").read_text()
template = (skill_dir / "references" / "page-template.html").read_text()
content = content_file.read_text()

html = (
    template.replace("/*__CHROME_CSS__*/", css)
            .replace("//__CHROME_JS__", js)
            .replace("<title>page</title>", f"<title>{name}</title>")
            .replace('<h1 data-anchor="title">…</h1>', content)
)

out = pathlib.Path(".agents/pages") / f"{name}.html"
out.parent.mkdir(parents=True, exist_ok=True)
out.write_text(html)
print(out)
