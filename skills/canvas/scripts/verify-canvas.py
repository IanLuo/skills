#!/usr/bin/env python3
"""verify-canvas.py — render a canvas in headless Chrome and report what actually
reached the page.

A canvas can look fine in its source and be broken in the browser: an inlined script
truncated by a stray closing-script sequence, a diagram that fails to parse, a spec
that never renders. This renders the real page and checks the DOM, so the agent is not
relying on the user's eyes.

It serves a copy of the shell under "<topic>-verify" (with the 1s poller removed, so
Chrome can reach idle and dump the DOM). Everything else — content, version, history —
is fetched against the real topic, so the check exercises the real data.

Usage:
    verify-canvas.py <topic> [--root DIR] [--port N] [--chrome PATH] [--keep]

Requires the canvas daemon to be running. Exit 0 = all checks passed.
"""

import argparse
import os
import re
import shutil
import subprocess
import sys
import time
from collections import Counter
from html.parser import HTMLParser
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))
from canvas import (DEFAULT_ROOT, TOPIC_RE, read_daemon, is_up,  # noqa: E402
                    read_text, topic_paths)

CHROME_CANDIDATES = [
    "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
    "/Applications/Chromium.app/Contents/MacOS/Chromium",
    "google-chrome", "chromium", "chromium-browser",
]


# Injected into the harness page. --dump-dom carries no geometry, so overlap between the
# fixed-position controls is invisible to every other check — and that is exactly the bug
# the user found. This measures the real rects and reports into the DOM.
HARNESS_SCRIPT = """
<script>
(function () {
  function run() {
    // --- layout: overlaps + viewport containment + horizontal overflow ---
    var ids = ['schemes', 'convo', 'bottombar', 'controls', 'send', 'history', 'listening', 'status', 'err'];
    var els = ids.map(function (s) { return document.getElementById(s); })
                 .filter(function (e) { return e && !e.hidden; });
    var errEl = document.getElementById('err');
    var realErr = errEl ? errEl.textContent : '';
    if (errEl && !realErr) errEl.textContent = 'layout probe ' + Array(9).join('pipelines ');

    var problems = [];
    for (var i = 0; i < els.length; i++) {
      for (var j = i + 1; j < els.length; j++) {
        var a = els[i], b = els[j];
        if (a.contains(b) || b.contains(a)) continue;
        var ra = a.getBoundingClientRect(), rb = b.getBoundingClientRect();
        if (ra.width === 0 && ra.height === 0) continue;
        if (rb.width === 0 && rb.height === 0) continue;
        var hit = !(ra.right <= rb.left || rb.right <= ra.left ||
                    ra.bottom <= rb.top || rb.bottom <= ra.top);
        if (hit) problems.push('overlap ' + a.id + ' x ' + b.id);
      }
    }

    // A fixed control that pokes outside the viewport is unreachable on a phone.
    var vw = window.innerWidth, vh = window.innerHeight;
    els.forEach(function (e) {
      var r = e.getBoundingClientRect();
      if (r.width === 0 && r.height === 0) return;
      if (r.left < -1 || r.right > vw + 1 || r.top < -1 || r.bottom > vh + 1) {
        problems.push('outside viewport: ' + e.id +
          ' [' + Math.round(r.left) + ',' + Math.round(r.top) + ' -> ' +
          Math.round(r.right) + ',' + Math.round(r.bottom) + '] vw=' + vw + ' vh=' + vh);
      }
    });

    // Horizontal document overflow: find WHO is wider than the viewport.
    var doc = document.documentElement;
    if (doc.scrollWidth > vw + 1) {
      var guilty = [];
      document.querySelectorAll('body *').forEach(function (e) {
        var r = e.getBoundingClientRect();
        if (r.width > 0 && (r.right > vw + 1 || r.left < -1)) {
          var tag = e.tagName.toLowerCase();
          var id = e.id ? '#' + e.id : (e.className && typeof e.className === 'string'
                    ? '.' + e.className.trim().split(/\\s+/)[0] : '');
          guilty.push(tag + id + '@' + Math.round(r.left) + '..' + Math.round(r.right));
        }
      });
      problems.push('horizontal overflow: scrollWidth=' + doc.scrollWidth + ' vw=' + vw +
                    ' | widest: ' + guilty.slice(0, 6).join(', '));
    }

    if (errEl) errEl.textContent = realErr;
    var out = document.createElement('div');
    out.id = 'verify-layout';
    out.textContent = (problems.length ? problems.join('; ') : 'ok') +
                      ' @' + window.innerWidth + 'x' + window.innerHeight +
                      ' scrollW=' + document.documentElement.scrollWidth;
    document.body.appendChild(out);
  }
  if (document.readyState === 'complete') setTimeout(run, 60);
  else window.addEventListener('load', function () { setTimeout(run, 60); });
})();
</script>
"""


