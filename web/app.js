'use strict';
/* Rapid-Entry Assessment Kiosk — single-page app (no build step; embedded in the Go binary). */
const $app = document.getElementById('app');
const S = { me: null, scope: null };
let W = null; // active workspace

// ---------- helpers ----------
function h(tag, props, ...kids) {
  const el = document.createElement(tag);
  for (const [k, v] of Object.entries(props || {})) {
    if (v == null || v === false) continue;
    if (k === 'class') el.className = v;
    else if (k.startsWith('on')) el.addEventListener(k.slice(2), v);
    else if (k === 'value') el.value = v;
    else if (k in el && k !== 'list') el[k] = v;
    else el.setAttribute(k, v);
  }
  for (const kid of kids.flat(Infinity)) {
    if (kid == null || kid === false) continue;
    el.append(kid.nodeType ? kid : document.createTextNode(String(kid)));
  }
  return el;
}
const add = (el, ...kids) => el.append(...kids.flat(Infinity).filter((k) => k != null && k !== false));
const fill = (el, ...kids) => el.replaceChildren(...kids.flat(Infinity).filter((k) => k != null && k !== false));
function mount(...nodes) { $app.replaceChildren(...nodes.flat()); window.scrollTo(0, 0); }
let toastT;
function toast(msg, bad) {
  const t = document.getElementById('toast');
  t.textContent = msg; t.className = 'show' + (bad ? ' bad' : '');
  clearTimeout(toastT); toastT = setTimeout(() => (t.className = ''), bad ? 4500 : 2200);
}
async function api(method, path, body) {
  const opt = { method, headers: { 'X-Requested-With': 'rea' }, credentials: 'same-origin' };
  if (body !== undefined) { opt.headers['Content-Type'] = 'application/json'; opt.body = JSON.stringify(body); }
  let res;
  try { res = await fetch('/api' + path, opt); } catch (e) { const err = new Error('Network unavailable'); err.offline = true; throw err; }
  let data = {};
  try { data = await res.json(); } catch (_) {}
  if (!res.ok) {
    if (res.status === 401 && S.me) { S.me = null; viewLogin(); }
    const err = new Error(data.error || 'Request failed'); err.status = res.status; err.data = data; throw err;
  }
  return data;
}
const todayStr = (off = 0) => { const d = new Date(); d.setDate(d.getDate() + off); return d.getFullYear() + '-' + String(d.getMonth() + 1).padStart(2, '0') + '-' + String(d.getDate()).padStart(2, '0'); };
const fmtDate = (s) => { const [y, m, d] = s.split('-').map(Number); return new Date(y, m - 1, d).toLocaleDateString(undefined, { day: 'numeric', month: 'short' }); };
const fmtTs = (s) => (s ? new Date(s).toLocaleString(undefined, { day: 'numeric', month: 'short', hour: '2-digit', minute: '2-digit' }) : '');
const num = (v) => (v == null ? '' : String(v));
const syncBadge = (s) => h('span', { class: 'badge ' + s.state, title: s.last_error || '' }, { yellow: '🟡 ', green: '🟢 ', red: '🔴 ' }[s.state] + s.label);

// ---------- login ----------
function viewLogin(step = 'id', ident = '', devCode = '') {
  const err = h('div', { class: 'err-msg' });
  const input = h('input', { value: step === 'id' ? ident : '', placeholder: step === 'id' ? 'Mobile number or email' : '6-digit code', inputmode: step === 'id' ? 'text' : 'numeric', autocomplete: step === 'id' ? 'username' : 'one-time-code', maxLength: 80, autofocus: true });
  const go = async () => {
    err.textContent = '';
    try {
      if (step === 'id') {
        const r = await api('POST', '/auth/request', { identifier: input.value });
        viewLogin('code', input.value, r.dev_code || '');
      } else {
        await api('POST', '/auth/verify', { identifier: ident, code: input.value });
        await boot();
      }
    } catch (e) { err.textContent = e.message; }
  };
  input.addEventListener('keydown', (e) => e.key === 'Enter' && go());
  mount(h('div', { class: 'wrap' },
    h('h1', {}, 'Marks Entry'),
    h('p', { class: 'mute' }, step === 'id' ? 'Sign in with your registered mobile number or email. We will send a one-time code.' : 'Enter the code we sent to ' + ident + '.'),
    h('div', { class: 'card' }, input, err,
      devCode && h('div', { class: 'banner small' }, 'Dev mode code: ', h('b', {}, devCode)),
      h('div', { class: 'row', style: 'margin-top:12px' },
        h('button', { class: 'btn grow', onclick: go }, step === 'id' ? 'Send code' : 'Verify & sign in'),
        step === 'code' && h('button', { class: 'btn ghost', onclick: () => viewLogin() }, 'Back')))));
}

// ---------- home ----------
async function viewHome(banner) {
  W = null; history.replaceState(null, '', location.pathname);
  const [me, scope] = await Promise.all([api('GET', '/me'), api('GET', '/scope')]);
  S.me = me.user; S.scope = scope;
  const atLimit = me.drafts.length >= me.draft_limit;
  const examCard = (e, extra) => h('div', { class: 'card' },
    h('div', { class: 'row sp' }, h('div', {}, h('b', {}, e.label), h('div', { class: 'small mute' }, e.status === 'draft' ? 'Draft' : 'Locked' + (e.locked_at ? ' · ' + fmtTs(e.locked_at) : ''))), h('div', { class: 'row' }, extra)));
  mount(h('div', { class: 'wrap' },
    h('div', { class: 'top' }, h('div', {}, h('h1', {}, 'Hi, ' + S.me.name.split(' ')[0]), h('div', { class: 'mute small' }, S.me.role === 'admin' ? 'Exam coordinator' : 'Teacher')),
      h('div', { class: 'row' },
        S.me.role === 'admin' && h('button', { class: 'pill sm on', onclick: () => viewAdmin() }, 'Admin Console'),
        h('button', { class: 'pill sm', onclick: async () => { await api('POST', '/auth/logout', {}); S.me = null; viewLogin(); } }, 'Sign out'))),
    banner && h('div', { class: 'banner bad' }, banner),
    h('button', { class: 'btn', style: 'width:100%;font-size:1.15rem;padding:18px', disabled: atLimit, id: 'new', onclick: () => viewCascade() }, '＋ New / Open Test'),
    atLimit && h('div', { class: 'banner bad' }, 'You already have 2 active drafts. Finalize & Lock or discard one below before starting a third marking sheet.'),
    me.drafts.length > 0 && h('h2', {}, 'Your drafts (' + me.drafts.length + ' / ' + me.draft_limit + ')'),
    me.drafts.map((e) => examCard(e, [
      h('button', { class: 'btn sm', onclick: () => openExam(e.id) }, 'Continue'),
      h('button', { class: 'btn sm ghost', onclick: () => discardDraft(e) }, 'Discard')])),
    h('h2', {}, 'Recent tests'),
    me.recent.length === 0 && h('p', { class: 'mute' }, 'Nothing yet.'),
    me.recent.map((e) => examCard(e, h('button', { class: 'btn sm ghost', onclick: () => openExam(e.id) }, e.status === 'draft' ? 'Open' : 'View')))));
}
async function discardDraft(e) {
  if (!confirm('Discard this draft?\n' + e.label + '\nAll marks entered in it will be deleted.')) return;
  try { await api('DELETE', '/exams/' + e.id); localStorage.removeItem('rea:stash:' + e.id); toast('Draft discarded'); viewHome(); } catch (x) { toast(x.message, true); }
}

