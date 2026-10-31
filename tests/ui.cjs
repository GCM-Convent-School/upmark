// Browser acceptance checks for the teacher journey (Playwright/Chromium, phone viewport). Usage: node tests/ui.cjs
// Set CHROME=/path/to/chromium to use a specific browser build.
const { chromium } = require('playwright');
const BASE = process.env.BASE || 'http://localhost:8080';
let pass = 0, fail = 0;
const ok = (c, m) => { c ? pass++ : fail++; console.log((c ? '  PASS ' : '  FAIL ') + m); };
const shot = (p, n) => p.screenshot({ path: '/tmp/shots/' + n + '.png' });

async function signIn(page, ident) {
  await page.goto(BASE);
  await page.fill('.lg-input', ident); await page.click('text=Get my WorkCode');
  const code = await page.locator('.lg-dev b').innerText();
  const boxes = page.locator('.lg-box');
  for (let i = 0; i < 6; i++) await boxes.nth(i).fill(code[i]);
  await page.waitForSelector('.app');
}
const toast = (page, re) => page.waitForFunction((s) => new RegExp(s).test(document.getElementById('toast').textContent), re.source);

(async () => {
  require('fs').mkdirSync('/tmp/shots', { recursive: true });
  const browser = await chromium.launch({ executablePath: process.env.CHROME || undefined });
  const ctx = await browser.newContext({ viewport: { width: 390, height: 780 }, deviceScaleFactor: 2, hasTouch: true, colorScheme: 'light' });
  const page = await ctx.newPage();
  page.on('pageerror', (e) => { fail++; console.log('  FAIL page error: ' + e.message); });

  console.log('Sign in');
  await page.goto(BASE);
  ok(await page.locator('h1:has-text("Marks in")').count() === 1, 'Sign-in screen shows the Bold School headline');
  ok(await page.locator('.lg-tabs button:has-text("Parent")').count() === 1, 'Sign-in offers a Parent tab');
  await shot(page, 't1-login');
  await signIn(page, '9000000001');
  ok(await page.locator('.tabbar').isVisible(), 'Phone layout shows the bottom tab bar');
  ok(await page.locator('.side').evaluate((e) => e.getBoundingClientRect().right <= 0 || getComputedStyle(e).display === 'none'), 'Phone layout keeps the desktop rail off-screen');
  ok(await page.locator('.hero-card').count() === 1, 'Today screen leads with one hero card');
  await shot(page, 't2-today');

  console.log('New test on one screen');
  await page.click('.tabbar button[aria-label="New"]'); await page.waitForSelector('#start');
  ok(await page.locator('#start').isDisabled(), 'Start is disabled until the setup is complete');
  await page.click('.nt .pill:has-text("Grade 8")'); await page.click('.nt .pill:text-is("FA2")');
  const subs = await page.locator('.nt section:nth-of-type(4) .pill').allInnerTexts();
  ok(subs.includes('Hindi') && subs.includes('Punjabi') && !subs.includes('English'), 'Only assigned subjects offered: ' + subs.join(', '));
  await page.click('.nt .pill:text-is("Hindi")'); await page.click('.nt .pill:text-is("20")');
  ok((await page.locator('.sum').innerText()).includes('FA2 · Hindi · Today · /20'), 'Summary bar reads the whole setup');
  await shot(page, 't3-new');
  await page.click('#start'); await page.waitForSelector('#prog');
  ok((await page.locator('#prog').innerText()) === '0 / 35', 'Workspace opens at 0 / 35');
  ok(await page.locator('.seg button.on').getAttribute('aria-label') === 'Card view', 'Card view is the phone default');
  ok(await page.locator('text=Send to parents').count() === 0, 'Teachers have no Send to parents button');

  console.log('Card entry');
  const key = (k) => page.click('.pad button.k:text-is("' + k + '")');
  await key('1'); await key('4'); await key('.5');
  ok((await page.locator('.sc .box').first().innerText()) === '14.5', '1, 4, .5 shows 14.5');
  await shot(page, 't4-card');
  await page.click('#nextbtn');
  await key('2'); await key('5');
  ok(await page.locator('.sc .box.err').count() === 1, '25 out of 20 is flagged');
  await page.click('#nextbtn'); await toast(page, /Maximum is 20/); ok(true, 'Next is refused while the mark is over the maximum');
  await key('⌫'); await key('⌫'); await key('2'); await key('0');
  ok(await page.locator('.sc.full').count() === 1, 'Full marks get the burst');
  await page.click('#nextbtn');
  await page.click('.sc .pill.ab');
  ok((await page.locator('#prog').innerText()) === '3 / 35', 'Absent counts as marked and moves on: 3 / 35');
  await page.keyboard.press('1'); await page.keyboard.press('2'); await page.keyboard.press('Enter');
  ok((await page.locator('#prog').innerText()) === '4 / 35', 'A hardware keyboard works in card view');

  console.log('Quick type in the table');
  await page.click('.seg button[aria-label="Table view"]'); await page.waitForSelector('#qt');
  await page.fill('#qt', '17 ab 19.5 ex');
  ok(/fills Roll 5 to 8/.test(await page.locator('.qt .hint').innerText()), 'Quick type previews which rolls it fills');
  await page.press('#qt', 'Enter'); await toast(page, /4 students filled/);
  ok((await page.locator('#prog').innerText()) === '8 / 35', 'Quick type fills four students at once');
  await page.fill('#qt', '12 30');
  ok(/Maximum is 20/.test(await page.locator('.qt .hint').innerText()), 'Quick type refuses a mark over the maximum');
  await page.fill('#qt', Array.from({ length: 27 }, (_, i) => String(5 + (i % 15))).join(' '));
  await page.press('#qt', 'Enter'); await toast(page, /27 students filled/);
  ok((await page.locator('#prog').innerText()) === '35 / 35', 'Whole class marked: 35 / 35');
  const cell = page.locator('#tbl tbody tr').nth(1).locator('input.sc-in').first();
  await cell.fill('ex');
  ok((await page.locator('#tbl tbody tr').nth(1).locator('button.st.on').innerText()) === 'EX', 'Typing ex in a cell marks the student exempt');
  await shot(page, 't5-table');

  console.log('Lock & send to admin');
  await page.click('#lockbtn'); await page.click('.ov button:has-text("Lock & send")');
  await page.waitForSelector('.banner.info:has-text("With admin")');
  ok(true, 'Locked sheet shows "With admin"');
  const examId = +(await page.evaluate(() => location.hash.split('/').pop()));

  console.log('Updates from the admin');
  const actx = await browser.newContext(); const ap = await actx.newPage();
  await signIn(ap, '9000000000');
  const post = (p, b) => ap.evaluate(async ([p, b]) => (await fetch('/api' + p, { method: 'POST', headers: { 'Content-Type': 'application/json', 'X-Requested-With': 'rea' }, body: JSON.stringify(b) })).status, [p, b]);
  ok(await post('/admin/exams/' + examId + '/remark', { note: 'Neat work' }) === 200 && await post('/admin/exams/' + examId + '/approve', {}) === 200, 'Admin remarks and approves (API)');
  await page.goto(BASE + '/#/dashboard'); await page.waitForSelector('.note');
  const notes = await page.locator('.note').allInnerTexts();
  ok(notes.some((n) => /Approved/.test(n)) && notes.some((n) => /Neat work/.test(n)), 'Today shows the approval and the remark');
  await shot(page, 't6-updates');

  console.log('Correction request');
  await page.goto(BASE + '/#/exam/' + examId); await page.waitForSelector('.banner.ok');
  await page.click('.sc button:has-text("Ask for a correction")');
  await page.fill('.ov input[inputmode="decimal"]', '15'); await page.fill('.ov textarea', 'Missed a question');
  await page.click('.ov button:has-text("Send to admin")'); await toast(page, /Correction request sent/);
  await page.waitForSelector('.sc .badge:has-text("Correction requested")');
  ok(true, 'Request is sent and the card shows it is waiting');

  console.log('Offline marking');
  await page.goto(BASE + '/#/dashboard'); await page.waitForSelector('.hero-card');
  await page.evaluate(() => navigator.serviceWorker.ready);
  await page.reload(); await page.waitForSelector('.hero-card');
  ok(await page.evaluate(() => !!navigator.serviceWorker.controller), 'Service worker controls the page');
  const off = await page.evaluate(async () => (await (await fetch('/api/exams', { method: 'POST', headers: { 'Content-Type': 'application/json', 'X-Requested-With': 'rea' }, body: JSON.stringify({ class_name: 'Grade 8', section: 'A', category: 'Formative', sub_type: 'FA3', date: new Date().toISOString().slice(0, 10), subjects: ['Punjabi'], max_total: 10 }) })).json()).exam_id);
  await page.goto(BASE + '/#/exam/' + off); await page.waitForSelector('#prog');
  await ctx.setOffline(true);
  await page.reload(); await page.waitForSelector('#prog');
  ok(await page.evaluate(() => !navigator.onLine), 'Sheet reopens from the phone with no network');
  await page.click('.seg button[aria-label="Card view"]').catch(() => {});
  await page.keyboard.press('7'); await page.keyboard.press('Enter'); await page.keyboard.press('8'); await page.keyboard.press('Enter');
  ok((await page.locator('#prog').innerText()) === '2 / 35', 'Two marks entered offline');
  ok(await page.evaluate((id) => !!localStorage.getItem('rea:stash:' + id), off), 'Offline marks are kept on the phone');
  await ctx.setOffline(false);
  await page.waitForFunction(async (id) => { const r = await fetch('/api/exams/' + id, { headers: { 'X-Requested-With': 'rea' } }); const d = await r.json(); return d.entries && d.entries.length === 2; }, off, { timeout: 15000 }).then(() => ok(true, 'Marks sync once the phone is back online'), () => ok(false, 'Marks did not sync after reconnecting'));

  console.log('Dark theme');
  const dctx = await browser.newContext({ viewport: { width: 390, height: 780 }, deviceScaleFactor: 2, colorScheme: 'dark' });
  const dp = await dctx.newPage(); await signIn(dp, '9000000002');
  const bg = await dp.evaluate(() => getComputedStyle(document.body).backgroundColor);
  ok(bg === 'rgb(18, 17, 16)', 'Dark mode follows the system: ' + bg);
  await shot(dp, 't7-dark');

  await browser.close();
  console.log(`\n${pass} passed, ${fail} failed`);
  process.exit(fail ? 1 : 0);
})().catch((e) => { console.error(e); process.exit(1); });