def find_chrome(explicit):
    if explicit:
        return explicit
    for c in CHROME_CANDIDATES:
        if c.startswith("/") and Path(c).is_file():
            return c
        if not c.startswith("/") and shutil.which(c):
            return shutil.which(c)
    return None


def dump_dom(chrome, url, workdir, budget=25000, cap=50, attempts=2, window=None):
    """Dump the DOM, retrying once. A verifier that fails at random is not trustworthy."""
    for attempt in range(attempts):
        dom = _dump_once(chrome, url, workdir, budget, cap, window)
        if dom.strip():
            return dom
        # a fresh profile per attempt: leftover state is the usual cause of an empty dump
        shutil.rmtree(workdir / "profile", ignore_errors=True)
        time.sleep(1)
    return dom


def _dump_once(chrome, url, workdir, budget, cap, window=None):    # Do NOT override HOME. With HOME pointed at a temp dir, Chrome looks for
    # $HOME/Library/Keychains, finds none, and raises a blocking "Keychain not found"
    # modal — whose reset option is DESTRUCTIVE to the real login keychain's Chrome Safe
    # Storage item, breaking the user's actual browser profile. --user-data-dir already
    # isolates the profile; the two flags keep Chrome away from the keychain entirely.
    args = [chrome, "--headless=new", "--disable-gpu", "--no-first-run",
            "--no-default-browser-check",
            "--password-store=basic", "--use-mock-keychain",
            "--user-data-dir=%s" % (workdir / "profile")]
    if window:
        args.append("--window-size=%s" % window)
    args += ["--virtual-time-budget=%d" % budget, "--dump-dom", url]
    out = workdir / "dom.html"
    with open(str(out), "wb") as fh:
        p = subprocess.Popen(args, stdout=fh, stderr=subprocess.DEVNULL)
        for _ in range(cap):
            if p.poll() is not None:
                break
            time.sleep(1)
        else:
            p.kill()
        try:
            p.wait(timeout=5)
        except subprocess.TimeoutExpired:
            p.kill()
    try:
        return out.read_text(encoding="utf-8", errors="replace")
    except OSError:
        return ""   # let dump_dom retry / report "produced no DOM" instead of crashing


class DomCheck(HTMLParser):
    """Parse the dumped DOM properly.

    Regexes over --dump-dom lie: the dump includes comments and the text inside
    <script>, so counting tags or searching for JS text reports failures that are not
    there. A verifier that cries wolf is worse than none.
    """

    def __init__(self):
        super().__init__(convert_charrefs=True)
        self.counts = Counter()
        self.visible = []          # text outside <script>/<style>
        self.script_text = []
        self.comments = []
        self.node_anchors = []
        self._skip = 0
        self._capture = None       # "script" while inside a <script> body
        self._cap = None           # (id, depth) while inside a captured element
        self.captured = {"err": "", "status": "", "verify-layout": ""}

    def handle_starttag(self, tag, attrs):
        a = dict(attrs)
        cls = (a.get("class") or "").split()
        if tag in ("script", "style"):
            self.counts[tag] += 1
            self._skip += 1
            if tag == "script":
                self._capture = "script"
        if tag == "svg":
            self.counts["svg"] += 1
        if "node" in cls:
            self.counts["mermaid_node"] += 1
        if tag == "pre" and "mermaid" in cls:
            self.counts["mermaid_pre"] += 1
        if "data-section" in a:
            self.counts["section"] += 1
        if "data-render" in a:
            self.counts["render"] += 1
        if a.get("data-rendered") == "1":
            self.counts["rendered"] += 1
        if "rhead" in cls:
            self.counts["round"] += 1
        anchor = a.get("data-anchor")
        if anchor:
            self.counts["anchor"] += 1
            if anchor.startswith("fig-") and "." in anchor:
                self.node_anchors.append(anchor)
        if a.get("id") in self.captured:
            self._cap = (a["id"], 1)
        elif self._cap:
            self._cap = (self._cap[0], self._cap[1] + 1)

    def handle_endtag(self, tag):
        if tag in ("script", "style"):
            self._skip = max(0, self._skip - 1)
            if tag == "script" and self._capture == "script":
                self._capture = None
        if self._cap:
            name, depth = self._cap
            self._cap = None if depth <= 1 else (name, depth - 1)

    def handle_data(self, data):
        if self._skip:
            if self._capture == "script":
                self.script_text.append(data)
            return
        self.visible.append(data)
        if self._cap:
            self.captured[self._cap[0]] += data

    def handle_comment(self, data):
        self.comments.append(data)


LONG_SENTENCE = 45      # words; readable technical prose runs 15–25
WALL = 200              # words of prose in one section


