// End-to-end acceptance checks against a running server (see README). Usage: node tests/acceptance.mjs
const BASE = process.env.BASE || 'http://localhost:8080';
const MOCK = process.env.MOCK || 'http://localhost:8099';
let pass = 0, fail = 0;
const ok = (c, m) => { c ? pass++ : fail++; console.log((c ? '  PASS ' : '  FAIL ') + m); };
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

async function login(ident) {
  const r = await fetch(BASE + '/api/auth/request', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ identifier: ident }) });
  const { dev_code } = await r.json();
  const v = await fetch(BASE + '/api/auth/verify', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ identifier: ident, code: dev_code }) });
  const cookie = v.headers.get('set-cookie').split(';')[0];
  return async (method, path, body) => {
    const res = await fetch(BASE + '/api' + path, { method, headers: { 'Content-Type': 'application/json', 'X-Requested-With': 'rea', cookie }, body: body === undefined ? undefined : JSON.stringify(body) });
    let data = {}; try { data = await res.json(); } catch {}
    const st = /dur=([\d.]+)/.exec(res.headers.get('server-timing') || '');
    return { status: res.status, data, ms: st ? +st[1] : null };
  };
}
const mock = async (p, body) => (await fetch(MOCK + p, { method: body ? 'POST' : 'GET', body: body ? JSON.stringify(body) : undefined })).json();
const today = new Date().toISOString().slice(0, 10);
const G8 = { class_name: 'Grade 8', section: 'A' }, G10 = { class_name: 'Grade 10', section: 'A' };
const day = (n) => new Date(Date.now() - n * 864e5).toISOString().slice(0, 10);

const admin = await login('9000000000');
const A = await login('9000000001'); // Teacher A
const B = await login('teacher.b@school.test'); // Teacher B

console.log('Scoping & OTP');
const sa = (await A('GET', '/scope')).data, sb = (await B('GET', '/scope')).data;
ok(sa.groups.some((g) => g.label === 'Grade 8-A' && g.subjects.includes('Punjabi')), 'Teacher A sees Grade 8-A Punjabi');
ok(!sb.groups.some((g) => g.subjects.includes('Punjabi')), 'Teacher B has no visibility of Punjabi');
let r = await B('POST', '/exams', { ...G8, category: 'Formative', sub_type: 'FA1', date: today, subjects: ['Punjabi'], max_total: 20 });
ok(r.status === 403, 'Teacher B cannot create a Punjabi test (403)');
ok((await fetch(BASE + '/api/auth/verify', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ identifier: '9000000001', code: '000000' }) })).status === 401, 'Wrong OTP rejected');
ok((await (await fetch(BASE + '/api/exams/1')).json()).error === 'Please sign in', 'Unauthenticated API call rejected');
ok((await fetch(BASE + '/api/exams', { method: 'POST', body: '{}' })).status === 403, 'Missing CSRF header rejected');

console.log('Same-day subject rule');
const base = { ...G8, category: 'Formative', sub_type: 'FA1', date: today };
r = await A('POST', '/exams', { ...base, subjects: ['Punjabi'], max_total: 20 });
ok(r.status === 201, 'Create Grade 8-A Formative Punjabi test'); const punjabi = r.data.exam_id;
r = await A('POST', '/exams', { ...base, subjects: ['Hindi'], max_total: 20 });
ok(r.status === 201, 'Hindi test same day succeeds'); const hindi = r.data.exam_id;
r = await A('POST', '/exams', { ...base, subjects: ['Punjabi'], max_total: 20 });
ok(r.status === 200 && r.data.state === 'existing' && r.data.exam_id === punjabi, 'Second Punjabi test opens the existing one');

console.log('2-draft ceiling');
r = await A('POST', '/exams', { ...G10, category: 'Formative', sub_type: 'FA1', date: today, subjects: ['Mathematics'], max_total: 20 });
ok(r.status === 409 && r.data.code === 'draft_limit', 'Third draft hard-blocked');
r = await A('DELETE', '/exams/' + hindi);
ok(r.status === 200, 'Discard a draft');
r = await A('POST', '/exams', { ...G10, category: 'Formative', sub_type: 'FA1', date: today, subjects: ['Mathematics'], max_total: 20 });
ok(r.status === 201, 'Creation succeeds after discarding'); const math10 = r.data.exam_id;
r = await A('POST', '/exams', { ...G8, category: 'Summative', sub_type: 'SA1', date: today, subjects: ['Mathematics'], max_total: 20 });
ok(r.status === 409, 'Blocked again at two drafts');
r = await A('DELETE', '/exams/' + math10); ok(r.status === 200, 'Cleanup draft');

