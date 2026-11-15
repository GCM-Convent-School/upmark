// Data safety: backups to the server and the SIS, automatic restore of a lost database, and importing
// an older database. Starts two extra UpMark servers next to the one run-all.sh starts. Usage: node tests/backup.mjs
import { spawn } from 'node:child_process';
import { mkdirSync, rmSync, writeFileSync, existsSync, readdirSync } from 'node:fs';
import { gunzipSync } from 'node:zlib';

const BASE = process.env.BASE || 'http://localhost:8080';
const MOCK = process.env.MOCK || 'http://localhost:8099';
let pass = 0, fail = 0;
const ok = (c, m) => { c ? pass++ : fail++; console.log((c ? '  PASS ' : '  FAIL ') + m); };
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
const today = new Date().toISOString().slice(0, 10);
const G8 = { class_name: 'Grade 8', section: 'A' };

async function login(base, ident) {
  const r = await fetch(base + '/api/auth/request', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ identifier: ident }) });
  const { dev_code } = await r.json();
  const v = await fetch(base + '/api/auth/verify', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ identifier: ident, code: dev_code }) });
  const cookie = v.headers.get('set-cookie').split(';')[0];
  const call = async (method, path, body) => {
    const res = await fetch(base + '/api' + path, { method, headers: { 'Content-Type': 'application/json', 'X-Requested-With': 'rea', cookie }, body: body === undefined ? undefined : JSON.stringify(body) });
    let data = {}; try { data = await res.clone().json(); } catch {}
    return { status: res.status, data, res };
  };
  return call;
}
const sisFiles = async () => ((await (await fetch(MOCK + '/__dump?doctype=File')).json()) || []).filter((f) => f.file_name.startsWith('upmark-backup-'));
const procs = [];
async function server(port, dir, env = {}) {
  if (!env.keep) rmSync(dir, { recursive: true, force: true });
  mkdirSync(dir, { recursive: true }); delete env.keep;
  for (const [k, v] of Object.entries(env.files || {})) { mkdirSync(dir + '/' + k.split('/').slice(0, -1).join('/'), { recursive: true }); writeFileSync(dir + '/' + k, v); }
  delete env.files;
  let log = '';
  const p = spawn('./rapid-entry', [], { env: { ...process.env, ADDR: ':' + port, DB_PATH: dir + '/rapid.db', DEV_OTP: '1', ADMIN_MOBILE: '9000000000', FRAPPE_URL: 'http://127.0.0.1:8099', FRAPPE_API_KEY: 'mock', FRAPPE_API_SECRET: 'mock', ...env } });
  p.stdout.on('data', (d) => (log += d)); p.stderr.on('data', (d) => (log += d));
  procs.push(p);
  const base = 'http://localhost:' + port;
  for (let i = 0; i < 100; i++) { try { if ((await fetch(base + '/healthz')).ok) break; } catch {} await sleep(100); }
  return { base, log: () => log };
}

