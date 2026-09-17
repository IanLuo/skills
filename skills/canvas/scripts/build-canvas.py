#!/usr/bin/env python3
"""build-canvas.py — assemble one self-contained canvas shell.

The agent edits only <root>/<topic>.content.html (section-keyed content). This
script inlines chrome.css + chrome.js + that content into <root>/<topic>.html,
so the canvas also works as a plain file:// page when the daemon is not running
(static mode: annotations in localStorage + copy-paste feedback).

Usage:
    build-canvas.py <topic> [--root DIR] [--content FILE] [--new] [--inline-mermaid]

    --new              write a starter content file if none exists
    --inline-mermaid   embed mermaid (≈3.4 MB) so diagrams render from file:// too
"""

import argparse
import json
import re
import sys
import json
from html.parser import HTMLParser
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))
from canvas import DEFAULT_ROOT, TOPIC_RE, sha  # noqa: E402

SKILL_DIR = Path(__file__).resolve().parent.parent
REFS = SKILL_DIR / "references"
MERMAID_ASSET = SKILL_DIR / "assets" / "mermaid.min.js"

STARTER = """\
<section data-section="s1">
  <p class="eyebrow">canvas</p>
  <h1 data-anchor="title">%s</h1>
  <div class="meta-row"><span><b>1</b> section</span><span class="sep">·</span><span>edit <code>%s/content.html</code> to change this</span></div>
  <p class="note" data-anchor="run-conclusion">Nothing has run yet — the first round rewrites this line with its conclusion.</p>
</section>
"""


RENDER_KINDS = {"compare", "cards", "bars", "flow"}


class ContentCheck(HTMLParser):
    """Catch at build time what would otherwise show up as a broken page."""

    def __init__(self):
        super().__init__()
        self.anchors = []
        self.section_ids = []
        self.sections = 0
        self.depth = 0
        self.nested = 0
        self.specs = []          # (kind, json_text)
        self.render_divs = 0
        self._kind = None
        self._in_json = False
        self._buf = []

    def handle_starttag(self, tag, attrs):
        a = dict(attrs)
        if tag == "section":
            self.sections += 1
            if "data-section" in a:
                self.section_ids.append(a["data-section"])
            if self.depth:
                self.nested += 1
            self.depth += 1
        if "data-anchor" in a:
            self.anchors.append(a["data-anchor"])
        if tag == "div" and "data-render" in a:
            self._kind = a["data-render"]
            self.render_divs += 1
        if tag == "script" and a.get("type") == "application/json":
            self._in_json = True
            self._buf = []

    def handle_data(self, data):
        if self._in_json:
            self._buf.append(data)

    def handle_endtag(self, tag):
        if tag == "section":
            self.depth = max(0, self.depth - 1)
        if tag == "script" and self._in_json:
            self._in_json = False
            self.specs.append((self._kind, "".join(self._buf)))


def check_content(content):
    """Return (errors, summary). Errors are fatal; the page would be broken."""
    c = ContentCheck()
    c.feed(content)
    errors = []

    if c.sections == 0:
        errors.append("no <section data-section=\"…\"> blocks — the daemon cannot hot-swap sections")
    if c.nested:
        errors.append("%d nested <section> — sections must be flat or hashing degrades to full replaces"
                      % c.nested)

    dupes = sorted({a for a in c.anchors if c.anchors.count(a) > 1})
    if dupes:
        errors.append("duplicate data-anchor ids: %s" % ", ".join(dupes))

    dupe_sections = sorted({s for s in c.section_ids if c.section_ids.count(s) > 1})
    if dupe_sections:
        errors.append("duplicate data-section ids: %s — show and the hot-swap key on a unique id"
                      % ", ".join(dupe_sections))

    for kind, raw in c.specs:
        try:
            json.loads(raw)
        except ValueError as e:
            errors.append("data-render=%s has invalid JSON: %s" % (kind, e))
            continue
        if kind not in RENDER_KINDS:
            errors.append("data-render=%s is not a known kind (%s)"
                          % (kind, ", ".join(sorted(RENDER_KINDS))))

    if c.render_divs != len(c.specs):
        errors.append("%d data-render block(s) but %d JSON payload(s) — each needs one"
                      % (c.render_divs, len(c.specs)))

    missing = [k for k, _ in c.specs if not k]
    if missing:
        errors.append("%d data-render block(s) with no sibling JSON <script>" % len(missing))

    summary = ("%d sections · %d anchors (%d unique) · %d data-render specs · %d bytes"
               % (c.sections, len(c.anchors), len(set(c.anchors)), len(c.specs), len(content)))
    return errors, summary


