// Admin space (desktop): Pulse, the approvals inbox with checks and corrections, send back, and every admin page.
const { chromium } = require('playwright');
const BASE = process.env.BASE || 'http://localhost:8080';
let pass = 0, fail = 0;
const ok = (c, m) => { c ? pass++ : fail++; console.log((c ? '  PASS ' : '  FAIL ') + m); };
const shot = (p, n) => p.screenshot({ path: '/tmp/shots/' + n + '.png' });
const today = new Date().toISOString().slice(0, 10);

(async () => {
  require('fs').mkdirSync('/tmp/shots', { recursive: true });
  const browser = await chromium.launch({ executablePath: process.env.CHROME || undefined });
  const login = async (id, vp = { width: 1360, height: 860 }, scheme = 'light') => {
    const pg = await (await browser.newContext({ viewport: vp, colorScheme: scheme })).newPage();
    pg.on('pageerror', (e) => { fail++; console.log('  FAIL page error: ' + e.message); });
    await pg.goto(BASE); await pg.fill('.lg-input', id); await pg.click('text=Get my WorkCode');
    const code = await pg.locator('.lg-dev b').innerText();
    for (let i = 0; i < 6; i++) await pg.locator('.lg-box').nth(i).fill(code[i]);
    await pg.waitForSelector('.app'); return pg;
  };
  const call = (pg, m, p, b) => pg.evaluate(async ([m, p, b]) => { const r = await fetch('/api' + p, { method: m, headers: { 'Content-Type': 'application/json', 'X-Requested-With': 'rea' }, body: b ? JSON.stringify(b) : undefined }); return { status: r.status, data: await r.json().catch(() => ({})) }; }, [m, p, b]);
  const toast = (pg, re) => pg.waitForFunction((s) => new RegExp(s).test(document.getElementById('toast').textContent), re.source);

  console.log('Setup: Teacher B locks a sheet with a suspicious 0');
  const t = await login('9000000002', { width: 390, height: 780 });
  const ex = (await call(t, 'POST', '/exams', { class_name: 'Grade 8', section: 'A', category: 'Summative', sub_type: 'SA2', date: today, subjects: ['Hindi'], max_total: 20 })).data.exam_id;
  const roster = (await call(t, 'GET', '/exams/' + ex)).data.roster;
  await call(t, 'PUT', '/exams/' + ex + '/marks', { entries: roster.map((s, i) => ({ student_id: s.id, subject: 'Hindi', status: i === 0 ? 'AB' : '', comps: i === 0 ? [] : [i % 21] })) });
  ok((await call(t, 'POST', '/exams/' + ex + '/finalize', {})).status === 200, 'Sheet locked');

  console.log('Pulse');
  const a = await login('9000000000');
  ok((await a.locator('.brand small').first().innerText()) === 'ADMIN CONSOLE', 'Admin lands in the admin console');
  const nav = await a.locator('button.nav').allInnerTexts();
  ok(['Pulse', 'Approvals', 'Marks explorer', 'Corrections log', 'Teacher allocation', 'Frappe sync'].every((x) => nav.some((n) => n.includes(x))) && !nav.some((n) => n.includes('New test')), 'Admin rail: ' + nav.map((x) => x.split('\n')[0]).join(' | '));
  ok(/1 sheet/i.test(await a.locator('.pulse .lead').innerText()), 'Lead tile counts what is waiting');
  ok(await a.locator('.pulse .stat:has-text("Parents read")').count() === 1, 'Pulse shows the parents-read tile');
  await shot(a, 'a1-pulse');

  console.log('Approvals inbox');
  await a.click('button.nav[data-route="a/approvals"]'); await a.waitForSelector('#ibd .flag');
  ok(await a.locator('.ib-item .badge:has-text("to check")').count() === 1, 'Inbox row carries a warning badge');
  const flags = await a.locator('#ibd .flag.warn').allInnerTexts();
  ok(flags.some((f) => /scored 0/.test(f)), 'Detail warns about a 0 not marked absent');
  ok(await a.locator('#ibd .dist > div').count() === 5, 'Detail shows a five-band spread');
  await shot(a, 'a2-inbox');
  await a.click('#ibd button:has-text("Send back")');
  await a.click('.ov button:has-text("Send back")');
  ok(await a.locator('.ov .err-msg:has-text("Please write a note")').count() === 1, 'Send back needs a note');
  await a.fill('.ov textarea', 'Roll 21 shows 0, please check'); await a.click('.ov button:has-text("Send back")');
  await toast(a, /Sent back/);
  ok((await call(t, 'GET', '/exams/' + ex)).data.exam.status === 'draft', 'Teacher has the sheet back');
  await t.goto(BASE + '/#/exam/' + ex); await t.waitForSelector('.banner.warn');
  ok(/Roll 21 shows 0/.test(await t.locator('.banner.warn').innerText()), 'Teacher sees the note on the sheet');
  await call(t, 'POST', '/exams/' + ex + '/finalize', {});
  await a.click('button.nav[data-route="a/approvals"]'); await a.waitForSelector('#approve');
  await a.keyboard.press('a'); await toast(a, /Approved/);
  ok((await call(a, 'GET', '/admin/approvals?state=pending')).data.exams.length === 0, 'Keyboard A approves the sheet');

  console.log('Correction in the same inbox');
  const r = await call(t, 'POST', '/exams/' + ex + '/corrections', { student_id: roster[3].id, subject: 'Hindi', status: '', comps: [4], reason: 'Missed a question' });
  ok(r.status === 201 && r.data.state === 'pending', 'Teacher asks for a correction');
  await a.click('button.nav[data-route="a/approvals"]'); await a.waitForSelector('.ib-item .badge:has-text("Correction")');
  await a.click('.ib-item:has(.badge:has-text("Correction"))'); await a.waitForSelector('#ibd button:has-text("Approve change")');
  ok(/Missed a question/.test(await a.locator('#ibd').innerText()), 'Correction detail shows the reason');
  await a.click('#ibd button:has-text("Approve change")'); await toast(a, /Correction approved/);
  ok((await call(t, 'GET', '/notifications')).data.notifications.some((n) => n.kind === 'correction_approved'), 'Teacher is told');

  console.log('Every admin page renders');
  for (const [route, sel] of [['a/marks', 'table.tbl'], ['a/analysis', '.stats'], ['a/reports', '.rcard'], ['a/corrections', 'table.tbl'], ['a/classes', 'table.tbl'], ['a/students', 'table.tbl'], ['a/allocations', '.pills'], ['a/teachers', 'text=Add a teacher'], ['a/sync', 'text=Master roster']]) {
    await a.goto(BASE + '/#/' + route); await a.waitForSelector(sel, { timeout: 8000 }).then(() => ok(true, route), () => ok(false, route + ' did not render ' + sel));
  }
  await a.click('button.nav[data-route="a/allocations"]'); await a.click('.pill:text-is("Grade 10")'); await a.click('.pill:text-is("A")');
  await a.waitForSelector('text=Grade 10-A · 38 students'); ok(true, 'Allocation picks class then section');

  console.log('Dark desktop + teacher isolation');
  const d = await login('9000000000', { width: 1360, height: 860 }, 'dark');
  await d.goto(BASE + '/#/a/approvals'); await d.waitForSelector('#ibd');
  ok(await d.evaluate(() => getComputedStyle(document.body).backgroundColor) === 'rgb(18, 17, 16)', 'Admin console in dark mode');
  await shot(d, 'a3-dark-inbox');
  await t.goto(BASE + '/#/a/approvals'); await t.waitForSelector('.hero-card');
  ok(await t.locator('#ibd').count() === 0, 'A teacher opening an admin link lands on Today');

  await browser.close();
  console.log(`\n${pass} passed, ${fail} failed`);
  process.exit(fail ? 1 : 0);
})().catch((e) => { console.error(e); process.exit(1); });
