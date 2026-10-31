'use strict';
/* GCM UPMARK: marks entry, approvals and parents' weekly report. Single-page app (no build step; embedded in
   the Go binary). Teachers, administrators and parents share one shell styled as "Bold School". */
const $app = document.getElementById('app');
const S = { me: null, data: null, counts: null, q: '', banner: '', notes: { notifications: [], unread: 0 }, offline: !navigator.onLine };
let W = null; // active grading workspace

// ---------- theme ----------
const store = { get: (k) => { try { return localStorage.getItem(k); } catch (_) { return null; } }, set: (k, v) => { try { v == null ? localStorage.removeItem(k) : localStorage.setItem(k, v); } catch (_) {} } };
function applyTheme(t) { if (t === 'light' || t === 'dark') document.documentElement.dataset.theme = t; else delete document.documentElement.dataset.theme; }
applyTheme(store.get('rea:theme'));
function cycleTheme() {
  const order = ['auto', 'light', 'dark'], cur = store.get('rea:theme') || 'auto', nx = order[(order.indexOf(cur) + 1) % 3];
  store.set('rea:theme', nx === 'auto' ? null : nx); applyTheme(nx);
  toast({ auto: 'Theme follows your phone', light: 'Light theme', dark: 'Dark theme' }[nx]);
}

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
const clean = (kids) => kids.flat(Infinity).filter((k) => k != null && k !== false);
const add = (el, ...kids) => el.append(...clean(kids));
const fill = (el, ...kids) => el.replaceChildren(...clean(kids));
function mount(...nodes) { document.body.classList.remove('ws'); $app.replaceChildren(...clean(nodes)); window.scrollTo(0, 0); }
let toastT;
function toast(msg, bad) {
  const t = document.getElementById('toast');
  t.textContent = msg; t.className = 'show' + (bad ? ' bad' : '');
  clearTimeout(toastT); toastT = setTimeout(() => (t.className = ''), bad ? 4500 : 2400);
}
async function api(method, path, body) {
  const opt = { method, headers: { 'X-Requested-With': 'rea' }, credentials: 'same-origin' };
  if (body !== undefined) { opt.headers['Content-Type'] = 'application/json'; opt.body = JSON.stringify(body); }
  let res;
  try { res = await fetch('/api' + path, opt); } catch (e) { const err = new Error('You are offline'); err.offline = true; throw err; }
  let data = {};
  try { data = await res.json(); } catch (_) {}
  if (!res.ok) {
    if (res.status === 401 && S.me) { S.me = null; viewLogin(); }
    const err = new Error(data.error || 'Request failed'); err.status = res.status; err.data = data; throw err;
  }
  return data;
}
// cachedGet keeps the last good answer on this device so screens still open without a connection.
async function cachedGet(path) {
  try { const d = await api('GET', path); store.set('rea:get:' + path, JSON.stringify(d)); return d; }
  catch (e) { const c = e.offline && store.get('rea:get:' + path); if (c) { S.offline = true; return JSON.parse(c); } throw e; }
}
const todayStr = (off = 0) => { const d = new Date(); d.setDate(d.getDate() + off); return d.getFullYear() + '-' + String(d.getMonth() + 1).padStart(2, '0') + '-' + String(d.getDate()).padStart(2, '0'); };
const fmtDate = (s) => { const [y, m, d] = s.split('-').map(Number); return new Date(y, m - 1, d).toLocaleDateString(undefined, { day: 'numeric', month: 'short' }); };
const fmtTs = (s) => (s ? new Date(s).toLocaleString(undefined, { day: 'numeric', month: 'short', hour: '2-digit', minute: '2-digit' }) : '');
const ago = (s) => { const m = Math.round((Date.now() - new Date(s)) / 60000); return m < 1 ? 'just now' : m < 60 ? m + 'm ago' : m < 1440 ? Math.round(m / 60) + 'h ago' : Math.round(m / 1440) + 'd ago'; };
const num = (v) => (v == null ? '' : String(v));
const label = (c, s) => (s ? c + '-' + s : c);
const shortCls = (c, s) => label(String(c).replace(/^(grade|class)\s*/i, ''), s);
const syncBadge = (s) => h('span', { class: 'badge ' + s.state, title: s.last_error || '' }, s.label);
const isAdmin = () => S.me && S.me.role === 'admin';
const SWATCH = ['var(--olive)', 'var(--pink)', 'var(--lav)', 'var(--sky)', 'var(--butter)'];
const swatchFor = (key) => { let n = 0; for (const c of String(key)) n = (n * 31 + c.charCodeAt(0)) >>> 0; return SWATCH[n % SWATCH.length]; };

// ---------- icons & shapes ----------
const ICONS = {
  home: 'M3 11l9-7 9 7v9a1 1 0 0 1-1 1h-5v-6H9v6H4a1 1 0 0 1-1-1z',
  grid: 'M3 3h7v7H3zM14 3h7v7h-7zM3 14h7v7H3zM14 14h7v7h-7z',
  plus: 'M12 5v14M5 12h14',
  chart: 'M4 20V10M10 20V4M16 20v-7M22 20H2',
  list: 'M8 6h13M8 12h13M8 18h13M3 6h.01M3 12h.01M3 18h.01',
  clock: 'M12 21a9 9 0 1 0 0-18 9 9 0 0 0 0 18zM12 7v5l3 2',
  users: 'M17 21v-2a4 4 0 0 0-4-4H7a4 4 0 0 0-4 4v2M10 11a4 4 0 1 0 0-8 4 4 0 0 0 0 8zM21 21v-2a4 4 0 0 0-3-3.87M16 3.13a4 4 0 0 1 0 7.75',
  school: 'M3 21h18M5 21V10l7-5 7 5v11M9 21v-6h6v6',
  refresh: 'M21 12a9 9 0 1 1-3-6.7L21 8M21 3v5h-5',
  check: 'M5 12l5 5L20 7',
  alert: 'M12 9v4M12 17h.01M10.3 3.9L1.8 18a2 2 0 0 0 1.7 3h17a2 2 0 0 0 1.7-3L13.7 3.9a2 2 0 0 0-3.4 0z',
  inbox: 'M22 12h-6l-2 3h-4l-2-3H2M5.5 5h13l3.5 7v6a2 2 0 0 1-2 2H4a2 2 0 0 1-2-2v-6z',
  logout: 'M9 21H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h4M16 17l5-5-5-5M21 12H9',
  menu: 'M3 6h18M3 12h18M3 18h18',
  search: 'M11 19a8 8 0 1 0 0-16 8 8 0 0 0 0 16zM21 21l-4.3-4.3',
  layers: 'M12 2l10 5-10 5L2 7zM2 17l10 5 10-5M2 12l10 5 10-5',
  lock: 'M5 11h14v10H5zM8 11V7a4 4 0 0 1 8 0v4',
  bell: 'M18 8a6 6 0 0 0-12 0c0 7-3 9-3 9h18s-3-2-3-9M13.7 21a2 2 0 0 1-3.4 0',
  pulse: 'M3 12h4l3-8 4 16 3-8h4',
  back: 'M15 6l-6 6 6 6',
  arrow: 'M5 12h14M13 6l6 6-6 6',
  moon: 'M21 12.8A9 9 0 1 1 11.2 3a7 7 0 0 0 9.8 9.8z',
  more: 'M5 12h.01M12 12h.01M19 12h.01',
  undo: 'M9 14L4 9l5-5M4 9h11a5 5 0 0 1 0 10h-1',
  msg: 'M21 15a2 2 0 0 1-2 2H7l-4 4V5a2 2 0 0 1 2-2h14a2 2 0 0 1 2 2z',
  bulb: 'M9 18h6M10 21h4M12 3a6 6 0 0 0-3.5 10.9V16h7v-2.1A6 6 0 0 0 12 3z',
};
const NS = 'http://www.w3.org/2000/svg';
function svgEl(tag, attrs) { const e = document.createElementNS(NS, tag); for (const [k, v] of Object.entries(attrs || {})) e.setAttribute(k, v); return e; }
function icon(name) { const s = svgEl('svg', { viewBox: '0 0 24 24', class: 'ic', 'aria-hidden': 'true' }); s.append(svgEl('path', { d: ICONS[name] || ICONS.grid })); return s; }
const FLOWER = [[78, 50], [64, 74.2], [36, 74.2], [22, 50], [36, 25.8], [64, 25.8], [50, 50]];
const BURST = '98,50 82.8,58.8 91.6,74 74,74 74,91.6 58.8,82.8 50,98 41.2,82.8 26,91.6 26,74 8.4,74 17.2,58.8 2,50 17.2,41.2 8.4,26 26,26 26,8.4 41.2,17.2 50,2 58.8,17.2 74,8.4 74,26 91.6,26 82.8,41.2';
// shape draws one of the brand's decorative shapes (flower, burst, bolt, squiggle) as an absolutely placed SVG.
function shape(kind, size, color, style, cls) {
  const s = svgEl('svg', { viewBox: kind === 'squig' ? '0 0 110 30' : '0 0 100 100', width: size, height: kind === 'squig' ? size * 30 / 110 : size, 'aria-hidden': 'true', class: cls || 'shape', style: style || '' });
  if (kind === 'flower') FLOWER.forEach(([cx, cy]) => s.append(svgEl('circle', { cx, cy, r: 22, fill: color })));
  else if (kind === 'burst') s.append(svgEl('polygon', { points: BURST, fill: color }));
  else if (kind === 'bolt') s.append(svgEl('path', { d: 'M58 4 L24 56 H48 L40 96 L76 40 H52 Z', fill: color }));
  else if (kind === 'squig') s.append(svgEl('path', { d: 'M5 15 Q 17.5 0 30 15 T 55 15 T 80 15 T 105 15', fill: 'none', stroke: color, 'stroke-width': 4, 'stroke-linecap': 'round' }));
  return s;
}

// ---------- dialogs (in place of the browser's confirm/prompt) ----------
function overlay(content) {
  const prev = document.activeElement;
  const ov = h('div', { class: 'ov', role: 'dialog', 'aria-modal': 'true', onclick: (e) => e.target === ov && close() }, h('div', { class: 'sheet' }, content));
  const close = () => { ov.remove(); document.removeEventListener('keydown', esc); if (prev && prev.focus) prev.focus(); };
  const esc = (e) => { if (e.key === 'Escape') close(); };
  document.addEventListener('keydown', esc);
  ov.close = close;
  document.body.append(ov);
  setTimeout(() => { const f = ov.querySelector('textarea, input, button.btn:not(.ghost)'); if (f) f.focus(); }, 0);
  return ov;
}
function ask(title, body, okLabel = 'Confirm', opts = {}) {
  return new Promise((resolve) => {
    let done = false;
    const finish = (v) => { if (done) return; done = true; ov.close(); resolve(v); };
    const ov = overlay([h('h2', {}, title), body && h('p', { class: 'mute', style: 'line-height:1.5' }, body),
      h('div', { class: 'row', style: 'margin-top:16px' }, h('button', { class: 'btn grow' + (opts.danger ? ' danger' : ''), onclick: () => finish(true) }, okLabel), h('button', { class: 'btn ghost', onclick: () => finish(false) }, opts.cancel || 'Cancel'))]);
    const obs = new MutationObserver(() => { if (!ov.isConnected) { obs.disconnect(); if (!done) { done = true; resolve(false); } } });
    obs.observe(document.body, { childList: true });
  });
}
function askNote(title, body, okLabel, required, placeholder) {
  return new Promise((resolve) => {
    let done = false;
    const ta = h('textarea', { rows: 3, maxLength: 1000, placeholder: placeholder || 'Write a short note' });
    const err = h('div', { class: 'err-msg' });
    const finish = (v) => { if (done) return; done = true; ov.close(); resolve(v); };
    const ov = overlay([h('h2', {}, title), body && h('p', { class: 'mute', style: 'line-height:1.5' }, body), ta, err,
      h('div', { class: 'row', style: 'margin-top:12px' }, h('button', { class: 'btn grow', onclick: () => { if (required && !ta.value.trim()) { err.textContent = 'Please write a note first.'; return; } finish(ta.value.trim()); } }, okLabel), h('button', { class: 'btn ghost', onclick: () => finish(null) }, 'Cancel'))]);
    const obs = new MutationObserver(() => { if (!ov.isConnected) { obs.disconnect(); if (!done) { done = true; resolve(null); } } });
    obs.observe(document.body, { childList: true });
  });
}

