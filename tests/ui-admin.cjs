// Admin console smoke test (Playwright). Usage: node tests/ui-admin.cjs
const { chromium } = require('playwright');
const BASE = process.env.BASE || 'http://localhost:8080';
let pass = 0, fail = 0;
const ok = (c, m) => { c ? pass++ : fail++; console.log((c ? '  PASS ' : '  FAIL ') + m); };
(async () => {
  require('fs').mkdirSync('/tmp/shots', { recursive: true });
  const browser = await chromium.launch();
  const page = await (await browser.newContext({ viewport: { width: 1200, height: 800 } })).newPage();
  page.on('pageerror', (e) => { fail++; console.log('  FAIL page error: ' + e.message); });
  page.on('dialog', (d) => d.accept());
  // seed one draft as Teacher A through the API so the explorer has something to show
  const lg = async (id) => { const r = await (await fetch(BASE + '/api/auth/request', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ identifier: id }) })).json(); const v = await fetch(BASE + '/api/auth/verify', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ identifier: id, code: r.dev_code }) }); return v.headers.get('set-cookie').split(';')[0]; };
  const ck = await lg('9000000001');
  await fetch(BASE + '/api/exams', { method: 'POST', headers: { 'Content-Type': 'application/json', 'X-Requested-With': 'rea', cookie: ck }, body: JSON.stringify({ class_name: 'Grade 8', section: 'A', category: 'Formative', sub_type: 'FA1', date: new Date().toISOString().slice(0, 10), subjects: ['Hindi'], max_total: 20 }) });
  await page.goto(BASE); await page.fill('input', '9000000000'); await page.click('text=Send code');
  await page.fill('input', await page.locator('.banner b').innerText()); await page.click('text=Verify & sign in');
  await page.click('text=Admin Console');
  await page.waitForSelector('text=Sync Master Roster Now'); ok(true, 'Admin console opens for administrator');
  await page.click('text=Sync Master Roster Now'); await page.waitForFunction(() => /synced in/.test(document.getElementById('toast').textContent));
  await page.waitForSelector('text=Last sync:'); ok(!(await page.locator('.card', { hasText: 'Last sync' }).innerText()).includes('never'), 'Manual master sync recorded');
  await page.screenshot({ path: '/tmp/shots/a1-overview.png' });
  await page.click('text=Marks Explorer'); await page.waitForSelector('table.tbl');
  ok((await page.locator('table.tbl tbody tr').count()) >= 1, 'Marks Explorer lists tests with sync health: ' + (await page.locator('.badge').first().innerText()));
  await page.screenshot({ path: '/tmp/shots/a2-marks.png' });
  await page.click('text=Teachers & Allocations'); await page.waitForSelector('text=Add teacher');
  const [n, m, e] = await page.locator('.card input').all(); await n.fill('Teacher C'); await m.fill('9000000003'); await e.fill('c@school.test');
  page.on('response', async (r) => { if (r.url().includes('/admin/users') && r.request().method() === 'POST') console.log('  POST users ->', r.status(), await r.text()); }); await page.click('button:text-is("Add")'); await page.waitForSelector('text=Teacher C'); ok(true, 'Teacher added');
  await page.locator('.card', { hasText: 'Teacher C' }).locator('text=Classes & subjects').click();
  await page.locator('details', { hasText: 'Grade 10-A' }).evaluate((d) => (d.open = true));
  await page.locator('details', { hasText: 'Grade 10-A' }).locator('.chip:text-is("Science")').click(); await page.click('text=Save allocations');
  await page.waitForFunction(() => /Allocations saved/.test(document.getElementById('toast').textContent)); ok(true, 'Allocation saved');
  await page.screenshot({ path: '/tmp/shots/a3-users.png' });
  await page.click('text=Corrections'); await page.waitForSelector('text=Correction requests inbox'); ok(true, 'Corrections inbox renders');
  // a teacher must not reach the admin console
  const t = await (await browser.newContext()).newPage();
  await t.goto(BASE); await t.fill('input', '9000000003'); await t.click('text=Send code');
  await t.fill('input', await t.locator('.banner b').innerText()); await t.click('text=Verify & sign in'); await t.waitForSelector('#new');
  ok((await t.locator('text=Admin Console').count()) === 0, 'Teacher does not see the Admin Console entry');
  ok((await t.evaluate(() => fetch('/api/admin/users', { credentials: 'same-origin' }).then((r) => r.status))) === 403, 'Teacher gets 403 on admin API');
  await browser.close(); console.log(`\n${pass} passed, ${fail} failed`); process.exit(fail ? 1 : 0);
})().catch((e) => { console.error(e); process.exit(1); });
