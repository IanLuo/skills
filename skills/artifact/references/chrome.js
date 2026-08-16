// Click-to-annotate + Done-copy-close. Keep verbatim. Uses
  // document.execCommand('copy'), NOT navigator.clipboard — unavailable on file://.
  const ANNOTATIONS = [];
  let target = null;   // {el, id, snippet}
  let severity = 'suggestion';

  document.addEventListener('click', (e) => {
    const el = e.target.closest('[data-anchor]');
    if (!el) return;
    e.preventDefault();
    target = { el, id: el.dataset.anchor, snippet: el.textContent.trim().slice(0, 80) };
    showForm(el);
  });

  // Position the dialog beside the clicked element, clamped fully on-screen.
  function showForm(el) {
    const f = document.getElementById('form');
    f.hidden = false;                                   // make it measurable
    const r = el.getBoundingClientRect();
    const w = f.offsetWidth, h = f.offsetHeight;
    let x = Math.max(4, Math.min(r.left, window.innerWidth - w - 4));
    let y = r.bottom + 6;
    if (y + h > window.innerHeight - 4) y = Math.max(4, r.top - h - 6);  // flip above if needed
    f.style.left = x + 'px';
    f.style.top = y + 'px';
    document.getElementById('comment').value = '';
    document.getElementById('comment').focus();
  }

  // Severity as three toggle buttons (no dropdown).
  document.querySelectorAll('#levels .lvl').forEach((b) => {
    b.onclick = () => {
      severity = b.dataset.sev;
      document.querySelectorAll('#levels .lvl').forEach((x) => x.classList.toggle('active', x === b));
    };
  });

  document.getElementById('cancel').onclick = () => {
    document.getElementById('form').hidden = true;
    target = null;
  };

  document.getElementById('add').onclick = () => {
    const c = document.getElementById('comment').value.trim();
    if (!target || !c) return;
    ANNOTATIONS.push({ anchor: { id: target.id, snippet: target.snippet }, comment: c, severity });
    target.el.classList.add('marked');
    document.getElementById('count').textContent = ANNOTATIONS.length;
    document.getElementById('form').hidden = true;
    target = null;
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

  document.getElementById('done').onclick = () => {
    const btn = document.getElementById('done');
    const status = document.getElementById('status');
    if (ANNOTATIONS.length === 0) { status.textContent = 'No feedback yet — click any element to add.'; return; }
    if (!copyText(renderFeedback())) {
      status.textContent = 'Copy failed — select manually:\n' + renderFeedback();
      return;
    }
    btn.textContent = 'Copied ✓';
    status.textContent = 'Paste it into the AI window, then close this tab.';
    setTimeout(() => { try { window.close(); } catch (err) {} }, 200);
  };

  document.getElementById('raw').onclick = () => {
    const status = document.getElementById('status');
    if (ANNOTATIONS.length === 0) { status.textContent = 'No feedback yet.'; return; }
    status.textContent = copyText(JSON.stringify(ANNOTATIONS)) ? 'Raw JSON copied.' : 'Copy failed.';
  };
