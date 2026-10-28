// Post-lock correction flow through the real UI: teacher requests, admin approves, timeline + ERP verified.
const { chromium } = require('playwright');
const BASE = process.env.BASE || 'http://localhost:8080', MOCK = process.env.MOCK || 'http://localhost:8099';
let pass = 0, fail = 0;
const ok = (c, m) => { c ? pass++ : fail++; console.log((c ? '  PASS ' : '  FAIL ') + m); };
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
async function cookieFor(id) {
  const r = await (await fetch(BASE + '/api/auth/request', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ identifier: id }) })).json();
  const v = await fetch(BASE + '/api/auth/verify', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ identifier: id, code: r.dev_code }) });
  return v.headers.get('set-cookie').split(';')[0];
}
(async () => {
  // setup via API: a locked Punjabi test with every student graded
  const ck = await cookieFor('9000000001');
  const call = async (m, p, b) => (await fetch(BASE + '/api' + p, { method: m, headers: { 'Content-Type': 'application/json', 'X-Requested-With': 'rea', cookie: ck }, body: b ? JSON.stringify(b) : undefined })).json();
  const day = new Date(Date.now() - 3 * 864e5).toISOString().slice(0, 10);
  const c = await call('POST', '/exams', { class_name: 'Grade 8', section: 'A', category: 'Summative', sub_type: 'SA2', date: day, subjects: ['Punjabi'], max_total: 50 });
  const ex = await call('GET', '/exams/' + c.exam_id);
  await call('PUT', '/exams/' + c.exam_id + '/marks', { entries: ex.roster.map((s, i) => ({ student_id: s.id, subject: 'Punjabi', status: '', comps: [i === 13 ? 42 : 30 + (i % 15)] })) });
  const fin = await call('POST', '/exams/' + c.exam_id + '/finalize', {}); ok(fin.ok, 'setup: locked test with 35 students');

  const browser = await chromium.launch();
  const login = async (id) => {
    const pg = await (await browser.newContext({ viewport: { width: 1100, height: 800 } })).newPage();
    pg.on('pageerror', (e) => { fail++; console.log('  FAIL page error: ' + e.message); }); pg.on('dialog', (d) => d.accept());
    await pg.goto(BASE); await pg.fill('input', id); await pg.click('text=Send code');
    await pg.fill('input', await pg.locator('.banner b').innerText()); await pg.click('text=Verify & sign in'); await pg.waitForSelector('#new'); return pg;
  };
  const t = await login('9000000001');
  await t.locator('.card', { hasText: 'SA2' }).locator('text=View').click(); await t.waitForSelector('#prog');
  ok(await t.locator('text=🔒 Locked').count() === 1 && (await t.locator('#tbl input:not(:disabled)').count()) === 0, 'Locked test is read-only for the teacher (no editable inputs, no Save/Finalize)');
  ok((await t.locator('text=Finalize & Lock').count()) === 0, 'No global unlock / finalize controls on a locked test');
  await t.locator('#tbl tbody tr', { has: t.locator('td:text-is("14")') }).click();
  await t.waitForSelector('text=Request Correction Unlock');
  ok((await t.locator('.banner').innerText()).includes('Current locked score: 42'), 'Modal shows the current locked score (42)');
  await t.locator('.sheet input').first().fill('47');
  await t.click('text=Submit request'); await t.waitForFunction(() => document.querySelector('.sheet .err-msg').textContent.length > 0).then(() => ok(true, 'Reason is mandatory (server message: ' + 'shown)'), () => ok(false, 'Reason is mandatory'));
  await t.locator('.sheet textarea').fill('Question 7 marks were not added (re-checked paper)');
  await t.screenshot({ path: '/tmp/shots/c1-modal.png' });
  await t.click('text=Submit request'); await t.waitForFunction(() => /Correction request sent/.test(document.getElementById('toast').textContent)); ok(true, 'Correction request submitted (42 → 47)');
  ok(await t.locator('tr:has(td:text-is("14")) .badge', { hasText: 'correction pending' }).count() === 1, 'Row shows "correction pending"');

  const a = await login('9000000000');
  await a.click('text=Admin Console'); await a.click('text=Corrections'); await a.waitForSelector('text=Correction requests inbox');
  const card = a.locator('.card', { hasText: 'Question 7 marks' });
  ok((await card.innerText()).includes('42 → 47'), 'Admin inbox shows old → proposed value with reason');
  await a.screenshot({ path: '/tmp/shots/c2-inbox.png' });
  await card.locator('text=Approve').click(); await a.click('.sheet >> text=Confirm');
  await a.waitForFunction(() => /Approved/.test(document.getElementById('toast').textContent)); ok(true, 'Admin approves in one click');
  // ERP + timeline
  let doc;
  for (let i = 0; i < 30 && !doc; i++) { await sleep(400); const rs = await (await fetch(MOCK + '/__dump?doctype=Assessment%20Result')).json(); doc = rs.find((r) => r.student === 'STU-8A-014' && r.docstatus === 1 && r.details[0].score === 47); }
  ok(!!doc, 'Frappe Assessment Result for Roll 14 now 47 (amended: ' + (doc && doc.name) + ')');
  await a.click('text=Marks Explorer'); await a.locator('tr', { hasText: 'SA2' }).locator('text=Timeline').click();
  await a.waitForSelector('.tl .ev');
  const tl = await a.locator('.tl .ev b').allInnerTexts();
  ok(['Test created', 'Finalized & locked', 'Correction requested', 'Correction approved', 'Correction re-synced to ERP'].every((x) => tl.includes(x)), 'Timeline chain: ' + tl.join(' → '));
  await a.screenshot({ path: '/tmp/shots/c3-timeline.png' });
  await browser.close(); console.log(`\n${pass} passed, ${fail} failed`); process.exit(fail ? 1 : 0);
})().catch((e) => { console.error(e); process.exit(1); });
