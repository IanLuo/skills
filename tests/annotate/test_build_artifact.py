#!/usr/bin/env python3
"""
test_build_artifact.py — behaviour tests for annotate's build-artifact.py.

The artifact is the round loop's page. A round answers the previous round's notes, so the
page has to carry two authored voices (the user's note, the agent's answer) and say what is
fresh. Without a contract the agent paraphrases the note into prose and the answer becomes
unfindable — which is the bug these tests exist to prevent.

Rules under test:

  1. chrome is inlined; both placeholders are gone
  2. `.ev.user` must carry data-thread (the pairing key)
  3. `.ev.user` must carry data-anchor == data-thread (note ids survive rounds)
  4. every user thread must have exactly one `.ev.agent[data-thread]` answering it
  5. duplicate thread ids fail (ambiguous pairing)
  6. an unknown data-status value fails (it would render silently unstyled)
  7. --prev: a thread that vanishes without being retired fails, and is named
  8. --prev: an explicitly retired thread passes
  9. --prev: a carried-forward thread passes
 10. a content file with no `.ev` rows still builds — plain artifacts are unaffected
 11. chrome.css defines the borrowed vocabulary (canvas selector names, not new ones)
 12. artifact-template.html documents the primitives

Usage: python3 tests/annotate/test_build_artifact.py     (run from anywhere)
Exit: 0 all pass, 1 any fail. Prints one line per failure, prefixed FAIL.
"""

import pathlib
import subprocess
import sys
import tempfile
from html.parser import HTMLParser

REPO = pathlib.Path(__file__).resolve().parents[2]
BUILD = REPO / "skills/annotate/scripts/build-artifact.py"
CHROME_CSS = REPO / "skills/annotate/references/chrome.css"
TEMPLATE = REPO / "skills/annotate/references/artifact-template.html"

PASS, FAILS = 0, []


def check(name, cond, detail=""):
    global PASS
    if cond:
        PASS += 1
        print(f"  ok   {name}")
    else:
        FAILS.append(name)
        print(f"FAIL {name}: {detail}")


class _Scan(HTMLParser):
    """Flat list of (tag, attrs) — the thread rows are siblings, so nesting is unneeded."""

    def __init__(self):
        super().__init__()
        self.tags = []

    def handle_starttag(self, tag, attrs):
        self.tags.append((tag, dict(attrs)))


def scan(html):
    s = _Scan()
    s.feed(html)
    return s.tags


def has_class(attrs, name):
    return name in (attrs.get("class") or "").split()


def build(cwd, name, content, extra=()):
    (cwd / "content.html").write_text(content)
    return subprocess.run(
        [sys.executable, str(BUILD), name, "content.html", *extra],
        cwd=cwd, capture_output=True, text=True,
    )


TITLE = '<h1 data-anchor="title">Round 2</h1>\n'

THREAD_OK = TITLE + """
<section class="round" data-round="2">
  <h2 class="rhead">Round 2 <span class="secpill new">1 new</span></h2>
  <div class="ev user" data-thread="r1e" data-anchor="r1e">
    <span class="who">you</span><div class="txt">edit or create?</div></div>
  <div class="ev agent" data-thread="r1e" data-anchor="r1e-answer" data-status="new">
    <span class="who">me</span><div class="txt">Both — the lease is on the topic.</div></div>
</section>
"""

PLAIN = TITLE + '<p data-anchor="body">A plain answer with no thread rows.</p>\n'