// ---------- cascade ----------
const C = {};
function viewCascade(step = 'class') {
  if (step === 'class') Object.assign(C, { class_name: null, section: null, category: null, sub_type: null, date: todayStr(), topic: '', subjects: [], combined: false, multi: false });
  const order = ['class', 'category', 'subtype', 'date', 'subject'];
  const crumbs = [];
  if (C.class_name != null && step !== 'class') crumbs.push(C.label);
  if (C.category && !['class', 'category'].includes(step)) crumbs.push(C.category);
  if (C.sub_type && ['date', 'subject', 'config'].includes(step)) crumbs.push(C.sub_type);
  if (['subject', 'config'].includes(step)) crumbs.push(fmtDate(C.date));
  const back = () => (step === 'class' ? viewHome() : step === 'config' ? viewCascade('subject') : viewCascade(order[order.indexOf(step) - 1]));
  const wrap = (title, ...body) => mount(h('div', { class: 'wrap' },
    h('button', { class: 'back', onclick: back }, '← Back'),
    h('div', { class: 'crumbs' }, crumbs.map((c) => h('span', { class: 'crumb' }, c))),
    h('h1', {}, title), body));
  const g = () => S.scope.groups.find((x) => x.class_name === C.class_name && x.section === C.section);
  if (step === 'class') {
    if (!S.scope.groups.length) return wrap('Select class', h('div', { class: 'banner' }, 'No classes are assigned to you yet. Ask the administrator.'));
    return wrap('Select class', h('div', { class: 'pills' }, S.scope.groups.map((x) => h('button', { class: 'pill', onclick: () => { Object.assign(C, { class_name: x.class_name, section: x.section, label: x.label }); viewCascade('category'); } }, x.label))));
  }
  if (step === 'category') return wrap('Test category', h('div', { class: 'pills' }, ['Formative', 'Summative'].map((c) => h('button', { class: 'pill', onclick: () => { C.category = c; viewCascade('subtype'); } }, c))));
  if (step === 'subtype') {
    const list = C.category === 'Formative' ? S.scope.formative : S.scope.summative;
    return wrap('Which test?', h('div', { class: 'pills' }, list.map((t) => h('button', { class: 'pill', onclick: () => { C.sub_type = t; viewCascade('date'); } }, t))));
  }
  if (step === 'date') {
    const dt = h('input', { type: 'date', value: C.date, max: todayStr() });
    const topic = h('input', { placeholder: 'Topic / chapter (optional)', maxLength: 120, value: C.topic });
    const withTopic = ['Weekly Test', 'Surprise Test'].includes(C.sub_type);
    const next = () => { if (!dt.value) return toast('Pick a date', true); C.date = dt.value; C.topic = topic.value; viewCascade('subject'); };
    return wrap('Test date', h('div', { class: 'pills' },
      h('button', { class: 'pill', onclick: () => { dt.value = todayStr(); next(); } }, 'Today · ' + fmtDate(todayStr())),
      h('button', { class: 'pill', onclick: () => { dt.value = todayStr(-1); next(); } }, 'Yesterday · ' + fmtDate(todayStr(-1)))),
      h('label', { class: 'f' }, 'Or pick another date (past dates allowed)'), dt,
      withTopic && [h('label', { class: 'f' }, 'Topic'), topic],
      h('div', { style: 'margin-top:16px' }, h('button', { class: 'btn', onclick: next }, 'Continue')));
  }
  if (step === 'subject') {
    const subs = g().subjects;
    const pick = async (list, combined) => {
      Object.assign(C, { subjects: list, combined });
      try {
        const r = await api('POST', '/exams/resolve', reqBody());
        if (r.state === 'existing') { toast('Opening existing test'); return openExam(r.exam_id); }
        viewCascade('config');
      } catch (e) { toast(e.message, true); }
    };
    const sel = new Set(C.subjects);
    const render = () => {
      const multiBox = h('div');
      if (C.multi) {
        multiBox.append(h('p', { class: 'mute' }, 'Select two or more subjects for one combined test.'),
          h('button', { class: 'btn', disabled: sel.size < 2, onclick: () => pick([...sel], true) }, 'Continue with ' + sel.size + ' subjects'));
      }
      wrap('Subject', h('div', { class: 'pills' },
        subs.map((s) => h('button', { class: 'pill' + (C.multi && sel.has(s) ? ' on' : ''), onclick: () => { if (!C.multi) return pick([s], false); sel.has(s) ? sel.delete(s) : sel.add(s); render(); } }, s)),
        subs.length > 1 && h('button', { class: 'pill' + (C.multi ? ' on' : ''), style: 'border-style:dashed', onclick: () => { C.multi = !C.multi; render(); } }, '⧉ Combined multi-subject')), multiBox);
    };
    return render();
  }
  if (step === 'config') {
    let total = 20, custom = false, split = false;
    let comps = [{ name: 'Theory', max: 80 }, { name: 'Practical', max: 20 }];
    const body = h('div');
    const draw = () => {
      fill(body, 
        h('div', { class: 'card' }, h('div', { class: 'row sp' }, h('b', {}, 'CBSE split components'), h('button', { class: 'pill sm' + (split ? ' on' : ''), onclick: () => { split = !split; draw(); } }, split ? 'On' : 'Off'))),
        split ? h('div', { class: 'card' },
          comps.map((c, i) => h('div', { class: 'row', style: 'margin:6px 0' },
            h('input', { value: c.name, placeholder: 'Component', maxLength: 60, style: 'flex:2', oninput: (e) => (c.name = e.target.value) }),
            h('input', { value: num(c.max), placeholder: 'Max', inputmode: 'decimal', style: 'flex:1', oninput: (e) => { c.max = parseFloat(e.target.value) || 0; sum.textContent = 'Total: ' + comps.reduce((a, x) => a + (x.max || 0), 0); } }),
            comps.length > 2 && h('button', { class: 'btn sm ghost', onclick: () => { comps.splice(i, 1); draw(); } }, '✕'))),
          h('div', { class: 'row sp' }, h('button', { class: 'btn sm ghost', disabled: comps.length >= 8, onclick: () => { comps.push({ name: '', max: 5 }); draw(); } }, '＋ Add component'), sum = h('b', {}, 'Total: ' + comps.reduce((a, x) => a + (x.max || 0), 0))))
          : h('div', {}, h('h2', {}, 'Total max marks'), h('div', { class: 'pills' },
            [10, 20, 50, 80, 100].map((m) => h('button', { class: 'pill' + (!custom && total === m ? ' on' : ''), onclick: () => { total = m; custom = false; draw(); } }, m)),
            h('button', { class: 'pill' + (custom ? ' on' : ''), onclick: () => { custom = true; draw(); } }, 'Custom')),
            custom && h('input', { inputmode: 'decimal', placeholder: 'Max marks (e.g. 25 or 12.5)', value: num(total), oninput: (e) => (total = parseFloat(e.target.value) || 0) })));
    };
    let sum; draw();
    return wrap('Max marks', h('p', { class: 'mute' }, C.subjects.join(' + ') + (C.combined ? ' (combined)' : '')), body,
      h('div', { style: 'margin-top:18px' }, h('button', { class: 'btn', style: 'width:100%', onclick: async () => {
        const r = { ...reqBody(), split, max_total: total, components: split ? comps : [] };
        try { const out = await api('POST', '/exams', r); openExam(out.exam_id); }
        catch (e) { if (e.data && e.data.code === 'draft_limit') viewHome(e.message); else toast(e.message, true); }
      } }, 'Start grading →')));
  }
}
const reqBody = () => ({ class_name: C.class_name, section: C.section, category: C.category, sub_type: C.sub_type, date: C.date, topic: C.topic, subjects: C.subjects, combined: C.combined });