try {
  const admin = await login(BASE, '9000000000');
  const A = await login(BASE, '9000000001');

  console.log('Something worth keeping');
  ok((await admin('POST', '/admin/users', { name: 'Teacher Z (Added)', mobile: '9000000077', role: 'teacher' })).status < 300, 'Admin adds a teacher');
  let r = await A('POST', '/exams', { ...G8, category: 'Formative', sub_type: 'FA1', date: today, subjects: ['Hindi'], max_total: 20, topic: 'Ch. 2' });
  const examId = r.data.exam_id;
  const kids = (await A('GET', '/exams/' + examId)).data.roster;
  r = await A('PUT', '/exams/' + examId + '/marks', { entries: kids.slice(0, 3).map((s, i) => ({ student_id: s.id, subject: 'Hindi', status: '', comps: [10 + i] })) });
  ok(r.status === 200, 'Teacher A starts a test and enters three marks (a draft, not in the SIS yet)');

  console.log('Backups');
  const ov = (await admin('GET', '/admin/overview')).data;
  ok(ov.storage && ov.storage.ok === true && ov.backup && ov.backup.last === '', 'Overview reports storage and that there is no backup yet');
  r = await admin('POST', '/admin/backup');
  ok(r.status === 200 && r.data.sis === true && /^upmark-backup-\d{8}-\d{6}\.db\.gz$/.test(r.data.file), 'Back up now writes a copy and uploads it to the SIS');
  ok(readdirSync('/tmp/rea-test/backups').some((f) => f === r.data.file), 'The copy is kept on the server next to the database');
  let files = await sisFiles();
  ok(files.length === 1 && files[0].is_private === '1', 'The SIS holds it as a private file');
  ok((await A('POST', '/admin/backup')).status === 403, 'Teachers cannot make backups');
  r = await admin('GET', '/admin/backup/download');
  const gz = Buffer.from(await r.res.arrayBuffer());
  ok(r.status === 200 && gz[0] === 0x1f && gz[1] === 0x8b && gunzipSync(gz).subarray(0, 15).toString() === 'SQLite format 3', 'Download a copy gives a gzipped SQLite database');
  ok((await A('GET', '/admin/backup/download')).status === 403, 'Teachers cannot download it');
  await sleep(1100);
  await admin('POST', '/admin/backup');
  ok((await admin('GET', '/admin/overview')).data.backup.last_sis !== '', 'Overview shows when the SIS last got a copy');
  ok((await sisFiles()).length === 2, 'Each backup is a new file in the SIS');

  console.log('A lost database comes back from the SIS');
  const B = await server(8081, '/tmp/rea-test-b');
  ok(/database restored from SIS backup upmark-backup-/.test(B.log()), 'A server with no database restores the newest SIS backup on start');
  const adminB = await login(B.base, '9000000000');
  const usersB = (await adminB('GET', '/admin/users')).data;
  ok(JSON.stringify(usersB).includes('Teacher Z (Added)'), 'The teacher added before is back');
  const ovB = (await adminB('GET', '/admin/overview')).data;
  ok(ovB.backup.restored_from === (await sisFiles()).at(-1).file_name, 'Overview says which backup it came from');
  const AB = await login(B.base, '9000000001');
  const eB = (await AB('GET', '/exams/' + examId)).data;
  ok(eB.exam && eB.exam.topic === 'Ch. 2' && eB.entries.filter((e) => e.total != null).length === 3, 'The unfinished test and its three marks are back');

  console.log('An existing database is never replaced');
  await adminB('POST', '/admin/users', { name: 'Only on B', mobile: '9000000088', role: 'teacher' });
  procs.pop().kill(); await sleep(300);
  const B2 = await server(8081, '/tmp/rea-test-b', { keep: true });
  ok(!/restored from SIS/.test(B2.log()), 'Restarting with its database in place restores nothing');
  ok(JSON.stringify((await (await login(B2.base, '9000000000'))('GET', '/admin/users')).data).includes('Only on B'), 'Data written after the restore is still there');
  procs.pop().kill(); await sleep(300);

  console.log('Older databases can be merged in');
  const snapshot = gunzipSync(gz);
  const C = await server(8082, '/tmp/rea-test-c', { AUTO_RESTORE: '0', SEED_DEMO: '1', files: { 'recovered/volume-1/rapid.db': snapshot } });
  ok(!/restored from SIS/.test(C.log()), 'AUTO_RESTORE=0 turns automatic restore off');
  const adminC = await login(C.base, '9000000000');
  let rec = (await adminC('GET', '/admin/recovery')).data;
  ok(rec.files.length === 1 && rec.files[0].file === 'volume-1/rapid.db' && rec.files[0].teachers >= 3 && rec.files[0].tests >= 1, 'The recovered file is listed with its teachers and tests');
  ok((await adminC('POST', '/admin/recovery/import', { file: '../rapid.db' })).status === 404, 'Only files in the recovered folder can be imported');
  r = await adminC('POST', '/admin/recovery/import', { file: 'volume-1/rapid.db' });
  ok(r.status === 200 && r.data.Teachers === 1 && r.data.Tests >= 1 && r.data.Marks >= 3, 'Import adds the missing teacher and the test with its marks (demo teachers matched, not duplicated)');
  ok(JSON.stringify((await adminC('GET', '/admin/users')).data).includes('Teacher Z (Added)'), 'The recovered teacher can be found');
  const AC = await login(C.base, '9000000001');
  const list = (await AC('GET', '/exams?status=')).data;
  ok(JSON.stringify(list).includes('Ch. 2'), 'Teacher A sees the recovered test');
  r = await adminC('POST', '/admin/recovery/import', { file: 'volume-1/rapid.db' });
  ok(r.status === 200 && r.data.Teachers === 0 && r.data.Tests === 0 && r.data.TestsSkipped >= 1, 'Importing again changes nothing');
  rec = (await adminC('GET', '/admin/recovery')).data;
  ok(rec.files[0].imported !== '', 'The file is marked as imported');
} finally {
  for (const p of procs) p.kill();
}
console.log(`\n${pass} passed, ${fail} failed`);
process.exit(fail ? 1 : 0);
