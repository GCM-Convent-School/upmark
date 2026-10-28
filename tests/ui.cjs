// Browser acceptance checks (Playwright/Chromium, phone viewport). Usage: node tests/ui.cjs
const { chromium } = require('playwright');
const BASE = process.env.BASE || 'http://localhost:8080';
let pass = 0, fail = 0;
const ok = (c, m) => { c ? pass++ : fail++; console.log((c ? '  PASS ' : '  FAIL ') + m); };
const shot = (p, n) => p.screenshot({ path: '/tmp/shots/' + n + '.png' });

(async () => {
  require('fs').mkdirSync('/tmp/shots', { recursive: true });
  const browser = await chromium.launch();
  const ctx = await browser.newContext({ viewport: { width: 390, height: 780 }, deviceScaleFactor: 2, hasTouch: true });
  const page = await ctx.newPage();
  page.on('pageerror', (e) => { fail++; console.log('  FAIL page error: ' + e.message); });
  page.on('dialog', (d) => d.accept());

  console.log('Login');
  await page.goto(BASE);
  await page.fill('input', '9000000001'); await page.click('text=Send code');
  const code = await page.locator('.banner b').innerText();
  await page.fill('input', code); await page.click('text=Verify & sign in');
  await page.waitForSelector('text=New / Open Test'); ok(true, 'OTP login lands on home'); await shot(page, '1-home');

  console.log('Cascade');
  await page.click('#new'); await page.click('button.pill:has-text("Grade 8-A")');
  await page.click('button.pill:has-text("Formative")'); await page.click('button.pill:has-text("FA2")');
  await page.click('button.pill:has-text("Today")'); await shot(page, '2-subject');
  const subs = await page.locator('.pills .pill').allInnerTexts();
  ok(!subs.includes('English') && subs.includes('Hindi') && subs.includes('Punjabi'), 'Only assigned subjects offered: ' + subs.join(', '));
  await page.click('button.pill:has-text("Hindi")'); await page.click('button.pill:has-text("20")'); await page.click('text=Start grading');
  await page.waitForSelector('#prog'); ok((await page.locator('.ctx .t').innerText()).includes('Grade 8-A • FA2 • Hindi'), 'Context bar shows class • test • subject • date');
  ok((await page.locator('#prog').innerText()) === '0 / 35 Graded', 'Progress counter 0 / 35');
  ok((await page.locator('.seg button.on').getAttribute('aria-label')) === 'Flashcard view', 'Flashcard view is the mobile default');

  console.log('Flashcard entry');
  const key = (k) => page.click('.pad button.k:text-is("' + k + '")');
  await key('1'); await key('4'); await key('.5');
  ok((await page.locator('.box').first().innerText()).startsWith('14.5'), '14 + [.5] key shows 14.5');
  await shot(page, '3-card');
  await page.click('#nextbtn');
  await key('2'); await key('5'); // 25 > max 20
  ok(await page.locator('.box.err').count() === 1 && (await page.locator('.err-msg').innerText()).includes('Maximum is 20'), 'Over-max entry turns red with message');
  await page.click('#nextbtn'); ok((await page.locator('.roll').innerText()) === 'Roll 2', 'Next is blocked while the entry is invalid');
  await key('⌫'); await key('⌫'); await key('1'); await key('8'); await page.click('#nextbtn');
  await page.click('.qs .pill:has-text("AB")'); ok((await page.locator('.roll').innerText()) === 'Roll 4', 'AB logs status and advances immediately');
  await key('7'); await page.click('#nextbtn');
  ok((await page.locator('#prog').innerText()) === '4 / 35 Graded', 'Progress counter updates (4 / 35: 14.5, 18, AB, 7)');

  console.log('Search');
  await page.fill('.toolbar input', '12'); ok((await page.locator('.roll').innerText()) === 'Roll 12', 'Jump to roll no. 12 via search'); await page.fill('.toolbar input', '');

  console.log('Table view + sort + no lost marks');
  await page.click('[aria-label="Table view"]');
  const vals = async () => page.$$eval('#tbl tbody tr', (rs) => Object.fromEntries(rs.map((r) => [r.children[0].textContent, r.querySelector('input').value])));
  let v = await vals(); ok(v['1'] === '14.5' && v['2'] === '18' && v['3'] === 'AB' && v['4'] === '7', 'Marks carried into table view');
  await shot(page, '4-table');
  for (const s of ['adm', 'name', 'roll']) { await page.selectOption('.toolbar select', s); const w = await vals(); ok(JSON.stringify(w) === JSON.stringify(v), 'Sorting by ' + s + ' loses no marks'); }
  await page.selectOption('.toolbar select', 'name');
  const firstNames = await page.$$eval('#tbl tbody tr td:nth-child(3)', (c) => c.map((x) => x.textContent));
  ok(JSON.stringify(firstNames) === JSON.stringify([...firstNames].sort((a, b) => a.localeCompare(b))), 'A–Z sort order correct'); await page.selectOption('.toolbar select', 'roll');
  const inp = page.locator('#tbl tbody tr:nth-child(5) input'); await inp.fill('-1'); ok(await inp.evaluate((e) => e.classList.contains('bad')), 'Table: -1 flagged red');
  await inp.fill('14.3'); ok(await inp.evaluate((e) => e.classList.contains('bad')), 'Table: 14.3 flagged red');
  await inp.fill('9'); ok(!(await inp.evaluate((e) => e.classList.contains('bad'))), 'Table: 9 accepted');
  await inp.press('Enter'); ok(await page.evaluate(() => document.activeElement.closest('tr').dataset.sid) !== null && (await page.evaluate(() => document.activeElement.closest('tr').children[0].textContent)) === '6', 'Enter moves to next row');

  console.log('Crash recovery');
  await page.fill('#tbl tbody tr:nth-child(6) input', '11'); await page.fill('#tbl tbody tr:nth-child(7) input', '12'); await page.fill('#tbl tbody tr:nth-child(8) input', '13');
  const unsaved = await page.evaluate(() => Object.keys(JSON.parse(localStorage.getItem(Object.keys(localStorage).find((k) => k.startsWith('rea:stash:'))))).length);
  ok(unsaved >= 5, unsaved + ' entries mirrored to localStorage before any Save');
  await page.close(); // simulate tab being killed
  const p2 = await ctx.newPage(); p2.on('dialog', (d) => d.accept());
  await p2.goto(BASE + '/'); await p2.waitForSelector('text=New / Open Test');
  await p2.click('text=Continue'); await p2.waitForSelector('#prog');
  await p2.click('[aria-label="Table view"]');
  const rec = await p2.$$eval('#tbl tbody tr', (rs) => Object.fromEntries(rs.map((r) => [r.children[0].textContent, r.querySelector('input').value])));
  ok(rec['1'] === '14.5' && rec['5'] === '9' && rec['6'] === '11' && rec['7'] === '12' && rec['8'] === '13', 'Reopened test restores all unsaved marks from local stash');
  await p2.click('#savebtn'); await p2.waitForFunction(() => /Draft saved/.test(document.getElementById('toast').textContent), null, { timeout: 5000 }).then(() => ok(true, 'Save Draft stores to server'), () => ok(false, 'Save Draft stores to server'));
  ok(await p2.evaluate(() => Object.keys(localStorage).filter((k) => k.startsWith('rea:stash:')).length) === 0, 'Stash cleared after successful save');

  console.log('Finalize gate');
  await p2.click('text=Finalize & Lock'); await p2.waitForSelector('#toast.show.bad');
  ok(await p2.locator('#tbl tbody tr').count() > 0 && (await p2.locator('#tbl tbody tr').count()) <= 35 - 8 && await p2.locator('tr.miss').count() > 0, 'Lock blocked; view filtered to ' + (await p2.locator('#tbl tbody tr').count()) + ' ungraded students, highlighted');
  await shot(p2, '5-lockgate');
  await browser.close();
  console.log(`\n${pass} passed, ${fail} failed`); process.exit(fail ? 1 : 0);
})().catch((e) => { console.error(e); process.exit(1); });
