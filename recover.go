package main

import (
	"database/sql"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Recovery merges an older UpMark database (copied off the server into <data>/recovered/) into the live
// one: teachers and hand-made allocations, tests with their marks, corrections and history, report
// publishing, and merit/demerit entries that never reached the SIS. Records already present are left
// alone, so importing the same file twice, or several old files, is safe.

func recoverDir(dbPath string) string { return filepath.Join(filepath.Dir(dbPath), "recovered") }

// recoverFiles finds candidates: recovered/<name>.db, or recovered/<name>/rapid.db as copied from a volume.
func recoverFiles(dbPath string) []string {
	dir := recoverDir(dbPath)
	var out []string
	a, _ := filepath.Glob(filepath.Join(dir, "*.db"))
	b, _ := filepath.Glob(filepath.Join(dir, "*", "*.db"))
	for _, f := range append(a, b...) {
		if st, err := os.Stat(f); err == nil && st.Size() > 0 {
			rel, _ := filepath.Rel(dir, f)
			out = append(out, rel)
		}
	}
	sort.Strings(out)
	return out
}

type recoverSummary struct {
	File     string `json:"file"`
	Teachers int    `json:"teachers"`
	Tests    int    `json:"tests"`
	Marks    int    `json:"marks"`
	First    string `json:"first"`
	Last     string `json:"last"`
	Imported string `json:"imported"`
	Error    string `json:"error,omitempty"`
}

func openOld(path string) (*sql.DB, error) {
	// openDB brings an older file up to the current schema; it works on the recovered copy, never the original volume
	return openDB(path)
}

func (a *App) summarize(rel string) recoverSummary {
	s := recoverSummary{File: rel, Imported: a.setting("recovered:" + rel)}
	old, err := openOld(filepath.Join(recoverDir(a.cfg.DBPath), rel))
	if err != nil {
		s.Error = err.Error()
		return s
	}
	defer old.Close()
	old.QueryRow(`SELECT COUNT(*) FROM users WHERE role='teacher'`).Scan(&s.Teachers)
	old.QueryRow(`SELECT COUNT(*) FROM exams`).Scan(&s.Tests)
	old.QueryRow(`SELECT COUNT(*) FROM results`).Scan(&s.Marks)
	old.QueryRow(`SELECT COALESCE(MIN(created_at),'') FROM users`).Scan(&s.First)
	old.QueryRow(`SELECT COALESCE(MAX(t),'') FROM (SELECT MAX(updated_at) t FROM results UNION SELECT MAX(created_at) FROM exams UNION SELECT MAX(created_at) FROM users UNION SELECT MAX(ts) FROM timeline)`).Scan(&s.Last)
	return s
}

type recoverResult struct {
	Teachers, Allocations, Tests, TestsSkipped, Marks, MarksSkipped, Corrections, Conduct, Reports int
}

func (a *App) importOld(rel string) (*recoverResult, error) {
	path := filepath.Join(recoverDir(a.cfg.DBPath), filepath.Clean(rel))
	if !strings.HasPrefix(path, recoverDir(a.cfg.DBPath)+string(os.PathSeparator)) {
		return nil, fmt.Errorf("bad file")
	}
	old, err := openOld(path)
	if err != nil {
		return nil, err
	}
	defer old.Close()
	tx, err := a.begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	res := &recoverResult{}

	// teachers and admins, matched by mobile or email
	users := map[int64]int64{}
	rows, err := old.Query(`SELECT id,name,COALESCE(mobile,''),COALESCE(email,''),role,active,created_at FROM users ORDER BY id`)
	if err != nil {
		return nil, err
	}
	type ou struct {
		id                                 int64
		name, mobile, email, role, created string
		active                             int
	}
	var olds []ou
	for rows.Next() {
		var u ou
		rows.Scan(&u.id, &u.name, &u.mobile, &u.email, &u.role, &u.active, &u.created)
		olds = append(olds, u)
	}
	rows.Close()
	for _, u := range olds {
		var nid int64
		err := tx.QueryRow(`SELECT id FROM users WHERE (mobile<>'' AND mobile=?) OR (email<>'' AND email=?) LIMIT 1`, u.mobile, u.email).Scan(&nid)
		if err == sql.ErrNoRows {
			r, err := tx.Exec(`INSERT INTO users(name,mobile,email,role,active,created_at) VALUES(?,NULLIF(?,''),NULLIF(?,''),?,?,?)`, u.name, u.mobile, u.email, u.role, u.active, u.created)
			if err != nil {
				return nil, fmt.Errorf("teacher %s: %w", u.name, err)
			}
			nid, _ = r.LastInsertId()
			if u.role == "teacher" {
				res.Teachers++
			}
		} else if err != nil {
			return nil, err
		}
		users[u.id] = nid
	}
	mapUser := func(id int64) int64 {
		if n, ok := users[id]; ok {
			return n
		}
		return 0
	}

	// allocations made by hand (the SIS ones come back with the next roster sync)
	rows, err = old.Query(`SELECT user_id,class_name,section,subject FROM allocations WHERE source='manual'`)
	if err != nil {
		return nil, err
	}
	type al struct {
		uid            int64
		cls, sec, subj string
	}
	var als []al
	for rows.Next() {
		var x al
		rows.Scan(&x.uid, &x.cls, &x.sec, &x.subj)
		als = append(als, x)
	}
	rows.Close()
	for _, x := range als {
		if r, err := tx.Exec(`INSERT OR IGNORE INTO allocations(user_id,class_name,section,subject,source) VALUES(?,?,?,?,'manual')`, mapUser(x.uid), x.cls, x.sec, x.subj); err == nil {
			if n, _ := r.RowsAffected(); n > 0 {
				res.Allocations++
			}
		}
	}

	// students are matched through their SIS id and section
	studentMap := map[int64]int64{}
	frappeOf := map[int64][2]string{}
	rows, err = old.Query(`SELECT id,frappe_id,student_group FROM students`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id int64
		var fid, g string
		rows.Scan(&id, &fid, &g)
		frappeOf[id] = [2]string{fid, g}
	}
	rows.Close()
	mapStudent := func(id int64) (int64, bool) {
		if n, ok := studentMap[id]; ok {
			return n, n != 0
		}
		k, ok := frappeOf[id]
		var nid int64
		if ok {
			tx.QueryRow(`SELECT id FROM students WHERE frappe_id=? AND student_group=?`, k[0], k[1]).Scan(&nid)
		}
		studentMap[id] = nid
		return nid, nid != 0
	}

	// tests: skipped when the same test (section, type, date, subjects) already exists here
	type oe struct {
		id                                                         int64
		cls, sec, grp, cat, sub, date, key, topic, status, created string
		combined, split, owner                                     int64
		max                                                        float64
		lockedAt                                                   sql.NullString
		lockedBy                                                   sql.NullInt64
		approvedAt, parentsSent                                    string
		approvedBy                                                 int64
	}
	rows, err = old.Query(`SELECT id,class_name,section,student_group,category,sub_type,exam_date,subject_key,topic,combined,split,max_total,status,owner_id,created_at,locked_at,locked_by,approved_at,approved_by,parents_sent_at FROM exams ORDER BY id`)
	if err != nil {
		return nil, err
	}
	var exams []oe
	for rows.Next() {
		var e oe
		if err := rows.Scan(&e.id, &e.cls, &e.sec, &e.grp, &e.cat, &e.sub, &e.date, &e.key, &e.topic, &e.combined, &e.split, &e.max, &e.status, &e.owner, &e.created, &e.lockedAt, &e.lockedBy, &e.approvedAt, &e.approvedBy, &e.parentsSent); err != nil {
			rows.Close()
			return nil, err
		}
		exams = append(exams, e)
	}
	rows.Close()
	for _, e := range exams {
		var exists int
		tx.QueryRow(`SELECT COUNT(*) FROM exams WHERE class_name=? AND section=? AND category=? AND exam_date=? AND subject_key=?`, e.cls, e.sec, e.cat, e.date, e.key).Scan(&exists)
		if exists > 0 {
			res.TestsSkipped++
			continue
		}
		var lockedBy any
		if e.lockedBy.Valid {
			lockedBy = mapUser(e.lockedBy.Int64)
		}
		r, err := tx.Exec(`INSERT INTO exams(class_name,section,student_group,category,sub_type,exam_date,subject_key,topic,combined,split,max_total,status,owner_id,created_at,locked_at,locked_by,approved_at,approved_by,parents_sent_at)
			VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, e.cls, e.sec, e.grp, e.cat, e.sub, e.date, e.key, e.topic, e.combined, e.split, e.max, e.status, mapUser(e.owner), e.created, e.lockedAt, lockedBy, e.approvedAt, mapUser(e.approvedBy), e.parentsSent)
		if err != nil {
			return nil, fmt.Errorf("test %d: %w", e.id, err)
		}
		nid, _ := r.LastInsertId()
		res.Tests++
		if err := copyRows(old, tx, `SELECT subject FROM exam_subjects WHERE exam_id=?`, e.id, func(v []any) error {
			_, err := tx.Exec(`INSERT INTO exam_subjects(exam_id,subject) VALUES(?,?)`, nid, v[0])
			return err
		}); err != nil {
			return nil, err
		}
		if err := copyRows(old, tx, `SELECT idx,name,max_marks FROM exam_components WHERE exam_id=?`, e.id, func(v []any) error {
			_, err := tx.Exec(`INSERT INTO exam_components(exam_id,idx,name,max_marks) VALUES(?,?,?,?)`, nid, v[0], v[1], v[2])
			return err
		}); err != nil {
			return nil, err
		}
		unsynced := false
		if err := copyRows(old, tx, `SELECT student_id,subject,status,comps,total,updated_at,updated_by,frappe_name,frappe_docstatus,synced_hash FROM results WHERE exam_id=?`, e.id, func(v []any) error {
			sid, ok := mapStudent(v[0].(int64))
			if !ok {
				res.MarksSkipped++
				return nil
			}
			if v[7] == "" && v[4] != nil {
				unsynced = true
			}
			_, err := tx.Exec(`INSERT INTO results(exam_id,student_id,subject,status,comps,total,updated_at,updated_by,frappe_name,frappe_docstatus,synced_hash) VALUES(?,?,?,?,?,?,?,?,?,?,?)`,
				nid, sid, v[1], v[2], v[3], v[4], v[5], mapUser(v[6].(int64)), v[7], v[8], v[9])
			if err == nil {
				res.Marks++
			}
			return err
		}); err != nil {
			return nil, err
		}
		if err := copyRows(old, tx, `SELECT ts,user_name,kind,detail FROM timeline WHERE exam_id=? ORDER BY id`, e.id, func(v []any) error {
			_, err := tx.Exec(`INSERT INTO timeline(exam_id,ts,user_name,kind,detail) VALUES(?,?,?,?,?)`, nid, v[0], v[1], v[2], v[3])
			return err
		}); err != nil {
			return nil, err
		}
		addTimeline(tx, nid, "System", "recovered", map[string]any{"message": "Restored from an earlier UpMark database (" + rel + ")"})
		if err := copyRows(old, tx, `SELECT student_id,subject,old_status,old_comps,old_total,new_status,new_comps,new_total,reason,state,requested_by,requested_at,decided_by,decided_at,decision_note FROM corrections WHERE exam_id=?`, e.id, func(v []any) error {
			sid, ok := mapStudent(v[0].(int64))
			if !ok {
				return nil
			}
			var decided any
			if v[12] != nil {
				decided = mapUser(v[12].(int64))
			}
			_, err := tx.Exec(`INSERT INTO corrections(exam_id,student_id,subject,old_status,old_comps,old_total,new_status,new_comps,new_total,reason,state,requested_by,requested_at,decided_by,decided_at,decision_note) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
				nid, sid, v[1], v[2], v[3], v[4], v[5], v[6], v[7], v[8], v[9], mapUser(v[10].(int64)), v[11], decided, v[13], v[14])
			if err == nil {
				res.Corrections++
			}
			return err
		}); err != nil {
			return nil, err
		}
		if unsynced { // finished marks that never reached the SIS go out now
			enqueue(tx, "exam", nid, 0, "", 0, true)
		}
	}

	// weekly report publishing history
	copyRows(old, tx, `SELECT class_name,section,week,published_at FROM weekly_reports`, nil, func(v []any) error {
		if r, err := tx.Exec(`INSERT OR IGNORE INTO weekly_reports(class_name,section,week,published_at) VALUES(?,?,?,?)`, v...); err == nil {
			if n, _ := r.RowsAffected(); n > 0 {
				res.Reports++
			}
		}
		return nil
	})

	// merit/demerit entries that were still waiting to reach the SIS (the rest come back from the SIS)
	copyRows(old, tx, `SELECT student_id,frappe_student,student_group,academic_year,date,category,point_type,points,remarks,awarded_by,awarded_by_name,created_at FROM conduct_points WHERE frappe_name='' AND state='pending'`, nil, func(v []any) error {
		sid, ok := mapStudent(v[0].(int64))
		if !ok {
			return nil
		}
		r, err := tx.Exec(`INSERT INTO conduct_points(student_id,frappe_student,student_group,academic_year,date,category,point_type,points,remarks,awarded_by,awarded_by_name,created_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`,
			sid, v[1], v[2], v[3], v[4], v[5], v[6], v[7], v[8], mapUser(v[9].(int64)), v[10], v[11])
		if err != nil {
			return nil
		}
		id, _ := r.LastInsertId()
		tx.Exec(`INSERT INTO jobs(kind,exam_id,correction_id,next_try,created_at) VALUES('conduct',0,?,?,?)`, id, nowStr(), nowStr())
		res.Conduct++
		return nil
	})

	if _, err := tx.Exec(`INSERT INTO settings(k,v) VALUES(?,?) ON CONFLICT(k) DO UPDATE SET v=excluded.v`, "recovered:"+rel, nowStr()); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	a.wake()
	return res, nil
}