// ---------- workspace ----------
const keyOf = (sid, subj) => sid + '|' + subj;
function checkField(str, max) {
  if (str === '' || str == null) return { empty: true, ok: true };
  if (/^-/.test(str)) return { ok: false, msg: 'Marks cannot be negative' };
  if (!/^\d+\.?$|^\d+\.[05]$/.test(str)) return { ok: false, msg: 'Use whole or .5 marks only' };
  const v = parseFloat(str);
  if (v > max) return { ok: false, msg: 'Maximum is ' + max };
  return { ok: true, v };
}
async function openExam(id) {
  try {
    const d = await api('GET', '/exams/' + id);
    const ex = d.exam;
    W = { exam: ex, id, roster: d.roster, vis: d.visible_subjects, entries: {}, dirty: new Set(), pending: new Set(d.pending_corrections.map((p) => keyOf(p.student_id, p.subject))),
      sync: d.sync, locked: ex.status === 'locked', mode: window.innerWidth >= 900 ? 'table' : 'card', sort: 'roll', q: '', ungraded: false, subj: d.visible_subjects[0], studentWise: false, idx: 0, af: 0, missing: new Set(), listIds: [] };
    for (const e of d.entries) W.entries[keyOf(e.student_id, e.subject)] = { status: e.status, comps: e.comps.map(num) };
    if (!W.locked) {
      try {
        const st = JSON.parse(localStorage.getItem('rea:stash:' + id) || 'null');
        if (st) { let n = 0; for (const [k, v] of Object.entries(st)) { const [sid, ...rest] = k.split('|'); if (W.roster.some((s) => String(s.id) === sid) && W.vis.includes(rest.join('|'))) { W.entries[k] = v; W.dirty.add(k); n++; } } if (n) toast('Restored ' + n + ' unsaved mark entr' + (n === 1 ? 'y' : 'ies') + ' from this device'); }
      } catch (_) {}
    }
    history.replaceState(null, '', '#exam/' + id);
    rebuildList(); viewWorkspace();
  } catch (e) { toast(e.message, true); if (!W) viewHome(); }
}
const scopeSubjects = () => (W.exam.combined && !W.studentWise ? [W.subj] : W.vis);
function entry(sid, subj) {
  const k = keyOf(sid, subj);
  if (!W.entries[k]) W.entries[k] = { status: '', comps: W.exam.components.map(() => '') };
  return W.entries[k];
}
function entryState(sid, subj) { // 'done' | 'partial' | 'empty' | 'bad'
  const e = W.entries[keyOf(sid, subj)];
  if (!e) return 'empty';
  if (e.status) return 'done';
  let filled = 0;
  for (let i = 0; i < e.comps.length; i++) { const c = checkField(e.comps[i], W.exam.components[i].max); if (!c.ok) return 'bad'; if (!c.empty) filled++; }
  return filled === e.comps.length ? 'done' : filled ? 'partial' : 'empty';
}
const studentDone = (sid) => scopeSubjects().every((s) => entryState(sid, s) === 'done');
function stash() {
  try {
    const o = {}; for (const k of W.dirty) o[k] = W.entries[k];
    if (W.dirty.size) localStorage.setItem('rea:stash:' + W.id, JSON.stringify(o)); else localStorage.removeItem('rea:stash:' + W.id);
  } catch (_) {}
}
function touch(sid, subj) { const k = keyOf(sid, subj); W.dirty.add(k); W.missing.delete(sid); stash(); updateProgress(); }
function rebuildList() {
  let l = W.roster.slice();
  const q = W.q.trim().toLowerCase();
  if (q) l = l.filter((s) => s.roll_no.toLowerCase() === q || s.roll_no.toLowerCase().startsWith(q) || s.admission_no.toLowerCase().includes(q) || s.name.toLowerCase().includes(q));
  const cmp = { roll: (a, b) => a.roll_sort - b.roll_sort || a.name.localeCompare(b.name), adm: (a, b) => a.admission_no.localeCompare(b.admission_no, undefined, { numeric: true }), name: (a, b) => a.name.localeCompare(b.name) }[W.sort];
  l.sort(cmp);
  if (W.ungraded) l = l.filter((s) => !studentDone(s.id));
  W.listIds = l.map((s) => s.id);
  W.idx = Math.min(W.idx, W.listIds.length);
}
const stById = (id) => W.roster.find((s) => s.id === id);
function fieldsFor(sid) { // flat list of {subj, ci}
  const f = []; for (const s of scopeSubjects()) W.exam.components.forEach((c, ci) => f.push({ subj: s, ci, comp: c }));
  return f;
}
function updateProgress() {
  const el = document.getElementById('prog'); if (!el) return;
  const done = W.roster.filter((s) => studentDone(s.id)).length;
  el.textContent = done + ' / ' + W.roster.length + ' Graded';
  const sv = document.getElementById('savebtn'); if (sv) sv.textContent = W.dirty.size ? 'Save Draft (' + W.dirty.size + ')' : 'Save Draft';
}
function viewWorkspace() {
  const ex = W.exam;
  const title = groupLabelJS(ex) + ' • ' + ex.sub_type + ' • ' + (ex.combined ? ex.subjects.join(' + ') : ex.subjects[0]) + ' • ' + fmtDate(ex.date);
  const seg = h('div', { class: 'seg' },
    h('button', { class: W.mode === 'card' ? 'on' : '', 'aria-label': 'Flashcard view', onclick: () => { W.mode = 'card'; rebuildList(); viewWorkspace(); } }, '🗂️', h('span', { class: 'lg' }, ' Cards')),
    h('button', { class: W.mode === 'table' ? 'on' : '', 'aria-label': 'Table view', onclick: () => { W.mode = 'table'; rebuildList(); viewWorkspace(); } }, '📋', h('span', { class: 'lg' }, ' Table')));
  const ctx = h('div', { class: 'ctx' },
    h('div', { class: 'row sp' }, h('button', { class: 'back', style: 'color:inherit;padding:0', onclick: goHome }, '←'), h('div', { class: 't grow', title }, title), h('div', { class: 'p', id: 'prog' })),
    h('div', { class: 'row sp', style: 'margin-top:8px' }, seg,
      h('div', { class: 'row' },
        h('button', { class: 'pill sm', 'aria-label': 'Timeline', style: 'background:var(--brand-ink);color:var(--brand);border:0', onclick: () => showTimeline() }, '🕘', h('span', { class: 'lg' }, ' Timeline')),
        W.locked ? h('span', { class: 'badge', style: 'background:var(--brand-ink);color:var(--brand)' }, '🔒 Locked') : [
          h('button', { class: 'pill sm', id: 'savebtn', style: 'background:var(--brand-ink);color:var(--brand);border:0', onclick: () => saveDraft(false) }, 'Save Draft'),
          h('button', { class: 'pill sm', style: 'background:#fff;color:#000;border:0', onclick: finalize }, 'Finalize & Lock')])));
  const qIn = h('input', { type: 'search', placeholder: 'Jump to roll no. / admission no. / name', value: W.q, oninput: (e) => { W.q = e.target.value; W.idx = 0; rebuildList(); renderBody(); } });
  const sortSel = h('select', { class: 'sortsel', onchange: (e) => { W.sort = e.target.value; rebuildList(); renderBody(); }, 'aria-label': 'Sort order' },
    [['roll', 'Roll No.'], ['adm', 'Admission No.'], ['name', 'A–Z']].map(([v, l]) => h('option', { value: v, selected: W.sort === v }, l)));
  const skip = h('button', { class: 'pill sm' + (W.ungraded ? ' on' : ''), id: 'skip', onclick: () => { W.ungraded = !W.ungraded; W.idx = 0; rebuildList(); document.getElementById('skip').classList.toggle('on', W.ungraded); renderBody(); } }, h('span', { class: 'lg' }, 'Show Ungraded Only'), h('span', { class: 'sh' }, 'Ungraded'));
  const tools = h('div', { class: 'toolbar' }, qIn, sortSel, skip);
  if (ex.combined && W.vis.length > 0) {
    tools.append(h('button', { class: 'pill sm' + (W.studentWise ? ' on' : ''), onclick: () => { W.studentWise = !W.studentWise; rebuildList(); viewWorkspace(); } }, W.studentWise ? 'Student-wise' : 'Subject-wise'));
    if (!W.studentWise) W.vis.forEach((s) => tools.append(h('button', { class: 'pill sm' + (W.subj === s ? ' on' : ''), onclick: () => { W.subj = s; W.af = 0; rebuildList(); viewWorkspace(); } }, s)));
  }
  mount(ctx, tools, h('div', { id: 'body' }), h('div', { id: 'pad' }));
  renderBody(); updateProgress();
}
const groupLabelJS = (ex) => (ex.section ? ex.class_name + '-' + ex.section : ex.class_name);
async function goHome() {
  if (W && W.dirty.size && !confirm('You have unsaved marks (kept safely on this device). Leave anyway?')) return;
  viewHome();
}
function renderBody() { W.mode === 'card' ? renderCard() : renderTable(); }