// ---------- router ----------
const rawRoute = () => location.hash.replace(/^#\/?/, '');
function go(route) { if (rawRoute() === route) render(); else location.hash = '#/' + route; }
window.addEventListener('hashchange', () => { if (rawRoute().startsWith('parent')) return viewParent(); if (S.me) render(); });

const NAV = {
  teacher: () => [
    { sec: 'MARKS', items: [['dashboard', 'Today', 'home'], ['new', 'New test', 'plus'], ['tests', 'My tests', 'list'], ['updates', 'Updates', 'bell', S.notes.unread]] },
    { sec: 'CLASSES', items: [['reports', 'Weekly reports', 'chart'], ['classes', 'My classes', 'users']] },
  ],
  admin: () => [
    { sec: 'WORKSPACE', items: [['a/dashboard', 'Pulse', 'pulse'], ['a/approvals', 'Approvals', 'inbox', S.counts && (S.counts.counts.pending_approvals + S.counts.counts.pending_corrections)], ['a/marks', 'Marks explorer', 'list'], ['a/analysis', 'Weekly analysis', 'chart'], ['a/reports', 'Student reports', 'users'], ['a/corrections', 'Corrections log', 'clock']] },
    { sec: 'SCHOOL', items: [['a/classes', 'Classes', 'school'], ['a/students', 'Students', 'users'], ['a/allocations', 'Teacher allocation', 'layers'], ['a/teachers', 'Teachers', 'users']] },
    { sec: 'ERP', items: [['a/sync', 'Frappe sync', 'refresh']] },
  ],
};
const TABS = {
  teacher: () => [['dashboard', 'Today', 'home'], ['new', 'New', 'plus'], ['tests', 'Tests', 'list'], ['reports', 'Reports', 'chart']],
  admin: () => [['a/dashboard', 'Pulse', 'pulse'], ['a/approvals', 'Approve', 'inbox', S.counts && (S.counts.counts.pending_approvals + S.counts.counts.pending_corrections)], ['a/marks', 'Marks', 'list'], ['a/analysis', 'Analysis', 'chart']],
};
async function signOut() { try { await api('POST', '/auth/logout', {}); } catch (_) {} S.me = null; store.set('rea:get:/me', null); history.replaceState(null, '', location.pathname); viewLogin(); }

// shell: ink rail (desktop) / floating tab bar (phone) + sticky header + content. `bare` hides the header.
function shellMount(o) {
  const admin = isAdmin();
  document.body.className = admin ? 'sp-admin' : 'sp-teacher';
  const cur = rawRoute() || (admin ? 'a/dashboard' : 'dashboard');
  const active = cur.startsWith('exam/') ? (admin ? 'a/marks' : 'tests') : cur;
  const app = h('div', { class: 'app' });
  const closeMenu = () => app.classList.remove('open');
  const side = h('aside', { class: 'side', 'aria-label': 'Main navigation' },
    h('div', { class: 'brand' }, h('div', { class: 'logo' }, 'U'), h('div', {}, h('b', {}, 'GCM UPMARK'), h('small', {}, admin ? 'ADMIN CONSOLE' : 'MARKS ENTRY'))),
    (admin ? NAV.admin() : NAV.teacher()).map((g) => [h('div', { class: 'navh' }, g.sec), g.items.map(([r, l, ic, badge]) =>
      h('button', { class: 'nav' + (active === r ? ' on' : ''), 'data-route': r, onclick: () => { closeMenu(); go(r); } }, icon(ic), l, badge ? h('span', { class: 'cnt' }, badge) : null))]),
    h('div', { class: 'me' }, h('div', { class: 'avatar' }, (S.me.name || '?')[0].toUpperCase()), h('div', { class: 'grow' }, h('div', { class: 'nm' }, S.me.name), h('small', {}, S.me.mobile || S.me.email)),
      h('button', { class: 'icon-btn', 'aria-label': 'Change theme', title: 'Light / dark', onclick: cycleTheme }, icon('moon')),
      h('button', { class: 'icon-btn', 'aria-label': 'Sign out', title: 'Sign out', onclick: signOut }, icon('logout'))));
  const tabs = h('nav', { class: 'tabbar', 'aria-label': 'Quick navigation' },
    (admin ? TABS.admin() : TABS.teacher()).map(([r, l, ic, badge]) => h('button', { class: active === r ? 'on' : '', 'aria-label': l, onclick: () => go(r) }, icon(ic), h('span', { class: 't' }, l), badge ? h('span', { class: 'cnt' }, badge) : null)),
    h('button', { 'aria-label': 'More', onclick: () => app.classList.add('open') }, icon('menu'), h('span', { class: 't' }, 'More')));
  let top = null;
  if (!o.bare) {
    const search = h('input', { type: 'search', placeholder: admin ? 'Search tests, teachers, classes' : 'Search your tests', 'aria-label': 'Search', value: S.q,
      onkeydown: (e) => { if (e.key === 'Enter') { S.q = e.target.value; go(admin ? 'a/marks' : 'tests'); } } });
    top = h('header', { class: 'top' },
      h('div', { class: 'grow' }, o.sub && h('div', { class: 'sub' }, o.sub), h('h1', {}, o.title)),
      h('div', { class: 'search' }, icon('search'), search), o.cta,
      !admin && h('button', { class: 'icon-btn', 'aria-label': 'Updates' + (S.notes.unread ? ' (' + S.notes.unread + ' new)' : ''), onclick: () => go('updates') }, icon('bell'), S.notes.unread ? h('span', { class: 'dot' }, S.notes.unread) : null));
  }
  add(app, side, h('div', { class: 'scrim', onclick: closeMenu }), h('main', {}, S.offline && h('div', { class: 'offline' }, 'Offline · marks are kept on this phone and sync when you are back online'), top, h('div', { class: o.bare ? '' : 'page', id: 'content' }, o.content)), !o.bare && tabs);
  mount(app);
}
const dateSub = () => new Date().toLocaleDateString(undefined, { weekday: 'short', day: 'numeric', month: 'short' });
const statCard = (ic, value, lbl, desc, tone) => h('div', { class: 'stat' + (tone ? ' ' + tone : '') }, h('div', { class: 'l' }, lbl), h('b', {}, value), desc && h('div', { class: 'd' }, desc));
const greet = () => { const hr = new Date().getHours(); return hr < 12 ? 'Good morning' : hr < 17 ? 'Good afternoon' : 'Good evening'; };

async function render() {
  if (rawRoute().startsWith('parent')) return viewParent();
  if (!S.me) return viewLogin();
  try {
    S.data = await cachedGet('/me'); S.me = S.data.user;
    if (!S.offline) S.notes = await api('GET', '/notifications').catch(() => S.notes);
    if (isAdmin()) S.counts = await cachedGet('/admin/overview');
    const r = rawRoute();
    if (r.startsWith('exam/')) return openExam(+r.slice(5), { noPush: true });
    const pages = isAdmin()
      ? { 'a/marks': pgAdminMarks, 'a/approvals': pgAdminApprovals, 'a/analysis': pgAdminAnalysis, 'a/reports': pgReports, 'a/corrections': pgAdminCorrections, 'a/classes': pgAdminClasses, 'a/students': pgAdminStudents, 'a/allocations': pgAdminAllocations, 'a/teachers': pgAdminTeachers, 'a/sync': pgAdminSync }
      : { classes: pgClasses, reports: pgReports, new: pgNewTest, tests: () => pgTests(''), drafts: () => pgTests('draft'), updates: pgUpdates };
    const fn = pages[r] || (isAdmin() ? pgAdminDashboard : pgDashboard);
    await fn();
  } catch (e) { if (S.me) shellMount({ title: e.offline ? 'Offline' : 'Something went wrong', content: h('div', { class: 'banner bad' }, e.offline ? 'This page needs a connection. Your Today screen and open marking sheets still work offline.' : e.message) }); }
}

// ---------- sign in (staff WorkCode, or parent admission number) ----------
function loginHero() {
  return h('div', { class: 'lg-hero' },
    shape('flower', 230, 'var(--pink)', 'left:-40px;top:40px'),
    shape('burst', 180, 'var(--butter)', 'right:-30px;top:20px'),
    h('div', { class: 'shape', style: 'left:46%;top:200px;width:260px;height:120px;border-radius:60px;background:var(--olive)' }),
    (() => { const s = shape('bolt', 120, 'var(--lav)', 'left:52%;top:150px'); s.firstChild.setAttribute('stroke', 'var(--ink)'); s.firstChild.setAttribute('stroke-width', '2.5'); s.firstChild.setAttribute('stroke-linejoin', 'round'); return s; })(),
    shape('squig', 200, 'var(--ink)', 'left:10px;top:310px'),
    h('div', { class: 'mark' }, h('span', {}, 'U'), 'UPMARK'));
}
function viewLogin(step = 'id', ident = '', devCode = '') {
  document.body.className = 'sp-teacher';
  const err = h('div', { class: 'lg-err', role: 'alert' });
  const setErr = (m) => { err.textContent = m || ''; };
  const masked = /@/.test(ident) ? ident.replace(/^(.).*(@.*)$/, '$1•••$2') : '•••••• ' + ident.slice(-4);
  const tabs = h('div', { class: 'lg-tabs', role: 'tablist' }, h('button', { class: 'on', role: 'tab', 'aria-selected': 'true' }, 'Teacher / Admin'), h('button', { role: 'tab', onclick: () => go('parent') }, 'Parent'));
  let form;
  if (step === 'id') {
    const input = h('input', { class: 'lg-input', value: ident, placeholder: 'Mobile number or email', inputmode: 'text', autocomplete: 'username', maxLength: 80, autofocus: true, 'aria-label': 'Mobile number or email' });
    const btn = h('button', { class: 'lg-btn', type: 'submit' }, 'Get my WorkCode');
    const submit = async (e) => {
      e && e.preventDefault(); setErr('');
      if (!input.value.trim()) return setErr('Enter your registered mobile number or email.');
      btn.disabled = true; btn.textContent = 'Sending…';
      try { const r = await api('POST', '/auth/request', { identifier: input.value }); viewLogin('code', input.value.trim(), r.dev_code || ''); }
      catch (x) { setErr(x.message); btn.disabled = false; btn.textContent = 'Get my WorkCode'; }
    };
    form = h('form', { class: 'lg-form', onsubmit: submit }, tabs,
      h('h1', { class: 'disp' }, 'Marks in.', h('br'), 'Worry out.'), h('p', { class: 'lg-sub' }, "GCM Convent School's marks app. Sign in with the mobile number the school has on file."),
      h('label', { class: 'lg-label' }, 'Mobile number or email'), input, err, btn,
      h('p', { class: 'lg-help' }, 'Your 6-digit WorkCode arrives in a few seconds.'));
  } else {
    const boxes = [...Array(6)].map((_, i) => h('input', { class: 'lg-box', inputmode: 'numeric', autocomplete: i === 0 ? 'one-time-code' : 'off', maxLength: 1, 'aria-label': 'WorkCode digit ' + (i + 1) }));
    const btn = h('button', { class: 'lg-btn', type: 'submit' }, 'Verify & sign in');
    const code = () => boxes.map((x) => x.value).join('');
    let busy = false;
    const submit = async (e) => {
      e && e.preventDefault(); if (busy) return; setErr('');
      if (code().length < 6) return setErr('Enter all 6 digits of your WorkCode.');
      busy = true; btn.disabled = true; btn.textContent = 'Verifying…';
      try { await api('POST', '/auth/verify', { identifier: ident, code: code() }); await boot(); }
      catch (x) { setErr(x.message); busy = false; btn.disabled = false; btn.textContent = 'Verify & sign in'; boxes.forEach((b) => { b.value = ''; b.classList.add('bad'); }); boxes[0].focus(); setTimeout(() => boxes.forEach((b) => b.classList.remove('bad')), 600); }
    };
    boxes.forEach((bx, i) => {
      bx.addEventListener('input', () => { bx.value = bx.value.replace(/\D/g, '').slice(-1); if (bx.value && i < 5) boxes[i + 1].focus(); if (code().length === 6) submit(); });
      bx.addEventListener('keydown', (e) => { if (e.key === 'Backspace' && !bx.value && i > 0) { boxes[i - 1].focus(); boxes[i - 1].value = ''; } if (e.key === 'ArrowLeft' && i > 0) boxes[i - 1].focus(); if (e.key === 'ArrowRight' && i < 5) boxes[i + 1].focus(); });
      bx.addEventListener('paste', (e) => { const t = (e.clipboardData.getData('text') || '').replace(/\D/g, '').slice(0, 6); if (!t) return; e.preventDefault(); t.split('').forEach((c, k) => { boxes[k].value = c; }); boxes[Math.min(t.length, 5)].focus(); if (t.length === 6) submit(); });
      bx.addEventListener('focus', () => bx.select());
    });
    const resend = h('button', { class: 'lg-link', type: 'button', disabled: true }, '');
    let left30 = 30;
    const tick = () => {
      if (!resend.isConnected) return;
      if (left30 > 0) { resend.disabled = true; resend.textContent = 'Resend in ' + left30 + 's'; left30--; setTimeout(tick, 1000); }
      else { resend.disabled = false; resend.textContent = 'Resend WorkCode'; }
    };
    resend.onclick = async () => {
      setErr(''); resend.disabled = true;
      try { const r = await api('POST', '/auth/request', { identifier: ident }); toast('A new WorkCode has been sent'); left30 = 30; tick(); if (r.dev_code) viewLogin('code', ident, r.dev_code); }
      catch (x) { setErr(x.message); resend.disabled = false; }
    };
    form = h('form', { class: 'lg-form', onsubmit: submit },
      h('h1', { class: 'disp' }, 'Enter your', h('br'), 'WorkCode'), h('p', { class: 'lg-sub' }, 'We sent a 6-digit WorkCode to ', h('b', {}, masked), '. It is valid for 5 minutes.'),
      h('div', { class: 'lg-boxes' }, boxes), err,
      devCode && h('div', { class: 'lg-dev' }, 'Dev mode WorkCode: ', h('b', {}, devCode)),
      btn,
      h('div', { class: 'lg-row' }, h('button', { class: 'lg-link', type: 'button', onclick: () => viewLogin('id', ident) }, 'Change number'), resend));
    setTimeout(() => { tick(); boxes[0].focus(); }, 0);
  }
  mount(h('div', { class: 'lgn' }, loginHero(), h('div', { class: 'lg-panel' }, form, h('div', { class: 'lg-foot' }, 'GCM Convent School · UpMark'))));
}

// ---------- teacher: Today ----------
const examState = (e) => e.status === 'draft' ? 'Draft' : (e.approved_at ? 'Approved' : 'With admin');
const examItem = (e, actions, extra) => h('div', { class: 'li' }, h('div', { class: 'grow' }, h('b', {}, e.label),
  h('div', { class: 'small mute' }, examState(e) + (e.owner_name ? ' · ' + e.owner_name : '') + (e.locked_at ? ' · ' + fmtTs(e.locked_at) : '')), extra), h('div', { class: 'row' }, actions));
const subjOf = (e) => (e.combined ? e.subjects.join(' + ') : e.subjects[0]);
const NOTE_TONE = { approved: ['var(--olive)', 'check'], returned: ['var(--pink)', 'undo'], remark: ['var(--sky)', 'msg'], nudge: ['var(--butter)', 'clock'], weekly_report: ['var(--lav)', 'chart'], correction_approved: ['var(--olive)', 'check'], correction_rejected: ['var(--pink)', 'alert'] };
function noteItem(n) {
  const [bg, ic] = NOTE_TONE[n.kind] || ['var(--key)', 'bell'];
  return h('div', { class: 'note' + (n.read ? '' : ' unread'), style: n.exam_id ? 'cursor:pointer' : '', onclick: n.exam_id ? () => openExam(n.exam_id) : null },
    h('span', { class: 'ni', style: 'background:' + bg }, icon(ic)),
    h('div', { class: 'grow' }, h('div', { style: 'font-weight:800' }, n.title), n.body && h('div', { class: 'small mute', style: 'margin-top:2px;line-height:1.4' }, n.body), h('div', { class: 'small mute', style: 'margin-top:4px' }, ago(n.created_at))));
}
async function markNotesRead() { if (S.notes.unread && !S.offline) { try { await api('POST', '/notifications/read', {}); } catch (_) {} } }

async function pgDashboard() {
  const d = S.data, atLimit = d.drafts.length >= d.draft_limit;
  const banner = S.banner; S.banner = '';
  const mine = await cachedGet('/exams?status=draft').then((x) => x.exams.filter((r) => r.exam.owner_id === S.me.id)).catch(() => d.drafts.map((e) => ({ exam: e, graded: 0, total: 0 })));
  const withAdmin = d.recent.filter((e) => e.status === 'locked' && !e.approved_at);
  const approved = d.recent.filter((e) => e.approved_at);
  const left = mine.length;
  const heroDraft = mine[0];
  const hero = heroDraft ? (() => {
    const e = heroDraft.exam, pct = heroDraft.total ? Math.round(heroDraft.graded / heroDraft.total * 100) : 0;
    return h('div', { class: 'tile t-olive hero-card' }, shape('flower', 170, '#F4EEE3', 'right:-46px;top:-40px;opacity:.55'),
      h('div', { style: 'position:relative' }, h('div', { class: 'eyebrow' }, shortCls(e.class_name, e.section) + ' · ' + e.sub_type), h('div', { class: 'ttl' }, subjOf(e))),
      h('div', { style: 'position:relative;display:flex;flex-direction:column;gap:10px' },
        h('div', { class: 'row', style: 'flex-wrap:nowrap' }, h('div', { class: 'prog' }, h('div', { style: 'width:' + pct + '%' })), h('b', { class: 'small', style: 'font-variant-numeric:tabular-nums' }, heroDraft.graded + ' / ' + heroDraft.total)),
        h('button', { class: 'go-pill', id: 'continue', onclick: () => openExam(e.id) }, heroDraft.graded ? 'Continue marking' : 'Start marking', h('span', { class: 'arr' }, icon('arrow')))));
  })() : h('div', { class: 'tile t-butter hero-card' }, shape('burst', 170, '#F4EEE3', 'right:-40px;top:-40px;opacity:.7'),
    h('div', { style: 'position:relative' }, h('div', { class: 'eyebrow' }, 'Nothing open'), h('div', { class: 'ttl' }, 'Start a new test')),
    h('button', { class: 'go-pill', id: 'new', style: 'position:relative', onclick: () => go('new') }, 'Set up a test in one screen', h('span', { class: 'arr' }, icon('plus'))));
  const tile2 = mine[1] ? h('button', { class: 'tile t-pink mini', onclick: () => openExam(mine[1].exam.id) }, shape('burst', 90, '#F4EEE3', 'right:-22px;bottom:-22px;opacity:.6'),
    h('div', { class: 'eyebrow', style: 'position:relative' }, 'Also open · ' + shortCls(mine[1].exam.class_name, mine[1].exam.section)), h('div', { style: 'position:relative' }, h('div', { class: 'ttl' }, subjOf(mine[1].exam)), h('div', { class: 'small', style: 'font-weight:700;margin-top:4px' }, mine[1].graded + ' / ' + mine[1].total + ' marked')))
    : h('button', { class: 'tile t-pink mini', id: heroDraft ? 'new' : null, disabled: atLimit, onclick: () => go('new') }, shape('burst', 90, '#F4EEE3', 'right:-22px;bottom:-22px;opacity:.6'),
      h('div', { class: 'eyebrow', style: 'position:relative' }, 'New'), h('div', { style: 'position:relative' }, h('div', { class: 'ttl' }, 'Start a test'), h('div', { class: 'small', style: 'font-weight:700;margin-top:4px' }, 'One screen, five taps')));
  const tile3 = withAdmin[0] ? h('button', { class: 'tile t-lav mini', onclick: () => openExam(withAdmin[0].id) }, shape('bolt', 90, '#F4EEE3', 'right:-12px;bottom:-14px;opacity:.6'),
    h('div', { class: 'eyebrow', style: 'position:relative' }, 'With admin · ' + shortCls(withAdmin[0].class_name, withAdmin[0].section)), h('div', { style: 'position:relative' }, h('div', { class: 'ttl' }, subjOf(withAdmin[0])), h('div', { class: 'small', style: 'font-weight:700;margin-top:4px' }, withAdmin.length > 1 ? withAdmin.length + ' sheets waiting' : 'Waiting for approval')))
    : h('button', { class: 'tile t-lav mini', onclick: () => go('tests') }, shape('bolt', 90, '#F4EEE3', 'right:-12px;bottom:-14px;opacity:.6'),
      h('div', { class: 'eyebrow', style: 'position:relative' }, 'Approved'), h('div', { style: 'position:relative' }, h('div', { class: 'ttl' }, approved.length ? approved.length + ' recent' : 'None yet'), h('div', { class: 'small', style: 'font-weight:700;margin-top:4px' }, 'See all tests')));
  const notes = S.notes.notifications.slice(0, 5);
  shellMount({
    title: left ? left + (left === 1 ? ' sheet' : ' sheets') + ' open' : 'All caught up', sub: dateSub() + ' · ' + greet() + ', ' + S.me.name.split(' ')[0],
    cta: h('button', { class: 'cta', disabled: atLimit, title: atLimit ? 'Lock or discard a sheet first' : '', onclick: () => go('new') }, icon('plus'), h('span', { class: 'lbl' }, 'New test')),
    content: [
      banner && h('div', { class: 'banner bad' }, banner),
      atLimit && h('div', { class: 'banner warn' }, 'You have ' + d.draft_limit + ' sheets open. Lock or discard one to start another test.'),
      h('div', { class: 'cols' },
        h('div', {}, hero, h('div', { class: 'tiles2', style: 'margin-top:12px' }, tile2, tile3),
          h('div', { class: 'card', style: 'margin-top:14px' }, h('div', { class: 'row sp' }, h('h3', {}, 'Recent tests'), h('button', { class: 'btn sm ghost', onclick: () => go('tests') }, 'See all')),
            d.recent.length === 0 ? h('div', { class: 'empty' }, 'Nothing yet. Your first test takes about a minute to set up.') : d.recent.slice(0, 6).map((e) => examItem(e, [
              e.status === 'draft' && e.owner_id === S.me.id && h('button', { class: 'btn sm ghost', onclick: () => discardDraft(e) }, 'Discard'),
              h('button', { class: 'btn sm' + (e.status === 'draft' ? '' : ' ghost'), onclick: () => openExam(e.id) }, e.status === 'draft' ? 'Open' : 'View')])))),
        h('div', {}, h('div', { class: 'row sp', style: 'margin:2px 0 10px' }, h('h3', { style: 'font-weight:800' }, 'Updates'), S.notes.unread ? h('span', { class: 'badge red' }, S.notes.unread + ' new') : null),
          notes.length ? notes.map(noteItem) : h('div', { class: 'empty' }, 'Approvals, remarks from the admin and weekly report notices show up here.'),
          S.notes.notifications.length > 5 && h('button', { class: 'btn ghost sm', onclick: () => go('updates') }, 'All updates'))),
    ],
  });
  markNotesRead();
}
async function pgUpdates() {
  shellMount({ title: 'Updates', sub: 'Approvals, remarks and weekly reports', content: h('div', { style: 'max-width:720px' }, S.notes.notifications.length ? S.notes.notifications.map(noteItem) : h('div', { class: 'empty' }, 'No updates yet.')) });
  await markNotesRead();
}
async function discardDraft(e) {
  if (!(await ask('Discard this sheet?', e.label + '. Every mark entered in it will be deleted.', 'Discard', { danger: true }))) return;
  try { await api('DELETE', '/exams/' + e.id); store.set('rea:stash:' + e.id, null); toast('Sheet discarded'); render(); } catch (x) { toast(x.message, true); }
}

// ---------- teacher: my classes (Frappe student details) ----------
async function pgClasses() {
  const scope = await cachedGet('/scope');
  const groups = scope.groups;
  const sel = S.clsSel && groups.find((g) => g.class_name === S.clsSel.class_name && g.section === S.clsSel.section) ? S.clsSel : (groups[0] || null);
  const out = h('div');
  const chips = h('div', { class: 'pills' }, groups.map((g) => h('button', { class: 'pill' + (sel && g.class_name === sel.class_name && g.section === sel.section ? ' on' : ''), onclick: () => { S.clsSel = g; render(); } }, g.label)));
  shellMount({ title: 'My classes', sub: 'Student details from the school ERP', content: [
    groups.length === 0 ? h('div', { class: 'card' }, h('div', { class: 'empty' }, 'No classes are assigned to you yet. Ask the administrator to assign your classes and subjects.')) : [chips, out]] });
  if (!sel) return;
  const d = await api('GET', '/roster?class_name=' + encodeURIComponent(sel.class_name) + '&section=' + encodeURIComponent(sel.section));
  const q = h('input', { placeholder: 'Search name, roll or admission no.', type: 'search' });
  const tbl = h('div');
  const draw = () => {
    const t = q.value.trim().toLowerCase();
    const list = d.students.filter((x) => !t || (x.name + ' ' + x.roll_no + ' ' + x.admission_no).toLowerCase().includes(t));
    const par = (n, m) => (n || m) ? h('div', {}, n || '', m && h('div', { class: 'small mute' }, m)) : h('span', { class: 'mute' }, '-');
    fill(tbl, h('div', { class: 'tscroll' }, h('table', { class: 'tbl' }, h('thead', {}, h('tr', {}, ['Roll', 'Admission no.', 'Name', 'Gender', 'Date of birth', 'Father', 'Mother'].map((x) => h('th', {}, x)))),
      h('tbody', {}, list.map((x) => h('tr', {}, h('td', {}, x.roll_no), h('td', {}, x.admission_no), h('td', {}, h('b', {}, x.name)), h('td', {}, x.gender), h('td', {}, x.dob ? fmtDate(x.dob) : ''), h('td', {}, par(x.father_name, x.father_mobile)), h('td', {}, par(x.mother_name, x.mother_mobile))))))));
  };
  q.oninput = draw;
  fill(out, h('div', { class: 'card' }, h('div', { class: 'row sp' }, h('div', {}, h('b', {}, sel.label), h('div', { class: 'small mute' }, d.students.length + ' students · You teach ' + (d.subjects.join(', ') || '-') + ' · ' + (d.frappe_configured ? 'Synced from Frappe' + (d.last_sync ? ' ' + fmtTs(d.last_sync) : '') : 'Frappe not configured'))),
    h('button', { class: 'btn', onclick: () => go('new') }, 'Start a test')), h('div', { style: 'margin:12px 0' }, q), tbl));
  draw();
}

// ---------- teacher: tests list ----------
async function pgTests(status0) {
  let status = status0; const q = S.q; S.q = '';
  const out = h('div'); const qIn = h('input', { type: 'search', placeholder: 'Filter by class, subject, test', value: q });
  const pillRow = h('div', { class: 'row' });
  const load = async () => {
    const d = await cachedGet('/exams?status=' + status + '&q=' + encodeURIComponent(qIn.value));
    fill(pillRow, [['', 'All'], ['draft', 'Open'], ['locked', 'Locked']].map(([v, l]) => h('button', { class: 'pill sm' + (status === v ? ' on' : ''), onclick: () => { status = v; load(); } }, l)));
    const st = (e) => e.status === 'draft' ? h('span', { class: 'badge yellow' }, 'Open') : e.approved_at ? h('span', { class: 'badge green' }, 'Approved') : h('span', { class: 'badge lav' }, 'With admin');
    fill(out, d.exams.length === 0 ? h('div', { class: 'empty' }, 'No tests match.') : h('div', { class: 'tscroll' }, h('table', { class: 'tbl' }, h('thead', {}, h('tr', {}, ['Test', 'Teacher', 'Marked', 'Status', ''].map((x) => h('th', {}, x)))),
      h('tbody', {}, d.exams.map((r) => h('tr', { class: 'click', onclick: () => openExam(r.exam.id) }, h('td', {}, h('b', {}, r.exam.label)), h('td', {}, r.exam.owner_name), h('td', {}, r.graded + ' / ' + r.total), h('td', {}, st(r.exam)), h('td', {}, h('button', { class: 'btn sm ghost' }, r.exam.status === 'draft' ? 'Open' : 'View'))))))));
  };
  qIn.addEventListener('input', () => { clearTimeout(qIn.t); qIn.t = setTimeout(load, 200); });
  shellMount({ title: status0 === 'draft' ? 'Open sheets' : 'My tests', sub: 'Tests in your classes and subjects', content: [h('div', { class: 'row', style: 'margin-bottom:14px' }, h('div', { class: 'grow' }, qIn), pillRow), h('div', { class: 'card' }, out)] });
  await load();
}

// ---------- teacher: new test on one screen ----------
const C = {};
async function pgNewTest() {
  const scope = await cachedGet('/scope'); S.scope = scope;
  Object.assign(C, { group: scope.groups.length === 1 ? scope.groups[0] : null, sub_type: null, category: null, date: todayStr(), topic: '', subjects: [], combined: false, total: 20, custom: false, split: false, comps: [{ name: 'Theory', max: 80 }, { name: 'Practical', max: 20 }] });
  const body = h('div', { class: 'nt' });
  const startBtn = h('button', { class: 'btn', id: 'start' }, 'Start marking', icon('arrow'));
  const sumText = h('div', { class: 'grow' });
  const ready = () => C.group && C.sub_type && C.date && C.subjects.length >= (C.combined ? 2 : 1) && (C.split ? C.comps.every((c) => c.name.trim() && c.max > 0) : C.total > 0);
  const summary = () => {
    const parts = [C.group && C.group.label, C.sub_type, C.subjects.join(' + '), C.date && (C.date === todayStr() ? 'Today' : fmtDate(C.date)), C.split ? '/' + C.comps.reduce((a, c) => a + (c.max || 0), 0) : '/' + C.total].filter(Boolean);
    sumText.textContent = parts.join(' · ');
    startBtn.disabled = !ready();
  };
  const sec = (n, title, ...kids) => h('section', { class: 'step' }, h('label', { class: 'f' }, n + ' · ' + title), kids);
  const draw = () => {
    const g = C.group;
    const withTopic = ['Weekly Test', 'Surprise Test'].includes(C.sub_type);
    const dt = h('input', { type: 'date', value: C.date, max: todayStr(), style: 'max-width:200px', 'aria-label': 'Test date', onchange: (e) => { C.date = e.target.value; draw(); } });
    const typeChip = (t, cat) => h('button', { class: 'pill' + (C.sub_type === t ? ' on' : ''), onclick: () => { C.sub_type = t; C.category = cat; draw(); } }, t);
    fill(body,
      sec(1, 'Class', scope.groups.length === 0 ? h('div', { class: 'banner' }, 'No classes are assigned to you yet. Ask the administrator.') :
        h('div', { class: 'pills' }, scope.groups.map((x) => h('button', { class: 'pill' + (g === x ? ' on' : ''), onclick: () => { C.group = x; C.subjects = []; draw(); } }, x.label, h('span', { class: 'small', style: 'margin-left:8px;opacity:.7' }, x.students))))),
      sec(2, 'Test', h('div', { class: 'pills' }, scope.formative.map((t) => typeChip(t, 'Formative'))), h('div', { class: 'pills', style: 'margin-top:0' }, scope.summative.map((t) => typeChip(t, 'Summative')))),
      sec(3, 'Date', h('div', { class: 'pills' },
        h('button', { class: 'pill' + (C.date === todayStr() ? ' on' : ''), onclick: () => { C.date = todayStr(); draw(); } }, 'Today'),
        h('button', { class: 'pill' + (C.date === todayStr(-1) ? ' on' : ''), onclick: () => { C.date = todayStr(-1); draw(); } }, 'Yesterday'), dt),
        withTopic && h('input', { placeholder: 'Topic or chapter (optional)', maxLength: 120, value: C.topic, oninput: (e) => (C.topic = e.target.value) })),
      sec(4, 'Subject', !g ? h('div', { class: 'mute small' }, 'Pick a class first.') : [
        h('div', { class: 'pills' }, g.subjects.map((s) => h('button', { class: 'pill' + (C.subjects.includes(s) ? ' on' : ''), onclick: () => { if (C.combined) C.subjects = C.subjects.includes(s) ? C.subjects.filter((x) => x !== s) : [...C.subjects, s]; else C.subjects = [s]; draw(); } }, s)),
          g.subjects.length > 1 && h('button', { class: 'pill' + (C.combined ? ' on' : ''), style: 'border-style:dashed', onclick: () => { C.combined = !C.combined; if (!C.combined) C.subjects = C.subjects.slice(0, 1); draw(); } }, C.combined ? 'Combined test: on' : 'Combine subjects')),
        C.combined && h('div', { class: 'small mute' }, 'Pick two or more subjects for one combined test.')]),
      sec(5, 'Out of', h('div', { class: 'pills' },
        !C.split && [10, 20, 25, 50, 80, 100].map((m) => h('button', { class: 'pill' + (!C.custom && C.total === m ? ' on' : ''), onclick: () => { C.total = m; C.custom = false; draw(); } }, m)),
        !C.split && h('button', { class: 'pill' + (C.custom ? ' on' : ''), onclick: () => { C.custom = true; draw(); } }, 'Other'),
        h('button', { class: 'pill' + (C.split ? ' on' : ''), style: 'border-style:dashed', onclick: () => { C.split = !C.split; draw(); } }, C.split ? 'Split into parts: on' : 'Split into parts')),
        C.custom && !C.split && h('input', { inputmode: 'decimal', placeholder: 'Maximum marks, e.g. 25 or 12.5', value: num(C.total), style: 'max-width:280px', oninput: (e) => { C.total = parseFloat(e.target.value) || 0; summary(); } }),
        C.split && h('div', { class: 'card' }, C.comps.map((c, i) => h('div', { class: 'row', style: 'margin:6px 0;flex-wrap:nowrap' },
          h('input', { value: c.name, placeholder: 'Part', maxLength: 60, style: 'flex:2', oninput: (e) => { c.name = e.target.value; summary(); } }),
          h('input', { value: num(c.max), placeholder: 'Max', inputmode: 'decimal', style: 'flex:1', oninput: (e) => { c.max = parseFloat(e.target.value) || 0; summary(); } }),
          C.comps.length > 2 && h('button', { class: 'btn sm ghost', 'aria-label': 'Remove part', onclick: () => { C.comps.splice(i, 1); draw(); } }, '✕'))),
          h('button', { class: 'btn sm ghost', disabled: C.comps.length >= 8, onclick: () => { C.comps.push({ name: '', max: 5 }); draw(); } }, 'Add part'))),
      h('div', { class: 'sum' }, sumText, startBtn));
    summary();
  };
  startBtn.onclick = async () => {
    if (!ready()) return;
    startBtn.disabled = true;
    try {
      const r = await api('POST', '/exams/resolve', reqBody());
      if (r.state === 'existing') { toast('This test already exists. Opening it.'); return openExam(r.exam_id); }
      const out = await api('POST', '/exams', { ...reqBody(), split: C.split, max_total: C.total, components: C.split ? C.comps : [] });
      openExam(out.exam_id);
    } catch (e) { startBtn.disabled = false; if (e.data && e.data.code === 'draft_limit') { S.banner = e.message; go('dashboard'); } else toast(e.message, true); }
  };
  draw();
  shellMount({ title: 'New test', sub: 'Everything on one screen', content: body });
}
const reqBody = () => ({ class_name: C.group.class_name, section: C.group.section, category: C.category, sub_type: C.sub_type, date: C.date, topic: C.topic, subjects: [...C.subjects].sort(), combined: C.combined });

// ---------- workspace (grading) ----------
const keyOf = (sid, subj) => sid + '|' + subj;
function checkField(str, max) {
  if (str === '' || str == null) return { empty: true, ok: true };
  if (/^-/.test(str)) return { ok: false, msg: 'Marks cannot be negative' };
  if (!/^\d+\.?$|^\d+\.[05]$/.test(str)) return { ok: false, msg: 'Use whole or .5 marks only' };
  const v = parseFloat(str);
  if (v > max) return { ok: false, msg: 'Maximum is ' + max };
  return { ok: true, v };
}
let autosaveT = null, wsKeys = null;
async function openExam(id, opts = {}) {
  try {
    let d;
    try { d = await api('GET', '/exams/' + id); store.set('rea:exam:' + id, JSON.stringify(d)); }
    catch (e) { const c = e.offline && store.get('rea:exam:' + id); if (!c) throw e; d = JSON.parse(c); S.offline = true; toast('Offline: working from the copy on this phone'); }
    const ex = d.exam;
    W = { exam: ex, id, roster: d.roster, vis: d.visible_subjects, entries: {}, dirty: new Set(), pending: new Set(d.pending_corrections.map((p) => keyOf(p.student_id, p.subject))),
      sync: d.sync, locked: ex.status === 'locked', mode: window.innerWidth >= 900 ? 'table' : 'card', sort: 'roll', q: '', ungraded: false, subj: d.visible_subjects[0], studentWise: false, idx: 0, af: 0, missing: new Set(), listIds: [], qtStart: null };
    for (const e of d.entries) W.entries[keyOf(e.student_id, e.subject)] = { status: e.status, comps: e.comps.map(num) };
    if (!W.locked) {
      try {
        const st = JSON.parse(store.get('rea:stash:' + id) || 'null');
        if (st) { let n = 0; for (const [k, v] of Object.entries(st)) { const [sid, ...rest] = k.split('|'); if (W.roster.some((s) => String(s.id) === sid) && W.vis.includes(rest.join('|'))) { W.entries[k] = v; W.dirty.add(k); n++; } } if (n) toast('Restored ' + n + ' unsaved mark' + (n === 1 ? '' : 's') + ' from this device'); }
      } catch (_) {}
      const first = W.roster.findIndex((s) => !W.vis.every((sub) => entryState(s.id, sub) === 'done'));
      W.idx = Math.max(0, first);
    }
    if (!opts.noPush && rawRoute() !== 'exam/' + id) history.pushState(null, '', '#/exam/' + id);
    rebuildList(); viewWorkspace();
    clearInterval(autosaveT);
    autosaveT = setInterval(() => { if (W && W.id === id && W.dirty.size && navigator.onLine) saveDraft(true); }, 20000);
  } catch (e) { toast(e.message, true); if (!W) go(isAdmin() ? 'a/dashboard' : 'dashboard'); }
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
    store.set('rea:stash:' + W.id, W.dirty.size ? JSON.stringify(o) : null);
  } catch (_) {}
}
function touch(sid, subj) { W.dirty.add(keyOf(sid, subj)); W.missing.delete(sid); stash(); updateProgress(); }
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
function fieldsFor() { const f = []; for (const s of scopeSubjects()) W.exam.components.forEach((c, ci) => f.push({ subj: s, ci, comp: c })); return f; }
function updateProgress() {
  const el = document.getElementById('prog'); if (!el) return;
  const done = W.roster.filter((s) => studentDone(s.id)).length;
  el.textContent = done + ' / ' + W.roster.length;
  const sv = document.getElementById('savebtn'); if (sv) sv.textContent = W.dirty.size ? 'Save (' + W.dirty.size + ')' : 'Saved';
}
const groupLabelJS = (ex) => label(ex.class_name, ex.section);
function viewWorkspace() {
  const ex = W.exam;
  const title = shortCls(ex.class_name, ex.section) + ' · ' + (ex.combined ? ex.subjects.join(' + ') : ex.subjects[0]);
  const seg = h('div', { class: 'seg' },
    h('button', { class: W.mode === 'card' ? 'on' : '', 'aria-label': 'Card view', onclick: () => { W.mode = 'card'; rebuildList(); viewWorkspace(); } }, 'Cards'),
    h('button', { class: W.mode === 'table' ? 'on' : '', 'aria-label': 'Table view', onclick: () => { W.mode = 'table'; rebuildList(); viewWorkspace(); } }, 'Table'));
  const ctx = h('div', { class: 'ctx' },
    h('div', { class: 'row', style: 'flex-wrap:nowrap' }, h('button', { class: 'icon-btn', 'aria-label': 'Back', onclick: leaveWorkspace }, icon('back')),
      h('div', { class: 'grow' }, h('div', { class: 't', title: ex.label }, title), h('div', { class: 'st2' }, ex.sub_type + (ex.topic ? ' · ' + ex.topic : '') + ' · ' + fmtDate(ex.date) + ' · out of ' + ex.max_total + (W.locked ? '' : ' · saved on this device'))),
      h('div', { class: 'p', id: 'prog' })),
    h('div', { class: 'row sp', style: 'margin-top:10px' }, seg,
      h('div', { class: 'row', style: 'gap:6px' },
        h('button', { class: 'pill sm', onclick: () => showTimeline() }, 'History'),
        W.locked ? null : [
          h('button', { class: 'pill sm', id: 'savebtn', onclick: () => saveDraft(false) }, 'Saved'),
          h('button', { class: 'btn sm', id: 'lockbtn', onclick: finalize }, h('span', { class: 'lg' }, 'Lock & send to admin'), h('span', { class: 'sh' }, 'Lock'))])));
  const qIn = h('input', { type: 'search', placeholder: 'Jump to roll no., admission no. or name', value: W.q, oninput: (e) => { W.q = e.target.value; W.idx = 0; rebuildList(); renderBody(); } });
  const sortSel = h('select', { class: 'sortsel', onchange: (e) => { W.sort = e.target.value; rebuildList(); renderBody(); }, 'aria-label': 'Sort order' },
    [['roll', 'Roll no.'], ['adm', 'Admission no.'], ['name', 'A–Z']].map(([v, l]) => h('option', { value: v, selected: W.sort === v }, l)));
  const skip = h('button', { class: 'pill sm' + (W.ungraded ? ' on' : ''), id: 'skip', onclick: () => { W.ungraded = !W.ungraded; W.idx = 0; rebuildList(); document.getElementById('skip').classList.toggle('on', W.ungraded); renderBody(); } }, h('span', { class: 'lg' }, 'Unmarked only'), h('span', { class: 'sh' }, 'Unmarked'));
  const tools = h('div', { class: 'toolbar' }, qIn, sortSel, skip);
  if (ex.combined && W.vis.length > 0) {
    tools.append(h('button', { class: 'pill sm' + (W.studentWise ? ' on' : ''), onclick: () => { W.studentWise = !W.studentWise; rebuildList(); viewWorkspace(); } }, W.studentWise ? 'Student-wise' : 'Subject-wise'));
    if (!W.studentWise) W.vis.forEach((s) => tools.append(h('button', { class: 'pill sm' + (W.subj === s ? ' on' : ''), onclick: () => { W.subj = s; W.af = 0; rebuildList(); viewWorkspace(); } }, s)));
  }
  shellMount({ bare: true, content: [ctx, statusBar(), tools, h('div', { id: 'body' }), h('div', { id: 'pad' })] });
  document.body.classList.add('ws');
  renderBody(); updateProgress();
  if (wsKeys) document.removeEventListener('keydown', wsKeys);
  wsKeys = workspaceKeys; document.addEventListener('keydown', wsKeys);
}
// statusBar says where the sheet stands. Teachers never send results to parents: approval puts them in the weekly report.
function statusBar() {
  const ex = W.exam, admin = isAdmin();
  const returned = !W.locked && S.notes.notifications.find((n) => n.exam_id === W.id && n.kind === 'returned');
  if (returned) return h('div', { class: 'banner warn', style: 'margin:0 16px 8px' }, h('b', {}, 'Sent back by the admin: '), returned.body);
  if (!W.locked) return null;
  if (!ex.approved_at) {
    return h('div', { class: 'banner info', style: 'margin:0 16px 8px' }, h('div', { class: 'row sp' },
      h('div', { class: 'grow' }, h('b', {}, 'With admin. '), admin ? 'Check the marks, then approve or send the sheet back.' : 'Waiting for approval. Tap a student to ask for a correction.'),
      admin && h('div', { class: 'row' },
        h('button', { class: 'btn sm', onclick: async () => { try { await api('POST', '/admin/exams/' + W.id + '/approve', {}); toast('Approved. Parents see it in the weekly report.'); openExam(W.id, { noPush: true }); } catch (e) { toast(e.message, true); } } }, 'Approve'),
        h('button', { class: 'btn sm ghost', onclick: async () => { const n = await askNote('Send back to teacher', 'The sheet opens again for the teacher, with your note.', 'Send back', true, 'What should the teacher fix?'); if (n == null) return; try { await api('POST', '/admin/exams/' + W.id + '/return', { note: n }); toast('Sent back'); openExam(W.id, { noPush: true }); } catch (e) { toast(e.message, true); } } }, 'Send back'))));
  }
  return h('div', { class: 'banner ok', style: 'margin:0 16px 8px' }, h('b', {}, 'Approved. '), 'These marks are in the parents\' weekly report. ' + (admin ? 'Click a student to change a score.' : 'Tap a student to ask for a correction.'));
}
async function leaveWorkspace() {
  if (W && W.dirty.size) {
    if (navigator.onLine && await saveDraft(true)) { /* saved */ }
    else if (!(await ask('Leave this sheet?', 'Some marks are not on the server yet. They stay on this device and sync next time you are online.', 'Leave', { cancel: 'Stay' }))) return;
  }
  clearInterval(autosaveT); document.removeEventListener('keydown', wsKeys); wsKeys = null;
  W = null; go(isAdmin() ? 'a/marks' : 'dashboard');
}
function renderBody() { W.mode === 'card' ? renderCard() : renderTable(); }

