package main

import (
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sync"

	_ "modernc.org/sqlite"
)

const schema = `
CREATE TABLE IF NOT EXISTS users(
  id INTEGER PRIMARY KEY, name TEXT NOT NULL, mobile TEXT UNIQUE, email TEXT UNIQUE,
  role TEXT NOT NULL CHECK(role IN('teacher','admin')), active INTEGER NOT NULL DEFAULT 1, created_at TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS otps(
  identifier TEXT PRIMARY KEY, code_hash TEXT NOT NULL, expires_at TEXT NOT NULL,
  attempts INTEGER NOT NULL DEFAULT 0, sent_at TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS sessions(
  token_hash TEXT PRIMARY KEY, user_id INTEGER NOT NULL REFERENCES users(id), expires_at TEXT NOT NULL);

CREATE TABLE IF NOT EXISTS students(
  id INTEGER PRIMARY KEY, frappe_id TEXT NOT NULL, admission_no TEXT NOT NULL DEFAULT '', roll_no TEXT NOT NULL DEFAULT '',
  roll_sort INTEGER NOT NULL DEFAULT 0, name TEXT NOT NULL, student_group TEXT NOT NULL,
  class_name TEXT NOT NULL, section TEXT NOT NULL DEFAULT '', active INTEGER NOT NULL DEFAULT 1,
  UNIQUE(student_group, frappe_id));
CREATE INDEX IF NOT EXISTS idx_students_cs ON students(class_name, section, active);
CREATE TABLE IF NOT EXISTS courses(name TEXT PRIMARY KEY);
CREATE TABLE IF NOT EXISTS allocations(
  user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  class_name TEXT NOT NULL, section TEXT NOT NULL DEFAULT '', subject TEXT NOT NULL,
  source TEXT NOT NULL DEFAULT 'manual', PRIMARY KEY(user_id, class_name, section, subject));

CREATE TABLE IF NOT EXISTS exams(
  id INTEGER PRIMARY KEY AUTOINCREMENT, class_name TEXT NOT NULL, section TEXT NOT NULL DEFAULT '',
  student_group TEXT NOT NULL, category TEXT NOT NULL, sub_type TEXT NOT NULL, exam_date TEXT NOT NULL,
  subject_key TEXT NOT NULL, topic TEXT NOT NULL DEFAULT '', combined INTEGER NOT NULL DEFAULT 0,
  split INTEGER NOT NULL DEFAULT 0, max_total REAL NOT NULL, status TEXT NOT NULL DEFAULT 'draft',
  owner_id INTEGER NOT NULL, created_at TEXT NOT NULL, locked_at TEXT, locked_by INTEGER);
CREATE UNIQUE INDEX IF NOT EXISTS uq_exam_day ON exams(class_name, section, category, exam_date, subject_key);
CREATE TABLE IF NOT EXISTS exam_subjects(
  exam_id INTEGER NOT NULL REFERENCES exams(id) ON DELETE CASCADE, subject TEXT NOT NULL, PRIMARY KEY(exam_id, subject));
CREATE TABLE IF NOT EXISTS exam_components(
  exam_id INTEGER NOT NULL REFERENCES exams(id) ON DELETE CASCADE, idx INTEGER NOT NULL, name TEXT NOT NULL,
  max_marks REAL NOT NULL, PRIMARY KEY(exam_id, idx));
CREATE TABLE IF NOT EXISTS results(
  exam_id INTEGER NOT NULL REFERENCES exams(id) ON DELETE CASCADE, student_id INTEGER NOT NULL,
  subject TEXT NOT NULL, status TEXT NOT NULL DEFAULT '', comps TEXT NOT NULL, total REAL,
  updated_at TEXT NOT NULL, updated_by INTEGER NOT NULL,
  frappe_name TEXT NOT NULL DEFAULT '', frappe_docstatus INTEGER NOT NULL DEFAULT -1, synced_hash TEXT NOT NULL DEFAULT '',
  PRIMARY KEY(exam_id, student_id, subject));

CREATE TABLE IF NOT EXISTS corrections(
  id INTEGER PRIMARY KEY AUTOINCREMENT, exam_id INTEGER NOT NULL, student_id INTEGER NOT NULL, subject TEXT NOT NULL,
  old_status TEXT NOT NULL, old_comps TEXT NOT NULL, old_total REAL,
  new_status TEXT NOT NULL, new_comps TEXT NOT NULL, new_total REAL,
  reason TEXT NOT NULL, state TEXT NOT NULL DEFAULT 'pending', requested_by INTEGER NOT NULL, requested_at TEXT NOT NULL,
  decided_by INTEGER, decided_at TEXT, decision_note TEXT NOT NULL DEFAULT '');
CREATE TABLE IF NOT EXISTS timeline(
  id INTEGER PRIMARY KEY AUTOINCREMENT, exam_id INTEGER NOT NULL, ts TEXT NOT NULL, user_name TEXT NOT NULL,
  kind TEXT NOT NULL, detail TEXT NOT NULL DEFAULT '{}');
CREATE INDEX IF NOT EXISTS idx_timeline_exam ON timeline(exam_id, id);
CREATE TRIGGER IF NOT EXISTS timeline_no_update BEFORE UPDATE ON timeline BEGIN SELECT RAISE(ABORT,'timeline is immutable'); END;
CREATE TRIGGER IF NOT EXISTS timeline_no_delete BEFORE DELETE ON timeline BEGIN SELECT RAISE(ABORT,'timeline is immutable'); END;

CREATE TABLE IF NOT EXISTS jobs(
  id INTEGER PRIMARY KEY AUTOINCREMENT, kind TEXT NOT NULL, exam_id INTEGER NOT NULL,
  student_id INTEGER NOT NULL DEFAULT 0, subject TEXT NOT NULL DEFAULT '', correction_id INTEGER NOT NULL DEFAULT 0,
  state TEXT NOT NULL DEFAULT 'pending', attempts INTEGER NOT NULL DEFAULT 0, next_try TEXT NOT NULL,
  last_error TEXT NOT NULL DEFAULT '', created_at TEXT NOT NULL);
CREATE INDEX IF NOT EXISTS idx_jobs_state ON jobs(state, next_try);
CREATE TABLE IF NOT EXISTS settings(k TEXT PRIMARY KEY, v TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS parent_msgs(
  exam_id INTEGER NOT NULL, student_id INTEGER NOT NULL, mobile TEXT NOT NULL, status TEXT NOT NULL DEFAULT 'pending',
  error TEXT NOT NULL DEFAULT '', sent_at TEXT NOT NULL DEFAULT '', PRIMARY KEY(exam_id, student_id, mobile));

CREATE TABLE IF NOT EXISTS notifications(
  id INTEGER PRIMARY KEY AUTOINCREMENT, user_id INTEGER NOT NULL, kind TEXT NOT NULL, title TEXT NOT NULL,
  body TEXT NOT NULL DEFAULT '', exam_id INTEGER NOT NULL DEFAULT 0, created_at TEXT NOT NULL, read_at TEXT NOT NULL DEFAULT '');
CREATE INDEX IF NOT EXISTS idx_notif_user ON notifications(user_id, id);
CREATE TABLE IF NOT EXISTS parent_sessions(
  token_hash TEXT PRIMARY KEY, student_id INTEGER NOT NULL, expires_at TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS weekly_reports(
  class_name TEXT NOT NULL, section TEXT NOT NULL DEFAULT '', week TEXT NOT NULL, published_at TEXT NOT NULL,
  PRIMARY KEY(class_name, section, week));
CREATE TABLE IF NOT EXISTS report_seen(
  student_id INTEGER NOT NULL, week TEXT NOT NULL, seen_at TEXT NOT NULL, PRIMARY KEY(student_id, week));
`