// copyRows runs q on the old database (with arg when not nil) and hands each row to fn.
func copyRows(old *sql.DB, _ *wtx, q string, arg any, fn func([]any) error) error {
	var rows *sql.Rows
	var err error
	if arg != nil {
		rows, err = old.Query(q, arg)
	} else {
		rows, err = old.Query(q)
	}
	if err != nil {
		return err
	}
	cols, _ := rows.Columns()
	var all [][]any
	for rows.Next() {
		v := make([]any, len(cols))
		p := make([]any, len(cols))
		for i := range v {
			p[i] = &v[i]
		}
		if err := rows.Scan(p...); err != nil {
			rows.Close()
			return err
		}
		for i, x := range v {
			if b, ok := x.([]byte); ok {
				v[i] = string(b)
			}
		}
		all = append(all, v)
	}
	rows.Close()
	for _, v := range all {
		if err := fn(v); err != nil {
			return err
		}
	}
	return nil
}

func (a *App) recoverRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/admin/recovery", a.wrap("admin", func(w http.ResponseWriter, r *http.Request, u *User) error {
		out := []recoverSummary{}
		for _, f := range recoverFiles(a.cfg.DBPath) {
			out = append(out, a.summarize(f))
		}
		writeJSON(w, 200, map[string]any{"files": out, "folder": recoverDir(a.cfg.DBPath)})
		return nil
	}))
	mux.HandleFunc("POST /api/admin/recovery/import", a.wrap("admin", func(w http.ResponseWriter, r *http.Request, u *User) error {
		var in struct{ File string }
		if err := decode(r, &in); err != nil {
			return err
		}
		ok := false
		for _, f := range recoverFiles(a.cfg.DBPath) {
			ok = ok || f == in.File
		}
		if !ok {
			return notFound("That file is not in the recovered folder")
		}
		res, err := a.importOld(in.File)
		if err != nil {
			return &apiErr{Code: 500, Msg: "Import failed, nothing was changed: " + err.Error()}
		}
		writeJSON(w, 200, res)
		return nil
	}))
}