def main():
    with tempfile.TemporaryDirectory() as td:
        cwd = pathlib.Path(td)

        # 1. chrome inlined, placeholders gone.
        r = build(cwd, "basic", PLAIN)
        out = cwd / ".agents/artifacts/basic.html"
        check("build exits 0", r.returncode == 0, r.stderr.strip())
        check("artifact written", out.is_file())
        if out.is_file():
            html = out.read_text()
            check("placeholders replaced",
                  "/*__CHROME_CSS__*/" not in html and "//__CHROME_JS__" not in html)
            check("content inlined", "A plain answer with no thread rows." in html)
            check("title set", "<title>basic</title>" in html)

        # 11. chrome defines the borrowed vocabulary.
        css = CHROME_CSS.read_text()
        for token in (".ev.user", ".ev.agent", ".ev .who", ".ev .txt", ".secpill",
                      '[data-status="new"]', '[data-status="changed"]',
                      '[data-status="settled"]'):
            check(f"chrome.css has {token}", token in css)

        # 12. the template documents the primitives for the writer.
        tpl = TEMPLATE.read_text()
        check("template documents data-thread", "data-thread" in tpl)
        check("template documents data-status", "data-status" in tpl)

        # 10. plain content (no .ev) still builds.
        check("plain artifact unaffected", r.returncode == 0)

        # 2/3/4. a well-formed thread builds clean.
        r = build(cwd, "threaded", THREAD_OK)
        check("threaded build exits 0", r.returncode == 0, r.stderr.strip())

        # 2. `.ev.user` without data-thread.
        r = build(cwd, "nothread", THREAD_OK.replace(
            'data-thread="r1e" data-anchor="r1e"', 'data-anchor="r1e"'))
        check("unthreaded user row fails", r.returncode != 0, "expected non-zero")
        check("unthreaded failure names the rule", "data-thread" in r.stderr, r.stderr)

        # 3. anchor must equal the thread id.
        r = build(cwd, "mismatch", THREAD_OK.replace(
            'data-thread="r1e" data-anchor="r1e"', 'data-thread="r1e" data-anchor="other"'))
        check("anchor/thread mismatch fails", r.returncode != 0, "expected non-zero")

        # 4. an unanswered thread.
        r = build(cwd, "unanswered", THREAD_OK.replace('class="ev agent"', 'class="ev agentX"'))
        check("unanswered thread fails", r.returncode != 0, "expected non-zero")
        check("unanswered failure names the thread", "r1e" in r.stderr, r.stderr)

        # 5. duplicate thread ids.
        dup = THREAD_OK + """
<div class="ev user" data-thread="r1e" data-anchor="r1e">
  <span class="who">you</span><div class="txt">again</div></div>
"""
        r = build(cwd, "dup", dup)
        check("duplicate thread id fails", r.returncode != 0, "expected non-zero")

        # 6. unknown data-status.
        r = build(cwd, "badstatus", THREAD_OK.replace('data-status="new"', 'data-status="fressh"'))
        check("unknown data-status fails", r.returncode != 0, "expected non-zero")
        check("bad-status failure names the value", "fressh" in r.stderr, r.stderr)

        # 7. --prev: a thread vanishes without being retired.
        prev = cwd / "prev.html"
        two_threads = THREAD_OK + """
<div class="ev user" data-thread="r1f" data-anchor="r1f">
  <span class="who">you</span><div class="txt">and this?</div></div>
<div class="ev agent" data-thread="r1f" data-anchor="r1f-answer">
  <span class="who">me</span><div class="txt">Answered.</div></div>
"""
        prev.write_text(two_threads)
        r = build(cwd, "dropped", THREAD_OK, extra=["--prev", str(prev)])
        check("dropped thread fails", r.returncode != 0, "expected non-zero")
        check("dropped failure names the thread", "r1f" in r.stderr, r.stderr)

        # 8. --prev: explicitly retired passes.
        retired = THREAD_OK.replace('<section class="round"', '<section class="round" data-retired="r1f"')
        r = build(cwd, "retired", retired, extra=["--prev", str(prev)])
        check("retired thread passes", r.returncode == 0, r.stderr.strip())

        # 9. --prev: carried forward passes.
        r = build(cwd, "carried", two_threads, extra=["--prev", str(prev)])
        check("carried thread passes", r.returncode == 0, r.stderr.strip())

    print(f"\n  {PASS} passed, {len(FAILS)} failed")
    return 1 if FAILS else 0


if __name__ == "__main__":
    sys.exit(main())
