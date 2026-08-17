// Click-to-annotate + view/edit/delete + Done-copy-close. Keep verbatim.
// Uses document.execCommand('copy'), NOT navigator.clipboard — unavailable on file://.
const ANNOTATIONS = [];
let target = null;    // {el, id, snippet}
let severity = 'suggestion';
let editing = null;   // ANNOTATIONS index being edited, or null

const $ = (id) => document.getElementById(id);

document.addEventListener('click', (e) => {
  const el = e.target.closest('[data-anchor]');
  if (!el) return;
  e.preventDefault();
  target = { el, id: el.dataset.anchor, snippet: el.textContent.trim().slice(0, 80) };
  showForm(el);
});

// Position the dialog beside the clicked element, clamped fully on-screen.
function showForm(el) {
  const f = $('form');
  f.hidden = false;                              // make it measurable
  const r = el.getBoundingClientRect();
  const w = f.offsetWidth, h = f.offsetHeight;
  let x = Math.max(4, Math.min(r.left, window.innerWidth - w - 4));
  let y = r.bottom + 6;
  if (y + h > window.innerHeight - 4) y = Math.max(4, r.top - h - 6);  // flip above if needed
  f.style.left = x + 'px';
  f.style.top = y + 'px';
  resetInput();
  renderList();
  $('comment').focus();                  // type immediately, no mouse needed
}

function resetInput() {
  $('comment').value = '';
  editing = null;
  $('add').textContent = 'Add';
}

// Severity as three toggle buttons (no dropdown).
document.querySelectorAll('#levels .lvl').forEach((b) => {
  b.onclick = () => {
    severity = b.dataset.sev;
    document.querySelectorAll('#levels .lvl').forEach((x) => x.classList.toggle('active', x === b));
  };
});

// Show this element's existing annotations with edit/delete (delegated).
function renderList() {
  const list = $('list');
  const mine = ANNOTATIONS.map((a, i) => ({ a, i })).filter(({ a }) => a.anchor.id === target.id);
  if (mine.length === 0) { list.classList.remove('show'); list.innerHTML = ''; return; }
  list.classList.add('show');
  list.innerHTML = '';
  mine.forEach(({ a, i }) => {
    const row = document.createElement('div');
    row.className = 'item';
    row.innerHTML =
      '<div class="row"><span class="dot" data-sev="' + a.severity + '"></span>' +
      '<span class="meta">' + escapeHtml(a.comment) + '</span>' +
      '<button data-act="edit" data-i="' + i + '">edit</button>' +
      '<button data-act="del" data-i="' + i + '">del</button></div>';
    list.appendChild(row);
  });
}

function escapeHtml(s) {
  return s.replace(/[&<>"']/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]));
}

$('list').addEventListener('click', (e) => {
  const btn = e.target.closest('button[data-act]');
  if (!btn || !target) return;
  const i = +btn.dataset.i;
  if (btn.dataset.act === 'edit') {
    editing = i;
    $('comment').value = ANNOTATIONS[i].comment;
    severity = ANNOTATIONS[i].severity;
    document.querySelectorAll('#levels .lvl').forEach((x) => x.classList.toggle('active', x.dataset.sev === severity));
    $('add').textContent = 'Save';
    $('comment').focus();
  } else if (btn.dataset.act === 'del') {
    ANNOTATIONS.splice(i, 1);
    if (ANNOTATIONS.filter((a) => a.anchor.id === target.id).length === 0) {
      target.el.classList.remove('marked');
    }
    $('count').textContent = ANNOTATIONS.length;
    renderList();
  }
});

$('cancel').onclick = () => { $('form').hidden = true; target = null; editing = null; };

$('add').onclick = () => {
  const c = $('comment').value.trim();
  if (!target || !c) return;
  if (editing !== null) {
    ANNOTATIONS[editing].comment = c;
    ANNOTATIONS[editing].severity = severity;
  } else {
    ANNOTATIONS.push({ anchor: { id: target.id, snippet: target.snippet }, comment: c, severity });
    target.el.classList.add('marked');
  }
  $('count').textContent = ANNOTATIONS.length;
  resetInput();
  renderList();
};

// Copy in a compact, readable line format (id (severity): comment — "snippet").
function renderFeedback() {
  return ANNOTATIONS.map((a) =>
    a.anchor.id + ' (' + a.severity + '): ' + a.comment +
    ' — "' + a.anchor.snippet.slice(0, 40) + '…"'
  ).join('\n');
}

function copyText(text) {
  const ta = document.createElement('textarea');
  ta.value = text;
  ta.style.position = 'fixed'; ta.style.opacity = '0';
  document.body.appendChild(ta);
  ta.select();
  let ok = false;
  try { ok = document.execCommand('copy'); } catch (err) { ok = false; }
  document.body.removeChild(ta);
  return ok;
}

$('done').onclick = () => {
  const btn = $('done');
  const status = $('status');
  if (ANNOTATIONS.length === 0) { status.textContent = 'No feedback yet — click any element to add.'; return; }
  if (!copyText(renderFeedback())) {
    status.textContent = 'Copy failed — select manually:\n' + renderFeedback();
    return;
  }
  btn.textContent = 'Copied ✓';
  status.textContent = 'Paste it into the AI window, then close this tab.';
  setTimeout(() => { try { window.close(); } catch (err) {} }, 200);
};

$('raw').onclick = () => {
  const status = $('status');
  if (ANNOTATIONS.length === 0) { status.textContent = 'No feedback yet.'; return; }
  status.textContent = copyText(JSON.stringify(ANNOTATIONS)) ? 'Raw JSON copied.' : 'Copy failed.';
};