// keyboard: digits / . / Backspace / Enter / A / E in card view; works with a desktop keyboard as well as the keypad.
function workspaceKeys(e) {
  if (!W || W.locked || W.mode !== 'card' || document.querySelector('.ov')) return;
  const t = e.target; if (t && (t.tagName === 'INPUT' || t.tagName === 'TEXTAREA' || t.tagName === 'SELECT')) return;
  if (e.metaKey || e.ctrlKey || e.altKey) return;
  const s = stById(W.listIds[W.idx]); if (!s) return;
  if (/^[0-9]$/.test(e.key)) { e.preventDefault(); typeKey(e.key); }
  else if (e.key === '.') { e.preventDefault(); typeKey('.5'); }
  else if (e.key === 'Backspace') { e.preventDefault(); typeKey('⌫'); }
  else if (e.key === 'Enter') { e.preventDefault(); advance(); }
  else if (e.key === 'a' || e.key === 'A') { e.preventDefault(); setStatus(s.id, 'AB', true); }
  else if (e.key === 'e' || e.key === 'E') { e.preventDefault(); setStatus(s.id, 'EX', true); }
  else if (e.key === 'ArrowLeft') { e.preventDefault(); stepCard(-1); }
  else if (e.key === 'ArrowRight') { e.preventDefault(); stepCard(1); }
}

