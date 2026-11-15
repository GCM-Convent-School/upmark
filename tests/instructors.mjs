// Teachers <-> SIS Instructors, both ways, and the same-day Assessment Plan time slots. Usage: node tests/instructors.mjs
const BASE = process.env.BASE || 'http://localhost:8080';
const MOCK = process.env.MOCK || 'http://localhost:8099';
let pass = 0, fail = 0;
const ok = (c, m) => { c ? pass++ : fail++; console.log((c ? '  PASS ' : '  FAIL ') + m); };
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
const until = async (fn, ms = 20000) => { const t = Date.now(); while (Date.now() - t < ms) { if (await fn()) return true; await sleep(300); } return false; };

async function login(ident) {
  const r = await fetch(BASE + '/api/auth/request', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ identifier: ident }) });
  const { dev_code } = await r.json();
  const v = await fetch(BASE + '/api/auth/verify', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ identifier: ident, code: dev_code }) });
  const cookie = (v.headers.get('set-cookie') || '').split(';')[0];
  return async (method, path, body) => {
    const res = await fetch(BASE + '/api' + path, { method, headers: { 'Content-Type': 'application/json', 'X-Requested-With': 'rea', cookie }, body: body === undefined ? undefined : JSON.stringify(body) });
    let data = {}; try { data = await res.json(); } catch {}
    return { status: res.status, data };
  };
}
const sis = async (method, path, body) => (await fetch(MOCK + path, { method, body: body ? JSON.stringify(body) : undefined })).json();
const dump = async (dt) => (await sis('GET', '/__dump?doctype=' + encodeURIComponent(dt))) || [];
const res = (dt) => '/api/resource/' + encodeURIComponent(dt);

const admin = await login('9000000000');
const users = async () => (await admin('GET', '/admin/users')).data.users;
const user = async (name) => (await users()).find((u) => u.name === name);
const sync = async () => (await admin('POST', '/admin/sync/master', {})).data;

console.log('Pull: Instructors become teachers');
let r = await sync();
ok(r.teachers_linked >= 1, 'Roster sync links teachers to their Instructors (' + r.teachers_linked + ')');
let ta = await user('Teacher A (Demo)');
ok(ta.sis_state === 'linked' && ta.sis_instructor === 'INS-0001', 'Teacher A is linked to INS-0001 through their Employee email');
ok((await user('Teacher B (Demo)')).sis_state === 'none', 'Teacher B, with no Instructor, shows as not in the SIS');

const emp = (await sis('POST', res('Employee'), { employee_name: 'Teacher C (SIS)', cell_number: '090000 00055', personal_email: 'Teacher.C@School.test', status: 'Active' })).data.name;
const insC = (await sis('POST', res('Instructor'), { instructor_name: 'Teacher C (SIS)', employee: emp, status: 'Active', instructor_log: [{ student_group: 'Grade 8-A', course: 'English', academic_year: '2026-27' }, { student_group: 'Old Group 2024', course: 'English', academic_year: '2024-25' }] })).data.name;
r = await sync();
ok(r.teachers_added === 1, 'A new Instructor in the SIS is added as a teacher');
let tc = await user('Teacher C (SIS)');
ok(tc && tc.mobile === '9000000055' && tc.email === 'teacher.c@school.test' && tc.sis_instructor === insC, 'Their mobile and email come from the Employee (cleaned up)');
const C = await login('9000000055');
const sc = (await C('GET', '/scope')).data.groups;
ok(sc.length === 1 && sc[0].label === 'Grade 8-A' && sc[0].subjects.join() === 'English', 'They can sign in and see the classes from the Instructor log');

await sis('PUT', res('Instructor') + '/' + insC, { instructor_name: 'Teacher C. Renamed' });
await sync();
ok(!!(await user('Teacher C. Renamed')), 'A name changed in the SIS changes here');
await sis('PUT', res('Instructor') + '/' + insC, { status: 'Left' });
r = await sync();
tc = await user('Teacher C. Renamed');
ok(r.teachers_left === 1 && tc.active === false, 'An Instructor marked Left is switched off here');
ok((await C('GET', '/scope')).status === 401, 'and is signed out');