console.log('CBSE split + sanity gate');
r = await A('POST', '/exams', { ...G8, category: 'Summative', sub_type: 'SA1', date: today, subjects: ['Mathematics'], split: true, components: [{ name: 'Theory', max: 80 }, { name: 'Practical', max: 20 }] });
ok(r.status === 201, 'Create Theory(80)+Practical(20) test'); const split = r.data.exam_id;
let ex = (await A('GET', '/exams/' + split)).data; const roster = ex.roster;
ok(ex.exam.max_total === 100 && ex.exam.components.length === 2, 'Total max is 100 from components');
const put = (sid, comps, status = '') => A('PUT', '/exams/' + split + '/marks', { entries: [{ student_id: sid, subject: 'Mathematics', status, comps }] });
ok((await put(roster[0].id, [81, 5])).status === 422, 'API rejects 81 in Theory');
ok((await put(roster[0].id, [50, -1])).status === 422, 'API rejects -1 in Practical');
ok((await put(roster[0].id, [50, 14.3])).status === 422, 'API rejects 14.3');
ok((await put(roster[0].id, [50, 14.25])).status === 422, 'API rejects 14.25');
ok((await put(roster[0].id, [50, 21])).status === 422, 'API rejects 21 in Practical (max 20)');
ok((await put(roster[0].id, [50, 14.5])).status === 200, 'API accepts 14.5');
ex = (await A('GET', '/exams/' + split)).data;
ok(ex.entries[0].total === 64.5, 'Total auto-computed as 64.5');

console.log('100% roster gate');
const n = roster.length;
const all = roster.slice(0, n - 1).map((s, i) => ({ student_id: s.id, subject: 'Mathematics', status: '', comps: [60 + (i % 20), 10] }));
r = await A('PUT', '/exams/' + split + '/marks', { entries: all }); ok(r.status === 200, 'Save draft with ' + all.length + ' of ' + n);
r = await A('POST', '/exams/' + split + '/finalize', {});
ok(r.status === 409 && r.data.missing.length === 1 && r.data.missing[0].student_id === roster[n - 1].id, 'Finalize blocked; exactly 1 student isolated');
r = await put(roster[n - 1].id, [], 'AB'); ok(r.status === 200, 'Mark last student AB');
r = await A('POST', '/exams/' + split + '/finalize', {}); ok(r.status === 200 && r.data.stats.absent === 1, 'Finalize succeeds');
r = await put(roster[0].id, [1, 1]); ok(r.status === 409, 'Locked test rejects further saves');
ok((await A('DELETE', '/exams/' + split)).status === 409, 'Locked test cannot be deleted');

console.log('ERP sync (draft -> locked)');
let synced = false;
for (let i = 0; i < 40 && !synced; i++) { await sleep(500); const s = (await A('GET', '/exams/' + split)).data.sync; synced = s.state === 'green'; }
ok(synced, 'Sync health turns green after worker pushes to Frappe');
let results = await mock('/__dump?doctype=Assessment%20Result');
ok(results.length === n && results.every((x) => x.docstatus === 1), 'Frappe holds ' + n + ' submitted Assessment Results');
const plans = await mock('/__dump?doctype=Assessment%20Plan');
ok(plans.length >= 1 && plans[0].assessment_criteria.length === 2, 'Assessment Plan created with 2 criteria');
const abRes = results.find((x) => x.custom_marks_status === 'AB'); ok(!!abRes && abRes.details.every((d) => d.score === 0), 'AB stored in status field with 0 score');
const groups = await mock('/__dump?doctype=Assessment%20Group');
ok(groups.some((g) => g.name === 'SA1' && g.parent_assessment_group === 'Summative'), 'Assessment Group tree auto-created');