func openDB(path string) (*sql.DB, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return nil, err
	}
	q := url.Values{}
	for _, p := range []string{"journal_mode(WAL)", "busy_timeout(5000)", "synchronous(NORMAL)", "foreign_keys(1)"} {
		q.Add("_pragma", p)
	}
	q.Add("_txlock", "immediate")
	db, err := sql.Open("sqlite", "file:"+path+"?"+q.Encode())
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(8)
	if _, err := db.Exec(schema); err != nil {
		return nil, fmt.Errorf("schema: %w", err)
	}
	if err := migrate(db); err != nil {
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return db, nil
}

// wtx is a write transaction guarded by a process-wide mutex. SQLite allows one writer at a time; queueing
// writers in Go (instead of letting them spin in SQLite's busy handler) keeps latency flat under load.
type wtx struct {
	*sql.Tx
	mu   *sync.Mutex
	done bool
}

func (a *App) begin() (*wtx, error) {
	a.wmu.Lock()
	tx, err := a.db.Begin()
	if err != nil {
		a.wmu.Unlock()
		return nil, err
	}
	return &wtx{Tx: tx, mu: &a.wmu}, nil
}

func (t *wtx) release() {
	if !t.done {
		t.done = true
		t.mu.Unlock()
	}
}

func (t *wtx) Commit() error   { defer t.release(); return t.Tx.Commit() }
func (t *wtx) Rollback() error { defer t.release(); return t.Tx.Rollback() }

// migrate adds columns introduced after the first release (CREATE TABLE IF NOT EXISTS never alters an existing table).
func migrate(db *sql.DB) error {
	add := map[string][][2]string{
		"exams": {{"approved_at", "TEXT NOT NULL DEFAULT ''"}, {"approved_by", "INTEGER NOT NULL DEFAULT 0"}, {"parents_sent_at", "TEXT NOT NULL DEFAULT ''"}},
		"students": {{"gender", "TEXT NOT NULL DEFAULT ''"}, {"dob", "TEXT NOT NULL DEFAULT ''"}, {"father_name", "TEXT NOT NULL DEFAULT ''"},
			{"father_mobile", "TEXT NOT NULL DEFAULT ''"}, {"mother_name", "TEXT NOT NULL DEFAULT ''"}, {"mother_mobile", "TEXT NOT NULL DEFAULT ''"}},
	}
	for table, cols := range add {
		have := map[string]bool{}
		rows, err := db.Query(`SELECT name FROM pragma_table_info('` + table + `')`)
		if err != nil {
			return err
		}
		for rows.Next() {
			var n string
			rows.Scan(&n)
			have[n] = true
		}
		rows.Close()
		for _, c := range cols {
			if !have[c[0]] {
				if _, err := db.Exec(`ALTER TABLE ` + table + ` ADD COLUMN ` + c[0] + ` ` + c[1]); err != nil {
					return err
				}
			}
		}
	}
	return nil
}
