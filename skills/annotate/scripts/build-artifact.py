#!/usr/bin/env python3
"""build-artifact.py — inline chrome.css + chrome.js + content into a single
self-contained artifact.html.

The AI writes only the CONTENT (the data + data-anchor elements). This script merges
it with the static chrome into ONE file, so the artifact works from file:// (which
treats each page as a unique origin and BLOCKS <link>/<script src> to sibling files).

Usage:
  build-artifact.py <name> <content-file> [--prev <previous-artifact.html>]

Reads:  this skill's references/artifact-template.html, chrome.css, chrome.js
        <content-file> (the AI's rendered body content)
        --prev: the previous round's artifact, to check nothing was silently dropped
Writes: .agents/artifacts/<name>.html  (single self-contained file)

Checking the round loop. A round answers notes the user left on the last one, and the
failure to design against is SILENT LOSS: a note paraphrased into prose, or dropped
outright, so the user cannot tell their words from the agent's or find the answer at
all. So the thread vocabulary is validated rather than trusted, and --prev proves
coverage against the previous round. A thread that is finished must be retired
explicitly (data-retired="r1a,r1b" on the round wrapper) — never just omitted.
"""

import pathlib
import sys
from html.parser import HTMLParser

STATUSES = ("new", "changed", "settled", "open")


class _Scan(HTMLParser):
    """Flat list of start-tag attributes. Thread rows are siblings, so nesting is unneeded."""

    def __init__(self):
        super().__init__()
        self.tags = []

    def handle_starttag(self, tag, attrs):
        self.tags.append(dict(attrs))


def _rows(html):
    s = _Scan()
    s.feed(html)
    return s.tags


def _classes(attrs):
    return (attrs.get("class") or "").split()


def user_threads(html):
    """Thread ids the user's notes declare — the notes a round is answerable for."""
    return {
        a["data-thread"]
        for a in _rows(html)
        if "ev" in _classes(a) and "user" in _classes(a) and a.get("data-thread")
    }


def retired_ids(html):
    """Ids explicitly retired by this document, so dropping them is not silent."""
    ids = set()
    for a in _rows(html):
        ids |= {t.strip() for t in (a.get("data-retired") or "").split(",") if t.strip()}
    return ids


def validate(content):
    """Structural contract for the thread vocabulary. Returns a list of problems."""
    bad = []
    users, agents = {}, {}

    for a in _rows(content):
        status = a.get("data-status")
        if status and status not in STATUSES:
            bad.append(
                f"data-status={status!r} is not one of {list(STATUSES)} — it would render "
                f"unmarked, silently burying the content it was meant to surface"
            )

        classes = _classes(a)
        if "ev" not in classes:
            continue
        thread = a.get("data-thread")

        if "user" in classes:
            if not thread:
                bad.append("an .ev.user row carries no data-thread — the note cannot be paired with its answer")
                continue
            if thread in users:
                bad.append(f"data-thread={thread!r} is claimed by more than one .ev.user row, so the pairing is ambiguous")
            users[thread] = a
            if a.get("data-anchor") != thread:
                bad.append(
                    f'.ev.user data-thread={thread!r} must carry data-anchor="{thread}" '
                    f"so the note keeps its id in the next round"
                )
        elif "agent" in classes:
            if not thread:
                bad.append("an .ev.agent row carries no data-thread — it answers nothing")
                continue
            if thread in agents:
                bad.append(f"data-thread={thread!r} has more than one .ev.agent answer")
            agents[thread] = a

    for thread in users:
        if thread not in agents:
            bad.append(f"note {thread!r} has no .ev.agent answer")
    for thread in agents:
        if thread not in users:
            bad.append(f".ev.agent {thread!r} answers no .ev.user note")
    return bad


def parse_argv(argv):
    """-> (name, content_path, prev_path_or_None, error_or_None)."""
    if len(argv) < 3:
        return None, None, None, "usage: build-artifact.py <name> <content-file> [--prev <artifact.html>]"
    rest = argv[3:]
    if not rest:
        return argv[1], pathlib.Path(argv[2]), None, None
    if len(rest) == 2 and rest[0] == "--prev":
        return argv[1], pathlib.Path(argv[2]), pathlib.Path(rest[1]), None
    return None, None, None, "usage: build-artifact.py <name> <content-file> [--prev <artifact.html>]"


def main(argv):
    name, content_file, prev_file, error = parse_argv(argv)
    if error:
        print(error, file=sys.stderr)
        return 2

    content = content_file.read_text()
    problems = validate(content)

    # Coverage against the previous round: a note may be carried forward or explicitly
    # retired, but never quietly disappear.
    carried = set()
    if prev_file:
        was = user_threads(prev_file.read_text())
        now = user_threads(content) | retired_ids(content)
        carried = was & now
        problems += [
            f"note {thread!r} was in the previous round and is neither carried forward "
            f"nor listed in data-retired"
            for thread in sorted(was - now)
        ]

    if problems:
        for problem in problems:
            print(f"build-artifact: {problem}", file=sys.stderr)
        return 1

    skill_dir = pathlib.Path(__file__).resolve().parent.parent
    css = (skill_dir / "references" / "chrome.css").read_text()
    js = (skill_dir / "references" / "chrome.js").read_text()
    template = (skill_dir / "references" / "artifact-template.html").read_text()

    html = (
        template.replace("/*__CHROME_CSS__*/", css)
                .replace("//__CHROME_JS__", js)
                .replace("<title>artifact</title>", f"<title>{name}</title>")
                .replace('<h1 data-anchor="title">…</h1>', content)
    )

    out = pathlib.Path(".agents/artifacts") / f"{name}.html"
    out.parent.mkdir(parents=True, exist_ok=True)
    out.write_text(html)
    print(out)
    if prev_file:
        print(f"  {len(carried)} thread(s) carried forward, {len(user_threads(content))} in this round")
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv))