// ---- flashcard ----
function renderCard() {
  const body = document.getElementById('body'), pad = document.getElementById('pad');
  const ids = W.listIds;
  if (ids.length === 0) { fill(pad, ); return fill(body, h('div', { class: 'deck' }, h('div', { class: 'card' }, W.ungraded ? '🎉 Nobody left ungraded.' : 'No student matches your search.'))); }
  if (W.idx >= ids.length) {
    const left = W.roster.filter((s) => !studentDone(s.id)).length;
    fill(pad, );
    return fill(body, h('div', { class: 'deck', style: 'padding-bottom:40px' }, h('div', { class: 'sc' },
      h('div', { class: 'nm' }, 'End of list'), h('p', { class: 'mute' }, left ? left + ' student(s) still ungraded.' : 'Everyone is accounted for.'),
      h('div', { class: 'row', style: 'justify-content:center' },
        left > 0 && h('button', { class: 'btn', onclick: () => { W.ungraded = true; W.idx = 0; rebuildList(); viewWorkspace(); } }, 'Show ungraded only'),
        h('button', { class: 'btn ghost', onclick: () => { W.idx = 0; renderCard(); } }, 'Back to first')))));
  }
  const s = stById(ids[W.idx]);
  const fields = fieldsFor(s.id);
  W.af = Math.min(W.af, fields.length - 1);
  const dim = scopeSubjects().every((sub) => entry(s.id, sub).status) && !W.locked ? ' dim' : '';
  const stat = scopeSubjects().length === 1 ? entry(s.id, scopeSubjects()[0]).status : '';
  const pendingKey = scopeSubjects().some((sub) => W.pending.has(keyOf(s.id, sub)));
  const rows = fields.map((f, i) => {
    const e = entry(s.id, f.subj), val = e.comps[f.ci], chk = checkField(val, f.comp.max);
    const label = [W.exam.combined ? f.subj : '', W.exam.split ? f.comp.name : ''].filter(Boolean).join(' · ');
    const statusFor = e.status;
    return h('div', {}, h('div', { class: 'field' }, label && h('span', { class: 'lbl' }, label),
      h('div', { class: 'box' + (i === W.af && !W.locked ? ' act' : '') + (!chk.ok ? ' err' : ''), onclick: () => { if (!W.locked) { W.af = i; renderCard(); } } },
        statusFor ? statusFor : (val === '' ? '—' : val), h('span', { class: 'den' }, '  / ' + f.comp.max))),
      !chk.ok && h('div', { class: 'err-msg' }, chk.msg));
  });
  const cardPrev = h('div', { class: 'row', style: 'justify-content:center;margin-top:8px' });
  const card = h('div', { class: 'sc' + dim },
    h('div', { class: 'small mute' }, (W.idx + 1) + ' of ' + ids.length), h('div', { class: 'roll' }, 'Roll ' + s.roll_no), h('div', { class: 'nm' }, s.name), h('div', { class: 'adm' }, 'Adm. ' + s.admission_no), rows,
    !W.locked && W.exam.combined && W.studentWise ? null : null,
    !W.locked && h('div', { class: 'qs' },
      ['AB', 'EX'].map((st) => h('button', { class: 'pill' + (stat === st ? ' on' : ''), onclick: () => setStatus(s.id, st, true) }, '[ ' + st + ' ]'))),
    W.locked && (pendingKey ? h('div', { class: 'banner' }, 'Correction request pending approval') : h('button', { class: 'btn', style: 'margin-top:12px', onclick: () => correctionModal(s) }, 'Request Correction Unlock')));
  fill(body, h('div', { class: 'deck' }, card));
  if (W.locked) return fill(pad, h('div', { class: 'foot' }, h('button', { class: 'btn ghost', onclick: () => stepCard(-1) }, '← Prev'), h('button', { class: 'btn', onclick: () => stepCard(1) }, 'Next →')));
  const key = (label, fn, cls) => h('button', { class: 'k ' + (cls || ''), onclick: fn }, label);
  const last = W.af >= fields.length - 1;
  fill(pad, h('div', { class: 'pad' },
    h('div', { class: 'grid' }, ['7', '8', '9', '4', '5', '6', '1', '2', '3'].map((d) => key(d, () => typeKey(d))), key('.5', () => typeKey('.5')), key('0', () => typeKey('0')), key('⌫', () => typeKey('⌫'))),
    h('div', { class: 'next' }, h('button', { class: 'btn ghost', style: 'flex:0 0 90px', onclick: () => stepCard(-1) }, '←'),
      h('button', { class: 'btn', id: 'nextbtn', onclick: advance }, last ? 'Next Student →' : 'Next Field →'))));
}
function stepCard(d) { W.idx = Math.max(0, Math.min(W.listIds.length, W.idx + d)); W.af = 0; renderCard(); }
function typeKey(k) {
  const s = stById(W.listIds[W.idx]); if (!s) return;
  const f = fieldsFor(s.id)[W.af]; const e = entry(s.id, f.subj);
  if (e.status) { e.status = ''; }
  let v = e.comps[f.ci];
  if (k === '⌫') v = v.slice(0, -1);
  else if (k === '.5') { if (v.includes('.')) return; v = (v || '0') + '.5'; }
  else { if (v.length >= 6 || v.includes('.')) return; v = v === '0' ? k : v + k; }
  e.comps[f.ci] = v; touch(s.id, f.subj); renderCard();
}
function advance() {
  const s = stById(W.listIds[W.idx]); if (!s) return;
  const fields = fieldsFor(s.id);
  for (const f of fields) { const c = checkField(entry(s.id, f.subj).comps[f.ci], f.comp.max); if (!c.ok) { toast(c.msg, true); return; } }
  if (W.af < fields.length - 1) { W.af++; return renderCard(); }
  W.idx++; W.af = 0; renderCard();
}
function setStatus(sid, st, advanceAfter) {
  for (const sub of scopeSubjects()) {
    const e = entry(sid, sub);
    e.status = e.status === st ? '' : st;
    if (e.status) e.comps = e.comps.map(() => '');
    touch(sid, sub);
  }
  if (advanceAfter && scopeSubjects().every((sub) => entry(sid, sub).status)) { W.idx++; W.af = 0; }
  renderCard();
}

