// canvas chrome — shared annotate/edit surface for a live canvas.
//
// Two modes, same page:
//   live   (served by canvas.py over http://127.0.0.1) — polls /v for a content
//          change, hot-swaps ONLY the sections whose hash changed, and POSTs the
//          user's annotations to /a so the agent can read them from disk.
//   static (opened from file://) — no fetch, no polling; annotations live in
//          localStorage and the Done button copies them for pasting into chat.
//
// Keep verbatim. Only the CONTENT (references/canvas-template.html body) is the
// agent's to write.

const CFG = Object.assign({ topic: 'canvas', live: false }, window.CANVAS || {});
const TOPIC = CFG.topic;
const LIVE = !!CFG.live && location.protocol.indexOf('http') === 0;
const $ = (id) => document.getElementById(id);
const CSS_ESCAPE = (s) => (window.CSS && CSS.escape ? CSS.escape(s) : s.replace(/"/g, '\\"'));

let ANNOTATIONS = [];
let target = null;            // {el, id, snippet}
let severity = 'suggestion';
let editing = null;           // annotation id being edited, or null
let lastV = null;
let mermaidOn = false;

const status = (msg) => { $('status').textContent = msg; };
// Errors get their own line. Sharing one with the 1s indicator meant every failure was
// overwritten within a second — which is how a broken diagram stayed invisible.
const fail = (msg) => { $('err').textContent = String(msg).slice(0, 200); };
const clearFail = () => { $('err').textContent = ''; };

// ── annotations: load / save ───────────────────────────────────────────────

const localKey = 'canvas:' + TOPIC;

function saveLocal() {
  try { localStorage.setItem(localKey, JSON.stringify(ANNOTATIONS)); } catch (e) { /* file:// may refuse */ }
}

function loadLocal() {
  try { return JSON.parse(localStorage.getItem(localKey) || '[]'); } catch (e) { return []; }
}

async function restore() {
  if (!LIVE) { ANNOTATIONS = loadLocal(); return; }
  try {
    const r = await fetch('/a/' + TOPIC, { cache: 'no-store' });
    if (r.ok) { ANNOTATIONS = (await r.json()).annotations || []; return; }
  } catch (e) { /* fall through */ }
  ANNOTATIONS = loadLocal();
}

async function persist(ann, method) {
  if (!LIVE) { saveLocal(); return; }
  try {
    const url = '/a/' + TOPIC + (method === 'DELETE' ? '?id=' + encodeURIComponent(ann.id) : '');
    const r = await fetch(url, {
      method,
      headers: { 'Content-Type': 'application/json' },
      body: method === 'DELETE' ? undefined : JSON.stringify(ann),
    });
    if (r.ok) { ANNOTATIONS = (await r.json()).annotations || ANNOTATIONS; }
  } catch (e) {
    status('out of sync with the daemon — kept this one locally');
    saveLocal();
  }
  loadHistory();   // keep the record current as notes are written
}

// ── annotations: render marks ──────────────────────────────────────────────

function snippetOf(el) {
  const clone = el.cloneNode(true);
  // Strip chrome and diagram internals before reading text. mermaid injects its own <style>
  // element INSIDE the rendered svg, so a note on a diagram box used to capture
  // "#mermaid-1234{font-family:…}" instead of the box's label; <defs> and <title> are the same
  // kind of noise.
  clone.querySelectorAll('.badge, style, defs, title, script').forEach((n) => n.remove());
  return clone.textContent.trim().replace(/\s+/g, ' ').slice(0, 80);
}

function mark() {
  document.querySelectorAll('[data-anchor]').forEach((el) => {
    el.classList.remove('marked', 'marked-done');
    el.querySelectorAll(':scope > .badge').forEach((b) => b.remove());
    const mine = ANNOTATIONS.filter((a) => a.anchor === el.dataset.anchor);
    if (mine.length === 0) return;
    const open = mine.filter((a) => !a.resolved).length;
    el.classList.add(open ? 'marked' : 'marked-done');
    // Never inject an HTML badge where the element's text is SOURCE for something else:
    // inside <pre> it corrupts mermaid/diagram source (an annotated diagram then fails to
    // parse), and inside an <svg> it is invalid. Marking a diagram is still visible via
    // the class alone.
    if (el.ownerSVGElement || el.tagName === 'PRE') return;
    const b = document.createElement('span');
    b.className = 'badge';
    b.textContent = String(mine.length);
    el.appendChild(b);
  });
  renderAnswers(document);
  const pending = ANNOTATIONS.filter((a) => !a.resolved).length;
  $('send').textContent = pending ? 'Send · ' + pending : 'Send';
}

// ── annotation dialog ──────────────────────────────────────────────────────

document.addEventListener('click', (e) => {
  if (e.target.closest('#form')) return;
  const el = e.target.closest('[data-anchor]');
  if (!el) return;
  e.preventDefault();
  e.stopPropagation();
  target = { el, id: el.dataset.anchor, snippet: snippetOf(el) };
  showForm(el);
});

function showForm(el) {
  // Centred by CSS, deliberately: the old code chased the clicked element around the
  // viewport and ended up cramped in a corner on long pages.
  $('form').hidden = false;
  resetInput();
  renderList();
  $('comment').focus();
}

function resetInput() {
  $('comment').value = '';
  editing = null;
  $('add').textContent = 'Add';
}

document.querySelectorAll('#levels .lvl').forEach((b) => {
  b.onclick = () => {
    severity = b.dataset.sev;
    document.querySelectorAll('#levels .lvl').forEach((x) => x.classList.toggle('active', x === b));
  };
});

function escapeHtml(s) {
  return s.replace(/[&<>"']/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]));
}

function rowHtml(a) {
  return '<div class="item' + (a.resolved ? ' resolved' : '') + '">' +
    '<div class="row"><span class="dot" data-sev="' + a.severity + '"></span>' +
    '<span class="meta">' + escapeHtml(a.comment) + '</span>' +
    '<button data-act="edit" data-i="' + a.id + '">edit</button>' +
    '<button data-act="del" data-i="' + a.id + '">del</button></div></div>';
}

function renderList() {
  const list = $('list');
  const mine = ANNOTATIONS.filter((a) => a.anchor === target.id);
  if (mine.length === 0) { list.classList.remove('show'); list.innerHTML = ''; return; }
  list.classList.add('show');
  const open = mine.filter((a) => !a.resolved);
  const done = mine.filter((a) => a.resolved);
  // Finished notes are the record, not the work — folded away unless asked for.
  list.innerHTML = open.map(rowHtml).join('') +
    (done.length
      ? '<details><summary>' + done.length + ' finished</summary>' + done.map(rowHtml).join('') + '</details>'
      : '');
}

$('list').addEventListener('click', (e) => {
  const btn = e.target.closest('button[data-act]');
  if (!btn || !target) return;
  const a = ANNOTATIONS.find((x) => x.id === btn.dataset.i);
  if (!a) return;
  if (btn.dataset.act === 'edit') {
    editing = a.id;
    $('comment').value = a.comment;
    severity = a.severity;
    document.querySelectorAll('#levels .lvl').forEach((x) => x.classList.toggle('active', x.dataset.sev === severity));
    $('add').textContent = 'Save';
    $('comment').focus();
  } else {
    ANNOTATIONS = ANNOTATIONS.filter((x) => x.id !== a.id);
    persist(a, 'DELETE');
    mark();
    renderList();
  }
});

function closeDialog() { $('form').hidden = true; target = null; editing = null; }

function addAnnotation() {
  const c = $('comment').value.trim();
  if (!target || !c) return;
  if (editing) {
    const a = ANNOTATIONS.find((x) => x.id === editing);
    if (a) { a.comment = c; a.severity = severity; persist(a, 'POST'); }
  } else {
    const a = { id: '', anchor: target.id, snippet: target.snippet, comment: c, severity, resolved: false };
    ANNOTATIONS.push(a);
    persist(a, 'POST');
  }
  mark();
  resetInput();
  closeDialog();   // one note per click: the dialog has done its job and gets out of the way
}

$('cancel').onclick = closeDialog;
$('add').onclick = addAnnotation;

// ── the composer was removed — design-system.md rev 6 ──────────────────────
// A canvas is read-and-point: a requirement becomes a note on the element it concerns,
// which is already the mechanism for every other note. The free-text box was the one
// affordance that made this page feel like a chat client — and so like /annotate.


// Escape closes the innermost open surface, and is NEVER allowed to discard typed text.
// The annotation dialog wins when it is open, because closing it drops the anchor the note
// is attached to, so it refuses while the comment has text. The panel only hides, so its
// draft (and the whole conversation) survives either way.
document.addEventListener('keydown', (e) => {
  if (e.key !== 'Escape') return;
  if (!$('form').hidden) {
    if ($('comment').value.trim() !== '') return;   // would lose the comment
    closeDialog();
    return;
  }
  if (!$('histpanel').hidden) $('histpanel').hidden = true;
});
$('comment').addEventListener('keydown', (e) => {
  if (e.key === 'Enter' && !e.shiftKey) { e.preventDefault(); addAnnotation(); }
});

// Copy in a compact, readable line format (anchor (severity): comment — "snippet").
function renderFeedback() {
  const pending = ANNOTATIONS.filter((a) => !a.resolved);
  return pending.map((a) =>
    a.anchor + ' (' + a.severity + '): ' + a.comment + ' — "' + (a.snippet || '').slice(0, 40) + '…"'
  ).join('\n');
}

function copyText(text) {
  const ta = document.createElement('textarea');
  ta.value = text;
  ta.style.position = 'fixed'; ta.style.opacity = '0';
  document.body.appendChild(ta);
  ta.select();
  let ok = false;
  try { ok = document.execCommand('copy'); } catch (e) { ok = false; }
  document.body.removeChild(ta);
  return ok;
}

$('send').onclick = async () => {
  const pending = ANNOTATIONS.filter((a) => !a.resolved);
  if (!LIVE) {   // no daemon: copying is the only transport left
    if (!pending.length) { status('nothing to send'); return; }
    status(copyText(renderFeedback()) ? 'copied — paste it into the chat'
                                      : 'copy failed: ' + renderFeedback());
    return;
  }
  if (!pending.length) { status('nothing to send yet — click an element first'); return; }
  $('send').disabled = true;
  try {
    const r = await fetch('/a/' + TOPIC + '/send', { method: 'POST' });
    const res = await r.json();
    status('sent · stored on disk — the agent reads it in your session');
    $('send').textContent = 'Sent ✓';
  } catch (e) {
    $('send').disabled = false;
    status('could not reach the daemon — nothing was sent');
  }
};

// ── live sync: swap only the sections whose hash changed ────────────────────

async function fetchVersion() {
  try {
    const r = await fetch('/v/' + TOPIC, { cache: 'no-store' });
    return r.ok ? await r.json() : null;
  } catch (e) { return null; }
}

function lockedSection() {
  // Don't yank the section the user is currently annotating.
  if (!target || !target.el.isConnected) return null;
  const sec = target.el.closest('[data-section]');
  return sec ? sec.dataset.section : null;
}

function place(el, i, content) {
  const at = content.children[i];
  if (at !== el) content.insertBefore(el, at || null);
}

async function renderMermaid(nodes) {
  if (!mermaidOn) return;
  // One bad diagram must not blank the others, and a failure must be VISIBLE —
  // silently leaving source text on the page was the round-1 bug.
  for (const n of nodes.filter((x) => !x.dataset.processed && !x.dataset.mermaidFailed)) {
    try {
      if (!n.dataset.src) n.dataset.src = n.textContent;
      await window.mermaid.run({ nodes: [n] });
    } catch (e) {
      n.dataset.mermaidFailed = '1';
      n.classList.add('mermaid-failed');
      fail('diagram ' + (n.dataset.anchor || '?') + ' failed: ' + String((e && e.message) || e).split('\n')[0]);
    }
  }
  anchorMermaidNodes();
  mark();   // re-mark: rendering the diagram replaced the node's inner HTML
}

// Give every rendered diagram node its own annotation anchor (<figure>.<node>),
// so the user can point at "the Agent box", not "that diagram".
function anchorMermaidNodes() {
  document.querySelectorAll('[data-anchor] svg').forEach((svg) => {
    const fig = svg.closest('[data-anchor]').dataset.anchor;
    svg.querySelectorAll('g.node, g.actor, g.cluster').forEach((g) => {
      if (g.dataset.anchor) return;
      const raw = (g.id || '').replace(/^(flowchart|mermaid|state|sequence)-?/, '').replace(/-\d+$/, '');
      const text = (g.textContent || '').trim().split('\n')[0];
      const slug = (raw && /^[a-zA-Z0-9_]+$/.test(raw)) ? raw : text.toLowerCase().replace(/[^a-z0-9]+/g, '-').replace(/^-|-$/g, '');
      if (slug) g.dataset.anchor = fig + '.' + slug;
    });
  });
}

// ── data-rendered shapes ──────────────────────────────────────────────────
// For repeating shapes the markup is chrome-owned and the agent writes ONLY data:
//   <div data-render="compare" data-anchor="cmp-1">
//     plus a child <script type="application/json"> payload holding {head, rows}
//   <\/div>
// (The closing tag above is written escaped on purpose: an HTML parser ends this whole
// script at the first closing-script sequence it meets — comments included — so writing
// one literally here would truncate this file and spill the rest into the page as text.)
// This is NOT mainly a token saving — measured 11-15% fewer, because JSON braces,
// quotes and keys cost nearly what the tags cost. It earns its place because every
// anchor is DERIVED from the data, so ids cannot drift and silently orphan the
// user's notes, and a fixed shape cannot be malformed.

function slug(s) {
  return String(s).toLowerCase().replace(/[^a-z0-9]+/g, '-').replace(/^-|-$/g, '');
}

function renderSpecs(scope) {
  scope.querySelectorAll('[data-render]').forEach((el) => {
    if (el.dataset.rendered === '1') return;
    const kind = el.dataset.render;
    let d;
    const raw = el.querySelector('script[type="application/json"]');
    try {
      d = JSON.parse(raw ? raw.textContent : el.dataset.json || '{}');
    } catch (e) {
      el.innerHTML = '<div class="spec-error">bad JSON in this ' + escapeHtml(kind) + ': ' + escapeHtml(e.message) + '</div>';
      el.dataset.rendered = '1';
      return;
    }
    const A = (item, name) => escapeHtml((item && item.a) || (el.dataset.anchor + '.' + slug(name)));
    let html = '';

    if (kind === 'compare') {
      html = '<table class="compare">' +
        (d.head ? '<tr>' + d.head.map((h) => '<th>' + escapeHtml(h) + '</th>').join('') + '</tr>' : '') +
        (d.rows || []).map((r) => {
          const cells = Array.isArray(r) ? r : (r.c || []);
          return '<tr data-anchor="' + A(Array.isArray(r) ? null : r, cells[0]) + '">' +
            cells.map((c) => '<td>' + escapeHtml(c) + '</td>').join('') + '</tr>';
        }).join('') +
        '</table>';
    } else if (kind === 'cards') {
      html = '<div class="cards">' + (d.cols || []).map((c) =>
        '<div class="card" data-anchor="' + A(c, c.k || c.h) + '">' +
        (c.k ? '<p class="kicker">' + escapeHtml(c.k) + '</p>' : '') +
        (c.h ? '<h3>' + escapeHtml(c.h) + '</h3>' : '') +
        (c.p ? '<p>' + escapeHtml(c.p) + '</p>' : '') + '</div>').join('') + '</div>';
    } else if (kind === 'bars') {
      const max = d.max || Math.max(1, ...(d.items || []).map((i) => i.v));
      html = '<div class="bars">' + (d.items || []).map((i) =>
        '<div class="bar" data-anchor="' + A(i, i.l) + '"><span>' + escapeHtml(i.l) + '</span>' +
        '<span class="track"><span class="fill" style="width:' + Math.round((i.v / max) * 100) + '%"></span></span>' +
        '<span>' + escapeHtml(i.t || i.v) + '</span></div>').join('') + '</div>';
    } else if (kind === 'flow') {
      html = '<div class="flow">' + (d.steps || []).map((s, i) =>
        (i ? '<span class="arr">→</span>' : '') +
        '<span class="step" data-anchor="' + A(s, s.t) + '">' + escapeHtml(s.t) + '</span>').join('') + '</div>';
    } else {
      html = '<div class="spec-error">unknown data-render kind: ' + escapeHtml(kind) + '</div>';
    }

    el.innerHTML = html;
    el.dataset.rendered = '1';
  });
}

// Record the served hashes on the inlined sections so an unchanged section is
// recognised as unchanged on the next poll.
function hydrateHashes(hashes) {
  $('content').querySelectorAll('[data-section]').forEach((el) => {
    const id = el.dataset.section;
    if (hashes[id]) el.dataset.hash = hashes[id];
  });
}

async function applySections(html, hashes) {
  const doc = new DOMParser().parseFromString('<div>' + html + '</div>', 'text/html');
  const incoming = Array.from(doc.body.firstChild.children);
  const content = $('content');
  const lock = lockedSection();
  const touched = [];

  incoming.forEach((sec, i) => {
    const id = sec.dataset.section;
    if (!id) return;
    const cur = content.querySelector('[data-section="' + CSS_ESCAPE(id) + '"]');
    if (cur && hashes[id] === cur.dataset.hash) { place(cur, i, content); return; }
    if (cur && lock === id) return;                 // retry on the next poll
    const node = document.importNode(sec, true);
    node.dataset.hash = hashes[id];
    if (cur) { cur.replaceWith(node); } else { content.appendChild(node); }
    place(node, i, content);
    touched.push(node);
  });

  // Drop sections the agent deleted, but never the one being annotated.
  Array.from(content.children).forEach((el) => {
    const id = el.dataset.section;
    if (!incoming.some((s) => s.dataset.section === id) && id !== lock) el.remove();
  });

  renderSpecs(content);
  decorateSections(content);
  mark();              // also re-quotes the notes each .answer replies to
  await renderMermaid(touched);
  clearFail();   // fresh content: an earlier render error is no longer relevant
}
async function sync() {
  if (document.visibilityState !== 'visible') return;
  const v = await fetchVersion();
  if (!v) return;
  // The indicator must stay honest even when nothing changed, so update it first. Fresh marking
  // comes before the early return: a new Send clears it without changing any content.
  indicator(v);
  markFresh(v.fresh);
  if (v.v === lastV) return;
  try {
    const r = await fetch('/c/' + TOPIC, { cache: 'no-store' });
    if (!r.ok) return;
    await applySections(await r.text(), v.sections);
    lastV = v.v;
    loadHistory();   // the update was logged as a new round entry
  } catch (e) { status('lost the daemon — reload when it is back'); }
}

// ── colour scheme ─────────────────────────────────────────────────────────
// Explicit, not OS-driven: the reader's choice wins, and every canvas looks the same for
// a given choice. Persisted per browser; ?scheme= overrides for screenshots.

const SCHEMES = ['slate', 'graphite', 'paper'];

function currentScheme() {
  return document.documentElement.getAttribute('data-scheme') || 'slate';
}

function mermaidTheme() {
  // Read the live tokens so diagrams follow the active scheme instead of a baked palette.
  const cs = getComputedStyle(document.documentElement);
  const v = (n) => cs.getPropertyValue(n).trim();
  return {
    fontFamily: '"IBM Plex Mono", ui-monospace, Menlo, monospace', fontSize: '13px',
    background: 'transparent',
    primaryColor: v('--surface'), primaryBorderColor: v('--line'),
    primaryTextColor: v('--ink'), secondaryColor: v('--surface-2'),
    tertiaryColor: v('--surface-2'), lineColor: v('--ink-3'), textColor: v('--ink'),
    noteBkgColor: v('--surface-2'), noteBorderColor: v('--line'),
    clusterBkg: v('--surface'), clusterBorder: v('--line'),
    edgeLabelBackground: v('--bg'),
  };
}

function mermaidConfig() {
  return { startOnLoad: false, theme: 'base', securityLevel: 'loose',
           themeVariables: mermaidTheme() };
}

function applyScheme(name, persist) {
  if (SCHEMES.indexOf(name) < 0) name = 'slate';
  document.documentElement.setAttribute('data-scheme', name);
  document.querySelectorAll('#schemes button').forEach((b) => {
    b.classList.toggle('on', b.dataset.pick === name);
    b.setAttribute('aria-pressed', b.dataset.pick === name ? 'true' : 'false');
  });
  if (persist) {
    try { localStorage.setItem('canvas:scheme', name); } catch (e) { /* file:// may refuse */ }
    redrawDiagrams();
  }
}

// Mermaid bakes its palette at render time, so a scheme switch must redraw — and that
// needs the original source, which rendering destroyed. Keep it before the first render.
async function redrawDiagrams() {
  if (!mermaidOn) return;
  const nodes = Array.from(document.querySelectorAll('pre.mermaid, div.mermaid'));
  nodes.forEach((n) => {
    if (n.dataset.src) n.textContent = n.dataset.src;
    delete n.dataset.processed;
    delete n.dataset.mermaidFailed;
    n.classList.remove('mermaid-failed');
  });
  window.mermaid.initialize(mermaidConfig());
  await renderMermaid(nodes);
  mark();
}

document.querySelectorAll('#schemes button').forEach((b) => {
  b.onclick = () => applyScheme(b.dataset.pick, true);
});

// The daemon keeps the page live and puts every note on disk the moment it is typed.
// There is no auto-wake: a round happens when a session is talking to the user. So the dot
// reports work state, and the text never implies a reader that does not exist.
function indicator(v) {
  const unsent = (v && v.unsent) || 0;
  const pending = (v && v.pending) || 0;
  const flagged = (v && v.flagged) || 0;
  const lastSend = (v && v.last_send_ts) || null;
  const el = $('listening');
  el.textContent = pending ? 'notes waiting' : 'live';
  el.classList.toggle('on', pending > 0);
  el.classList.toggle('warn', flagged > 0);

  // An empty Send is a no-op that still pings the agent, so the button turns itself
  // off once everything has been sent. (Found by the user pressing it with nothing
  // pending — the log recorded a send with count 0.)
  const send = $('send');
  send.disabled = LIVE && unsent === 0;
  send.textContent = unsent ? 'Send · ' + unsent : (pending ? 'Sent ✓' : 'Send');

  // The status pill carries the state as a dot too, so it is legible at a glance.
  const st = $('status');
  st.classList.toggle('on', pending > 0);
  st.classList.toggle('warn', flagged > 0);
  const clock = (iso) => (String(iso).match(/T(\d\d:\d\d)/) || [null, iso])[1];
  const badge = $('convonotice');
  if (badge) { badge.hidden = !flagged; badge.textContent = flagged ? String(flagged) : ''; }
  if (flagged) {
    // Flagged notes are waiting on the USER, not on an agent. Reporting them as work in
    // progress was a lie the page had no way to detect.
    status('⚠ ' + flagged + ' note(s) need your decision — flagged, not being worked');
    return;
  }
  if (unsent) {
    status(unsent + ' note(s) not sent yet — press Send');
  } else if (pending) {
    // The page cannot wake anyone, and saying otherwise would promise a reader that is
    // not there. Say what is true: the notes are on disk, and a session collects them.
    status(pending + ' note(s) waiting — ask the agent to check this canvas');
  } else if (lastSend) {
    status('all notes resolved · last send ' + clock(lastSend));
  } else {
    status('live · write a note to begin');
  }
}

// ── history: the record of this topic, shown on the page ───────────────────
// The daemon logs every annotation, resolve, delete, content change and agent line
// to <topic>/history.jsonl. Rounds are DERIVED here — a round is the events since the
// previous agent reply — so nothing has to be numbered at write time.

let HISTORY = [];

async function loadHistory() {
  if (!LIVE) {
    // Static (file://): no daemon, so no round history — render the empty state rather
    // than leave the panel blank when it is opened.
    $('histbody').innerHTML = '<p class="empty">No history without the daemon — your notes still copy for pasting into chat.</p>';
    return;
  }
  try {
    const r = await fetch('/h/' + TOPIC, { cache: 'no-store' });
    if (!r.ok) return;
    HISTORY = (await r.json()).events || [];
    renderHistory();
  } catch (e) { /* history is a convenience; never block the page on it */ }
}

function rounds(events) {
  const out = [];
  let cur = { items: [], open: true };
  events.forEach((e) => {
    cur.items.push(e);
    if (e.kind === 'agent') { cur.open = false; out.push(cur); cur = { items: [], open: true }; }
  });
  if (cur.items.length) out.push(cur);
  return out;
}

function evLine(e) {
  if (e.kind === 'user') {
    // Flagged notes are questions for the reader, so the row that raised it says so.
    const mine = ANNOTATIONS.find((a) => a.id === e.id);
    const needs = !!(mine && mine.flagged && !mine.resolved);
    return '<div class="ev user' + (needs ? ' needs' : '') + '" data-jump="' + escapeHtml(e.anchor) + '">' +
      '<span class="who">' + (needs ? 'needs you' : 'you') + '</span>' +
      '<span class="txt"><b>' + escapeHtml(e.anchor) + '</b> — ' + escapeHtml(e.comment) + '</span>' +
      (needs ? '<button class="answered" data-ack="' + escapeHtml(e.id) + '" title="mark this answered">answered</button>' : '') +
      '</div>';
  }
  if (e.kind === 'flag') {
    // The worker's own escalation text: the closest thing to "the message asking for input".
    return '<div class="ev flag needs"><span class="who">needs you</span>' +
      '<span class="txt"><b>decision required</b> — ' + escapeHtml(e.note || 'see the flagged note') +
      '</span><button class="answered" data-ack="' + escapeHtml((e.ids || []).join(',')) +
      '" title="mark this answered">answered</button></div>';
  }
  if (e.kind === 'agent') {
    return '<div class="ev agent"><span class="who">agent</span><span class="txt">' +
      escapeHtml(e.text) + '</span></div>';
  }
  if (e.kind === 'content') {
    const parts = [];
    if (e.changed && e.changed.length) parts.push('rewrote ' + e.changed.map((s) => '§' + s.replace(/^s/, '')).join(', '));
    if (e.added && e.added.length) parts.push('added ' + e.added.map((s) => '§' + s.replace(/^s/, '')).join(', '));
    if (e.removed && e.removed.length) parts.push('removed ' + e.removed.map((s) => '§' + s.replace(/^s/, '')).join(', '));
    if (!parts.length) return '';
    return '<div class="ev meta"><span class="who"></span><span class="txt">' + escapeHtml(parts.join(' · ')) + '</span></div>';
  }
  if (e.kind === 'resolve') {
    return '<div class="ev meta"><span class="who"></span><span class="txt">resolved ' +
      (e.ids || []).length + ' note(s)</span></div>';
  }
  if (e.kind === 'wake') {
    // Legacy event: before the delivery loop was removed, a daemon woke a dispatch agent and
    // logged whether that reached anyone. Nothing wakes anyone now, so a wake line is read as
    // what it is — a marker that the page was sent.
    return '<div class="ev meta"><span class="who"></span><span class="txt">' +
      (e.ok ? 'sent to the agent (legacy)'
            : 'sent — nothing collected it (legacy)') +
      '</span></div>';
  }
  if (e.kind === 'delete') {
    return '<div class="ev meta"><span class="who"></span><span class="txt">you deleted a note on <b>' +
      escapeHtml(e.anchor || '?') + '</b></span></div>';
  }
  return '';
}

// ── section status: what is settled, what is new, what is still open ───────
// A canvas accumulates every decision ever made, so it turns into a wall of text and the
// live question gets lost in it. Content may mark a section data-status="settled|new|open";
// settled sections FOLD (the reader can still open them), new ones are flagged, and the
// reader can answer "what changed since I last looked?" by scanning the pills alone.
// No attribute = untouched, so every existing canvas is unchanged.
// ── answers: the reply to a note, bound to the note ────────────────────────
// A round writes `<div class="answer" data-answers="<note id>">`. Quoting the note back, in
// place, is what makes the pair travel together: before this, the reader had the note in the
// Conversation panel and the answer somewhere in the prose, with nothing joining them.
function renderAnswers(scope) {
  scope.querySelectorAll('.answer[data-answers]').forEach((el) => {
    // Its own flag, NOT `data-rendered`: that one belongs to the data-render renderer, and
    // reusing it made verify count an answer block as a rendered spec (2 of 1).
    if (el.dataset.quoted === '1') return;
    const note = ANNOTATIONS.find((a) => a.id === el.dataset.answers);
    // build-canvas.py writes the note into the file, so the quote works with no daemon and no
    // localStorage. Fall back to the annotations, and wait for them rather than rendering a
    // quote that never fills.
    const inlined = Object.prototype.hasOwnProperty.call(el.dataset, 'note');
    if (!inlined && !note && ANNOTATIONS.length === 0) return;
    el.dataset.quoted = '1';
    const head = document.createElement('div');
    head.className = 'answer-head';
    const label = document.createElement('span');
    label.className = 'answer-label';
    const text = inlined ? el.dataset.note : (note ? note.comment : null);
    label.textContent = text !== null ? 'answering your note' : 'your note is gone';
    const q = document.createElement('blockquote');
    q.className = 'answer-note';
    q.textContent = text !== null ? text
                                  : 'note ' + el.dataset.answers + ' is no longer on this page';
    head.appendChild(label);
    head.appendChild(q);
    el.insertBefore(head, el.firstChild);
  });
}

// ── what a round just changed ───────────────────────────────────────────────
// The daemon knows which sections changed since the last Send and says so on /v. Derived, never
// authored: the content talks about the subject, the chrome points at what is new. It clears by
// itself when the user sends again.
function markFresh(fresh) {
  const list = fresh || [];
  document.querySelectorAll('#content > section[data-section]').forEach((sec) => {
    const pill = sec.querySelector(':scope > .secpill.fresh');
    const isNew = list.indexOf(sec.dataset.section) >= 0;
    if (isNew && !pill) {
      const p = document.createElement('span');
      p.className = 'secpill fresh';
      p.textContent = 'new since your send';
      sec.insertBefore(p, sec.firstChild);
    } else if (!isNew && pill) {
      pill.remove();
    }
  });
}

function decorateSections(scope) {
  scope.querySelectorAll('[data-section][data-status]').forEach((sec) => {
    if (sec.dataset.decorated === '1') return;
    sec.dataset.decorated = '1';
    const st = sec.dataset.status;
    const pill = document.createElement('span');
    pill.className = 'secpill ' + st;
    pill.textContent = st;
    sec.insertBefore(pill, sec.firstChild);
    if (st !== 'settled') return;
    const kids = Array.from(sec.children).filter((c) => c !== pill);
    if (kids.length < 2) return;
    const head = kids[0];
    const det = document.createElement('details');
    det.className = 'secfold';
    const sum = document.createElement('summary');
    sec.insertBefore(det, head);
    det.appendChild(sum);
    sum.appendChild(head);
    kids.slice(1).forEach((c) => det.appendChild(c));
  });
}

function renderHistory() {
  const body = $('histbody');
  const groups = rounds(HISTORY);
  $('history').textContent = 'Conversation · ' + groups.length;
  // The badge is driven from /v every second (see indicator), not from history load.
  if (!groups.length) { body.innerHTML = '<p class="empty">Nothing yet on this topic.</p>'; return; }
  body.innerHTML = groups.map((g, i) => {
    const n = i + 1;   // groups are oldest-first; .reverse() below puts the newest on top
    const lines = g.items.map(evLine).join('');
    return '<div class="round' + (g.open ? ' open' : '') + '">' +
      '<div class="rhead">round ' + n + (g.open ? ' · current' : '') + '</div>' + lines + '</div>';
  }).reverse().join('');
  // Marking a decision answered resolves the note, which is what the badge counts — this
  // is the reader's own way out of a flagged state, without waiting for an agent to ack it.
  body.querySelectorAll('[data-ack]').forEach((el) => {
    el.onclick = async (ev) => {
      ev.stopPropagation();
      const ids = String(el.dataset.ack || '').split(',').filter(Boolean);
      if (!ids.length) return;
      el.disabled = true;
      try {
        await fetch('/a/' + TOPIC + '/ack', { method: 'POST',
          headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ ids }) });
      } catch (e) {
        el.disabled = false;
        status('could not reach the daemon — nothing was cleared');
        return;
      }
      await restore();       // refresh ANNOTATIONS so the rows and badge agree
      await loadHistory();
      mark();
      status('marked answered');
    };
  });

  body.querySelectorAll('[data-jump]').forEach((el) => {
    el.onclick = () => {
      const t = document.querySelector('[data-anchor="' + CSS_ESCAPE(el.dataset.jump) + '"]');
      if (!t) { status('that element is gone from the page'); return; }
      // A note on a chrome element opens the panel, so it can be read where it was written.
      if (t.closest('#histpanel')) $('histpanel').hidden = false;
      t.scrollIntoView({ behavior: 'smooth', block: 'center' });
      t.classList.add('flash');
      setTimeout(() => t.classList.remove('flash'), 1600);
    };
  });
}

$('history').onclick = () => {
  const p = $('histpanel');
  p.hidden = !p.hidden;
  if (!p.hidden) loadHistory();
};
$('histclose').onclick = () => { $('histpanel').hidden = true; };

// Tapping outside closes the panel — a full-height drawer with no way out except the
// small × is a trap on a phone.
document.addEventListener('click', (e) => {
  const panel = $('histpanel');
  if (panel.hidden) return;
  if (e.target.closest('#histpanel') || e.target.closest('#history')) return;
  panel.hidden = true;
}, true);

// ── boot ───────────────────────────────────────────────────────────────────

async function initMermaid() {
  if (!window.mermaid) {
    if (!LIVE) return;
    // Load unconditionally. A HEAD probe used to guard this and the browser cached the
    // stale 501 from before do_HEAD existed, so the page refused a library that was
    // being served fine. onerror is the only check worth having.
    try {
      await new Promise((res, rej) => {
        const s = document.createElement('script');
        s.src = '/mermaid.js';
        s.onload = res; s.onerror = rej;
        document.head.appendChild(s);
      });
    } catch (e) {
      fail('mermaid failed to load — diagrams stay as source text');
      return;
    }
  }
  try {
    window.mermaid.initialize(mermaidConfig());
    mermaidOn = true;
    await renderMermaid(Array.from(document.querySelectorAll('pre.mermaid, div.mermaid')));
  } catch (e) {
    // Never silent: a swallowed error here is how round 1 shipped a page of source text.
    fail('mermaid init failed: ' + String((e && e.message) || e).slice(0, 120));
  }
}

(async function boot() {
  await restore();
  applyScheme(currentScheme(), false);   // sync the switcher with what the head script applied
  renderSpecs(document);
  decorateSections(document);
  mark();
  await initMermaid();
  if (!LIVE) {
    status('static file — Done copies your feedback for pasting');
    return;
  }
  const v = await fetchVersion();
  if (!v) { status('daemon unreachable — annotations are kept locally'); return; }
  // Adopt the served content only if the inlined copy is already stale.
  const inlined = $('content').dataset.v;
  if (inlined && inlined !== v.v) {
    await applySections(await (await fetch('/c/' + TOPIC, { cache: 'no-store' })).text(), v.sections);
  } else {
    hydrateHashes(v.sections);   // fresh shell: remember hashes so the first update swaps one section, not all
  }
  lastV = v.v;
  indicator(v);
  markFresh(v.fresh);
  loadHistory();
  setInterval(sync, 1000);
})();