def inline_answer_notes(content, feedback_path):
    """Write each answer's note into the file, so the join survives without the daemon.

    In static (`file://`) mode the page reads annotations from localStorage, so an answer opened
    in another browser, or after storage was cleared, rendered bare — losing the one thing the
    block is for. The note is on disk at build time, so it goes into the document. Runtime still
    falls back to the annotations when the attribute is absent (an answer whose note has since
    been deleted), and says the note is gone rather than dropping the join silently.
    """
    try:
        notes = {a.get("id"): (a.get("comment") or "")
                 for a in json.loads(feedback_path.read_text(encoding="utf-8"))["annotations"]}
    except (OSError, ValueError, KeyError, TypeError):
        return content

    def add(m):
        tag, aid = m.group(0), m.group(1)
        if "data-note=" in tag or aid not in notes:
            return tag
        text = (notes[aid].replace("&", "&amp;").replace("<", "&lt;")
                .replace(">", "&gt;").replace('"', "&quot;"))
        return tag[:-1] + ' data-note="%s">' % text

    return re.sub(r'<div\b[^>]*\bdata-answers="([^"]+)"[^>]*>', add, content)


def build(args):
    topic = args.topic
    if not TOPIC_RE.match(topic):
        raise SystemExit("build-canvas: bad topic %r (want ^[a-z0-9][a-z0-9-]{0,63}$)" % topic)

    root = Path(args.root)
    topic_path = root / topic
    topic_path.mkdir(parents=True, exist_ok=True)
    content_path = Path(args.content) if args.content else topic_path / "content.html"
    shell_path = topic_path / "index.html"

    if not content_path.is_file():
        if not args.new:
            raise SystemExit("build-canvas: no content at %s (use --new to scaffold)" % content_path)
        content_path.write_text(STARTER % (topic, topic), encoding="utf-8")

    content = content_path.read_text(encoding="utf-8")
    content = inline_answer_notes(content, topic_path / "feedback.json")

    errors, summary = check_content(content)
    print("check    " + summary)
    if errors:
        for e in errors:
            print("error    " + e, file=sys.stderr)
        raise SystemExit("build-canvas: refusing to build a broken canvas")
    css = (REFS / "chrome.css").read_text(encoding="utf-8")
    js = (REFS / "chrome.js").read_text(encoding="utf-8")
    template = (REFS / "canvas-template.html").read_text(encoding="utf-8")

    mermaid_inline = ""
    if args.inline_mermaid:
        if not MERMAID_ASSET.is_file():
            raise SystemExit("build-canvas: --inline-mermaid but %s is missing" % MERMAID_ASSET)
        mermaid_inline = MERMAID_ASSET.read_text(encoding="utf-8")

    # Anything inlined INSIDE the <script> must not contain `</script`, or the HTML
    # parser ends the script there and spills the remainder into the page as text.
    # (A `</script>` inside a JS comment caused exactly that — diagrams never rendered.)
    for label, text in (("chrome.js", js), ("mermaid.min.js", mermaid_inline)):
        if re.search(r"</script", text, re.I):
            raise SystemExit(
                "build-canvas: %s contains a literal `</script` — an HTML parser would "
                "truncate the inlined script there. Escape it as <\\/script." % label)

    html = (
        template
        .replace("/*__CHROME_CSS__*/", css)
        .replace("//__LIVE_CONFIG__",
                 'window.CANVAS = { topic: "%s", live: true };' % topic)
        .replace("//__MERMAID_INLINE__", mermaid_inline)
        .replace("//__CHROME_JS__", js)
        .replace("<title>canvas</title>", "<title>canvas · %s</title>" % topic)
        .replace("__CONTENT_V__", sha(content))
        .replace("<!--__CONTENT__-->", content.rstrip())
    )
    shell_path.write_text(html, encoding="utf-8")

    print(shell_path)
    print("content  %s" % content_path)
    try:
        port = json.loads((root / ".daemon.json").read_text())["port"]
        print("open     http://127.0.0.1:%d/t/%s" % (port, topic))
    except (OSError, ValueError, KeyError):
        # No daemon yet — its port is picked at start time, so do not guess one.
        print("open     (start the daemon: canvas.py start --root %s --open %s)" % (args.root, topic))
    return 0


def main(argv=None):
    p = argparse.ArgumentParser(prog="build-canvas.py")
    p.add_argument("topic")
    p.add_argument("--root", default=DEFAULT_ROOT)
    p.add_argument("--content", default=None, help="content file (default <root>/<topic>/content.html)")
    p.add_argument("--new", action="store_true", help="scaffold a starter content file")
    p.add_argument("--inline-mermaid", action="store_true",
                   help="embed mermaid so diagrams render offline from file://")
    return build(p.parse_args(argv))


if __name__ == "__main__":
    sys.exit(main())