// ---- table ----
function renderTable() {
  const body = document.getElementById('body'), pad = document.getElementById('pad'); fill(pad, );
  const subs = scopeSubjects(), comps = W.exam.components;
  const head = h('tr', {}, h('th', {}, 'Roll'), h('th', {}, 'Admission'), h('th', {}, 'Student'),
    subs.flatMap((s) => comps.map((c) => h('th', {}, [W.exam.combined ? s : '', W.exam.split ? c.name : (W.exam.combined ? '' : 'Score')].filter(Boolean).join(' · '), ' /' + c.max))), h('th', {}, W.locked ? '' : 'Status'));
  const rows = W.listIds.map((id) => tableRow(stById(id)));
  fill(body, h('div', { class: 'tw' }, h('table', { class: 't', id: 'tbl' }, h('thead', {}, head), h('tbody', {}, rows)),
    rows.length === 0 && h('p', { class: 'mute', style: 'padding:16px' }, W.ungraded ? '🎉 Nobody left ungraded.' : 'No student matches your search.')));
}
function tableRow(s) {
  const subs = scopeSubjects(), comps = W.exam.components;
  const dimmed = subs.every((x) => entry(s.id, x).status);
  const tr = h('tr', { 'data-sid': s.id, class: (dimmed ? 'dim ' : '') + (W.missing.has(s.id) ? 'miss ' : '') + (W.locked ? 'click' : '') });
  const cells = [h('td', {}, s.roll_no), h('td', {}, s.admission_no), h('td', {}, h('b', {}, s.name), subs.some((x) => W.pending.has(keyOf(s.id, x))) && h('span', { class: 'badge', style: 'margin-left:6px' }, 'correction pending'))];
  for (const sub of subs) comps.forEach((c, ci) => {
    const e = entry(s.id, sub), chk = checkField(e.comps[ci], c.max);
    const inp = h('input', { class: 'sc-in' + (chk.ok ? '' : ' bad'), value: e.status || e.comps[ci], disabled: W.locked || !!e.status, inputmode: 'decimal', 'data-col': subs.indexOf(sub) * comps.length + ci, 'aria-label': s.name + ' ' + sub + ' ' + c.name, autocomplete: 'off',
      oninput: (ev) => { e.comps[ci] = ev.target.value.trim(); const k = checkField(e.comps[ci], c.max); ev.target.classList.toggle('bad', !k.ok); ev.target.title = k.msg || ''; touch(s.id, sub); },
      onkeydown: (ev) => tableNav(ev, inp) });
    cells.push(h('td', {}, inp));
  });
  cells.push(h('td', {}, W.locked ? null : ['AB', 'EX'].map((st) => h('button', { class: 'st' + (subs.every((x) => entry(s.id, x).status === st) ? ' on' : ''), onclick: () => { setStatus(s.id, st, false); const ntr = tableRow(s); tr.replaceWith(ntr); } }, st))));
  tr.append(...cells);
  if (W.locked) tr.addEventListener('click', () => (subs.some((x) => W.pending.has(keyOf(s.id, x))) ? toast('A correction is already pending for this student') : correctionModal(s)));
  return tr;
}
function tableNav(ev, inp) {
  const tr = inp.closest('tr'); const col = inp.dataset.col;
  const go = (row) => { if (!row) return; const t = row.querySelector('input[data-col="' + col + '"]:not(:disabled)') || row.querySelector('input:not(:disabled)'); if (t) { t.focus(); t.select(); } };
  if (ev.key === 'Enter' || ev.key === 'ArrowDown') { ev.preventDefault(); go(tr.nextElementSibling); }
  else if (ev.key === 'ArrowUp') { ev.preventDefault(); go(tr.previousElementSibling); }
  else if (ev.key === 'ArrowRight' && inp.selectionStart === inp.value.length) { const n = tr.querySelector('input[data-col="' + (+col + 1) + '"]'); if (n) { ev.preventDefault(); n.focus(); n.select(); } }
  else if (ev.key === 'ArrowLeft' && inp.selectionStart === 0) { const n = tr.querySelector('input[data-col="' + (+col - 1) + '"]'); if (n) { ev.preventDefault(); n.focus(); n.select(); } }
}