console.log('Push: teachers made or changed here update the SIS');
r = await admin('POST', '/admin/users', { name: 'Teacher D (UpMark)', mobile: '9000000066', role: 'teacher' });
const idD = r.data.id;
ok(await until(async () => (await user('Teacher D (UpMark)')).sis_state === 'linked'), 'A teacher added here becomes an Instructor');
let insD = (await dump('Instructor')).find((i) => i.instructor_name === 'Teacher D (UpMark)');
ok(insD && insD.status === 'Active' && !insD.employee, 'Active, and without an Employee when none matches');
await admin('PUT', '/admin/users/' + idD + '/allocations', { allocations: [{ class_name: 'Grade 10', section: 'A', subject: 'Science' }, { class_name: 'Grade 8', section: 'A', subject: 'Science' }] });
ok(await until(async () => { const i = (await dump('Instructor')).find((x) => x.name === insD.name); return (i.instructor_log || []).length === 2; }), 'Their classes are written to the Instructor log');
insD = (await dump('Instructor')).find((x) => x.name === insD.name);
ok(insD.instructor_log.every((l) => l.academic_year === '2026-27' && l.course === 'Science') && insD.instructor_log.some((l) => l.student_group === 'Grade 10-A'), 'with the current academic year and the SIS student group');
ok((await admin('GET', '/admin/users/' + idD + '/allocations')).data.allocations.every((a) => a.source === 'frappe'), 'Those allocations are now SIS ones');
await sync();
ok((await user('Teacher D (UpMark)')).allocations === 2, 'The next roster sync keeps them');
await admin('PUT', '/admin/allocations/assign', { class_name: 'Grade 8', section: 'A', subject: 'Science', user_id: idD, assigned: false });
ok(await until(async () => (await dump('Instructor')).find((x) => x.name === insD.name).instructor_log.length === 1), 'Removing a class here removes it from the SIS');

const empE = (await sis('POST', res('Employee'), { employee_name: 'Teacher E', cell_number: '9000000067', status: 'Active' })).data.name;
r = await admin('POST', '/admin/users', { name: 'Teacher E', mobile: '9000000067', role: 'teacher' });
ok(await until(async () => (await user('Teacher E')).sis_state === 'linked'), 'A teacher with an Employee in the SIS is added too');
ok((await dump('Instructor')).find((i) => i.instructor_name === 'Teacher E').employee === empE, 'and their Instructor is linked to that Employee (matched by mobile)');

ta = await user('Teacher D (UpMark)');
await admin('PUT', '/admin/users/' + idD, { ...ta, active: false });
ok(await until(async () => (await dump('Instructor')).find((x) => x.name === insD.name).status === 'Left'), 'Deactivating here marks the Instructor as Left');
await sync();
ok((await user('Teacher D (UpMark)')).active === false, 'and the next sync keeps them switched off');

console.log('Teachers who were here before the sync');
const tb = await user('Teacher B (Demo)');
ok((await (await login('9000000001'))('POST', '/admin/users/' + tb.id + '/sis')).status === 403, 'Only admins can add teachers to the SIS');
r = await admin('POST', '/admin/users/' + tb.id + '/sis');
ok(r.status === 200 && r.data.queued === 1, 'Add to SIS for one teacher');
ok(await until(async () => (await user('Teacher B (Demo)')).sis_state === 'linked'), 'Teacher B is now an Instructor');
const insB = (await dump('Instructor')).find((i) => i.instructor_name === 'Teacher B (Demo)');
ok(insB.instructor_log.length === 5, 'with all five of their classes');
r = await admin('POST', '/admin/users/sis-all');
ok(r.status === 200 && r.data.queued === 0, 'Add all: nobody left to add');

console.log('An Instructor log row for another year is kept');
await admin('PUT', '/admin/users/' + tc.id, { ...tc, name: 'Teacher C. Renamed', active: true });
ok(await until(async () => (await dump('Instructor')).find((x) => x.name === insC).status === 'Active'), 'Reactivating here makes the Instructor Active again');
ok((await dump('Instructor')).find((x) => x.name === insC).instructor_log.some((l) => l.student_group === 'Old Group 2024'), 'Last year\'s log rows are left alone');

console.log('Deleted in the SIS');
await sis('DELETE', res('Instructor') + '/' + insB.name);
r = await sync();
ok((await user('Teacher B (Demo)')).active === false && (await user('Teacher B (Demo)')).sis_state === 'none', 'A teacher whose Instructor was deleted is switched off and unlinked');

console.log('Two tests for one section on the same day');
const A = await login('9000000001');
const day = new Date(Date.now() - 2 * 864e5).toISOString().slice(0, 10);
for (const subj of ['Hindi', 'Punjabi']) {
  const e = (await A('POST', '/exams', { class_name: 'Grade 8', section: 'A', category: 'Formative', sub_type: 'FA1', date: day, subjects: [subj], max_total: 20 })).data.exam_id;
  const kid = (await A('GET', '/exams/' + e)).data.roster[0];
  await A('PUT', '/exams/' + e + '/marks', { entries: [{ student_id: kid.id, subject: subj, status: '', comps: [15] }] });
}
ok(await until(async () => (await dump('Assessment Plan')).filter((p) => p.student_group === 'Grade 8-A' && p.schedule_date === day).length === 2, 30000), 'Both reach the SIS (no OverlapError)');
const plans = (await dump('Assessment Plan')).filter((p) => p.student_group === 'Grade 8-A' && p.schedule_date === day).map((p) => p.from_time + '-' + p.to_time).sort();
ok(plans.join() === '09:00:00-10:00:00,10:00:00-11:00:00', 'They take back-to-back hours: ' + plans.join(', '));

console.log(`\n${pass} passed, ${fail} failed`);
process.exit(fail ? 1 : 0);
