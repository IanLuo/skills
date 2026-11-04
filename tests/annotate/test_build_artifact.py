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
 13. an id on a container fails, naming the anchor and what to do instead
 14. the same content anchored per unit builds
 15. cells are not units — a table row anchor is a unit, not a container
 16. omitted end tags (<li>a<li>b) must not hide a container
 17. content that copies the template's chrome fails, naming the ids
 18. content that copies a chrome placeholder fails, naming the placeholder

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
CHROME_JS = REPO / "skills/annotate/references/chrome.js"
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

RUNG_OK = TITLE + """
<section class="rung" data-rung="r3-scope">
  <h3 data-anchor="r3-scope">R3 · Scope in / out</h3>
  <div class="rung-body">
    <p data-anchor="r3-scope-q">V1 ships X. Not Y.</p>
  </div>
  <div class="answers" data-for="r3-scope">
    <button data-choice="accept">Accept →</button>
    <button data-choice="B">B · narrower V1</button>
    <button data-choice="other">Other…</button>
  </div>
</section>
"""

PLAIN = TITLE + '<p data-anchor="body">A plain answer with no thread rows.</p>\n'


def main():
    with tempfile.TemporaryDirectory() as td:
        cwd = pathlib.Path(td)

        # 1. chrome inlined, placeholders gone.
        r = build(cwd, "basic", PLAIN)
        out = cwd / "basic.html"
        check("build exits 0", r.returncode == 0, r.stderr.strip())
        check("artifact written beside content", out.is_file())
        check("legacy project artifact path unused", not (cwd / ".agents/artifacts/basic.html").exists())
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
                      '[data-status="settled"]', ".answers", "#rail", ".rung.done"):
            check(f"chrome.css has {token}", token in css)

        # 12. the template documents the primitives for the writer.
        tpl = TEMPLATE.read_text()
        check("template documents data-thread", "data-thread" in tpl)
        check("template documents data-status", "data-status" in tpl)
        check("template documents data-rung", "data-rung" in tpl)
        check("template carries the rail", 'id="rail"' in tpl)

        # 19b. The page is re-read while the agent applies the round, so Done must
        #      copy without closing it. A runtime-only rule, so it is checked at the
        #      source: no window.close call survives in the chrome.
        check("chrome never closes the page", "window.close" not in CHROME_JS.read_text())

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

        # 13. An id on a container is a build error: chrome.js targets with
        #     closest('[data-anchor]'), so the whole container becomes one click target
        #     and the reader cannot point at the line they mean.
        container = TITLE + """
<section data-anchor="whole">
  <h2>Heading</h2>
  <p>First.</p>
  <p>Second.</p>
</section>
"""
        r = build(cwd, "container", container)
        check("container anchor fails", r.returncode != 0, "expected non-zero")
        check("container failure names the anchor", "whole" in r.stderr, r.stderr)
        check("container failure says what to do", "units inside it" in r.stderr, r.stderr)

        # 14. the same content anchored per unit builds.
        units = TITLE + """
<section>
  <h2 data-anchor="heading">Heading</h2>
  <p data-anchor="first">First.</p>
  <p data-anchor="second">Second.</p>
</section>
"""
        r = build(cwd, "units", units)
        check("per-unit anchors build", r.returncode == 0, r.stderr.strip())

        # 15. cells are not units — the row is the unit, so a row anchor is not a container.
        rows = TITLE + """
<table><tbody>
  <tr data-anchor="row-1"><td>a</td><td>b</td><td>c</td></tr>
  <tr data-anchor="row-2"><td>d</td><td>e</td><td>f</td></tr>
</tbody></table>
"""
        r = build(cwd, "rows", rows)
        check("table row anchors build", r.returncode == 0, r.stderr.strip())

        # 16. end tags omitted (<li>a<li>b) must not hide a container.
        loose = TITLE + '<ul data-anchor="list"><li>a<li>b<li>c</ul>\n'
        r = build(cwd, "loose", loose)
        check("unclosed <li> still counts as a container", r.returncode != 0, "expected non-zero")

        # 17. The content file must not copy the template's chrome: the page would
        #     carry two #done buttons, and chrome.js binds to the first while the
        #     reader clicks the second.
        pasted = TITLE + """
<button id="done">Done · <span id="count">0</span></button>
<div id="status"></div>
<div id="form" hidden>
  <div id="list"></div><textarea id="comment"></textarea>
  <div id="levels"></div>
  <div class="actions"><button id="add">Add</button><button id="cancel">Cancel</button></div>
</div>
"""
        r = build(cwd, "copied", pasted)
        check("copied chrome fails", r.returncode != 0, "expected non-zero")
        check("copied chrome names #done", "two #done buttons" in r.stderr, r.stderr)
        check("copied chrome says what to do", "body content only" in r.stderr, r.stderr)

        # 19. Output follows the content file into an external temp/session directory,
        #     not the working project directory.
        with tempfile.TemporaryDirectory() as external_dir:
            inputs = pathlib.Path(external_dir)
            (inputs / "content.html").write_text(PLAIN)
            r = subprocess.run(
                [sys.executable, str(BUILD), "external", str(inputs / "content.html")],
                cwd=cwd, capture_output=True, text=True,
            )
            check("external content builds", r.returncode == 0, r.stderr.strip())
            check("artifact follows external content path", (inputs / "external.html").is_file())
            check("external build leaves no project artifact", not (cwd / ".agents/artifacts/external.html").exists())

        # 18. A pasted chrome placeholder is filled before the content is inserted, so
        #     it survives as literal text — the same copy mistake, caught earlier.
        r = build(cwd, "placeholder", TITLE + "<script>//__CHROME_JS__</script>\n")
        check("copied placeholder fails", r.returncode != 0, "expected non-zero")
        check("copied placeholder is named", "__CHROME_JS__" in r.stderr, r.stderr)

        # 20. A well-formed rung builds — controls are not mistaken for a container anchor.
        r = build(cwd, "rungs", RUNG_OK)
        check("rung with answer controls builds", r.returncode == 0, r.stderr.strip())
        if r.returncode == 0:
            html = (cwd / "rungs.html").read_text()
            check("built page carries the rail", 'id="rail-count"' in html)
            check("built page carries the choice verb formatter", "option ' + a.choice" in html)

        # 21. A group that points at no anchor would record a click against nothing.
        r = build(cwd, "dangling", RUNG_OK.replace('data-for="r3-scope"', 'data-for="r3-scop"'))
        check("dangling answer group fails", r.returncode != 0, "expected non-zero")
        check("dangling group names its target", "r3-scop" in r.stderr, r.stderr)

        # 22. Two groups for one rung make the recorded choice ambiguous.
        twice = RUNG_OK.replace(
            'data-choice="other">Other…</button>',
            'data-choice="other">Other…</button></div>'
            '<div class="answers" data-for="r3-scope">'
            '<button data-choice="accept">Accept →</button>')
        r = build(cwd, "twogroups", twice)
        check("duplicate answer group fails", r.returncode != 0, "expected non-zero")

        # 23. A rung wrapper naming no anchor never paints its answered state.
        r = build(cwd, "danglung", RUNG_OK.replace('data-rung="r3-scope"', 'data-rung="r3"'))
        check("dangling data-rung fails", r.returncode != 0, "expected non-zero")
        check("dangling rung names it", "r3" in r.stderr, r.stderr)

    print(f"\n  {PASS} passed, {len(FAILS)} failed")
    return 1 if FAILS else 0


if __name__ == "__main__":
    sys.exit(main())