// ---- commit ----
function payloadFor(keys) {
  const out = [];
  for (const k of keys) {
    const i = k.indexOf('|'); const sid = +k.slice(0, i), subject = k.slice(i + 1); const e = W.entries[k];
    out.push({ student_id: sid, subject, status: e.status, comps: e.comps.map((c) => (c === '' ? null : parseFloat(c))) });
  }
  return out;
}
async function saveDraft(silent) {
  if (W.locked) return true;
  if (!W.dirty.size) { if (!silent) toast('Nothing new to save'); return true; }
  for (const k of W.dirty) { const sid = +k.split('|')[0]; const sub = k.slice(k.indexOf('|') + 1); if (entryState(sid, sub) === 'bad') { toast('Fix the red entries before saving', true); return false; } }
  const keys = [...W.dirty];
  try {
    const r = await api('PUT', '/exams/' + W.id + '/marks', { entries: payloadFor(keys) });
    keys.forEach((k) => W.dirty.delete(k)); stash();
    if (!silent) toast('Draft saved (' + r.saved + ' entries)'); updateProgress(); return true;
  } catch (e) { toast(e.offline ? 'Offline — marks are safe on this device. Try again shortly.' : e.message, true); return false; }
}
async function finalize() {
  if (!(await saveDraft(true))) return;
  const left = W.roster.filter((s) => !W.vis.every((sub) => entryState(s.id, sub) === 'done'));
  if (left.length && !W.exam.combined) return flagMissing(left.map((s) => s.id));
  if (!confirm('Finalize & Lock this marking sheet?\nIt cannot be edited afterwards; only single-student correction requests are possible.')) return;
  try {
    const r = await api('POST', '/exams/' + W.id + '/finalize', {});
    toast('Locked ✔  ' + r.stats.graded + ' graded, ' + r.stats.absent + ' absent, ' + r.stats.exempt + ' exempt');
    openExam(W.id);
  } catch (e) {
    if (e.data && e.data.missing) flagMissing([...new Set(e.data.missing.map((m) => m.student_id))]);
    toast(e.message, true);
  }
}
function flagMissing(ids) {
  W.missing = new Set(ids); W.ungraded = true; W.idx = 0;
  const first = W.vis.find((sub) => ids.some((id) => entryState(id, sub) !== 'done'));
  if (W.exam.combined && !W.studentWise) W.studentWise = true;
  rebuildList(); W.listIds = ids.slice().sort((a, b) => stById(a).roll_sort - stById(b).roll_sort); viewWorkspace();
  toast(ids.length + ' student(s) not yet accounted for — highlighted below', true);
}

// ---- timeline & corrections ----
function overlay(content) {
  const ov = h('div', { class: 'ov', onclick: (e) => e.target === ov && ov.remove() }, h('div', { class: 'sheet' }, content));
  document.body.append(ov); return ov;
}
const kindText = {
  created: 'Test created', draft_saved: 'Initial draft saved', draft_updated: 'Draft updated', locked: 'Finalized & locked', discarded: 'Draft discarded',
  erp_synced: 'Synced to Frappe Education', correction_requested: 'Correction requested', correction_approved: 'Correction approved', correction_rejected: 'Correction rejected', correction_synced: 'Correction re-synced to ERP' };
function eventDetail(ev) {
  const d = ev.detail || {};
  switch (ev.kind) {
    case 'created': return (d.subjects || []).join(', ') + ' · max ' + d.max_total + (d.split ? ' (split: ' + d.components.map((c) => c.name + ' ' + c.max).join(' + ') + ')' : '');
    case 'draft_saved': case 'draft_updated': return d.entries_total + ' entries stored';
    case 'locked': return d.graded + ' graded · ' + d.absent + ' AB · ' + d.exempt + ' EX' + (d.average != null ? ' · avg ' + d.average + ' / ' + d.max_total : '');
    case 'correction_requested': return 'Roll ' + d.roll_no + ' ' + d.student + ' (' + d.subject + '): ' + d.old + ' → ' + d.new + ' — “' + d.reason + '”';
    case 'correction_approved': case 'correction_rejected': return 'Roll ' + d.roll_no + ' ' + d.student + ': ' + d.old + ' → ' + d.new + (d.note ? ' — ' + d.note : '');
    case 'correction_synced': return 'Roll ' + d.roll_no + ' ' + d.student + ' · ERP doc ' + d.erp_document;
    case 'erp_synced': return d.message;
    default: return '';
  }
}
async function showTimeline(examId) {
  const id = typeof examId === 'number' ? examId : W.id;
  try {
    const d = await api('GET', '/exams/' + id + '/timeline');
    overlay([h('div', { class: 'row sp' }, h('h2', { style: 'margin:0' }, 'Timeline'), h('button', { class: 'btn sm ghost', onclick: (e) => e.target.closest('.ov').remove() }, 'Close')), h('p', { class: 'mute small' }, d.label),
      h('div', { class: 'tl' }, d.events.map((ev) => h('div', { class: 'ev' }, h('b', {}, kindText[ev.kind] || ev.kind), h('div', { class: 'small mute' }, ev.user + ' · ' + fmtTs(ev.ts)), h('div', { class: 'small' }, eventDetail(ev))))) ]);
  } catch (e) { toast(e.message, true); }
}
function correctionModal(s) {
  const subs = W.vis.filter((x) => !W.pending.has(keyOf(s.id, x)));
  if (!subs.length) return toast('A correction is already pending for this student');
  let subj = subs[0], status = '';
  const body = h('div'), err = h('div', { class: 'err-msg' });
  let vals = [], reason = h('textarea', { rows: 3, placeholder: 'Reason / evidence (mandatory)', maxLength: 1000 });
  const draw = () => {
    const cur = entry(s.id, subj), comps = W.exam.components;
    vals = comps.map(() => '');
    const inputs = comps.map((c, i) => h('div', { class: 'row', style: 'margin:6px 0' }, h('span', { style: 'min-width:90px' }, W.exam.split ? c.name : 'Score'),
      h('input', { inputmode: 'decimal', placeholder: '/ ' + c.max, value: vals[i], disabled: !!status, oninput: (e) => { vals[i] = e.target.value.trim(); const k = checkField(vals[i], c.max); e.target.classList.toggle('bad', !k.ok); err.textContent = k.msg || ''; } })));
    fill(body, 
      h('p', {}, h('b', {}, 'Roll ' + s.roll_no + ' · ' + s.name)),
      subs.length > 1 && h('div', { class: 'pills' }, subs.map((x) => h('button', { class: 'pill sm' + (x === subj ? ' on' : ''), onclick: () => { subj = x; draw(); } }, x))),
      h('div', { class: 'banner' }, 'Current locked score: ', h('b', {}, cur.status || cur.comps.join(' + ') + (cur.comps.length > 1 ? ' = ' + cur.comps.reduce((a, b) => a + parseFloat(b || 0), 0) : ''))),
      h('div', { class: 'pills' }, [['', 'Score'], ['AB', 'AB'], ['EX', 'EX']].map(([v, l]) => h('button', { class: 'pill sm' + (status === v ? ' on' : ''), onclick: () => { status = v; draw(); } }, l))),
      h('label', { class: 'f' }, 'Proposed new score'), inputs, err, h('label', { class: 'f' }, 'Reason / evidence note'), reason);
  };
  draw();
  const ov = overlay([h('h2', { style: 'margin-top:0' }, 'Request Correction Unlock'), body,
    h('div', { class: 'row', style: 'margin-top:14px' }, h('button', { class: 'btn grow', onclick: async () => {
      const comps = vals.map((v) => (v === '' ? null : parseFloat(v)));
      for (let i = 0; i < comps.length && !status; i++) { const k = checkField(vals[i], W.exam.components[i].max); if (!k.ok) return (err.textContent = k.msg); if (k.empty) return (err.textContent = 'Enter the proposed score'); }
      try { await api('POST', '/exams/' + W.id + '/corrections', { student_id: s.id, subject: subj, status, comps: status ? [] : comps, reason: reason.value });
        W.pending.add(keyOf(s.id, subj)); ov.remove(); toast('Correction request sent to the administrator'); renderBody(); }
      catch (e) { err.textContent = e.message; }
    } }, 'Submit request'), h('button', { class: 'btn ghost', onclick: () => ov.remove() }, 'Cancel'))]);
}

