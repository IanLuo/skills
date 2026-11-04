// Click-to-annotate + rung answer controls + view/edit/delete + copy. Keep verbatim.
// Uses document.execCommand('copy'), NOT navigator.clipboard — unavailable on file://.
//
// Two ways to answer a rung:
//   - the rung's own buttons (.answers [data-choice]) — one click, no typing:
//     accept / option B / change
//   - clicking any anchored element — the free-text form
// The copied payload carries the verb where the severity used to sit, so the agent
// resolves "accept" and "option B" instead of guessing at prose.
const ANNOTATIONS = [];
let target = null;      // {el, id, snippet}
let severity = 'suggestion';
let editing = null;     // ANNOTATIONS index being edited, or null
let pendingAction = 'note';   // what the open form will record

const $ = (id) => document.getElementById(id);

function escapeHtml(s) {
  return s.replace(/[&<>"']/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]));
}

// The verb the agent parses. A plain note keeps its severity.
function verb(a) {
  if (a.action === 'accept') return 'accept';
  if (a.action === 'option') return 'option ' + a.choice;
  if (a.action === 'change') return 'change';
  return a.severity;
}

function snippetOf(el) {
  return el.textContent.trim().replace(/\s+/g, ' ').slice(0, 80);
}

// ── One click on a rung's answer buttons ────────────────────────────────────
document.addEventListener('click', (e) => {
  // A click elsewhere with nothing typed just dismisses the box. With text, it stays —
  // losing a half-written note to a stray click is the one loss the loop cannot undo.
  const f = $('form');
  if (!f.hidden && !e.target.closest('#form') && $('comment').value.trim() === '') closeDialog();

  const btn = e.target.closest('[data-choice]');
  if (btn) { e.preventDefault(); e.stopPropagation(); choose(btn); return; }

  const el = e.target.closest('[data-anchor]');
  if (!el) return;
  e.preventDefault();
  target = { el, id: el.dataset.anchor, snippet: snippetOf(el) };
  showForm(el, 'note');
});

function choose(btn) {
  const group = btn.closest('[data-for]');
  const id = group && group.dataset.for;
  if (!id) return;
  const choice = btn.dataset.choice;

  // One answer per rung: a second click replaces the first.
  const existing = ANNOTATIONS.findIndex((a) => a.anchor.id === id && a.action !== 'note');

  // Clicking the chosen answer again clears it — an undo without a menu.
  if (existing !== -1 && ANNOTATIONS[existing].choice === choice) {
    ANNOTATIONS.splice(existing, 1);
    syncRung(id);
    sync();
    return;
  }

  if (choice === 'other') {
    const el = document.querySelector('[data-anchor="' + id + '"]');
    if (!el) return;
    target = { el, id, snippet: snippetOf(el) };
    showForm(el, 'change');
    return;
  }

  if (existing !== -1) ANNOTATIONS.splice(existing, 1);
  // The snippet names the rung's heading, not the whole block — the heading is what
  // the agent matches back to a section.
  const rung = group.closest('[data-rung]');
  const head = (rung && rung.querySelector('[data-anchor]')) || btn;
  ANNOTATIONS.push({
    anchor: { id, snippet: snippetOf(head) },
    comment: choice === 'accept' ? 'accepted the recommendation' : btn.textContent.trim(),
    severity,
    action: choice === 'accept' ? 'accept' : 'option',
    choice,
  });
  syncRung(id);
  sync();
}

// The rung's state is derived from the annotations, never toggled blind: a plain note
// on a rung must not mark it answered, and deleting the note must not clear an answer.
function syncRung(id) {
  const a = ANNOTATIONS.find((x) => x.anchor.id === id && x.action !== 'note');
  if (!a) return mark(id, null);
  mark(id, a.action === 'accept' ? 'accept' : a.action === 'option' ? a.choice : 'other');
}

// Paint the rung's state: one chosen button, and the answered class on its wrapper.
function mark(id, choice) {
  const rung = document.querySelector('[data-rung="' + id + '"]');
  if (rung) rung.classList.toggle('done', !!choice);
  const group = document.querySelector('[data-for="' + id + '"]');
  if (!group) return;
  group.querySelectorAll('[data-choice]').forEach((b) => {
    b.classList.toggle('chosen', !!choice && b.dataset.choice === choice);
  });
}

// ── The rail: what is left, without scrolling ───────────────────────────────
function updateRail() {
  const rungs = document.querySelectorAll('[data-rung]');
  const rail = $('rail');
  if (!rail) return;
  // A plain artifact has no rungs — the rail would only be noise.
  if (rungs.length === 0) { rail.hidden = true; return; }
  rail.hidden = false;
  document.body.classList.add('has-rail');

  const done = Array.prototype.filter.call(rungs, (r) => r.classList.contains('done')).length;
  const notes = ANNOTATIONS.filter((a) => a.action === 'note').length;
  $('rail-count').textContent =
    (rungs.length - done) + ' open · ' + done + ' answered' +
    (notes ? ' · ' + notes + (notes === 1 ? ' note' : ' notes') : '');
}

function sync() {
  $('count').textContent = ANNOTATIONS.length;
  updateRail();
  if (target) renderList();
}

$('rail-next').onclick = () => {
  const next = Array.prototype.find.call(
    document.querySelectorAll('[data-rung]'), (r) => !r.classList.contains('done'));
  (next || document.body).scrollIntoView({ block: 'center', behavior: 'smooth' });
};

// Position the dialog BESIDE the clicked element, not over it: the reader is answering
// the line the dialog would otherwise cover. Right of the element if it fits, else
// left, else below — clamped fully on-screen.
function showForm(el, action) {
  const f = $('form');
  f.hidden = false;                              // make it measurable
  const r = el.getBoundingClientRect();
  const w = f.offsetWidth, h = f.offsetHeight;
  const gap = 10, edge = 8;
  let x = r.right + gap;
  if (x + w > window.innerWidth - edge) x = r.left - w - gap;
  // No gutter on either side (a full-width column): pin to the right edge, which covers
  // the end of a line rather than the line itself.
  if (x < edge) x = Math.max(edge, window.innerWidth - w - edge);
  let y = r.top;
  if (y + h > window.innerHeight - edge) y = Math.max(edge, r.bottom - h);
  f.style.left = x + 'px';
  f.style.top = y + 'px';
  resetInput();
  pendingAction = action || 'note';
  renderList();
  $('comment').focus();                  // type immediately, no mouse needed
}

function resetInput() {
  $('comment').value = '';
  editing = null;
  pendingAction = 'note';
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
      '<span class="meta">' + escapeHtml(verb(a) + ': ' + a.comment) + '</span>' +
      '<button data-act="edit" data-i="' + i + '">edit</button>' +
      '<button data-act="del" data-i="' + i + '">del</button></div>';
    list.appendChild(row);
  });
}