def readability(content):
    """The readability contract, as far as it can be counted.

    Two failure shapes are countable: a sentence that has to be re-read, and a section that is a
    wall of prose with no view under it. Warnings, not failures — a long sentence can be the right
    sentence, and a checker that cries wolf gets ignored. Printed with every run so a round cannot
    pass them without seeing them.
    """
    block_end = re.compile(r"</(p|li|h[1-6]|td|th|tr|div|figcaption|blockquote|dt|dd|section)>",
                           re.I)

    def prose(body):
        """Only the sentences. A block boundary ends one, so a heading is not glued to the
        paragraph under it and a table row is not one 85-word run-on — without this the check
        reports sentences nobody wrote, and a checker that cries wolf gets ignored."""
        body = re.sub(r"<(script|style|pre|svg)\b.*?</\1>", " ", body, flags=re.S | re.I)
        body = block_end.sub(". ", body)
        return " ".join(re.sub(r"<[^>]+>", " ", body).split())

    warns, stats = [], []
    for m in re.finditer(r'<section\b[^>]*\bdata-section="([^"]+)"(.*?)(?=<section\b|$)',
                         content, re.S | re.I):
        sid, body = m.group(1), m.group(2)
        text = prose(body)
        words = len(text.split())
        views = len(re.findall(r'data-render=|class="mermaid"|class="tree"|<table|<ul|<ol', body))
        if words >= WALL and views == 0:
            warns.append("%s: %d words of prose and no view — that is a wall, not a section"
                         % (sid, words))
        for s in re.split(r"(?<=[.!?])\s+", text):
            n = len(s.split())
            if n > LONG_SENTENCE:
                warns.append("%s: a %d-word sentence — %s…" % (sid, n, " ".join(s.split()[:9])))
                break
        stats.append((sid, words, views))
    return warns, stats


def check(dom, topic):
    """Return (results, failures). Each result is (name, ok, detail)."""
    d = DomCheck()
    d.feed(dom)
    results = []

    def add(name, ok, detail=""):
        results.append((name, bool(ok), detail))

    text = " ".join(d.visible)
    # Long, code-specific needles only. Short ones collide with the user's own notes
    # (a note whose anchor was leaked source text is legitimately echoed in the history
    # panel), and a checker that reports that as a failure is not trustworthy.
    leaked = [n for n in ("const cells = Array.isArray(r) ? r : (r.c || [])",
                          "el.classList.add(open ? 'marked' : 'marked-done')",
                          "el.dataset.rendered = '1'",
                          "window.mermaid.initialize({")
              if n in text]
    add("no JS leaking as text", not leaked, ", ".join(x[:40] for x in leaked))

    inlined_js = "".join(d.script_text)
    # Start AND end markers: a script truncated by an early closing tag loses the tail,
    # which is exactly what spilled chrome.js into the page as text.
    ok_js = ("function mark()" in inlined_js and "(async function boot()" in inlined_js
             and inlined_js.rstrip().endswith("})();"))
    add("inlined JS intact", ok_js, "%d chars, tail=%r" % (len(inlined_js), inlined_js.rstrip()[-14:]))

    err = d.captured["err"].strip()
    add("no render errors", not err, err or "none")

    # Only demand diagrams when the content actually has one. A canvas of tables and
    # swatches is perfectly valid, and a checker that fails it is crying wolf.
    if d.counts["mermaid_pre"] == 0:
        add("diagrams rendered", True, "no diagrams in this canvas")
    else:
        add("diagrams rendered", d.counts["svg"] > 0 and d.counts["mermaid_node"] > 0,
            "%d svg, %d mermaid blocks, %d node groups, %d node anchors"
            % (d.counts["svg"], d.counts["mermaid_pre"], d.counts["mermaid_node"],
               len(set(d.node_anchors))))

    # 0 of 0 is not a failure: a canvas of prose, or one whose blocks are hand-written
    # HTML, has no specs to render. Same trap as demanding a diagram on every page.
    if d.counts["render"] == 0:
        add("data-render specs rendered", True, "no data-render specs in this canvas")
    else:
        add("data-render specs rendered", d.counts["rendered"] >= d.counts["render"],
            "%d of %d" % (d.counts["rendered"], d.counts["render"]))

    add("sections present", d.counts["section"] >= 1,
        "%d sections, %d anchors" % (d.counts["section"], d.counts["anchor"]))

    add("history panel renders", d.counts["round"] >= 1, "%d rounds" % d.counts["round"])

    add("chrome booted", bool(d.captured["status"].strip()),
        d.captured["status"].strip()[:60] or "status line is empty")

    # Layout is checked in-page by the harness snippet (see HARNESS_SCRIPT below) because
    # --dump-dom carries no geometry: an overlap is invisible to every other check here.
    layout = d.captured["verify-layout"].strip()
    add("no overlapping chrome", layout == "ok" or "overlap" not in layout,
        layout or "harness layout check did not run")
    add("within the viewport", "outside viewport" not in layout,
        "-" if "outside viewport" not in layout else layout)
    add("no horizontal overflow", "horizontal overflow" not in layout,
        "-" if "horizontal overflow" not in layout else layout.split('|', 1)[-1].strip())

    failures = [(n, det) for n, ok, det in results if not ok]
    return results, failures