// ---------- admin ----------
async function viewAdmin(tab = 'overview') {
  W = null; history.replaceState(null, '', location.pathname);
  const tabs = [['overview', 'Overview & Sync'], ['marks', 'Marks Explorer'], ['corrections', 'Corrections'], ['users', 'Teachers & Allocations']];
  const shell = h('div', { class: 'wrap wide' }, h('div', { class: 'top' }, h('h1', {}, 'Admin Console'), h('button', { class: 'pill sm', onclick: () => viewHome() }, '← Marking')),
    h('div', { class: 'tabs' }, tabs.map(([k, l]) => h('button', { class: 'pill' + (k === tab ? ' on' : ''), onclick: () => viewAdmin(k) }, l))));
  const content = h('div'); shell.append(content); mount(shell);
  try { await ({ overview: adminOverview, marks: adminMarks, corrections: adminCorrections, users: adminUsers }[tab])(content); } catch (e) { fill(content, h('div', { class: 'banner bad' }, e.message)); }
}
async function adminOverview(el) {
  const o = await api('GET', '/admin/overview'); const c = o.counts;
  const res = h('div');
  let last = o.last_master_result; try { const j = JSON.parse(last); last = j.students_seen + ' students in ' + j.groups + ' groups (' + j.students_added + ' new, ' + j.students_updated + ' updated, ' + j.students_retired + ' retired), ' + j.courses + ' courses, ' + (j.duration_ms) + ' ms'; } catch (_) {}
  const btn = h('button', { class: 'btn', onclick: async () => { btn.disabled = true; btn.textContent = 'Syncing…'; try { const r = await api('POST', '/admin/sync/master', {}); toast('Roster synced in ' + r.duration_ms + ' ms'); viewAdmin('overview'); } catch (e) { toast(e.message, true); btn.disabled = false; btn.textContent = 'Sync Master Roster Now'; } } }, 'Sync Master Roster Now');
  add(el, h('div', { class: 'grid2' }, [['Students', c.students], ['Teachers', c.teachers], ['Drafts', c.drafts], ['Locked tests', c.locked], ['Pending corrections', c.pending_corrections], ['Sync queue', c.pending_jobs]].map(([l, v]) => h('div', { class: 'stat' }, h('b', {}, v), h('span', { class: 'mute small' }, l)))),
    h('div', { class: 'card' }, h('h2', { style: 'margin-top:0' }, 'Frappe Education sync'),
      !o.frappe_configured && h('div', { class: 'banner bad' }, 'Frappe is not configured (FRAPPE_URL / FRAPPE_API_KEY / FRAPPE_API_SECRET). Marks are safe in SQLite and will sync once it is.'),
      h('p', { class: 'mute' }, 'Automatic pull every 7 days. Last sync: ', h('b', {}, o.last_master_sync ? fmtTs(o.last_master_sync) : 'never'), h('br'), last), btn, res),
    h('div', { class: 'card' }, h('b', {}, 'Sync health legend'), h('p', { class: 'small' }, '🟡 Saved Locally (Draft) · 🟢 Locked & Synced to ERP · 🔴 Sync Pending (Auto-Retrying)')));
}
async function adminMarks(el) {
  const scope = await api('GET', '/scope');
  const fc = h('select', {}, h('option', { value: '' }, 'All classes'), scope.groups.map((g) => h('option', { value: g.label }, g.label)));
  const fs = h('select', {}, [['', 'All statuses'], ['draft', 'Drafts'], ['locked', 'Locked']].map(([v, l]) => h('option', { value: v }, l)));
  const out = h('div');
  const load = async () => {
    const d = await api('GET', '/admin/exams?class=' + encodeURIComponent(fc.value) + '&status=' + fs.value);
    fill(out, h('table', { class: 'tbl' }, h('thead', {}, h('tr', {}, ['Test', 'Teacher', 'Progress', 'Status', 'Sync health', ''].map((x) => h('th', {}, x)))),
      h('tbody', {}, d.exams.map((r) => h('tr', {}, h('td', {}, r.exam.label), h('td', {}, r.exam.owner_name), h('td', {}, r.graded + ' / ' + r.total), h('td', {}, r.exam.status), h('td', {}, syncBadge(r.sync)),
        h('td', {}, h('button', { class: 'btn sm ghost', onclick: () => showTimeline(r.exam.id) }, 'Timeline'), ' ', h('button', { class: 'btn sm ghost', onclick: () => openExam(r.exam.id) }, 'Open')))))),
      d.exams.length === 0 && h('p', { class: 'mute' }, 'No tests match.'));
  };
  fc.onchange = fs.onchange = load;
  add(el, h('div', { class: 'row' }, fc, fs, h('button', { class: 'btn sm ghost', onclick: load }, '↻ Refresh')), out); await load();
}
async function adminCorrections(el) {
  const d = await api('GET', '/admin/corrections');
  const decide = (c, action) => {
    const note = h('input', { placeholder: 'Optional note' });
    const ov = overlay([h('h2', { style: 'margin-top:0' }, (action === 'approve' ? 'Approve' : 'Reject') + ' correction'), h('p', {}, 'Roll ' + c.roll_no + ' · ' + c.student + ': ', h('b', {}, c.old + ' → ' + c.new)), note,
      h('div', { class: 'row', style: 'margin-top:12px' }, h('button', { class: 'btn grow' + (action === 'reject' ? ' danger' : ''), onclick: async () => { try { await api('POST', '/admin/corrections/' + c.id + '/' + action, { note: note.value }); ov.remove(); toast(action === 'approve' ? 'Approved — re-syncing to ERP' : 'Rejected'); viewAdmin('corrections'); } catch (e) { toast(e.message, true); } } }, 'Confirm'), h('button', { class: 'btn ghost', onclick: () => ov.remove() }, 'Cancel'))]);
  };
  add(el, h('h2', {}, 'Correction requests inbox'), d.corrections.length === 0 && h('p', { class: 'mute' }, 'No requests.'),
    d.corrections.map((c) => h('div', { class: 'card' }, h('div', { class: 'row sp' }, h('div', {}, h('b', {}, 'Roll ' + c.roll_no + ' · ' + c.student + ' (Adm. ' + c.admission_no + ')'), h('div', { class: 'small mute' }, c.label + ' · ' + c.subject)),
      h('div', { style: 'font-size:1.4rem;font-weight:800' }, c.old + ' → ' + c.new)),
      h('p', {}, '“' + c.reason + '”'), h('div', { class: 'small mute' }, 'Requested by ' + c.requested_by + ' · ' + fmtTs(c.requested_at)),
      c.state === 'pending' ? h('div', { class: 'row', style: 'margin-top:8px' }, h('button', { class: 'btn sm', onclick: () => decide(c, 'approve') }, 'Approve'), h('button', { class: 'btn sm danger', onclick: () => decide(c, 'reject') }, 'Reject'), h('button', { class: 'btn sm ghost', onclick: () => showTimeline(c.exam_id) }, 'Timeline'))
        : h('div', { class: 'small' }, h('span', { class: 'badge' }, c.state), ' by ' + c.decided_by + ' · ' + fmtTs(c.decided_at)))));
}
async function adminUsers(el) {
  const [d, scope] = await Promise.all([api('GET', '/admin/users'), api('GET', '/scope')]);
  const name = h('input', { placeholder: 'Full name' }), mob = h('input', { placeholder: 'Mobile (10 digits)', inputmode: 'numeric' }), em = h('input', { placeholder: 'Email', type: 'email' });
  const role = h('select', {}, h('option', { value: 'teacher' }, 'Teacher'), h('option', { value: 'admin' }, 'Administrator / Exam coordinator'));
  const err = h('div', { class: 'err-msg' });
  add(el, h('div', { class: 'card' }, h('b', {}, 'Add teacher'), h('div', { class: 'grid2', style: 'margin-top:8px' }, name, mob, em, role), err,
    h('button', { class: 'btn', style: 'margin-top:10px', onclick: async () => { try { await api('POST', '/admin/users', { name: name.value, mobile: mob.value, email: em.value, role: role.value }); toast('User added'); viewAdmin('users'); } catch (e) { err.textContent = e.message; } } }, 'Add')),
    d.users.map((u) => {
      const holder = h('div');
      return h('div', { class: 'card' }, h('div', { class: 'row sp' }, h('div', {}, h('b', {}, u.name), ' ', h('span', { class: 'badge' }, u.role), !u.active && h('span', { class: 'badge red' }, 'inactive'), h('div', { class: 'small mute' }, [u.mobile, u.email].filter(Boolean).join(' · ') + ' · ' + u.allocations + ' allocations')),
        h('div', { class: 'row' }, u.role === 'teacher' && h('button', { class: 'btn sm ghost', onclick: () => allocEditor(holder, u, scope) }, 'Classes & subjects'),
          h('button', { class: 'btn sm ghost', onclick: async () => { try { await api('PUT', '/admin/users/' + u.id, { ...u, active: !u.active }); viewAdmin('users'); } catch (e) { toast(e.message, true); } } }, u.active ? 'Deactivate' : 'Activate'))), holder);
    }));
}
async function allocEditor(holder, u, scope) {
  if (holder.childNodes.length) return fill(holder, );
  const cur = (await api('GET', '/admin/users/' + u.id + '/allocations')).allocations;
  const sel = new Set(cur.map((a) => a.class_name + '\x00' + a.section + '\x00' + a.subject));
  add(holder, h('div', { style: 'margin-top:10px' }, scope.groups.map((g) => {
    const box = h('div', { class: 'chips', style: 'margin-top:8px' });
    const drawChips = () => fill(box, g.subjects.map((s) => { const k = g.class_name + '\x00' + g.section + '\x00' + s; return h('button', { class: 'chip' + (sel.has(k) ? ' on' : ''), onclick: () => { sel.has(k) ? sel.delete(k) : sel.add(k); drawChips(); } }, s); }));
    drawChips();
    return h('details', { open: g.subjects.some((s) => sel.has(g.class_name + '\x00' + g.section + '\x00' + s)) }, h('summary', {}, g.label), box);
  }), h('button', { class: 'btn', style: 'margin-top:10px', onclick: async () => {
    const allocations = [...sel].map((k) => { const [class_name, section, subject] = k.split('\x00'); return { class_name, section, subject }; });
    try { await api('PUT', '/admin/users/' + u.id + '/allocations', { allocations }); toast('Allocations saved'); viewAdmin('users'); } catch (e) { toast(e.message, true); } } }, 'Save allocations')));
}

// ---------- boot ----------
async function boot() {
  try {
    const me = await api('GET', '/me'); S.me = me.user;
    const m = location.hash.match(/^#exam\/(\d+)/);
    S.scope = await api('GET', '/scope');
    if (m) return openExam(+m[1]);
    viewHome();
  } catch (e) { viewLogin(); }
}
window.addEventListener('beforeunload', (e) => { if (W && W.dirty && W.dirty.size) { stash(); } });
boot();
