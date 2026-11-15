// Merits & demerits against the mock SIS: give, scope, validation, push, pull, delete, offline. Usage: node tests/conduct.mjs
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
    return { status: res.status, data };
  };
}
const sis = async (method, path, body) => (await fetch(MOCK + path, { method, body: body ? JSON.stringify(body) : undefined })).json();
const sisPoints = async () => sis('GET', '/__dump?doctype=' + encodeURIComponent('Student Conduct Point')) || [];
const until = async (fn, ms = 15000) => { const t = Date.now(); while (Date.now() - t < ms) { if (await fn()) return true; await sleep(300); } return false; };
const today = new Date().toISOString().slice(0, 10);
const q = (g) => '?class_name=' + encodeURIComponent(g.class_name) + '&section=' + encodeURIComponent(g.section);

const admin = await login('9000000000');
const A = await login('9000000001');
const B = await login('teacher.b@school.test');

console.log('Categories and academic year from the SIS');
ok(await until(async () => (await A('GET', '/conduct/categories')).data.academic_year === '2026-27'), 'Current academic year is read from Education Settings');
const cats = (await A('GET', '/conduct/categories')).data.categories;
ok(cats.length === 18 && cats.some((c) => c.name === 'Sports Achievement' && c.point_type === 'Merit' && c.default_points === 5), '18 categories with type and default points');

const sa = (await A('GET', '/scope')).data.groups, sb = (await B('GET', '/scope')).data.groups;
const G8 = sa.find((g) => g.label === 'Grade 8-A');
const sec = (await A('GET', '/conduct' + q(G8))).data;
const kids = sec.students;
ok(kids.length > 10 && kids.every((s) => s.merit === 0 && s.demerit === 0), 'Section starts with no points');

console.log('Teachers give points');
let r = await A('POST', '/conduct', { student_ids: kids.slice(0, 3).map((s) => s.id), category: 'Sports Achievement', remarks: 'House football' });
ok(r.status === 201 && r.data.created === 3 && r.data.points === 5, 'A merit for three students takes the category default (5)');
r = await A('POST', '/conduct', { student_ids: [kids[0].id], category: 'Late to School', points: 2 });
ok(r.status === 201 && r.data.point_type === 'Demerit', 'A demerit with points changed by the teacher');
ok(await until(async () => (await sisPoints()).length === 4), 'All four entries reach the SIS');
let pts = await sisPoints();
const first = pts.find((p) => p.category === 'Sports Achievement');
ok(first.academic_year === '2026-27' && first.student_group === 'Grade 8-A' && first.points === 5, 'SIS entry carries section, academic year and points');
ok(first.awarded_by === 'teacher.a@school.test' && /House football · Given by Teacher A/.test(first.remarks), 'Awarded By is the teacher\'s SIS user; the remark names them too');

const s8 = (await A('GET', '/conduct' + q(G8))).data;
const k0 = s8.students.find((s) => s.id === kids[0].id);
ok(k0.merit === 5 && k0.demerit === 2 && k0.net === 3 && s8.totals.merit === 15 && s8.totals.net === 13, 'Totals: merit 5, demerit 2, net 3; section net 13');
ok(s8.recent.length === 4 && s8.recent.every((e) => e.state === 'synced' && e.awarded_by === 'Teacher A (Demo)'), 'Latest entries show who gave them and that they are in the SIS');

const bG = sb.find((g) => g.label === 'Grade 8-A') || sb[0];
const bKid = (await B('GET', '/conduct' + q(bG))).data.students[5];
r = await B('POST', '/conduct', { student_ids: [bKid.id], category: 'Helping Others / Kindness' });
ok(r.status === 201, 'Teacher B gives a merit in a section they teach');
ok(await until(async () => (await sisPoints()).some((p) => /Given by Teacher B/.test(p.remarks || '') && p.awarded_by === 'api@school.test')), 'A teacher with no SIS user still gets in, named in the remark');