console.log('Surgical correction & timeline');
ex = (await B('GET', '/exams/' + split)).data; // Teacher B shares Grade 8-A but not Mathematics
ok(ex.error, 'Teacher B (not assigned Mathematics) cannot open this test');
const target = roster[13]; // roll 14
const cur = (await A('GET', '/exams/' + split)).data.entries.find((e) => e.student_id === target.id);
r = await A('POST', '/exams/' + split + '/corrections', { student_id: target.id, subject: 'Mathematics', status: '', comps: [cur.comps[0] + 3, cur.comps[1]], reason: '' });
ok(r.status === 400, 'Correction needs a reason');
const newTheory = cur.comps[0] + 3;
r = await A('POST', '/exams/' + split + '/corrections', { student_id: target.id, subject: 'Mathematics', status: '', comps: [newTheory, cur.comps[1]], reason: 'Totalling error on page 3' });
ok(r.status === 201, 'Teacher submits correction'); const cid = r.data.correction_id;
ok((await A('POST', '/exams/' + split + '/corrections', { student_id: target.id, subject: 'Mathematics', status: '', comps: [newTheory, 1], reason: 'again' })).status === 409, 'Duplicate pending request blocked');
ok((await A('POST', '/admin/corrections/' + cid + '/approve', {})).status === 403, 'Teacher cannot approve');
r = await admin('POST', '/admin/corrections/' + cid + '/approve', { note: 'Verified against paper' }); ok(r.status === 200, 'Admin approves');
ex = (await A('GET', '/exams/' + split)).data;
const after = ex.entries.find((e) => e.student_id === target.id);
ok(after.comps[0] === newTheory, 'Only the target student row updated in SQLite');
ok(ex.entries.filter((e) => e.student_id !== target.id && e.status === '').every((e) => e.comps[0] >= 60 && e.comps[0] < 80), 'Other students untouched');
let tl;
for (let i = 0; i < 30; i++) { await sleep(400); tl = (await A('GET', '/exams/' + split + '/timeline')).data.events; if (tl.some((e) => e.kind === 'correction_synced')) break; }
ok(['created', 'draft_saved', 'locked', 'correction_requested', 'correction_approved', 'correction_synced'].every((k) => tl.some((e) => e.kind === k)), 'Timeline shows full chain: ' + tl.map((e) => e.kind).join(' > '));
results = await mock('/__dump?doctype=Assessment%20Result');
const amended = results.find((x) => x.student === 'STU-8A-' + String(14).padStart(3, '0') && x.docstatus === 1);
ok(amended && amended.details[0].score === newTheory && amended.amended_from, 'Frappe: roll 14 amended (cancel + new submitted doc) with new score');
ok(results.filter((x) => x.docstatus === 2).length === 1, 'Exactly one Assessment Result cancelled (surgical)');

console.log('Dashboards, class/section/teacher allocation API');
const me = (await A('GET', '/me')).data;
ok(me.stats.classes === 2 && me.stats.subjects >= 4 && me.stats.locked >= 1, 'Teacher dashboard stats: ' + JSON.stringify(me.stats));
const lst = (await B('GET', '/exams')).data.exams;
ok(lst.every((r) => r.exam.subjects.some((s) => ['English', 'Science', 'Hindi'].includes(s))), 'Teacher B test list contains only tests in B\'s own subjects (' + lst.length + ')');
ok((await B('GET', '/admin/classes')).status === 403, 'Teacher cannot read admin class overview');
const cls = (await admin('GET', '/admin/classes')).data.classes;
ok(cls.length === 2 && cls.find((c) => c.label === 'Grade 8-A').students >= 35, 'Admin class overview lists sections with counts');
const mx = (await admin('GET', '/admin/allocations?class_name=Grade%208&section=A')).data;
ok(mx.subjects.find((s) => s.subject === 'Punjabi').teachers.length === 1 && mx.teachers.length >= 2, 'Allocation matrix: Punjabi -> 1 teacher');
const tb = mx.teachers.find((t) => t.name.startsWith('Teacher B'));
ok((await admin('PUT', '/admin/allocations/assign', { class_name: 'Grade 8', section: 'A', subject: 'Punjabi', user_id: tb.id, assigned: true })).status === 200, 'Admin assigns Teacher B to Punjabi');
ok((await B('GET', '/scope')).data.groups.some((g) => g.subjects.includes('Punjabi')), 'Teacher B now sees Punjabi (selection takes effect immediately)');
ok((await admin('PUT', '/admin/allocations/assign', { class_name: 'Grade 8', section: 'A', subject: 'Punjabi', user_id: tb.id, assigned: false })).status === 200 && !(await B('GET', '/scope')).data.groups.some((g) => g.subjects.includes('Punjabi')), 'Revoking removes access again');
ok((await admin('PUT', '/admin/allocations/assign', { class_name: 'Nope', section: 'Z', subject: 'Punjabi', user_id: tb.id, assigned: true })).status === 400, 'Unknown class/section rejected');
ok((await A('PUT', '/admin/allocations/assign', { class_name: 'Grade 8', section: 'A', subject: 'Punjabi', user_id: tb.id, assigned: true })).status === 403, 'Teacher cannot assign teachers');