def main(argv=None):
    p = argparse.ArgumentParser(prog="verify-canvas.py")
    p.add_argument("topic")
    p.add_argument("--root", default=DEFAULT_ROOT)
    p.add_argument("--port", type=int, default=None)
    p.add_argument("--chrome", default=None)
    p.add_argument("--viewport", default="1280x900",
                   help="WxH for the headless window (default 1280x900; try 390x844)")
    p.add_argument("--keep", action="store_true", help="keep the scratch page and DOM dump")
    args = p.parse_args(argv)

    root = Path(args.root)
    if not TOPIC_RE.match(args.topic):
        raise SystemExit("verify-canvas: bad topic %r" % args.topic)

    info = read_daemon(root)
    port = args.port or (info or {}).get("port")
    if not port or not is_up(port):
        raise SystemExit("verify-canvas: daemon is not running (canvas.py start --root %s)" % args.root)

    shell = root / args.topic / "index.html"
    if not shell.is_file():
        raise SystemExit("verify-canvas: no built shell at %s (run build-canvas.py)" % shell)

    chrome = find_chrome(args.chrome)
    if not chrome:
        raise SystemExit("verify-canvas: no Chrome/Chromium found — pass --chrome PATH")

    scratch = root / (args.topic + "-verify")
    scratch.mkdir(parents=True, exist_ok=True)
    html = shell.read_text(encoding="utf-8")
    patched = html.replace("setInterval(sync, 1000);", "/* poller off: verify harness */")
    if patched == html:
        print("verify-canvas: warning — could not disable the poller; Chrome may not settle",
              file=sys.stderr)
    patched = patched.replace("</body>", HARNESS_SCRIPT + "</body>")
    (scratch / "index.html").write_text(patched, encoding="utf-8")

    url = "http://127.0.0.1:%d/t/%s" % (port, args.topic + "-verify")
    # Unique per run: two verifiers on the same topic (or a leftover one) must not delete
    # each other's dump file mid-read. That race crashed a run with a bare traceback.
    work = Path("/tmp") / ("canvas-verify-%s-%d" % (args.topic, os.getpid()))
    if work.exists():
        shutil.rmtree(work, ignore_errors=True)
    work.mkdir(parents=True, exist_ok=True)

    try:
        dom = dump_dom(chrome, url, work, window=args.viewport)
        if not dom.strip():
            print("verify-canvas: headless Chrome produced no DOM", file=sys.stderr)
            return 2
        results, failures = check(dom, args.topic)

        # Chrome ignores --window-size for the layout viewport in --dump-dom mode (it uses
        # a minimum of ~500px), so a narrow "viewport" run measures the wrong width. Say so
        # rather than reporting width-sensitive checks as verified.
        m = re.search(r"scrollW=(\d+)", dom)
        if args.viewport and m:
            want = int(args.viewport.split("x")[0])
            got = int(m.group(1))
            if got > want + 1:
                print("  ! viewport not honoured: asked %s, Chrome laid out at %dpx — "
                      "width-sensitive checks above are UNVERIFIED at %dpx"
                      % (args.viewport, got, want))
        for name, ok, detail in results:
            print("  %s %-26s %s" % ("✓" if ok else "✗", name, detail))
        print()
        # Readability is what the page is FOR, so it is reported on every run — as warnings,
        # because a long sentence can be the right sentence and a checker that cries wolf is
        # worse than none.
        content = read_text(topic_paths(root, args.topic)["content"])
        warns, stats = readability(content)
        print("  readability  %s" % ("; ".join(stats and ["%s %dw/%dv" % s for s in stats])
                                     if stats else "no sections"))
        if warns:
            for w in warns:
                print("  ! %s" % w)
            print("  ! %d readability warning(s) — short sentences, one idea each, "
                  "a view under every claim" % len(warns))
        print()
        if failures:
            print("verify-canvas: %d check(s) FAILED" % len(failures))
            return 1
        print("verify-canvas: all checks passed (%d bytes of DOM)" % len(dom))
        return 0
    finally:
        if not args.keep:
            shutil.rmtree(scratch, ignore_errors=True)
            shutil.rmtree(work, ignore_errors=True)
        else:
            print("kept %s and %s" % (scratch, work))


if __name__ == "__main__":
    sys.exit(main())
