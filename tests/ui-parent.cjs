// Parent portal (phone): admission no. + last 4 digits of either parent's mobile, the weekly report, class percentile,
// language (auto from the phone, switchable), "I've seen this".
const { chromium } = require('playwright');
const BASE = process.env.BASE || 'http://localhost:8080';
let pass = 0, fail = 0;
const ok = (c, m) => { c ? pass++ : fail++; console.log((c ? '  PASS ' : '  FAIL ') + m); };
const shot = (p, n) => p.screenshot({ path: '/tmp/shots/' + n + '.png', fullPage: true });
const today = new Date().toISOString().slice(0, 10);

(async () => {
  require('fs').mkdirSync('/tmp/shots', { recursive: true });
  const browser = await chromium.launch({ executablePath: process.env.CHROME || undefined });
  const phone = async (locale = 'en-IN', scheme = 'light') => {
    const pg = await (await browser.newContext({ viewport: { width: 390, height: 780 }, deviceScaleFactor: 2, locale, colorScheme: scheme })).newPage();
    pg.on('pageerror', (e) => { fail++; console.log('  FAIL page error: ' + e.message); });
    return pg;
  };
  const staff = async (id) => {
    const pg = await phone(); await pg.goto(BASE); await pg.fill('.lg-input', id); await pg.click('text=Get my WorkCode');
    const code = await pg.locator('.lg-dev b').innerText();
    for (let i = 0; i < 6; i++) await pg.locator('.lg-box').nth(i).fill(code[i]);
    await pg.waitForSelector('.app'); return pg;
  };
  const call = (pg, m, p, b) => pg.evaluate(async ([m, p, b]) => { const r = await fetch('/api' + p, { method: m, headers: { 'Content-Type': 'application/json', 'X-Requested-With': 'rea' }, body: b ? JSON.stringify(b) : undefined }); return { status: r.status, data: await r.json().catch(() => ({})) }; }, [m, p, b]);

  console.log('Setup: two approved tests this week');
  const t = await staff('9000000001'), adm = await staff('9000000000');
  const mk = async (subject, f) => {
    const ex = (await call(t, 'POST', '/exams', { class_name: 'Grade 8', section: 'A', category: 'Summative', sub_type: 'SA1', date: today, subjects: [subject], max_total: 20 })).data.exam_id;
    const roster = (await call(t, 'GET', '/exams/' + ex)).data.roster;
    await call(t, 'PUT', '/exams/' + ex + '/marks', { entries: roster.map((s, i) => ({ student_id: s.id, subject, status: '', comps: [f(i)] })) });
    await call(t, 'POST', '/exams/' + ex + '/finalize', {}); await call(adm, 'POST', '/admin/exams/' + ex + '/approve', {});
    return roster;
  };
  const roster = await mk('Hindi', (i) => (i === 4 ? 19 : 8 + (i % 10)));
  await mk('Punjabi', (i) => (i === 4 ? 9 : 10 + (i % 8)));
  const kid = (await call(t, 'GET', '/roster?class_name=Grade%208&section=A')).data.students.find((s) => s.id === roster[4].id);
  ok(!!kid.father_mobile && !!kid.mother_mobile, 'Student has both parent mobiles on file');

  console.log('Sign in');
  const p = await phone();
  await p.goto(BASE); await p.click('.lg-tabs button:has-text("Parent")'); await p.waitForSelector('text=See the report');
  ok(p.url().endsWith('#/parent'), 'Parent tab opens #/parent');
  await shot(p, 'p1-login');
  await p.fill('.lg-input >> nth=0', kid.admission_no); await p.fill('.lg-input >> nth=1', '0000'); await p.click('text=See the report');
  await p.waitForSelector('.lg-err:not(:empty)'); ok(true, 'Wrong digits are refused: ' + await p.locator('.lg-err').innerText());
  await p.fill('.lg-input >> nth=0', kid.admission_no.toLowerCase()); await p.fill('.lg-input >> nth=1', kid.father_mobile.slice(-4)); await p.click('text=See the report');
  await p.waitForSelector('.pp-hero .big'); ok(true, "Father's last 4 digits sign in (admission no. in any case)");

  console.log('Weekly report');
  const hero = await p.locator('.pp-hero').innerText();
  ok(hero.includes(kid.name) && /Ahead of \d+% of class Grade 8-A/.test(hero), 'Hero names the child and the class percentile');
  ok(/class percentile/i.test(await p.locator('.tile.t-olive').innerText()), 'Percentile tile is class-wise');
  const strong = await p.locator('.tile.t-sky').innerText(), weak = await p.locator('.tile.t-pink').innerText();
  ok(/hindi/i.test(strong) && /punjabi/i.test(weak), 'Strongest and practise subjects are picked from the marks');
  ok(await p.locator('.sbar').count() === 2 && await p.locator('.sbar .cm').count() === 2, 'Subject bars carry a class-average marker');
  ok(/punjabi/i.test(await p.locator('.tile.t-lav').innerText()), 'One thing to try points at the weak subject');
  await shot(p, 'p2-report');
  await p.click('#seen'); await p.waitForFunction(() => /Seen/.test(document.getElementById('seen').textContent));
  ok(true, '"I\'ve seen this" is recorded');
  const seen = (await call(p, 'GET', '/parent/report')).data.seen_at;
  ok(!!seen, 'The read is stored for this week (Pulse counts it once the week is published)');

  console.log('Languages');
  await p.click('.lang button:has-text("हिं")'); await p.waitForSelector('text=मैंने देख लिया >> nth=0').catch(() => {});
  ok(/कक्षा परसेंटाइल/.test(await p.locator('.tile.t-olive').innerText()), 'Hindi switch translates the report');
  await p.click('.lang button:has-text("ਪੰ")');
  ok(/ਜਮਾਤ ਪਰਸੈਂਟਾਈਲ/.test(await p.locator('.tile.t-olive').innerText()), 'Punjabi switch translates the report');
  await shot(p, 'p3-punjabi');
  await p.reload(); await p.waitForSelector('.pp-hero .big');
  ok(/ਜਮਾਤ/.test(await p.locator('.tile.t-olive').innerText()), 'Language choice is remembered');

  const h = await phone('hi-IN', 'dark');
  await h.goto(BASE + '/#/parent'); await h.waitForSelector('.lg-input');
  ok(/आपके बच्चे का/.test(await h.locator('h1').innerText()), 'A Hindi phone opens the parent page in Hindi');
  await h.fill('.lg-input >> nth=0', kid.admission_no); await h.fill('.lg-input >> nth=1', kid.mother_mobile.slice(-4)); await h.click('.lg-btn');
  await h.waitForSelector('.pp-hero .big'); ok(true, "Mother's last 4 digits sign in too");
  ok(/देख लिया/.test(await h.locator('#seen').innerText()), 'Seen state is shared by both parents');
  await shot(h, 'p4-hindi-dark');

  console.log('Sign out');
  await p.click('.pp button:has-text("ਸਾਈਨ ਆਊਟ")'); await p.waitForSelector('.lg-input');
  ok((await call(p, 'GET', '/parent/report')).status === 401, 'Signed out');

  await browser.close();
  console.log(`\n${pass} passed, ${fail} failed`);
  process.exit(fail ? 1 : 0);
})().catch((e) => { console.error(e); process.exit(1); });