// ---- card view ----
function rollStrip() {
  const ids = W.listIds;
  const strip = h('div', { class: 'rstrip', role: 'list', 'aria-label': 'Students' }, ids.map((id, i) => {
    const s = stById(id), st = scopeSubjects().map((sub) => entryState(id, sub));
    const cls = i === W.idx ? 'cur' : st.includes('bad') ? 'bad' : st.every((x) => x === 'done') ? 'done' : '';
    return h('button', { class: cls, role: 'listitem', 'aria-label': 'Roll ' + s.roll_no + ' ' + s.name, onclick: () => { W.idx = i; W.af = 0; renderCard(); } }, s.roll_no || i + 1);
  }));
  setTimeout(() => { const c = strip.querySelector('.cur'); if (c) strip.scrollLeft = c.offsetLeft - strip.clientWidth / 2 + c.clientWidth / 2; }, 0);
  return strip;
}
function renderCard() {
  const body = document.getElementById('body'), pad = document.getElementById('pad');
  const ids = W.listIds;
  if (ids.length === 0) { fill(pad); return fill(body, h('div', { class: 'deck' }, h('div', { class: 'empty' }, W.ungraded ? 'Everyone is marked.' : 'No student matches your search.'))); }
  if (W.idx >= ids.length) {
    const left = W.roster.filter((s) => !studentDone(s.id)).length;
    fill(pad);
    return fill(body, rollStrip(), h('div', { class: 'deck', style: 'padding-bottom:60px' }, h('div', { class: 'tile ' + (left ? 't-pink' : 't-olive'), style: 'text-align:center;padding:28px' }, shape('burst', 160, '#F4EEE3', 'right:-40px;top:-40px;opacity:.5'),
      h('div', { class: 'disp', style: 'font-size:2.6rem;position:relative' }, left ? left + ' left' : 'All marked'), h('p', { style: 'position:relative;font-weight:600' }, left ? left + ' student' + (left > 1 ? 's are' : ' is') + ' still unmarked.' : 'Every student has a mark, AB or EX.'),
      h('div', { class: 'row', style: 'justify-content:center;position:relative' },
        left > 0 && h('button', { class: 'btn', onclick: () => { W.ungraded = true; W.idx = 0; rebuildList(); viewWorkspace(); } }, 'Show unmarked'),
        !left && !W.locked && h('button', { class: 'btn', onclick: finalize }, 'Lock & send to admin'),
        h('button', { class: 'btn ghost', style: 'background:#FFFDF8;color:#17150F', onclick: () => { W.idx = 0; renderCard(); } }, 'Back to first')))));
  }
  const s = stById(ids[W.idx]);
  const fields = fieldsFor();
  W.af = Math.min(W.af, fields.length - 1);
  const stat = scopeSubjects().length === 1 ? entry(s.id, scopeSubjects()[0]).status : '';
  const dim = scopeSubjects().every((sub) => entry(s.id, sub).status) ? ' dim' : '';
  const pendingKey = scopeSubjects().some((sub) => W.pending.has(keyOf(s.id, sub)));
  const single = fields.length === 1;
  const full = single && !stat && entry(s.id, fields[0].subj).comps[fields[0].ci] === String(fields[0].comp.max);
  const rows = fields.map((f, i) => {
    const e = entry(s.id, f.subj), val = e.comps[f.ci], chk = checkField(val, f.comp.max);
    const lbl = [W.exam.combined ? f.subj : '', W.exam.split ? f.comp.name : ''].filter(Boolean).join(' · ');
    return h('div', {}, h('div', { class: 'field' }, lbl && h('span', { class: 'lbl' }, lbl),
      h('button', { class: 'box' + (i === W.af && !W.locked ? ' act' : '') + (!chk.ok ? ' err' : '') + (!e.status && val === '' ? ' empty' : ''), 'aria-label': (lbl || 'Mark') + ' for ' + s.name, onclick: () => { if (W.locked) return correctionModal(s); W.af = i; renderCard(); } },
        e.status ? e.status : (val === '' ? '–' : val)), h('span', { class: 'den' }, '/' + f.comp.max)),
      !chk.ok && h('div', { class: 'err-msg' }, chk.msg));
  });
  const card = h('div', { class: 'sc' + dim + (full ? ' full' : '') + (single ? '' : ' multi') },
    shape('burst', 260, 'var(--butter)', '', 'burst'),
    h('div', { class: 'roll', style: 'position:relative' }, 'Roll ' + s.roll_no + ' · Adm. ' + s.admission_no + ' · ' + (W.idx + 1) + ' of ' + ids.length), h('div', { class: 'nm' }, s.name),
    pendingKey && h('span', { class: 'badge yellow', style: 'position:relative' }, 'Correction requested'), rows,
    !W.locked && h('div', { class: 'qs' }, [['AB', 'Absent', 'ab'], ['EX', 'Exempt', 'ex']].map(([st, l, c]) => h('button', { class: 'pill ' + c + (stat === st ? ' on' : ''), onclick: () => setStatus(s.id, st, true) }, l))),
    W.locked && h('button', { class: 'btn', style: 'margin-top:14px;position:relative', onclick: () => correctionModal(s) }, isAdmin() ? 'Change this score' : 'Ask for a correction'));
  fill(body, rollStrip(), h('div', { class: 'deck' }, card));
  if (W.locked) return fill(pad, h('div', { class: 'foot' }, h('button', { class: 'btn ghost', onclick: () => stepCard(-1) }, 'Previous'), h('button', { class: 'btn', onclick: () => stepCard(1) }, 'Next')));
  const key = (lab, fn, aria) => h('button', { class: 'k', 'aria-label': aria || lab, onclick: fn }, lab);
  const last = W.af >= fields.length - 1;
  const nxt = stById(ids[W.idx + 1]);
  fill(pad, h('div', { class: 'pad' },
    h('div', { class: 'grid' }, ['1', '2', '3', '4', '5', '6', '7', '8', '9'].map((d) => key(d, () => typeKey(d))), key('.5', () => typeKey('.5'), 'Half mark'), key('0', () => typeKey('0')), key('⌫', () => typeKey('⌫'), 'Delete')),
    h('div', { class: 'next' }, h('button', { class: 'btn ghost', 'aria-label': 'Previous student', onclick: () => stepCard(-1) }, icon('back')),
      h('button', { class: 'btn', id: 'nextbtn', onclick: advance }, last ? (nxt ? 'Next · Roll ' + nxt.roll_no : 'Finish') : 'Next part'))));
}
function stepCard(d) { W.idx = Math.max(0, Math.min(W.listIds.length, W.idx + d)); W.af = 0; renderCard(); }
function typeKey(k) {
  const s = stById(W.listIds[W.idx]); if (!s) return;
  const f = fieldsFor()[W.af]; const e = entry(s.id, f.subj);
  if (e.status) { e.status = ''; }
  let v = e.comps[f.ci];
  if (k === '⌫') v = v.slice(0, -1);
  else if (k === '.5') { if (v.includes('.')) return; v = (v || '0') + '.5'; }
  else { if (v.length >= 6 || v.includes('.')) return; v = v === '0' ? k : v + k; }
  e.comps[f.ci] = v; touch(s.id, f.subj); renderCard();
}
function advance() {
  const s = stById(W.listIds[W.idx]); if (!s) return;
  const fields = fieldsFor();
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

// ---- table view, with quick type: "17 14.5 ab 19" fills several students at once ----
function quickTypePlan(text, startIdx) {
  const toks = text.trim().split(/[\s,;]+/).filter(Boolean);
  const subs = scopeSubjects(), comps = W.exam.components;
  const per = subs.length * comps.length;
  const plan = []; let i = startIdx, f = 0;
  for (const raw of toks) {
    const tok = raw.toLowerCase();
    if (i >= W.listIds.length) return { plan, error: 'More marks than students left in the list' };
    const sid = W.listIds[i];
    if (tok === 'ab' || tok === 'a' || tok === 'ex' || tok === 'e') {
      if (f !== 0) return { plan, error: '"' + raw + '" must start a new student' };
      plan.push({ sid, status: tok[0] === 'a' ? 'AB' : 'EX' }); i++; continue;
    }
    if (tok === '-' || tok === '_') { f++; if (f >= per) { f = 0; i++; } continue; }
    const sub = subs[Math.floor(f / comps.length)], ci = f % comps.length;
    const chk = checkField(raw, comps[ci].max);
    if (!chk.ok || chk.empty) return { plan, error: 'Roll ' + stById(sid).roll_no + ': ' + (chk.msg || 'not a mark') + ' ("' + raw + '")' };
    plan.push({ sid, sub, ci, val: raw.replace(/\.$/, '') });
    f++; if (f >= per) { f = 0; i++; }
  }
  return { plan, next: i };
}
function applyQuickType(plan) {
  for (const p of plan) {
    if (p.status) { for (const sub of scopeSubjects()) { const e = entry(p.sid, sub); e.status = p.status; e.comps = e.comps.map(() => ''); touch(p.sid, sub); } }
    else { const e = entry(p.sid, p.sub); e.status = ''; e.comps[p.ci] = p.val; touch(p.sid, p.sub); }
  }
}
function quickTypeBar() {
  if (W.locked) return null;
  const firstOpen = () => Math.max(0, W.listIds.findIndex((id) => !studentDone(id)));
  if (W.qtStart == null || W.qtStart >= W.listIds.length) W.qtStart = firstOpen();
  const from = h('select', { 'aria-label': 'Start from roll', onchange: (e) => { W.qtStart = +e.target.value; preview(); } }, W.listIds.map((id, i) => h('option', { value: i, selected: i === W.qtStart }, 'from Roll ' + stById(id).roll_no)));
  const hint = h('span', { class: 'hint' });
  const inp = h('input', { id: 'qt', placeholder: 'Type marks with spaces: 17 14.5 ab 19', autocomplete: 'off', inputmode: 'text', 'aria-label': 'Quick type marks' });
  const preview = () => {
    const r = quickTypePlan(inp.value, W.qtStart);
    if (!inp.value.trim()) { hint.textContent = 'ab = absent · ex = exempt · - = skip'; hint.style.color = ''; return r; }
    const sids = [...new Set(r.plan.map((p) => p.sid))];
    hint.textContent = r.error ? r.error : sids.length ? 'fills Roll ' + stById(sids[0]).roll_no + (sids.length > 1 ? ' to ' + stById(sids[sids.length - 1]).roll_no : '') + ' · Enter' : '';
    hint.style.color = r.error ? 'var(--bad)' : '';
    return r;
  };
  inp.oninput = preview;
  inp.onkeydown = (e) => {
    if (e.key !== 'Enter') return;
    e.preventDefault();
    const r = preview();
    if (r.error || !r.plan.length) return;
    applyQuickType(r.plan);
    W.qtStart = Math.min(r.next, W.listIds.length - 1);
    const n = new Set(r.plan.map((p) => p.sid)).size;
    renderTable(); toast(n + ' student' + (n > 1 ? 's' : '') + ' filled');
    const q = document.getElementById('qt'); if (q) q.focus();
  };
  preview();
  return h('div', { class: 'qt' }, h('span', { class: 'lab' }, 'QUICK TYPE'), inp, from, hint);
}
function renderTable() {
  const body = document.getElementById('body'), pad = document.getElementById('pad'); fill(pad);
  const subs = scopeSubjects(), comps = W.exam.components;
  const head = h('tr', {}, h('th', {}, 'Roll'), h('th', {}, 'Adm.'), h('th', {}, 'Student'),
    subs.flatMap((s) => comps.map((c) => h('th', {}, [W.exam.combined ? s : '', W.exam.split ? c.name : (W.exam.combined ? '' : 'Mark')].filter(Boolean).join(' · '), ' /' + c.max))), h('th', {}, W.locked ? '' : 'Status'));
  const rows = W.listIds.map((id) => tableRow(stById(id)));
  fill(body, quickTypeBar(),
    !W.locked && h('div', { class: 'kbd' }, h('span', {}, h('kbd', {}, '↵'), ' or ', h('kbd', {}, '↓'), ' next row'), h('span', {}, h('kbd', {}, '←'), ' ', h('kbd', {}, '→'), ' parts'), h('span', {}, 'type ', h('kbd', {}, 'ab'), ' or ', h('kbd', {}, 'ex'), ' in a cell')),
    h('div', { class: 'tw' }, h('table', { class: 't', id: 'tbl' }, h('thead', {}, head), h('tbody', {}, rows)),
      rows.length === 0 && h('p', { class: 'mute', style: 'padding:16px' }, W.ungraded ? 'Everyone is marked.' : 'No student matches your search.')));
}
function tableRow(s) {
  const subs = scopeSubjects(), comps = W.exam.components;
  const dimmed = subs.every((x) => entry(s.id, x).status);
  const tr = h('tr', { 'data-sid': s.id, class: (dimmed ? 'dim ' : '') + (W.missing.has(s.id) ? 'miss ' : '') + (W.locked ? 'click' : '') });
  const cells = [h('td', {}, h('b', {}, s.roll_no)), h('td', { class: 'mute' }, s.admission_no), h('td', {}, h('b', {}, s.name), subs.some((x) => W.pending.has(keyOf(s.id, x))) && h('span', { class: 'badge yellow', style: 'margin-left:6px' }, 'correction requested'))];
  for (const sub of subs) comps.forEach((c, ci) => {
    const e = entry(s.id, sub), chk = checkField(e.comps[ci], c.max);
    const inp = h('input', { class: 'sc-in' + (chk.ok ? '' : ' bad'), value: e.status || e.comps[ci], disabled: W.locked, readOnly: !!e.status, inputmode: 'decimal', 'data-col': subs.indexOf(sub) * comps.length + ci, 'aria-label': s.name + ' ' + sub + ' ' + c.name, autocomplete: 'off',
      oninput: (ev) => {
        const v = ev.target.value.trim();
        if (/^(ab|ex)$/i.test(v)) { setStatusRow(s, v.toUpperCase(), tr); return; }
        e.comps[ci] = v; const k = checkField(v, c.max); ev.target.classList.toggle('bad', !k.ok); ev.target.title = k.msg || ''; touch(s.id, sub);
      },
      onkeydown: (ev) => { if (e.status && (ev.key === 'Backspace' || ev.key === 'Delete')) { ev.preventDefault(); setStatusRow(s, e.status, tr); return; } tableNav(ev, inp); } });
    cells.push(h('td', {}, inp));
  });
  cells.push(h('td', {}, W.locked ? null : ['AB', 'EX'].map((st) => h('button', { class: 'st' + (subs.every((x) => entry(s.id, x).status === st) ? ' on' : ''), onclick: () => setStatusRow(s, st, tr) }, st))));
  tr.append(...cells);
  if (W.locked) tr.addEventListener('click', () => correctionModal(s));
  return tr;
}
function setStatusRow(s, st, tr) {
  for (const sub of scopeSubjects()) { const e = entry(s.id, sub); e.status = e.status === st ? '' : st; if (e.status) e.comps = e.comps.map(() => ''); touch(s.id, sub); }
  const nr = tableRow(s); tr.replaceWith(nr);
  const next = nr.nextElementSibling; const t = next && next.querySelector('input:not(:disabled)'); if (t) { t.focus(); t.select(); }
}
function tableNav(ev, inp) {
  const tr = inp.closest('tr'); const col = inp.dataset.col;
  const go2 = (row) => { if (!row) return; const t = row.querySelector('input[data-col="' + col + '"]:not(:disabled)') || row.querySelector('input:not(:disabled)'); if (t) { t.focus(); t.select(); } };
  if (ev.key === 'Enter' || ev.key === 'ArrowDown') { ev.preventDefault(); go2(tr.nextElementSibling); }
  else if (ev.key === 'ArrowUp') { ev.preventDefault(); go2(tr.previousElementSibling); }
  else if (ev.key === 'ArrowRight' && inp.selectionStart === inp.value.length) { const n = tr.querySelector('input[data-col="' + (+col + 1) + '"]'); if (n) { ev.preventDefault(); n.focus(); n.select(); } }
  else if (ev.key === 'ArrowLeft' && inp.selectionStart === 0) { const n = tr.querySelector('input[data-col="' + (+col - 1) + '"]'); if (n) { ev.preventDefault(); n.focus(); n.select(); } }
}

// ---- save, lock, offline sync ----
function payloadFrom(obj) {
  return Object.entries(obj).map(([k, e]) => { const i = k.indexOf('|'); return { student_id: +k.slice(0, i), subject: k.slice(i + 1), status: e.status, comps: e.comps.map((c) => (c === '' ? null : parseFloat(c))) }; });
}
async function saveDraft(silent) {
  if (W.locked) return true;
  if (!W.dirty.size) { if (!silent) toast('Everything is saved'); return true; }
  for (const k of W.dirty) { const sid = +k.split('|')[0]; const sub = k.slice(k.indexOf('|') + 1); if (entryState(sid, sub) === 'bad') { if (!silent) toast('Fix the red marks before saving', true); return false; } }
  const keys = [...W.dirty], obj = {}; keys.forEach((k) => (obj[k] = W.entries[k]));
  try {
    const r = await api('PUT', '/exams/' + W.id + '/marks', { entries: payloadFrom(obj) });
    keys.forEach((k) => W.dirty.delete(k)); stash(); S.offline = false;
    if (!silent) toast('Saved ' + r.saved + ' mark' + (r.saved === 1 ? '' : 's')); updateProgress(); return true;
  } catch (e) { if (!silent || !e.offline) toast(e.offline ? 'Offline. Marks are safe on this phone and sync when you reconnect.' : e.message, true); return false; }
}
// syncStashes pushes marks typed offline on other sheets once the connection is back.
async function syncStashes() {
  if (!navigator.onLine || !S.me) return;
  let keys = []; try { keys = Object.keys(localStorage).filter((k) => k.startsWith('rea:stash:')); } catch (_) {}
  let n = 0;
  for (const k of keys) {
    const id = +k.slice(10); if (W && W.id === id) continue;
    let obj; try { obj = JSON.parse(store.get(k) || 'null'); } catch (_) {} if (!obj) continue;
    try { await api('PUT', '/exams/' + id + '/marks', { entries: payloadFrom(obj) }); store.set(k, null); n++; }
    catch (e) { if (e.status === 409 || e.status === 404 || e.status === 403) store.set(k, null); }
  }
  if (n) toast('Synced marks from ' + n + ' sheet' + (n > 1 ? 's' : '') + ' saved offline');
}
window.addEventListener('online', async () => { S.offline = false; const o = document.querySelector('.offline'); if (o) o.remove(); if (W && W.dirty.size) await saveDraft(true); syncStashes(); });
window.addEventListener('offline', () => { S.offline = true; toast('Offline. Keep marking, it saves on this phone.'); });
async function finalize() {
  if (!(await saveDraft(true))) { if (!navigator.onLine) toast('Locking needs a connection. Your marks are safe on this phone.', true); return; }
  const left = W.roster.filter((s) => !W.vis.every((sub) => entryState(s.id, sub) === 'done'));
  if (left.length && !W.exam.combined) return flagMissing(left.map((s) => s.id));
  if (!(await ask('Lock and send to admin?', 'After locking, the admin checks and approves the sheet. Approved marks go into the parents\' weekly report. You can still ask for a correction later.', 'Lock & send'))) return;
  try {
    const r = await api('POST', '/exams/' + W.id + '/finalize', {});
    toast('Sent to admin · ' + r.stats.graded + ' marked, ' + r.stats.absent + ' absent, ' + r.stats.exempt + ' exempt');
    openExam(W.id, { noPush: true });
  } catch (e) {
    if (e.data && e.data.missing) flagMissing([...new Set(e.data.missing.map((m) => m.student_id))]);
    toast(e.message, true);
  }
}
function flagMissing(ids) {
  W.missing = new Set(ids); W.ungraded = true; W.idx = 0;
  if (W.exam.combined && !W.studentWise) W.studentWise = true;
  rebuildList(); W.listIds = ids.slice().sort((a, b) => stById(a).roll_sort - stById(b).roll_sort); viewWorkspace();
  toast(ids.length + ' student' + (ids.length > 1 ? 's' : '') + ' still unmarked, shown below', true);
}

// ---- history & corrections ----
const kindText = {
  created: 'Test created', draft_saved: 'First marks saved', draft_updated: 'Marks updated', locked: 'Locked & sent to admin', discarded: 'Sheet discarded',
  approved: 'Approved by admin', returned: 'Sent back to teacher', remark: 'Remark from admin', erp_synced: 'Synced to Frappe', correction_requested: 'Correction requested', correction_approved: 'Correction approved', correction_rejected: 'Correction not approved', correction_synced: 'Correction synced to Frappe' };
function eventDetail(ev) {
  const d = ev.detail || {};
  switch (ev.kind) {
    case 'created': return (d.subjects || []).join(', ') + ' · out of ' + d.max_total + (d.split ? ' (' + d.components.map((c) => c.name + ' ' + c.max).join(' + ') + ')' : '');
    case 'draft_saved': case 'draft_updated': return d.entries_total + ' marks stored';
    case 'approved': return 'Counts in the parents\' weekly report';
    case 'returned': case 'remark': return d.note;
    case 'locked': return d.graded + ' marked · ' + d.absent + ' AB · ' + d.exempt + ' EX' + (d.average != null ? ' · average ' + d.average + ' / ' + d.max_total : '');
    case 'correction_requested': return 'Roll ' + d.roll_no + ' ' + d.student + ' (' + d.subject + '): ' + d.old + ' → ' + d.new + ' · ' + d.reason;
    case 'correction_approved': case 'correction_rejected': return 'Roll ' + d.roll_no + ' ' + d.student + ': ' + d.old + ' → ' + d.new + (d.note ? ' · ' + d.note : '');
    case 'correction_synced': return 'Roll ' + d.roll_no + ' ' + d.student + ' · ERP doc ' + d.erp_document;
    case 'erp_synced': return d.message;
    default: return '';
  }
}
function timelineList(events) { return h('div', { class: 'tl' }, events.map((ev) => h('div', { class: 'ev' }, h('b', {}, kindText[ev.kind] || ev.kind), h('div', { class: 'small mute' }, ev.user + ' · ' + fmtTs(ev.ts)), h('div', { class: 'small' }, eventDetail(ev))))); }
async function showTimeline(examId) {
  const id = typeof examId === 'number' ? examId : W.id;
  try {
    const d = await api('GET', '/exams/' + id + '/timeline');
    overlay([h('div', { class: 'row sp' }, h('h2', {}, 'History'), h('button', { class: 'btn sm ghost', onclick: (e) => e.target.closest('.ov').close() }, 'Close')), h('p', { class: 'mute small' }, d.label), timelineList(d.events)]);
  } catch (e) { toast(e.message, true); }
}
// correctionModal: an admin changes a locked score directly; a teacher sends a request to the admin's approval inbox.
function correctionModal(s) {
  const admin = isAdmin();
  const subs = W.vis;
  let subj = subs[0], status = '';
  const body = h('div'), err = h('div', { class: 'err-msg' });
  let vals = [];
  const reason = h('textarea', { rows: 3, placeholder: admin ? 'Reason (kept in the history)' : 'What happened? e.g. a question was not added', maxLength: 1000 });
  const draw = () => {
    const cur = entry(s.id, subj), comps = W.exam.components;
    vals = comps.map(() => '');
    const inputs = comps.map((c, i) => h('div', { class: 'row', style: 'margin:6px 0;flex-wrap:nowrap' }, h('span', { style: 'min-width:90px;font-weight:700' }, W.exam.split ? c.name : 'New mark'),
      h('input', { inputmode: 'decimal', placeholder: 'out of ' + c.max, value: vals[i], disabled: !!status, oninput: (e) => { vals[i] = e.target.value.trim(); const k = checkField(vals[i], c.max); e.target.classList.toggle('bad', !k.ok); err.textContent = k.msg || ''; } })));
    fill(body,
      h('p', {}, h('b', {}, 'Roll ' + s.roll_no + ' · ' + s.name)),
      W.pending.has(keyOf(s.id, subj)) && h('div', { class: 'banner warn' }, 'A correction for this student is already waiting for the admin.'),
      subs.length > 1 && h('div', { class: 'pills' }, subs.map((x) => h('button', { class: 'pill sm' + (x === subj ? ' on' : ''), onclick: () => { subj = x; draw(); } }, x))),
      h('div', { class: 'banner' }, 'Current mark: ', h('b', {}, cur.status || cur.comps.join(' + ') + (cur.comps.length > 1 ? ' = ' + cur.comps.reduce((a, b) => a + parseFloat(b || 0), 0) : ''))),
      h('div', { class: 'pills' }, [['', 'Mark'], ['AB', 'Absent'], ['EX', 'Exempt']].map(([v, l]) => h('button', { class: 'pill sm' + (status === v ? ' on' : ''), onclick: () => { status = v; draw(); } }, l))),
      inputs, err, h('label', { class: 'f' }, 'Reason'), reason);
  };
  draw();
  const ov = overlay([h('h2', {}, admin ? 'Change a score' : 'Ask for a correction'), !admin && h('p', { class: 'mute small' }, 'The admin approves it before the mark changes.'), body,
    h('div', { class: 'row', style: 'margin-top:14px' }, h('button', { class: 'btn grow', onclick: async () => {
      const comps = vals.map((v) => (v === '' ? null : parseFloat(v)));
      for (let i = 0; i < comps.length && !status; i++) { const k = checkField(vals[i], W.exam.components[i].max); if (!k.ok) return (err.textContent = k.msg); if (k.empty) return (err.textContent = 'Enter the new mark'); }
      try {
        const r = await api('POST', '/exams/' + W.id + '/corrections', { student_id: s.id, subject: subj, status, comps: status ? [] : comps, reason: reason.value });
        ov.close(); toast(r.state === 'pending' ? 'Correction request sent to the admin' : 'Score updated'); openExam(W.id, { noPush: true });
      } catch (e) { err.textContent = e.message; }
    } }, admin ? 'Save change' : 'Send to admin'), h('button', { class: 'btn ghost', onclick: () => ov.close() }, 'Cancel'))]);
}

// ---------- admin space ----------
async function runMasterSync(btn) {
  const old = btn.textContent; btn.disabled = true; btn.textContent = 'Syncing…';
  try { const r = await api('POST', '/admin/sync/master', {}); toast('Roster synced in ' + r.duration_ms + ' ms'); render(); }
  catch (e) { toast(e.message, true); btn.disabled = false; btn.textContent = old; }
}
async function pgAdminDashboard() {
  const o = S.counts, c = o.counts, p = o.parents || {};
  const waiting = c.pending_approvals + c.pending_corrections;
  const [appr, corr, drafts] = await Promise.all([api('GET', '/admin/approvals?state=pending'), api('GET', '/admin/corrections?state=pending'), api('GET', '/admin/exams?status=draft')]);
  const overdue = drafts.exams.filter((r) => Date.now() - new Date(r.exam.created_at) > 3 * 864e5);
  const readPct = p.students ? Math.round(p.seen / p.students * 100) + '%' : '–';
  const nudgeAll = async () => {
    if (!(await ask('Remind ' + overdue.length + ' teacher' + (overdue.length > 1 ? 's' : '') + '?', 'Each teacher with a sheet open for more than 3 days gets a reminder on their Today screen.', 'Send reminders'))) return;
    let n = 0; for (const r of overdue) { try { await api('POST', '/admin/exams/' + r.exam.id + '/nudge', { note: '' }); n++; } catch (_) {} }
    toast(n + ' reminder' + (n === 1 ? '' : 's') + ' sent');
  };
  const syncBtn = h('button', { class: 'btn ghost', onclick: (e) => runMasterSync(e.target) }, 'Sync roster now');
  shellMount({
    title: 'Pulse', sub: dateSub() + ' · ' + greet(),
    cta: h('button', { class: 'cta', onclick: () => go('a/approvals') }, icon('inbox'), h('span', { class: 'lbl' }, 'Approvals' + (waiting ? ' (' + waiting + ')' : ''))),
    content: [
      h('div', { class: 'pulse' },
        h('button', { class: 'tile t-ink lead', style: 'text-align:left', onclick: () => go('a/approvals') }, shape('burst', 130, 'var(--butter)', 'right:-24px;top:-34px'),
          h('div', { class: 'eyebrow', style: 'position:relative;opacity:.7' }, 'Waiting on you'), h('div', { class: 'disp', style: 'position:relative' }, waiting ? waiting + (waiting === 1 ? ' sheet' : ' items') : 'All clear'),
          h('div', { class: 'small', style: 'position:relative;margin-top:8px;opacity:.8' }, c.pending_approvals + ' to approve · ' + c.pending_corrections + ' correction' + (c.pending_corrections === 1 ? '' : 's'))),
        statCard('', c.approved_week, 'Approved this week', c.locked + ' locked in total', 't-olive'),
        h('div', { class: 'stat t-pink' }, h('div', { class: 'l' }, 'Open over 3 days'), h('b', {}, overdue.length), overdue.length ? h('button', { class: 'btn sm', style: 'margin-top:8px', onclick: nudgeAll }, 'Remind teachers') : h('div', { class: 'd' }, 'Nobody is behind')),
        statCard('', readPct, 'Parents read', p.week ? p.seen + ' of ' + p.students + ' · week of ' + fmtDate(p.week) : 'No weekly report sent yet', 't-sky')),
      h('div', { class: 'cols' },
        h('div', {}, h('div', { class: 'card' }, h('div', { class: 'row sp' }, h('h3', {}, 'Waiting on you'), h('button', { class: 'btn sm', onclick: () => go('a/approvals') }, 'Open inbox')),
          waiting === 0 ? h('div', { class: 'empty' }, 'Nothing waiting. New sheets from teachers land here.') : [
            appr.exams.slice(0, 5).map((r) => h('div', { class: 'li' }, h('div', { class: 'grow' }, h('b', {}, r.exam.label), h('div', { class: 'small mute' }, r.exam.owner_name + ' · locked ' + ago(r.exam.locked_at) + (r.warnings ? ' · ' : '')), r.warnings ? h('span', { class: 'badge red' }, r.warnings + ' to check') : null), h('button', { class: 'btn sm ghost', onclick: () => { S.ibSel = { t: 'exam', id: r.exam.id }; go('a/approvals'); } }, 'Review'))),
            corr.corrections.slice(0, 3).map((x) => h('div', { class: 'li' }, h('div', { class: 'grow' }, h('b', {}, 'Correction · Roll ' + x.roll_no + ' ' + x.student), h('div', { class: 'small mute' }, x.old + ' → ' + x.new + ' · ' + x.subject + ' · ' + x.requested_by)), h('button', { class: 'btn sm ghost', onclick: () => { S.ibSel = { t: 'corr', id: x.id }; go('a/approvals'); } }, 'Review')))]),
          h('div', { class: 'card' }, h('div', { class: 'row sp' }, h('h3', {}, 'Recent activity'), h('button', { class: 'btn sm ghost', onclick: () => go('a/marks') }, 'All tests')),
            S.data.recent.length === 0 ? h('div', { class: 'empty' }, 'No tests yet.') : S.data.recent.map((e) => examItem(e, h('button', { class: 'btn sm ghost', onclick: () => openExam(e.id) }, 'Open'))))),
        h('div', {},
          overdue.length > 0 && h('div', { class: 'card' }, h('h3', {}, 'Open for more than 3 days'), overdue.slice(0, 6).map((r) => h('div', { class: 'li' }, h('div', { class: 'grow' }, h('b', {}, r.exam.label), h('div', { class: 'small mute' }, r.exam.owner_name + ' · ' + r.graded + ' / ' + r.total + ' marked')),
            h('button', { class: 'btn sm ghost', onclick: async () => { try { await api('POST', '/admin/exams/' + r.exam.id + '/nudge', { note: '' }); toast('Reminder sent to ' + r.exam.owner_name); } catch (e) { toast(e.message, true); } } }, 'Remind')))),
          h('div', { class: 'card' }, h('h3', {}, 'School'), h('div', { class: 'grid2' }, h('div', { class: 'mstat' }, h('div', { class: 'eyebrow' }, 'Students'), h('b', {}, c.students)), h('div', { class: 'mstat' }, h('div', { class: 'eyebrow' }, 'Teachers'), h('b', {}, c.teachers)), h('div', { class: 'mstat' }, h('div', { class: 'eyebrow' }, 'Open sheets'), h('b', {}, c.drafts)), h('div', { class: 'mstat' }, h('div', { class: 'eyebrow' }, 'Sync queue'), h('b', {}, c.pending_jobs)))),
          h('div', { class: 'card' }, h('h3', {}, 'Frappe'), !o.frappe_configured && h('div', { class: 'banner bad' }, 'Frappe is not configured. Marks stay safe here and sync once it is.'),
            h('p', { class: 'mute', style: 'margin:0 0 10px' }, 'Roster pulls every 7 days. Last sync: ', h('b', {}, o.last_master_sync ? fmtTs(o.last_master_sync) : 'never')), syncBtn))),
    ],
  });
}
let ibKeys = null;
// pgAdminApprovals is one inbox for submitted sheets and teachers' correction requests, with the checks next to them.
async function pgAdminApprovals() {
  const [d, cr] = await Promise.all([api('GET', '/admin/approvals'), api('GET', '/admin/corrections?state=pending')]);
  const pend = d.exams.filter((r) => !r.approved), done = d.exams.filter((r) => r.approved).slice(0, 12);
  const items = [...pend.map((r) => ({ t: 'exam', id: r.exam.id, r })), ...cr.corrections.map((c) => ({ t: 'corr', id: c.id, c }))];
  const all = [...items, ...done.map((r) => ({ t: 'exam', id: r.exam.id, r }))];
  if (!S.ibSel || !all.some((x) => x.t === S.ibSel.t && x.id === S.ibSel.id)) S.ibSel = items[0] ? { t: items[0].t, id: items[0].id } : null;
  const list = h('div', { class: 'card ib-list', style: 'padding:10px' });
  const detail = h('div', { class: 'card', id: 'ibd', style: 'padding:0;overflow:hidden' });
  const approve = async (id) => { try { await api('POST', '/admin/exams/' + id + '/approve', {}); toast('Approved. Parents see it in the weekly report.'); render(); } catch (e) { toast(e.message, true); } };
  const itemBtn = (x) => {
    const on = S.ibSel && x.t === S.ibSel.t && x.id === S.ibSel.id;
    if (x.t === 'exam') {
      const e = x.r.exam;
      return h('button', { class: 'ib-item' + (on ? ' on' : ''), onclick: () => { S.ibSel = { t: 'exam', id: e.id }; drawList(); drawDetail(); if (innerWidth < 1000) document.getElementById('ibd').scrollIntoView({ behavior: 'smooth' }); } },
        h('span', { class: 'ib-sw', style: 'background:' + swatchFor(e.class_name + e.section) }, shortCls(e.class_name, e.section)),
        h('span', { class: 'grow' }, h('span', { class: 'tt' }, e.sub_type + ' · ' + subjOf(e)), h('span', { class: 'ss' }, e.owner_name + ' · ' + (x.r.approved ? 'approved ' + ago(e.approved_at) : 'locked ' + ago(e.locked_at)))),
        x.r.approved ? h('span', { class: 'badge green' }, 'Approved') : x.r.warnings ? h('span', { class: 'badge red' }, x.r.warnings + ' to check') : h('span', { class: 'badge yellow' }, 'Review'));
    }
    const c = x.c;
    return h('button', { class: 'ib-item' + (on ? ' on' : ''), onclick: () => { S.ibSel = { t: 'corr', id: c.id }; drawList(); drawDetail(); if (innerWidth < 1000) document.getElementById('ibd').scrollIntoView({ behavior: 'smooth' }); } },
      h('span', { class: 'ib-sw', style: 'background:var(--sky)' }, '±'),
      h('span', { class: 'grow' }, h('span', { class: 'tt' }, 'Roll ' + c.roll_no + ' · ' + c.student), h('span', { class: 'ss' }, c.subject + ' · ' + c.old + ' → ' + c.new + ' · ' + c.requested_by)), h('span', { class: 'badge blue' }, 'Correction'));
  };
  const drawList = () => fill(list,
    h('div', { class: 'row sp', style: 'padding:4px 6px 6px' }, h('h3', { style: 'margin:0' }, 'Inbox'), h('span', { class: 'small mute' }, h('kbd', {}, 'J'), ' ', h('kbd', {}, 'K'), ' move · ', h('kbd', {}, 'A'), ' approve')),
    items.length ? items.map(itemBtn) : h('div', { class: 'empty' }, 'Nothing waiting. New submissions from teachers appear here.'),
    done.length > 0 && [h('div', { class: 'eyebrow mute', style: 'padding:12px 8px 4px' }, 'Recently approved'), done.map((r) => itemBtn({ t: 'exam', id: r.exam.id, r }))]);
  const drawDetail = async () => {
    const sel = S.ibSel;
    if (!sel) return fill(detail, h('div', { style: 'padding:40px', class: 'empty' }, 'Pick an item to review it.'));
    if (sel.t === 'corr') {
      const c = cr.corrections.find((x) => x.id === sel.id);
      const decide = async (action) => {
        const note = action === 'reject' ? await askNote('Not approve this correction?', 'The teacher sees your note.', 'Do not approve', true, 'Why not?') : '';
        if (note == null) return;
        try { await api('POST', '/admin/corrections/' + c.id + '/' + action, { note }); toast(action === 'approve' ? 'Correction approved' : 'Correction not approved'); S.ibSel = null; render(); } catch (e) { toast(e.message, true); }
      };
      return fill(detail, h('div', { style: 'padding:22px' },
        h('div', { class: 'eyebrow mute' }, 'Correction request · ' + c.label), h('div', { class: 'disp', style: 'font-size:2.4rem;margin-top:6px' }, 'Roll ' + c.roll_no + ' · ' + c.student),
        h('div', { class: 'row', style: 'margin:18px 0;gap:16px' }, h('div', { class: 'mstat', style: 'flex:1' }, h('div', { class: 'eyebrow' }, 'Now'), h('b', {}, c.old)), h('span', { class: 'disp', style: 'font-size:2rem' }, '→'), h('div', { class: 'mstat t-olive', style: 'flex:1' }, h('div', { class: 'eyebrow' }, 'Asked for'), h('b', {}, c.new))),
        h('div', { class: 'banner' }, h('b', {}, c.requested_by + ': '), c.reason), h('div', { class: 'small mute' }, c.subject + ' · requested ' + fmtTs(c.requested_at)),
        h('div', { class: 'row', style: 'margin-top:16px' }, h('button', { class: 'btn', onclick: () => decide('approve') }, icon('check'), 'Approve change'), h('button', { class: 'btn ghost', onclick: () => decide('reject') }, 'Do not approve'), h('button', { class: 'btn ghost', onclick: () => openExam(c.exam_id) }, 'Open sheet'))));
    }
    fill(detail, h('div', { style: 'padding:40px', class: 'mute' }, 'Loading…'));
    const [rv, tl] = await Promise.all([api('GET', '/admin/exams/' + sel.id + '/review'), api('GET', '/exams/' + sel.id + '/timeline')]);
    if (!S.ibSel || S.ibSel.id !== sel.id) return;
    const e = rv.exam, max = Math.max(1, ...rv.bins.map((b) => b.n));
    const delta = rv.prev_avg_pct != null ? Math.round((rv.avg_pct - rv.prev_avg_pct) * 10) / 10 : null;
    fill(detail,
      h('div', { class: 'row', style: 'padding:20px 22px 16px;border-bottom:1px solid var(--line);align-items:flex-start' },
        h('div', { class: 'grow' }, h('div', { class: 'eyebrow mute' }, shortCls(e.class_name, e.section) + ' · ' + subjOf(e) + ' · ' + e.owner_name + ' · out of ' + e.max_total), h('div', { class: 'disp', style: 'font-size:2.6rem;margin-top:6px' }, e.sub_type + (e.topic ? ' · ' + e.topic : '')), h('div', { class: 'small mute', style: 'margin-top:4px' }, fmtDate(e.date))),
        e.approved_at ? h('span', { class: 'badge green', style: 'font-size:.85rem;padding:8px 14px' }, 'Approved ' + ago(e.approved_at)) : h('button', { class: 'btn', id: 'approve', onclick: () => approve(e.id) }, 'Approve', icon('check'))),
      h('div', { class: 'cols', style: 'gap:0' },
        h('div', { style: 'padding:18px 22px;border-right:1px solid var(--line)' },
          h('div', { class: 'grid2', style: 'grid-template-columns:repeat(3,minmax(0,1fr))' },
            h('div', { class: 'mstat' }, h('div', { class: 'eyebrow' }, 'Average'), h('b', {}, rv.avg_pct + '%')),
            h('div', { class: 'mstat' }, h('div', { class: 'eyebrow' }, 'Absent'), h('b', {}, rv.absent + (rv.exempt ? ' +' + rv.exempt + ' EX' : ''))),
            h('div', { class: 'mstat' }, h('div', { class: 'eyebrow' }, 'Vs last'), h('b', {}, delta == null ? '–' : (delta > 0 ? '▲ ' : delta < 0 ? '▼ ' : '') + Math.abs(delta)))),
          h('div', { class: 'eyebrow', style: 'margin:18px 0 10px' }, 'Spread'),
          h('div', { class: 'dist' }, rv.bins.map((b) => h('div', {}, h('span', { class: 'small', style: 'font-weight:800' }, b.n), h('div', { class: 'bar' + (b.n === max ? ' top' : ''), style: 'height:' + Math.max(4, Math.round(b.n / max * 110)) + 'px' }), h('span', { class: 'small mute', style: 'font-size:.7rem;white-space:nowrap' }, b.label)))),
          h('div', { class: 'eyebrow', style: 'margin:18px 0 8px' }, 'Checks'),
          rv.flags.length ? rv.flags.map((f) => h('div', { class: 'flag ' + f.level }, icon(f.level === 'warn' ? 'alert' : 'check'), f.text)) : h('div', { class: 'flag good' }, icon('check'), 'Nothing unusual in this sheet.')),
        h('div', { style: 'padding:18px 22px' }, h('div', { class: 'eyebrow', style: 'margin-bottom:12px' }, 'History'), timelineList(tl.events),
          h('div', { class: 'row', style: 'margin-top:8px' },
            h('button', { class: 'btn sm ghost', onclick: () => openExam(e.id) }, 'Open sheet'),
            !e.approved_at && h('button', { class: 'btn sm ghost', onclick: async () => { const n = await askNote('Send back to ' + e.owner_name + '?', 'The sheet opens again for the teacher, with your note.', 'Send back', true, 'What should the teacher fix?'); if (n == null) return; try { await api('POST', '/admin/exams/' + e.id + '/return', { note: n }); toast('Sent back to ' + e.owner_name); S.ibSel = null; render(); } catch (x) { toast(x.message, true); } } }, 'Send back'),
            h('button', { class: 'btn sm ghost', onclick: async () => { const n = await askNote('Remark for ' + e.owner_name, 'Shows on the teacher\'s Today screen.', 'Send remark', true); if (n == null) return; try { await api('POST', '/admin/exams/' + e.id + '/remark', { note: n }); toast('Remark sent'); drawDetail(); } catch (x) { toast(x.message, true); } } }, 'Remark')))));
  };
  const keys = (ev) => {
    if (!document.getElementById('ibd')) { document.removeEventListener('keydown', keys); ibKeys = null; return; }
    if (document.querySelector('.ov') || /INPUT|TEXTAREA|SELECT/.test((ev.target || {}).tagName || '')) return;
    const i = items.findIndex((x) => S.ibSel && x.t === S.ibSel.t && x.id === S.ibSel.id);
    if (ev.key === 'j' || ev.key === 'k') { const n = items[Math.max(0, Math.min(items.length - 1, i + (ev.key === 'j' ? 1 : -1)))]; if (n) { S.ibSel = { t: n.t, id: n.id }; drawList(); drawDetail(); } }
    else if (ev.key === 'a' && S.ibSel && S.ibSel.t === 'exam') { const b = document.getElementById('approve'); if (b) b.click(); }
  };
  if (ibKeys) document.removeEventListener('keydown', ibKeys);
  ibKeys = keys; document.addEventListener('keydown', keys);
  shellMount({ title: 'Approvals', sub: pend.length + ' sheet' + (pend.length === 1 ? '' : 's') + ' and ' + cr.corrections.length + ' correction' + (cr.corrections.length === 1 ? '' : 's') + ' waiting',
    cta: pend.length > 1 ? h('button', { class: 'cta', onclick: async () => {
      const flagged = pend.filter((r) => r.warnings).length;
      if (!(await ask('Approve all ' + pend.length + ' sheets?', flagged ? flagged + ' of them have checks you have not looked at.' : 'None of them has a warning.', 'Approve all'))) return;
      for (const r of pend) { try { await api('POST', '/admin/exams/' + r.exam.id + '/approve', {}); } catch (_) {} } toast('Approved'); render(); } }, 'Approve all (' + pend.length + ')') : null,
    content: h('div', { class: 'inbox' }, list, detail) });
  drawList(); drawDetail();
}
async function pgAdminMarks() {
  const q0 = S.q; S.q = '';
  const [scope, users] = await Promise.all([api('GET', '/scope'), api('GET', '/admin/users')]);
  const classes = [...new Set(scope.groups.map((g) => g.class_name))];
  const fc = h('select', { 'aria-label': 'Class' }, h('option', { value: '' }, 'All classes'), classes.map((c) => h('option', { value: c }, c)));
  const fs = h('select', { 'aria-label': 'Section' }, h('option', { value: '' }, 'All sections'));
  const ft = h('select', { 'aria-label': 'Teacher' }, h('option', { value: '' }, 'All teachers'), users.users.filter((u) => u.role === 'teacher').map((u) => h('option', { value: u.id }, u.name)));
  const fst = h('select', { 'aria-label': 'Status' }, [['', 'All statuses'], ['draft', 'Open'], ['locked', 'Locked']].map(([v, l]) => h('option', { value: v }, l)));
  const out = h('div');
  const load = async () => {
    const d = await api('GET', '/admin/exams?class_name=' + encodeURIComponent(fc.value) + '&section=' + encodeURIComponent(fs.value) + '&owner=' + ft.value + '&status=' + fst.value);
    const q = q0.toLowerCase();
    const rows = d.exams.filter((r) => !q || (r.exam.label + ' ' + r.exam.owner_name).toLowerCase().includes(q));
    fill(out, rows.length === 0 ? h('div', { class: 'empty' }, 'No tests match.') : h('div', { class: 'tscroll' }, h('table', { class: 'tbl' }, h('thead', {}, h('tr', {}, ['Test', 'Teacher', 'Marked', 'Status', 'Sync', ''].map((x) => h('th', {}, x)))),
      h('tbody', {}, rows.map((r) => h('tr', {}, h('td', {}, h('b', {}, r.exam.label)), h('td', {}, r.exam.owner_name), h('td', {}, r.graded + ' / ' + r.total), h('td', {}, r.exam.status === 'draft' ? h('span', { class: 'badge yellow' }, 'Open') : r.exam.approved_at ? h('span', { class: 'badge green' }, 'Approved') : h('span', { class: 'badge lav' }, 'To approve')), h('td', {}, syncBadge(r.sync)),
        h('td', {}, h('div', { class: 'row', style: 'flex-wrap:nowrap' }, h('button', { class: 'btn sm ghost', onclick: () => showTimeline(r.exam.id) }, 'History'), h('button', { class: 'btn sm ghost', onclick: () => openExam(r.exam.id) }, 'Open')))))))));
  };
  fc.onchange = () => { fill(fs, h('option', { value: '' }, 'All sections'), scope.groups.filter((g) => g.class_name === fc.value && g.section).map((g) => h('option', { value: g.section }, 'Section ' + g.section))); load(); };
  fs.onchange = ft.onchange = fst.onchange = load;
  shellMount({ title: 'Marks explorer', sub: 'Every marking sheet in the school', content: [h('div', { class: 'filters' }, fc, fs, ft, fst), h('div', { class: 'card' }, out)] });
  await load();
}
const CF = { state: '', q: '' };
async function pgAdminCorrections() {
  const d = await api('GET', '/admin/corrections');
  const list = h('div');
  const chips = h('div', { class: 'row' });
  const q = h('input', { type: 'search', placeholder: 'Search student, roll, test, subject, reason', value: CF.q });
  const bar = h('div', { class: 'row', style: 'margin-bottom:14px;gap:10px;flex-wrap:wrap' }, chips, h('div', { class: 'grow' }, q));
  const stateLbl = (c) => c.state === 'pending' ? h('span', { class: 'badge yellow' }, 'Waiting') : c.state === 'rejected' ? h('span', { class: 'badge red' }, 'Not approved') : h('span', { class: 'badge green' }, c.decision_note === 'Edited directly by administrator' ? 'Edited by admin' : 'Approved');
  const draw = () => {
    const t = CF.q.trim().toLowerCase();
    const rows = d.corrections.filter((c) => (!CF.state || c.state === CF.state) && (!t || [c.student, c.roll_no, c.admission_no, c.label, c.subject, c.reason, c.requested_by].join(' ').toLowerCase().includes(t)));
    fill(chips, [['', 'All (' + d.corrections.length + ')'], ['pending', 'Waiting'], ['approved', 'Applied'], ['rejected', 'Not approved']].map(([v, l]) => h('button', { class: 'pill sm' + (CF.state === v ? ' on' : ''), onclick: () => { CF.state = v; draw(); } }, l)));
    fill(list, rows.length === 0 ? h('div', { class: 'empty' }, 'No records.') : h('div', { class: 'card' }, h('div', { class: 'tscroll' }, h('table', { class: 'tbl' },
      h('thead', {}, h('tr', {}, ['When', 'Student', 'Test · Subject', 'Change', 'Reason', 'By', 'Status', ''].map((x) => h('th', {}, x)))),
      h('tbody', {}, rows.map((c) => h('tr', {}, h('td', {}, fmtTs(c.decided_at || c.requested_at)), h('td', {}, h('b', {}, c.student), h('div', { class: 'small mute' }, 'Roll ' + c.roll_no + ' · Adm. ' + c.admission_no)),
        h('td', {}, c.label, h('div', { class: 'small mute' }, c.subject)), h('td', {}, h('b', {}, c.old + ' → ' + c.new)), h('td', {}, c.reason),
        h('td', {}, c.decided_by || c.requested_by), h('td', {}, stateLbl(c)),
        h('td', {}, c.state === 'pending' ? h('button', { class: 'btn sm', onclick: () => { S.ibSel = { t: 'corr', id: c.id }; go('a/approvals'); } }, 'Review') : h('button', { class: 'btn sm ghost', onclick: () => showTimeline(c.exam_id) }, 'History')))))))));
  };
  q.oninput = () => { CF.q = q.value; clearTimeout(q.t); q.t = setTimeout(draw, 150); };
  shellMount({ title: 'Corrections log', sub: 'Every change to a locked sheet: who, when and why', content: [h('div', { class: 'banner' }, 'Teachers ask for corrections from a locked sheet. The requests wait in Approvals. Admins can also change a score directly from the sheet; each change is kept here.'), bar, list] });
  draw();
}
const A = { class_name: null, section: null }; // allocation page selection
async function pgAdminClasses() {
  const d = await api('GET', '/admin/classes');
  shellMount({ title: 'Classes', sub: 'Rosters pulled from Frappe student groups', content: h('div', { class: 'card' }, d.classes.length === 0 ? h('div', { class: 'empty' }, 'No classes yet. Run "Sync roster now" from Pulse.') :
    h('div', { class: 'tscroll' }, h('table', { class: 'tbl' }, h('thead', {}, h('tr', {}, ['Class', 'Section', 'Students', 'Subjects staffed', 'Teachers', 'Open', 'Locked', ''].map((x) => h('th', {}, x)))),
      h('tbody', {}, d.classes.map((g) => h('tr', {}, h('td', {}, h('b', {}, g.class_name)), h('td', {}, g.section || '—'), h('td', {}, g.students), h('td', {}, g.subjects), h('td', {}, g.teachers), h('td', {}, g.drafts), h('td', {}, g.locked),
        h('td', {}, h('button', { class: 'btn sm ghost', onclick: () => { A.class_name = g.class_name; A.section = g.section; go('a/allocations'); } }, 'Assign teachers')))))))) });
}
async function pgAdminAllocations() {
  const scope = await api('GET', '/scope');
  const classes = [...new Set(scope.groups.map((g) => g.class_name))];
  const body = h('div');
  const draw = async () => {
    const secs = scope.groups.filter((g) => g.class_name === A.class_name);
    const parts = [h('label', { class: 'f' }, '1 · Class'), h('div', { class: 'pills' }, classes.map((c) => h('button', { class: 'pill sm' + (A.class_name === c ? ' on' : ''), onclick: () => { A.class_name = c; const s = scope.groups.filter((g) => g.class_name === c); A.section = s.length === 1 ? s[0].section : null; draw(); } }, c)))];
    if (A.class_name) parts.push(h('label', { class: 'f' }, '2 · Section'), h('div', { class: 'pills' }, secs.map((g) => h('button', { class: 'pill sm' + (A.section === g.section ? ' on' : ''), onclick: () => { A.section = g.section; draw(); } }, g.section || 'Whole class'))));
    const matrix = h('div');
    if (A.class_name && A.section !== null) {
      parts.push(h('label', { class: 'f' }, '3 · Teacher per subject'), matrix);
      fill(body, parts);
      const m = await api('GET', '/admin/allocations?class_name=' + encodeURIComponent(A.class_name) + '&section=' + encodeURIComponent(A.section));
      const assign = async (subject, user_id, assigned) => { try { await api('PUT', '/admin/allocations/assign', { class_name: A.class_name, section: A.section, subject, user_id, assigned }); draw(); } catch (e) { toast(e.message, true); } };
      fill(matrix, h('div', { class: 'small mute', style: 'margin-bottom:8px' }, label(A.class_name, A.section) + ' · ' + m.students + ' students'), h('div', { class: 'tscroll' }, h('table', { class: 'tbl' }, h('thead', {}, h('tr', {}, ['Subject', 'Assigned teachers', 'Add teacher'].map((x) => h('th', {}, x)))),
        h('tbody', {}, m.subjects.map((s) => {
          const free = m.teachers.filter((t) => !s.teachers.some((x) => x.id === t.id));
          const sel = h('select', { 'aria-label': 'Add teacher for ' + s.subject, style: 'min-width:220px' }, h('option', { value: '' }, free.length ? 'Select teacher…' : (m.teachers.length ? 'All teachers already assigned' : 'No teachers yet: add them in Teachers')), free.map((t) => h('option', { value: t.id }, t.name)));
          return h('tr', {}, h('td', {}, h('b', {}, s.subject)),
            h('td', {}, s.teachers.length === 0 ? h('span', { class: 'mute' }, 'Unassigned') : h('div', { class: 'chips' }, s.teachers.map((t) => h('span', { class: 'chip on' }, t.name, h('button', { class: 'x', 'aria-label': 'Remove ' + t.name, onclick: () => assign(s.subject, t.id, false) }, '✕'))))),
            h('td', {}, h('div', { class: 'row', style: 'flex-wrap:nowrap' }, sel, h('button', { class: 'btn sm', onclick: () => sel.value ? assign(s.subject, +sel.value, true) : toast('Select a teacher first', true) }, 'Assign'))));
        })))));
    } else fill(body, parts);
  };
  shellMount({ title: 'Teacher allocation', sub: 'Class, then section, then a teacher for every subject', content: h('div', { class: 'card' }, body) });
  await draw();
}
async function pgAdminTeachers() {
  const [d, scope] = await Promise.all([api('GET', '/admin/users'), api('GET', '/scope')]);
  const name = h('input', { placeholder: 'Full name' }), mob = h('input', { placeholder: 'Mobile (10 digits)', inputmode: 'numeric' }), em = h('input', { placeholder: 'Email', type: 'email' });
  const role = h('select', {}, h('option', { value: 'teacher' }, 'Teacher'), h('option', { value: 'admin' }, 'Administrator / exam coordinator'));
  const err = h('div', { class: 'err-msg' });
  shellMount({ title: 'Teachers', sub: 'Add teachers and choose what they can see', content: [
    h('div', { class: 'card' }, h('h3', {}, 'Add a teacher'), h('div', { class: 'grid2' }, name, mob, em, role), err,
      h('button', { class: 'btn', style: 'margin-top:10px', onclick: async () => { try { await api('POST', '/admin/users', { name: name.value, mobile: mob.value, email: em.value, role: role.value }); toast('User added'); render(); } catch (e) { err.textContent = e.message; } } }, 'Add')),
    importCard(),
    d.users.map((u) => {
      const holder = h('div');
      return h('div', { class: 'card' }, h('div', { class: 'row sp' }, h('div', {}, h('b', {}, u.name), ' ', h('span', { class: 'badge' + (u.role === 'admin' ? ' lav' : '') }, u.role), !u.active && h('span', { class: 'badge red' }, 'inactive'), h('div', { class: 'small mute' }, [u.mobile, u.email].filter(Boolean).join(' · ') + ' · ' + u.allocations + ' allocations')),
        h('div', { class: 'row' }, u.role === 'teacher' && h('button', { class: 'btn sm ghost', onclick: () => allocEditor(holder, u, scope) }, 'Classes & subjects'),
          h('button', { class: 'btn sm ghost', onclick: async () => { try { await api('PUT', '/admin/users/' + u.id, { ...u, active: !u.active }); render(); } catch (e) { toast(e.message, true); } } }, u.active ? 'Deactivate' : 'Activate'))), holder);
    })] });
}
async function importPost(path, file, dry) {
  if (!file.files[0]) { toast('Choose a CSV or PDF file first', true); return null; }
  const fd = new FormData(); fd.append('file', file.files[0]);
  const res = await fetch('/api' + path + (dry ? '?dry=1' : ''), { method: 'POST', headers: { 'X-Requested-With': 'rea' }, credentials: 'same-origin', body: fd });
  const data = await res.json().catch(() => ({}));
  if (!res.ok) throw new Error(data.error || 'Import failed');
  return data;
}
function importCard() {
  const file = h('input', { type: 'file', accept: '.csv,.pdf,text/csv,application/pdf' });
  const out = h('div', { style: 'margin-top:10px' });
  const preview = async () => {
    try {
      const d = await importPost('/admin/users/import', file, true); if (!d) return;
      const n = d.rows.filter((r) => r.status === 'new').length;
      fill(out, h('div', { class: 'tscroll' }, h('table', { class: 'tbl' }, h('thead', {}, h('tr', {}, ['Name', 'Mobile', 'Email', 'Result'].map((x) => h('th', {}, x)))),
        h('tbody', {}, d.rows.map((r) => h('tr', {}, h('td', {}, r.name), h('td', {}, r.mobile), h('td', {}, r.email), h('td', {}, r.status === 'new' ? 'Will be added' : r.status === 'exists' ? 'Skipped: ' + r.note : 'Invalid: ' + r.note)))))),
        h('button', { class: 'btn', style: 'margin-top:10px', disabled: n === 0, onclick: async () => { try { const x = await importPost('/admin/users/import', file, false); toast(x.added + ' teachers imported'); render(); } catch (e) { toast(e.message, true); } } }, 'Import ' + n + ' new teachers'));
    } catch (e) { toast(e.message, true); }
  };
  return h('div', { class: 'card' }, h('h3', {}, 'Import teachers (CSV or PDF)'),
    h('p', { class: 'mute small', style: 'margin-top:0' }, 'One teacher per row with a name and a 10-digit mobile number (email optional). You see a preview before anything is saved. PDFs must contain real text, not a scanned image.'),
    h('div', { class: 'row' }, file, h('button', { class: 'btn ghost', onclick: preview }, 'Preview')), out);
}
function studentImportCard() {
  const file = h('input', { type: 'file', accept: '.csv,.pdf,text/csv,application/pdf' });
  const out = h('div', { style: 'margin-top:10px' });
  const preview = async () => {
    try {
      const d = await importPost('/admin/students/import', file, true); if (!d) return;
      const n = d.rows.filter((r) => r.status === 'new').length;
      fill(out, h('div', { class: 'tscroll' }, h('table', { class: 'tbl' }, h('thead', {}, h('tr', {}, ['Roll', 'Admission no.', 'Name', 'Class', 'Section', 'Result'].map((x) => h('th', {}, x)))),
        h('tbody', {}, d.rows.map((r) => h('tr', {}, h('td', {}, r.roll_no), h('td', {}, r.admission_no), h('td', {}, r.name), h('td', {}, r.class_name), h('td', {}, r.section), h('td', {}, r.status === 'new' ? 'Will be added' : r.status === 'exists' ? 'Skipped: ' + r.note : 'Invalid: ' + r.note)))))),
        h('button', { class: 'btn', style: 'margin-top:10px', disabled: n === 0, onclick: async () => { try { const x = await importPost('/admin/students/import', file, false); toast(x.added + ' students imported'); render(); } catch (e) { toast(e.message, true); } } }, 'Import ' + n + ' new students'));
    } catch (e) { toast(e.message, true); }
  };
  return h('div', { class: 'card' }, h('h3', {}, 'Import students (CSV or PDF)'),
    h('p', { class: 'mute small', style: 'margin-top:0' }, 'One student per row: roll no., admission no., name, class and section (the class and section must already exist, e.g. 5TH A). Review the preview first. Students added this way live in this app only: their marks are saved here but cannot be pushed to Frappe until the student exists there (use the Frappe Student ID as the admission no. and the next fetch will match them).'),
    h('div', { class: 'row' }, file, h('button', { class: 'btn ghost', onclick: preview }, 'Preview')), out);
}
const TAGS = { below: ['Below average', 'var(--pink)'], average: ['Average', 'var(--butter)'], excellent: ['Excellent', 'var(--olive)'] };
function tagLegend() { return h('div', { class: 'row', style: 'gap:14px;flex-wrap:wrap' }, Object.values(TAGS).map(([l, c]) => h('span', { class: 'small' }, h('span', { style: 'display:inline-block;width:10px;height:10px;border-radius:3px;margin-right:6px;background:' + c }), l))); }
function stackBar(t) {
  const n = t.below + t.average + t.excellent;
  if (!n) return h('span', { class: 'mute small' }, t.absent ? t.absent + ' absent / exempt' : 'no marks');
  const seg = (k) => h('div', { title: TAGS[k][0] + ': ' + t[k], style: 'height:100%;background:' + TAGS[k][1] + ';width:' + (t[k] / n * 100) + '%' });
  return h('div', { style: 'display:flex;height:16px;border-radius:8px;overflow:hidden;background:var(--key);min-width:140px;flex:1;gap:2px' }, seg('below'), seg('average'), seg('excellent'));
}
function barRows(list, nameKey) {
  return list.length === 0 ? h('div', { class: 'empty' }, 'No submitted tests in this period.') : list.map((t) => h('div', { class: 'row', style: 'flex-wrap:nowrap;margin:8px 0;gap:12px' },
    h('div', { style: 'width:130px;flex:none;font-weight:700;overflow:hidden;text-overflow:ellipsis;white-space:nowrap' }, t[nameKey]), stackBar(t),
    h('div', { class: 'small mute', style: 'width:150px;flex:none;text-align:right' }, 'avg ' + t.avg + '% · ' + t.below + ' / ' + t.average + ' / ' + t.excellent)));
}
function donut(o) {
  const n = o.below + o.average + o.excellent;
  const a = n ? o.below / n * 100 : 0, b = n ? a + o.average / n * 100 : 0;
  const bg = n ? 'conic-gradient(' + TAGS.below[1] + ' 0 ' + a + '%,' + TAGS.average[1] + ' ' + a + '% ' + b + '%,' + TAGS.excellent[1] + ' ' + b + '% 100%)' : 'var(--key)';
  return h('div', { style: 'width:170px;height:170px;border-radius:50%;flex:none;display:grid;place-items:center;background:' + bg }, h('div', { style: 'width:110px;height:110px;border-radius:50%;background:var(--card);display:grid;place-items:center;text-align:center' }, h('div', {}, h('div', { class: 'disp', style: 'font-size:2rem' }, o.avg + '%'), h('div', { class: 'small mute' }, 'average'))));
}
async function pgAdminAnalysis() {
  const days = h('select', { style: 'width:auto' }, [[7, 'Last 7 days'], [14, 'Last 14 days'], [30, 'Last 30 days']].map(([v, l]) => h('option', { value: v }, l)));
  days.value = S.anDays || 7;
  days.onchange = () => { S.anDays = days.value; render(); };
  const d = await api('GET', '/admin/analysis?days=' + days.value);
  const o = d.overall, n = o.below + o.average + o.excellent;
  const maxT = Math.max(1, ...d.daily.map((x) => x.tests));
  shellMount({ title: 'Weekly analysis', sub: 'Performance of submitted tests', cta: days, content: [
    h('div', { class: 'stats' }, statCard('', d.tests, 'Tests submitted', 'in the last ' + d.days + ' days', 't-olive'), statCard('', n, 'Marks analysed', o.absent + ' absent / exempt', 't-sky'), statCard('', o.avg + '%', 'Average score', '', 't-butter'), statCard('', o.below, 'Below average', 'students to watch', 't-pink')),
    h('div', { class: 'card' }, h('h3', {}, 'Overall'), n === 0 ? h('div', { class: 'empty' }, 'No locked tests in this period yet.') :
      h('div', { class: 'row', style: 'gap:28px;align-items:center;flex-wrap:wrap' }, donut(o), h('div', {}, Object.keys(TAGS).map((k) => h('div', { style: 'margin:6px 0' }, h('span', { style: 'display:inline-block;width:12px;height:12px;border-radius:3px;margin-right:8px;background:' + TAGS[k][1] }), h('b', {}, TAGS[k][0]), ' · ' + o[k] + ' (' + Math.round(o[k] / n * 100) + '%)')),
        h('div', { class: 'small mute', style: 'margin-top:8px' }, 'Below average is under ' + d.rules.below_under + '%, average ' + d.rules.below_under + '–' + (d.rules.excellent_from - 0.01) + '%, excellent ' + d.rules.excellent_from + '% and above.')))),
    h('div', { class: 'card' }, h('h3', {}, 'Tests per day'), h('div', { style: 'display:flex;align-items:flex-end;gap:8px;height:120px;margin-top:10px' },
      d.daily.map((x) => h('div', { style: 'flex:1;display:flex;flex-direction:column;align-items:center;justify-content:flex-end;height:100%;min-width:0' },
        h('div', { class: 'small' }, x.tests || ''), h('div', { title: x.tests + ' tests', style: 'width:100%;max-width:46px;border-radius:10px 10px 4px 4px;background:var(--lav);height:' + (x.tests ? Math.max(6, x.tests / maxT * 80) : 2) + 'px;opacity:' + (x.tests ? 1 : .3) }),
        h('div', { class: 'small mute', style: 'margin-top:4px' }, x.date.slice(8) + '/' + x.date.slice(5, 7)))))),
    h('div', { class: 'card' }, h('div', { class: 'row sp' }, h('h3', { style: 'margin:0' }, 'By class'), tagLegend()), barRows(d.by_class, 'label')),
    h('div', { class: 'card' }, h('div', { class: 'row sp' }, h('h3', { style: 'margin:0' }, 'By subject'), tagLegend()), barRows(d.by_subject, 'label')),
    h('div', { class: 'card' }, h('h3', {}, 'By teacher'), d.teachers.length === 0 ? h('div', { class: 'empty' }, 'No submitted tests in this period.') :
      h('div', { class: 'tscroll' }, h('table', { class: 'tbl' }, h('thead', {}, h('tr', {}, ['Teacher', 'Tests', 'Average', 'Below', 'Average', 'Excellent'].map((x) => h('th', {}, x)))),
        h('tbody', {}, d.teachers.map((t) => h('tr', {}, h('td', {}, t.name), h('td', {}, t.tests), h('td', {}, t.avg + '%'), h('td', {}, t.below), h('td', {}, t.average), h('td', {}, t.excellent))))))),
    h('div', { class: 'card' }, h('h3', {}, 'Students to watch (' + d.weak_total + ')'), d.weak.length === 0 ? h('div', { class: 'empty' }, 'No students below ' + d.rules.below_under + '%.') :
      h('div', { class: 'tscroll' }, h('table', { class: 'tbl' }, h('thead', {}, h('tr', {}, ['Student', 'Roll', 'Class', 'Subject', 'Score'].map((x) => h('th', {}, x)))),
        h('tbody', {}, d.weak.map((w) => h('tr', {}, h('td', {}, w.name), h('td', {}, w.roll), h('td', {}, w.class), h('td', {}, w.subject), h('td', {}, h('span', { class: 'badge red' }, w.pct + '%')))))))),
  ] });
}
async function pgAdminStudents() {
  const cls = await api('GET', '/admin/classes');
  const sel = h('select', {}, h('option', { value: '' }, 'All classes'), cls.classes.map((c) => h('option', { value: c.class_name + '\x00' + c.section }, c.label + ' (' + c.students + ')')));
  const q = h('input', { placeholder: 'Search name or admission no.', type: 'search' });
  const list = h('div');
  const load = async () => {
    const [c, sc] = sel.value.split('\x00');
    const d = await api('GET', '/admin/students?q=' + encodeURIComponent(q.value) + (sel.value ? '&class_name=' + encodeURIComponent(c) + '&section=' + encodeURIComponent(sc) : ''));
    fill(list, h('div', { class: 'small mute', style: 'margin:10px 0' }, d.total + ' students' + (d.total > d.students.length ? ' (showing first ' + d.students.length + ')' : '')),
      h('div', { class: 'tscroll' }, h('table', { class: 'tbl' }, h('thead', {}, h('tr', {}, ['Class', 'Section', 'Roll', 'Admission no.', 'Name'].map((x) => h('th', {}, x)))),
        h('tbody', {}, d.students.map((s) => h('tr', {}, h('td', {}, s.class_name), h('td', {}, s.section), h('td', {}, s.roll_no), h('td', {}, s.admission_no), h('td', {}, s.name)))))));
  };
  sel.onchange = load; q.oninput = load;
  const o = S.counts;
  shellMount({ title: 'Students', sub: 'Roster fetched from Frappe', content: [
    h('div', { class: 'card' }, h('div', { class: 'row sp' }, h('div', {}, h('b', {}, 'Frappe ERP'), h('div', { class: 'small mute' }, o.frappe_configured ? 'Connected. Last fetch: ' + (o.last_master_sync ? fmtTs(o.last_master_sync) : 'never') : 'Not configured (FRAPPE_URL / FRAPPE_API_KEY / FRAPPE_API_SECRET)')),
      h('button', { class: 'btn', disabled: !o.frappe_configured, onclick: (e) => runMasterSync(e.target) }, 'Fetch students from Frappe'))),
    studentImportCard(),
    h('div', { class: 'card' }, h('div', { class: 'row' }, h('div', { style: 'flex:1;min-width:180px' }, sel), h('div', { style: 'flex:2;min-width:180px' }, q)), list)] });
  load();
}
async function allocEditor(holder, u, scope) {
  if (holder.childNodes.length) return fill(holder);
  const cur = (await api('GET', '/admin/users/' + u.id + '/allocations')).allocations;
  const sel = new Set(cur.map((a) => a.class_name + '\x00' + a.section + '\x00' + a.subject));
  add(holder, h('div', { style: 'margin-top:10px' }, scope.groups.map((g) => {
    const box = h('div', { class: 'chips', style: 'margin-top:8px' });
    const drawChips = () => fill(box, g.subjects.map((s) => { const k = g.class_name + '\x00' + g.section + '\x00' + s; return h('button', { class: 'chip' + (sel.has(k) ? ' on' : ''), onclick: () => { sel.has(k) ? sel.delete(k) : sel.add(k); drawChips(); } }, s); }));
    drawChips();
    return h('details', { open: g.subjects.some((s) => sel.has(g.class_name + '\x00' + g.section + '\x00' + s)) }, h('summary', {}, g.label), box);
  }), h('button', { class: 'btn', style: 'margin-top:10px', onclick: async () => {
    const allocations = [...sel].map((k) => { const [class_name, section, subject] = k.split('\x00'); return { class_name, section, subject }; });
    try { await api('PUT', '/admin/users/' + u.id + '/allocations', { allocations }); toast('Allocations saved'); render(); } catch (e) { toast(e.message, true); } } }, 'Save allocations')));
}
async function pgAdminSync() {
  const o = S.counts; let last = o.last_master_result;
  try { const j = JSON.parse(last); last = j.students_seen + ' students in ' + j.groups + ' groups (' + j.students_added + ' new, ' + j.students_updated + ' updated, ' + j.students_retired + ' retired), ' + j.courses + ' courses, ' + j.duration_ms + ' ms'; } catch (_) {}
  const d = await api('GET', '/admin/exams?status=');
  const bad = d.exams.filter((r) => r.sync.state === 'red');
  const btn = h('button', { class: 'btn', onclick: (e) => runMasterSync(e.target) }, 'Sync roster now');
  shellMount({ title: 'Frappe sync', sub: 'Roster pull and marks push', content: [
    h('div', { class: 'card' }, h('h3', {}, 'Master roster'), !o.frappe_configured && h('div', { class: 'banner bad' }, 'Frappe is not configured (FRAPPE_URL / FRAPPE_API_KEY / FRAPPE_API_SECRET). Marks are safe here and sync once it is.'),
      h('p', { class: 'mute' }, 'Pulls every 7 days. Last sync: ', h('b', {}, o.last_master_sync ? fmtTs(o.last_master_sync) : 'never'), h('br'), last), btn),
    h('div', { class: 'card' }, h('h3', {}, 'What the badges mean'), h('div', { class: 'row' }, h('span', { class: 'badge yellow' }, 'Saved here'), h('span', { class: 'badge green' }, 'Synced to ERP'), h('span', { class: 'badge red' }, 'Retrying'))),
    h('div', { class: 'card' }, h('h3', {}, 'Needs attention (' + bad.length + ')'), bad.length === 0 ? h('div', { class: 'empty' }, 'Everything is syncing normally.') : bad.map((r) => h('div', { class: 'li' }, h('div', {}, h('b', {}, r.exam.label), h('div', { class: 'small mute' }, r.sync.last_error || 'queued')), syncBadge(r.sync)))),
  ] });
}

// ---------- staff: per-student weekly report (screen + print) ----------
const R = { cls: null, sid: '', days: 7 };
async function pgReports() {
  const scope = await api('GET', '/scope');
  const groups = scope.groups;
  if (!R.cls || !groups.find((g) => g.class_name === R.cls.class_name && g.section === R.cls.section)) R.cls = groups[0] || null;
  const out = h('div');
  const clsSel = h('select', { style: 'width:auto', onchange: () => { R.cls = groups[+clsSel.value]; R.sid = ''; render(); } }, groups.map((g, i) => h('option', { value: i, selected: R.cls && g.class_name === R.cls.class_name && g.section === R.cls.section }, g.label)));
  const daySel = h('select', { style: 'width:auto', onchange: () => { R.days = +daySel.value; load(); } }, [[7, 'Last 7 days'], [14, 'Last 14 days'], [30, 'Last 30 days']].map(([v, l]) => h('option', { value: v, selected: R.days === v }, l)));
  const stuSel = h('select', { style: 'width:auto;max-width:100%', onchange: () => { R.sid = stuSel.value; load(); } }, h('option', { value: '' }, 'Whole class'));
  const printBtn = h('button', { class: 'btn', onclick: () => window.print() }, 'Print');
  shellMount({ title: isAdmin() ? 'Student reports' : 'Weekly reports', sub: 'Parents see these automatically each week; print copies here', content: [
    groups.length === 0 ? h('div', { class: 'card' }, h('div', { class: 'empty' }, 'No classes available.')) :
      [h('div', { class: 'row noprint', style: 'margin-bottom:14px;gap:10px;flex-wrap:wrap' }, clsSel, stuSel, daySel, printBtn), out]] });
  if (!R.cls) return;
  const base = () => '/reports/students?class_name=' + encodeURIComponent(R.cls.class_name) + '&section=' + encodeURIComponent(R.cls.section) + '&days=' + R.days;
  let all = null;
  const load = async () => {
    if (!all || all.key !== base()) {
      const d = await api('GET', base()); all = { key: base(), d };
      fill(stuSel, h('option', { value: '' }, 'Whole class (' + d.students.length + ')'), d.students.map((x) => h('option', { value: x.id, selected: String(x.id) === String(R.sid) }, (x.roll_no ? x.roll_no + '. ' : '') + x.name)));
    }
    const d = all.d;
    const list = d.students.filter((x) => !R.sid || String(x.id) === String(R.sid));
    fill(out, list.length === 0 ? h('div', { class: 'card' }, h('div', { class: 'empty' }, 'No students.')) : list.map((x) => reportCard(x, d)));
  };
  await load();
}
function reportCard(x, d) {
  const tag = (t) => t ? h('span', { class: 'rtag', style: 'background:' + TAGS[t][1] }, TAGS[t][0]) : h('span', { class: 'mute' }, '-');
  const bars = x.subjects.map((s) => h('div', { class: 'rbar' }, h('div', { class: 'rl' }, s.subject),
    h('div', { class: 'rtrack' }, h('div', { class: 'rfill', style: 'width:' + Math.min(100, s.avg) + '%;background:' + TAGS[s.tag][1] }), h('div', { class: 'rcls', style: 'left:' + Math.min(100, s.class_avg) + '%', title: 'Class average ' + s.class_avg + '%' })),
    h('div', { class: 'rv' }, s.avg + '%')));
  return h('section', { class: 'card rcard' },
    h('div', { class: 'rhead' }, h('div', { class: 'rschool' }, 'GCM CONVENT SCHOOL'), h('div', { class: 'rtitle' }, 'Weekly Progress Report'), h('div', { class: 'rper' }, fmtDate(d.from) + ' – ' + fmtDate(d.to))),
    h('div', { class: 'rinfo' }, h('div', {}, h('small', {}, 'Student'), h('b', {}, x.name)), h('div', {}, h('small', {}, 'Class'), h('b', {}, d.class)), h('div', {}, h('small', {}, 'Roll no.'), h('b', {}, x.roll_no || '-')), h('div', {}, h('small', {}, 'Admission no.'), h('b', {}, x.admission_no || '-')),
      x.father_name && h('div', {}, h('small', {}, 'Parent / Guardian'), h('b', {}, x.father_name))),
    x.tests + x.absent === 0 ? h('div', { class: 'empty' }, 'No approved tests in this period.') : [
      h('div', { class: 'rsum' }, h('div', {}, h('small', {}, 'Overall average'), h('b', { class: 'big' }, x.tests ? x.avg + '%' : '-')), h('div', {}, h('small', {}, 'Performance'), tag(x.tag)), h('div', {}, h('small', {}, 'Tests taken'), h('b', {}, x.tests)), h('div', {}, h('small', {}, 'Absent / exempt'), h('b', {}, x.absent))),
      x.subjects.length > 0 && [h('h4', {}, 'Subject-wise average'), h('div', { class: 'rbars' }, bars), h('div', { class: 'small mute' }, 'Coloured bar = student · marker line = class average')],
      h('h4', {}, 'Test details'),
      h('div', { class: 'tscroll' }, h('table', { class: 'tbl' }, h('thead', {}, h('tr', {}, ['Date', 'Test', 'Subject', 'Marks', '%', 'Class avg', 'Remark'].map((c) => h('th', {}, c)))),
        h('tbody', {}, x.detail.map((t) => h('tr', {}, h('td', {}, fmtDate(t.date)), h('td', {}, t.test), h('td', {}, t.subject),
          h('td', {}, t.status ? (t.status === 'AB' ? 'Absent' : 'Exempt') : t.score + ' / ' + t.max), h('td', {}, t.status ? '-' : t.pct + '%'), h('td', {}, t.status ? '-' : t.class_avg + '%'), h('td', {}, tag(t.tag)))))))],
    h('div', { class: 'rsign' }, h('div', {}, 'Class Teacher'), h('div', {}, 'Parent / Guardian'), h('div', {}, 'Principal')));
}

// ---------- parents: sign in with admission no. + last 4 digits, then the weekly report ----------
const PT = {
  en: { school: 'GCM CONVENT SCHOOL', loginT: ["Your child's", 'week.'], loginS: 'Enter the admission number and the last 4 digits of the mobile number the school has for you.', adm: 'Admission number', last4: 'Last 4 digits of mobile', go: 'See the report', help: "Either parent's mobile number works.", staff: 'Teacher / Admin', parent: 'Parent',
    weekOf: 'Week of {d}', soFar: 'This week so far', avgLbl: 'average this week', ahead: 'Ahead of {p}% of class {c} this week', up: '{n} points up on last week', down: '{n} points down on last week', same: 'Same as last week',
    pct: 'CLASS PERCENTILE', pctOf: 'of {n} students', best: 'STRONGEST', focus: 'PRACTISE', bySub: 'By subject', avgKey: '| = class average', trend: 'Term trend', trendKey: 'Class percentile each week', tests: 'Tests this week', ab: 'Absent', ex: 'Exempt',
    tryT: 'ONE THING TO TRY', tryWeak: 'Ten minutes of {s} practice on most evenings this week. {n} scored {p}% against a class average of {c}%.', tryGood: '{n} is at or above the class average in every subject. Ask them to explain one thing they learned this week.',
    seen: "I've seen this", seenDone: 'Seen · thank you', seenNote: 'The school can see that you have read this report.', none: 'No approved tests yet this term. The report appears here once the school approves the first marks.', out: 'Sign out', noTest: 'No marks this week' },
  hi: { school: 'GCM CONVENT SCHOOL', loginT: ['आपके बच्चे का', 'हफ़्ता'], loginS: 'प्रवेश संख्या और स्कूल में दर्ज मोबाइल नंबर के आख़िरी 4 अंक डालें।', adm: 'प्रवेश संख्या', last4: 'मोबाइल के आख़िरी 4 अंक', go: 'रिपोर्ट देखें', help: 'माता या पिता, किसी का भी नंबर चलेगा।', staff: 'शिक्षक / एडमिन', parent: 'अभिभावक',
    weekOf: '{d} से शुरू हफ़्ता', soFar: 'इस हफ़्ते अब तक', avgLbl: 'इस हफ़्ते का औसत', ahead: 'इस हफ़्ते कक्षा {c} के {p}% बच्चों से आगे', up: 'पिछले हफ़्ते से {n} अंक ज़्यादा', down: 'पिछले हफ़्ते से {n} अंक कम', same: 'पिछले हफ़्ते जितना ही',
    pct: 'कक्षा परसेंटाइल', pctOf: '{n} बच्चों में', best: 'सबसे अच्छा', focus: 'अभ्यास करें', bySub: 'विषय अनुसार', avgKey: '| = कक्षा औसत', trend: 'सत्र का रुझान', trendKey: 'हर हफ़्ते का कक्षा परसेंटाइल', tests: 'इस हफ़्ते के टेस्ट', ab: 'अनुपस्थित', ex: 'छूट',
    tryT: 'इस हफ़्ते एक कोशिश', tryWeak: 'इस हफ़्ते ज़्यादातर शाम {s} का दस मिनट अभ्यास करें। {n} के {p}% अंक आए, कक्षा का औसत {c}% है।', tryGood: '{n} हर विषय में कक्षा औसत के बराबर या ऊपर है। उनसे इस हफ़्ते सीखी एक बात समझाने को कहें।',
    seen: 'मैंने देख लिया', seenDone: 'देख लिया · धन्यवाद', seenNote: 'स्कूल देख सकता है कि आपने यह रिपोर्ट पढ़ ली है।', none: 'इस सत्र में अभी कोई स्वीकृत टेस्ट नहीं है। स्कूल के पहले अंक स्वीकृत करते ही रिपोर्ट यहाँ दिखेगी।', out: 'साइन आउट', noTest: 'इस हफ़्ते कोई अंक नहीं' },
  pa: { school: 'GCM CONVENT SCHOOL', loginT: ['ਤੁਹਾਡੇ ਬੱਚੇ ਦਾ', 'ਹਫ਼ਤਾ'], loginS: 'ਦਾਖ਼ਲਾ ਨੰਬਰ ਅਤੇ ਸਕੂਲ ਕੋਲ ਦਰਜ ਮੋਬਾਈਲ ਨੰਬਰ ਦੇ ਆਖ਼ਰੀ 4 ਅੰਕ ਭਰੋ।', adm: 'ਦਾਖ਼ਲਾ ਨੰਬਰ', last4: 'ਮੋਬਾਈਲ ਦੇ ਆਖ਼ਰੀ 4 ਅੰਕ', go: 'ਰਿਪੋਰਟ ਦੇਖੋ', help: 'ਮਾਤਾ ਜਾਂ ਪਿਤਾ, ਕਿਸੇ ਦਾ ਵੀ ਨੰਬਰ ਚੱਲੇਗਾ।', staff: 'ਅਧਿਆਪਕ / ਐਡਮਿਨ', parent: 'ਮਾਪੇ',
    weekOf: '{d} ਤੋਂ ਸ਼ੁਰੂ ਹਫ਼ਤਾ', soFar: 'ਇਸ ਹਫ਼ਤੇ ਹੁਣ ਤੱਕ', avgLbl: 'ਇਸ ਹਫ਼ਤੇ ਦੀ ਔਸਤ', ahead: 'ਇਸ ਹਫ਼ਤੇ ਜਮਾਤ {c} ਦੇ {p}% ਬੱਚਿਆਂ ਤੋਂ ਅੱਗੇ', up: 'ਪਿਛਲੇ ਹਫ਼ਤੇ ਨਾਲੋਂ {n} ਅੰਕ ਵੱਧ', down: 'ਪਿਛਲੇ ਹਫ਼ਤੇ ਨਾਲੋਂ {n} ਅੰਕ ਘੱਟ', same: 'ਪਿਛਲੇ ਹਫ਼ਤੇ ਜਿੰਨਾ ਹੀ',
    pct: 'ਜਮਾਤ ਪਰਸੈਂਟਾਈਲ', pctOf: '{n} ਬੱਚਿਆਂ ਵਿੱਚੋਂ', best: 'ਸਭ ਤੋਂ ਵਧੀਆ', focus: 'ਅਭਿਆਸ ਕਰੋ', bySub: 'ਵਿਸ਼ੇ ਅਨੁਸਾਰ', avgKey: '| = ਜਮਾਤ ਦੀ ਔਸਤ', trend: 'ਸੈਸ਼ਨ ਦਾ ਰੁਝਾਨ', trendKey: 'ਹਰ ਹਫ਼ਤੇ ਦਾ ਜਮਾਤ ਪਰਸੈਂਟਾਈਲ', tests: 'ਇਸ ਹਫ਼ਤੇ ਦੇ ਟੈਸਟ', ab: 'ਗੈਰਹਾਜ਼ਰ', ex: 'ਛੋਟ',
    tryT: 'ਇਸ ਹਫ਼ਤੇ ਇੱਕ ਕੋਸ਼ਿਸ਼', tryWeak: 'ਇਸ ਹਫ਼ਤੇ ਜ਼ਿਆਦਾਤਰ ਸ਼ਾਮਾਂ ਨੂੰ {s} ਦਾ ਦਸ ਮਿੰਟ ਅਭਿਆਸ ਕਰੋ। {n} ਦੇ {p}% ਅੰਕ ਆਏ, ਜਮਾਤ ਦੀ ਔਸਤ {c}% ਹੈ।', tryGood: '{n} ਹਰ ਵਿਸ਼ੇ ਵਿੱਚ ਜਮਾਤ ਦੀ ਔਸਤ ਦੇ ਬਰਾਬਰ ਜਾਂ ਉੱਪਰ ਹੈ। ਉਨ੍ਹਾਂ ਨੂੰ ਇਸ ਹਫ਼ਤੇ ਸਿੱਖੀ ਇੱਕ ਗੱਲ ਸਮਝਾਉਣ ਲਈ ਕਹੋ।',
    seen: 'ਮੈਂ ਦੇਖ ਲਿਆ', seenDone: 'ਦੇਖ ਲਿਆ · ਧੰਨਵਾਦ', seenNote: 'ਸਕੂਲ ਦੇਖ ਸਕਦਾ ਹੈ ਕਿ ਤੁਸੀਂ ਇਹ ਰਿਪੋਰਟ ਪੜ੍ਹ ਲਈ ਹੈ।', none: 'ਇਸ ਸੈਸ਼ਨ ਵਿੱਚ ਹਾਲੇ ਕੋਈ ਮਨਜ਼ੂਰ ਟੈਸਟ ਨਹੀਂ। ਸਕੂਲ ਵੱਲੋਂ ਪਹਿਲੇ ਅੰਕ ਮਨਜ਼ੂਰ ਹੁੰਦੇ ਹੀ ਰਿਪੋਰਟ ਇੱਥੇ ਦਿਖੇਗੀ।', out: 'ਸਾਈਨ ਆਊਟ', noTest: 'ਇਸ ਹਫ਼ਤੇ ਕੋਈ ਅੰਕ ਨਹੀਂ' },
};
const LOCALE = { en: 'en-IN', hi: 'hi-IN', pa: 'pa-IN' };
function pLang() { const s = store.get('rea:lang'); if (PT[s]) return s; const n = (navigator.language || 'en').toLowerCase(); return n.startsWith('hi') ? 'hi' : n.startsWith('pa') ? 'pa' : 'en'; }
const tr = (k, vars) => { let s = PT[pLang()][k] || PT.en[k]; for (const [a, b] of Object.entries(vars || {})) s = s.split('{' + a + '}').join(b); return s; };
const pDate = (s, o) => { const [y, m, d] = s.split('-').map(Number); return new Date(y, m - 1, d).toLocaleDateString(LOCALE[pLang()], o || { day: 'numeric', month: 'short' }); };
function langSwitch(onchange) {
  return h('div', { class: 'lang', role: 'group', 'aria-label': 'Language' }, [['en', 'EN'], ['hi', 'हिं'], ['pa', 'ਪੰ']].map(([k, l]) =>
    h('button', { class: pLang() === k ? 'on' : '', 'aria-pressed': String(pLang() === k), onclick: () => { store.set('rea:lang', k); document.documentElement.lang = k; onchange(); } }, l)));
}
const P = { week: '' };
async function viewParent() {
  document.body.className = 'sp-teacher'; document.documentElement.lang = pLang();
  let d;
  try { d = await api('GET', '/parent/report' + (P.week ? '?week=' + P.week : '')); }
  catch (e) { if (e.status === 401) return viewParentLogin(); return mount(h('div', { class: 'pp' }, h('div', { class: 'banner bad' }, e.message))); }
  const st = d.student, first = st.name.split(' ')[0];
  const head = h('div', { class: 'pp-hero' }, shape('flower', 200, 'var(--pink)', 'right:-60px;top:-50px'),
    h('div', { class: 'row sp', style: 'position:relative' }, h('div', { class: 'eyebrow' }, tr('school')), langSwitch(viewParent)),
    h('div', { style: 'position:relative;margin-top:20px;font-size:.88rem;font-weight:700' }, st.name + ' · ' + st.class + (d.week ? ' · ' + (d.complete ? tr('weekOf', { d: pDate(d.week) }) : tr('soFar')) : '')),
    d.avg != null ? [h('div', { class: 'big' }, Math.round(d.avg) + '%'), h('div', { class: 'sub' }, tr('ahead', { p: d.percentile, c: st.class }) + (d.prev_avg != null ? '. ' + (Math.round(d.avg - d.prev_avg) > 0 ? tr('up', { n: Math.round(d.avg - d.prev_avg) }) : Math.round(d.avg - d.prev_avg) < 0 ? tr('down', { n: Math.round(d.prev_avg - d.avg) }) : tr('same')) + '.' : ''))]
      : h('div', { class: 'sub', style: 'margin-top:14px;padding-bottom:20px' }, d.week ? tr('noTest') : tr('none')));
  const body = [];
  if (d.week) {
    const subs = d.subjects || [];
    const best = subs[0], weak = subs.length > 1 ? subs[subs.length - 1] : null;
    body.push(h('div', { class: 'tiles2', style: 'margin-top:10px' },
      d.percentile != null && h('div', { class: 'tile t-olive', style: 'padding:14px' }, h('div', { class: 'eyebrow' }, tr('pct')), h('div', { class: 'disp', style: 'font-size:2.6rem;margin-top:6px' }, d.percentile), h('div', { class: 'small', style: 'font-weight:700' }, tr('pctOf', { n: d.class_size }))),
      best && h('div', { class: 'tile t-sky', style: 'padding:14px' }, h('div', { class: 'eyebrow' }, tr('best')), h('div', { class: 'disp', style: 'font-size:1.6rem;margin-top:6px' }, best.subject), h('div', { class: 'small', style: 'font-weight:700' }, Math.round(best.pct) + '%')),
      weak && h('div', { class: 'tile t-pink', style: 'padding:14px' }, h('div', { class: 'eyebrow' }, tr('focus')), h('div', { class: 'disp', style: 'font-size:1.6rem;margin-top:6px' }, weak.subject), h('div', { class: 'small', style: 'font-weight:700' }, Math.round(weak.pct) + '%'))));
    if (subs.length) body.push(h('div', { class: 'card', style: 'margin-top:12px' }, h('div', { class: 'row sp' }, h('h3', { class: 'disp', style: 'font-size:1.4rem;margin:0' }, tr('bySub')), h('span', { class: 'small mute' }, tr('avgKey'))),
      subs.map((s, i) => h('div', { class: 'sbar', style: 'margin-top:12px' }, h('div', { class: 'row sp', style: 'font-weight:700' }, h('span', {}, s.subject), h('span', {}, Math.round(s.pct) + '%')),
        h('div', { class: 'tr' }, h('div', { class: 'fl', style: 'width:' + Math.min(100, s.pct) + '%;background:' + SWATCH[i % 4] }), h('div', { class: 'cm', style: 'left:' + Math.min(99, s.class_avg) + '%' }))))));
    const below = subs.filter((s) => s.pct < s.class_avg).sort((a, b) => (a.pct - a.class_avg) - (b.pct - b.class_avg))[0];
    if (subs.length) body.push(h('div', { class: 'tile t-lav', style: 'margin-top:12px;padding:16px' }, h('div', { class: 'eyebrow' }, tr('tryT')),
      h('div', { style: 'font-weight:600;line-height:1.45;margin-top:6px' }, below ? tr('tryWeak', { s: below.subject, n: first, p: Math.round(below.pct), c: Math.round(below.class_avg) }) : tr('tryGood', { n: first }))));
    if ((d.tests || []).length) body.push(h('div', { class: 'card', style: 'margin-top:12px;padding:8px 16px' }, h('div', { class: 'eyebrow', style: 'padding:8px 0' }, tr('tests')),
      d.tests.map((t) => h('div', { class: 'row', style: 'padding:10px 0;border-top:1px solid var(--line);flex-wrap:nowrap' }, h('span', { style: 'width:10px;height:10px;border-radius:50%;flex:none;background:' + swatchFor(t.subject) }),
        h('div', { class: 'grow' }, h('b', {}, t.subject), h('span', { class: 'mute' }, ' · ' + t.test), h('div', { class: 'small mute' }, pDate(t.date))),
        h('div', { class: 'disp', style: 'font-size:1.3rem;font-variant-numeric:tabular-nums' }, t.status ? (t.status === 'AB' ? tr('ab') : tr('ex')) : t.score + '/' + t.max)))));
  }
  const weeks = d.weeks.filter((w) => w.percentile != null);
  if (weeks.length > 1) body.push(h('div', { class: 'card', style: 'margin-top:12px' }, h('div', { class: 'row sp' }, h('h3', { class: 'disp', style: 'font-size:1.4rem;margin:0' }, tr('trend')), h('span', { class: 'small mute' }, tr('trendKey'))),
    h('div', { class: 'trend' }, weeks.slice(-12).map((w) => h('div', { style: 'cursor:pointer', onclick: () => { P.week = w.week; viewParent(); } }, h('small', { style: 'color:var(--ink)' }, w.percentile), h('div', { class: 'b' + (w.week === d.week ? ' on' : ''), style: 'height:' + Math.max(6, w.percentile) + '%' }), h('small', {}, pDate(w.week, { day: 'numeric', month: 'numeric' })))))));
  if (d.week) {
    const seenBtn = h('button', { class: 'btn block', id: 'seen', style: 'margin-top:14px;min-height:58px;font-size:1.02rem' + (d.seen_at ? ';background:var(--olive);color:#17150F' : ''), onclick: async () => {
      if (d.seen_at) return;
      try { await api('POST', '/parent/seen', { week: d.week }); d.seen_at = new Date().toISOString(); seenBtn.textContent = tr('seenDone'); seenBtn.style.background = 'var(--olive)'; seenBtn.style.color = '#17150F'; } catch (e) { toast(e.message, true); }
    } }, d.seen_at ? tr('seenDone') : tr('seen'));
    body.push(seenBtn, h('div', { class: 'small mute', style: 'text-align:center;margin-top:8px' }, tr('seenNote')));
  }
  body.push(h('div', { class: 'row', style: 'justify-content:center;margin-top:24px' },
    h('button', { class: 'btn ghost sm', onclick: cycleTheme }, icon('moon')),
    h('button', { class: 'btn ghost sm', onclick: async () => { try { await api('POST', '/parent/logout', {}); } catch (_) {} P.week = ''; viewParentLogin(); } }, tr('out'))));
  mount(h('div', { class: 'pp' }, head, body));
}
function viewParentLogin() {
  document.body.className = 'sp-teacher'; document.documentElement.lang = pLang();
  const adm = h('input', { class: 'lg-input', placeholder: 'e.g. ADM20211024', autocomplete: 'off', autocapitalize: 'characters', maxLength: 40, 'aria-label': tr('adm') });
  const l4 = h('input', { class: 'lg-input', placeholder: '• • • •', inputmode: 'numeric', maxLength: 4, autocomplete: 'off', 'aria-label': tr('last4'), style: 'letter-spacing:.4em;text-align:center;font-size:1.4rem' });
  const err = h('div', { class: 'lg-err', role: 'alert' });
  const btn = h('button', { class: 'lg-btn', type: 'submit' }, tr('go'));
  l4.oninput = () => { l4.value = l4.value.replace(/\D/g, '').slice(0, 4); };
  const submit = async (e) => {
    e.preventDefault(); err.textContent = '';
    if (!adm.value.trim() || l4.value.length !== 4) { err.textContent = tr('loginS'); return; }
    btn.disabled = true;
    try { await api('POST', '/parent/login', { admission_no: adm.value, last4: l4.value }); P.week = ''; viewParent(); }
    catch (x) { err.textContent = x.message; btn.disabled = false; }
  };
  const form = h('form', { class: 'lg-form', onsubmit: submit },
    h('div', { class: 'lg-tabs', role: 'tablist' }, h('button', { type: 'button', role: 'tab', onclick: () => { history.replaceState(null, '', location.pathname); S.me ? render() : boot(); } }, tr('staff')), h('button', { type: 'button', class: 'on', role: 'tab', 'aria-selected': 'true' }, tr('parent'))),
    h('div', { class: 'row sp' }, h('span'), langSwitch(viewParentLogin)),
    h('h1', { class: 'disp' }, tr('loginT')[0], h('br'), tr('loginT')[1]), h('p', { class: 'lg-sub' }, tr('loginS')),
    h('label', { class: 'lg-label' }, tr('adm')), adm, h('label', { class: 'lg-label' }, tr('last4')), l4, err, btn, h('p', { class: 'lg-help' }, tr('help')));
  mount(h('div', { class: 'lgn' }, loginHero(), h('div', { class: 'lg-panel' }, form, h('div', { class: 'lg-foot' }, 'GCM Convent School · UpMark'))));
  setTimeout(() => adm.focus(), 0);
}

// ---------- boot ----------
async function boot() {
  if (rawRoute().startsWith('parent')) return viewParent();
  try { const me = await cachedGet('/me'); S.me = me.user; S.data = me; render(); syncStashes(); } catch (e) { viewLogin(); }
}
window.addEventListener('beforeunload', () => { if (W && W.dirty && W.dirty.size) stash(); });
if ('serviceWorker' in navigator && location.protocol !== 'file:') window.addEventListener('load', () => navigator.serviceWorker.register('/sw.js').catch(() => {}));
boot();