$('list').addEventListener('click', (e) => {
  // Deleting a row removes the button that was clicked, so by the time the event
  // reaches the document listener `closest('#form')` finds nothing and the box would
  // close as if the click had landed outside. Stop it here.
  e.stopPropagation();
  const btn = e.target.closest('button[data-act]');
  if (!btn || !target) return;
  const i = +btn.dataset.i;
  if (btn.dataset.act === 'edit') {
    editing = i;
    $('comment').value = ANNOTATIONS[i].comment;
    severity = ANNOTATIONS[i].severity;
    pendingAction = ANNOTATIONS[i].action;
    document.querySelectorAll('#levels .lvl').forEach((x) => x.classList.toggle('active', x.dataset.sev === severity));
    $('add').textContent = 'Save';
    $('comment').focus();
  } else if (btn.dataset.act === 'del') {
    const gone = ANNOTATIONS.splice(i, 1)[0];
    if (gone) syncRung(gone.anchor.id);
    sync();
    renderList();
  }
});

function closeDialog() { $('form').hidden = true; target = null; editing = null; }
function addAnnotation() {
  const c = $('comment').value.trim();
  if (!target || !c) return;
  if (editing !== null) {
    ANNOTATIONS[editing].comment = c;
    ANNOTATIONS[editing].severity = severity;
  } else {
    ANNOTATIONS.push({
      anchor: { id: target.id, snippet: target.snippet },
      comment: c,
      severity,
      action: pendingAction,
    });
  }
  target.el.classList.add('marked');
  syncRung(target.id);
  sync();
  resetInput();
  closeDialog();          // the answer is recorded — leaving the box open only covers the page
}
$('cancel').onclick = closeDialog;
$('add').onclick = addAnnotation;

// Keyboard: Esc closes the dialog UNLESS the field has text (would lose it);
// Enter in the field adds.
document.addEventListener('keydown', (e) => {
  if (e.key !== 'Escape' || $('form').hidden) return;
  if ($('comment').value.trim() !== '') return;  // typed something — don't close
  closeDialog();
});
$('comment').addEventListener('keydown', (e) => {
  if (e.key === 'Enter' && !e.shiftKey) { e.preventDefault(); addAnnotation(); }  // Shift+Enter = newline
});

// Copy in a compact, readable line format:
//   <id> (<verb>): <comment> — "<snippet>"
// The verb is accept / option <choice> / change, or the severity for a plain note.
function renderFeedback() {
  return ANNOTATIONS.map((a) =>
    a.anchor.id + ' (' + verb(a) + '): ' + a.comment +
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

// Copy and stay open. The page is often re-read while the agent applies the round;
// closing it is the reader's act, not the button's.
$('done').onclick = () => {
  const btn = $('done');
  const status = $('status');
  if (ANNOTATIONS.length === 0) { status.textContent = 'Nothing yet — answer a rung or click any line.'; return; }
  if (!copyText(renderFeedback())) {
    status.textContent = 'Copy failed — select manually:\n' + renderFeedback();
    return;
  }
  btn.textContent = 'Copied ✓';
  status.textContent = 'Pasted? Close the tab when done.';
};

updateRail();
