// Admin space: separate nav, class -> section -> teacher allocation, teacher space isolation.
const { chromium } = require('playwright');
const BASE = process.env.BASE || 'http://localhost:8080';
let pass = 0, fail = 0;
const ok = (c, m) => { c ? pass++ : fail++; console.log((c ? '  PASS ' : '  FAIL ') + m); };
(async () => {
  require('fs').mkdirSync('/tmp/shots', { recursive: true });
  const browser = await chromium.launch();
  const login = async (id, vp = { width: 1280, height: 820 }) => {
    const pg = await (await browser.newContext({ viewport: vp })).newPage();
    pg.on('pageerror', (e) => { fail++; console.log('  FAIL page error: ' + e.message); }); pg.on('dialog', (d) => d.accept());
    await pg.goto(BASE); await pg.fill('input', id); await pg.click('text=Send code');
    await pg.fill('input', await pg.locator('.banner b').innerText()); await pg.click('text=Verify & sign in'); await pg.waitForSelector('.side'); return pg;
  };
  const a = await login('9000000000');
  ok((await a.locator('.brand b').first().innerText()) === 'GCM UPMARK' && (await a.locator('.brand small').first().innerText()) === 'ADMIN CONSOLE', 'Admin lands in the Admin Console shell (GCM UPMARK)');
  const adminNav = await a.locator('button.nav').allInnerTexts();
  ok(['Marks Explorer', 'Corrections', 'Classes & Sections', 'Teacher Allocation', 'Teachers', 'Frappe Sync'].every((x) => adminNav.some((n) => n.includes(x))) && !adminNav.some((n) => n.includes('New / Open Test')), 'Admin sidebar has admin topics only: ' + adminNav.map((x) => x.split('\n')[0]).join(' | '));
  await a.click('text=Sync Master Roster Now'); await a.waitForFunction(() => /synced in/.test(document.getElementById('toast').textContent)); ok(true, 'Sync Master Roster Now works');
  await a.click('button.nav:has-text("Classes & Sections")'); await a.waitForSelector('table.tbl');
  ok((await a.locator('table.tbl tbody tr').count()) === 2, 'Classes & Sections lists both class-sections with counts');
  await a.click('button.nav:has-text("Teachers")'); await a.waitForSelector('text=Add teacher');
  const [n, m, e] = await a.locator('.card input').all(); await n.fill('Teacher C'); await m.fill('9000000003'); await e.fill('c@school.test');
  await a.click('button:text-is("Add")'); await a.waitForSelector('text=Teacher C'); ok(true, 'Teacher added');
  await a.click('button.nav:has-text("Teacher Allocation")');
  await a.click('button.pill:text-is("Grade 10")'); await a.waitForSelector('text=2 · Section'); await a.click('button.pill:text-is("A")'); await a.waitForSelector('text=3 · Teacher per subject'); await a.waitForSelector('table.tbl tbody tr');
  ok(await a.locator('text=Grade 10-A · 38 students').count() === 1, 'Class → section selection shows the right roster size');
  const row = a.locator('tr', { has: a.locator('b:text-is("Science")') });
  await row.locator('select').selectOption({ label: 'Teacher C' }); await row.locator('button:text-is("Assign")').click();
  await a.waitForSelector('tr:has(b:text-is("Science")) .chip:has-text("Teacher C")'); ok(true, 'Teacher C assigned to Grade 10-A Science');
  await a.screenshot({ path: '/tmp/shots/a1-alloc.png' });
  await a.click('button.nav:has-text("Marks Explorer")'); await a.waitForSelector('select[aria-label="Teacher"]'); ok(await a.locator('select[aria-label="Section"]').count() === 1, 'Marks Explorer has class / section / teacher filters');

  const t = await login('9000000003', { width: 1280, height: 820 });
  ok((await t.locator('.brand small').first().innerText()) === 'MARKS ENTRY', 'Teacher gets the separate Marks Entry space');
  const teacherNav = await t.locator('button.nav').allInnerTexts();
  ok(!teacherNav.some((x) => /Marks Explorer|Corrections|Allocation|Frappe/.test(x)) && teacherNav.some((x) => x.includes('New / Open Test')), 'Teacher sidebar has no admin topics: ' + teacherNav.map((x) => x.split('\n')[0]).join(' | '));
  await t.click('#new'); await t.waitForSelector('text=Select class');
  ok((await t.locator('.pills .pill').allInnerTexts()).join() === 'Grade 10', 'Teacher C only sees the class allocated to them (Grade 10)');
  await t.goto(BASE + '/#/a/allocations'); await t.waitForSelector('.side');
  ok((await t.locator('text=Teacher per subject').count()) === 0 && (await t.locator('.brand small').first().innerText()) === 'MARKS ENTRY', 'Teacher opening an admin URL is kept in the teacher space');
  ok((await t.evaluate(() => fetch('/api/admin/users', { credentials: 'same-origin' }).then((r) => r.status))) === 403, 'Teacher gets 403 on admin API');
  await browser.close(); console.log(`\n${pass} passed, ${fail} failed`); process.exit(fail ? 1 : 0);
})().catch((e) => { console.error(e); process.exit(1); });