console.log('Rules');
// a teacher with no classes yet: every section is outside their scope
ok((await admin('POST', '/admin/users', { name: 'Teacher C (New)', mobile: '9000000003', role: 'teacher' })).status < 300, 'Admin adds a teacher with no classes');
const C = await login('9000000003');
ok((await C('POST', '/conduct', { student_ids: [kids[1].id], category: 'Leadership' })).status === 403, 'A teacher cannot give points outside their sections');
ok((await C('GET', '/conduct' + q(G8))).status === 403, 'Nor see that section\'s points');
ok((await A('POST', '/conduct', { student_ids: [kids[1].id], category: 'Leadership', points: 101 })).status === 400, 'More than 100 points is refused');
ok((await A('POST', '/conduct', { student_ids: [kids[1].id], category: 'Leadership', date: '2099-01-01' })).status === 400, 'A future date is refused');
ok((await A('POST', '/conduct', { student_ids: [kids[1].id], category: 'Not a category' })).status === 400, 'An unknown category is refused');
ok((await A('POST', '/conduct', { student_ids: [], category: 'Leadership' })).status === 400, 'No students is refused');
const late = s8.recent.find((e) => e.category === 'Late to School');
ok((await A('DELETE', '/conduct/' + late.id)).status === 403, 'Teachers cannot delete an entry');

console.log('Admin deletes; the SIS follows');
ok((await admin('DELETE', '/conduct/' + late.id)).status === 200, 'Admin deletes the demerit');
ok(await until(async () => !(await sisPoints()).some((p) => p.category === 'Late to School')), 'It is removed from the SIS');
ok((await A('GET', '/conduct' + q(G8))).data.students.find((s) => s.id === kids[0].id).demerit === 0, 'Totals drop it straight away');

console.log('Entries made in ERPNext come back to UpMark');
const target = (await sis('GET', '/__dump?doctype=Student')).find((s) => s.name === first.student);
await sis('POST', '/api/resource/' + encodeURIComponent('Student Conduct Point'), { student: target.name, student_group: 'Grade 8-A', academic_year: '2026-27', date: today, category: 'Bullying', points: 10, remarks: 'Entered at the office', awarded_by: 'teacher.a@school.test' });
ok((await admin('POST', '/admin/sync/master', {})).status === 200, 'Sync roster now also pulls points');
let s = (await A('GET', '/conduct' + q(G8))).data;
const bull = s.recent.find((e) => e.category === 'Bullying');
ok(bull && bull.points === 10 && bull.point_type === 'Demerit' && bull.remarks === 'Entered at the office', 'An ERPNext entry shows up with its category, points and remarks');
await sis('DELETE', '/api/resource/' + encodeURIComponent('Student Conduct Point') + '/' + encodeURIComponent((await sisPoints()).find((p) => p.category === 'Bullying').name));
await admin('POST', '/admin/sync/master', {});
s = (await A('GET', '/conduct' + q(G8))).data;
ok(!s.recent.some((e) => e.category === 'Bullying'), 'Deleting it in ERPNext removes it here too');
ok(s.recent.filter((e) => e.category === 'Sports Achievement').length === 3, 'Entries given from UpMark are not duplicated by the pull');

console.log('No connection to the SIS');
await sis('POST', '/__down', {});
r = await A('POST', '/conduct', { student_ids: [kids[2].id], category: 'Leadership', remarks: 'Monitor duty' });
ok(r.status === 201, 'Points are accepted while the SIS is down');
await sleep(1500);
s = (await A('GET', '/conduct' + q(G8))).data;
ok(s.recent.find((e) => e.category === 'Leadership').state === 'pending', 'The entry waits, marked as not yet in the SIS');
const school = (await admin('GET', '/admin/conduct')).data;
ok(school.pending_sync >= 1, 'Admin sees how many entries are waiting');
await sis('POST', '/__up', {});
ok(await until(async () => (await sisPoints()).some((p) => p.category === 'Leadership'), 40000), 'It reaches the SIS once it is back');

console.log('Admin view and reports');
const sc = (await admin('GET', '/admin/conduct')).data;
ok(sc.sections.some((x) => x.label === 'Grade 8-A' && x.merit > 0) && sc.top.length > 0, 'School view lists sections and the leaders');
ok((await A('GET', '/admin/conduct')).status === 403, 'Teachers cannot open the school view');
const rep = (await A('GET', '/reports/students' + q(G8) + '&days=30')).data;
const rk = rep.students.find((x) => x.id === kids[2].id);
ok(rk.conduct.merit === 10 && rk.conduct.net === 10, 'The student report carries the year\'s conduct totals');

console.log(`\n${pass} passed, ${fail} failed`);
process.exit(fail ? 1 : 0);