console.log('Master sync & sync health');
await mock('/__addstudent', { Group: 'Grade 8-A', ID: 'STU-8A-NEW', Name: 'Zara Newcomer', Roll: '36', Admission: 'ADM999999' });
const t0 = Date.now(); r = await admin('POST', '/admin/sync/master', {}); const dt = Date.now() - t0;
ok(r.status === 200 && r.data.students_added === 1, 'Manual sync pulls the new student');
ok(dt < 2000, 'Sync completed in ' + dt + ' ms (< 2 s)');
ok((await A('GET', '/exams/' + punjabi)).data.roster.some((s) => s.name === 'Zara Newcomer'), 'New student appears in class list');
await mock('/__down', {});
r = await A('POST', '/exams', { ...G10, category: 'Formative', sub_type: 'FA2', date: day(1), subjects: ['Mathematics'], max_total: 10 });
const d2 = r.data.exam_id; const roster10 = (await A('GET', '/exams/' + d2)).data.roster;
await A('PUT', '/exams/' + d2 + '/marks', { entries: [{ student_id: roster10[0].id, subject: 'Mathematics', status: '', comps: [7] }] });
let red = false; for (let i = 0; i < 40 && !red; i++) { await sleep(500); red = (await A('GET', '/exams/' + d2)).data.sync.state === 'red'; }
ok(red, '🔴 badge while Frappe is down');
await mock('/__up', {});
let back = false; for (let i = 0; i < 80 && !back; i++) { await sleep(1000); back = (await A('GET', '/exams/' + d2)).data.sync.state === 'yellow'; }
ok(back, '🟡 badge returns once the retry succeeds after Frappe is back');

console.log('Performance (20 concurrent teachers saving to SQLite)');
const test = (await A('GET', '/exams/' + punjabi)).data;
const pct = (a, q) => a[Math.min(a.length - 1, Math.floor(a.length * q))];
async function load(label, think, rounds) {
  const srv = [], e2e = [];
  await Promise.all(Array.from({ length: 20 }, async (_, t) => {
    for (let i = 0; i < rounds; i++) {
      if (think) await sleep(think * (0.6 + Math.random() * 0.8));
      const s = test.roster[(t * 3 + i) % test.roster.length]; const t1 = performance.now();
      const x = await A('PUT', '/exams/' + punjabi + '/marks', { entries: [{ student_id: s.id, subject: 'Punjabi', status: '', comps: [(i % 20) + 0] }] });
      e2e.push(performance.now() - t1); srv.push(x.ms); if (x.status !== 200) fail++;
    }
  }));
  srv.sort((a, b) => a - b); e2e.sort((a, b) => a - b);
  console.log(`  ${label}: ${srv.length} saves; server handler p50=${pct(srv, .5)}ms p95=${pct(srv, .95)}ms max=${srv.at(-1)}ms; end-to-end p95=${pct(e2e, .95).toFixed(1)}ms`);
  return pct(srv, .95);
}
await load('burst, no think time (stress, informational)', 0, 15);
const paced = await load('paced, a save every ~0.5 s per teacher', 500, 20);
ok(paced < 5, 'p95 server-side save time < 5 ms with 20 teachers saving concurrently (' + paced + ' ms)');

console.log(`\n${pass} passed, ${fail} failed`);
process.exit(fail ? 1 : 0);
