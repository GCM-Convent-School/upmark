package main

import (
	"bytes"
	"context"
	"crypto/sha1"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Frappe is a minimal REST client for the Frappe / ERPNext Education instance.
type Frappe struct {
	base, key, secret string
	hc                *http.Client
}

func newFrappe(c Config) *Frappe {
	return &Frappe{base: c.FrappeURL, key: c.FrappeKey, secret: c.FrappeSecret, hc: &http.Client{Timeout: 20 * time.Second}}
}

func (f *Frappe) Enabled() bool { return f.base != "" && f.key != "" && f.secret != "" }

type frappeDoc = map[string]any

func (f *Frappe) do(method, path string, q url.Values, body any, out any) error {
	if !f.Enabled() {
		return errNotConfigured
	}
	u := f.base + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, u, rd)
	req.Header.Set("Authorization", "token "+f.key+":"+f.secret)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := f.hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if resp.StatusCode >= 300 {
		return &frappeError{Status: resp.StatusCode, Body: string(data)}
	}
	if out != nil {
		return json.Unmarshal(data, out)
	}
	return nil
}

type frappeError struct {
	Status int
	Body   string
}

func (e *frappeError) Error() string {
	b := e.Body
	if len(b) > 300 {
		b = b[:300]
	}
	return fmt.Sprintf("frappe HTTP %d: %s", e.Status, b)
}

func (f *Frappe) List(doctype string, fields []string, filters [][]any) ([]frappeDoc, error) {
	q := url.Values{"limit_page_length": {"0"}}
	fb, _ := json.Marshal(fields)
	q.Set("fields", string(fb))
	if len(filters) > 0 {
		b, _ := json.Marshal(filters)
		q.Set("filters", string(b))
	}
	var out struct{ Data []frappeDoc }
	err := f.do("GET", "/api/resource/"+url.PathEscape(doctype), q, nil, &out)
	return out.Data, err
}

func (f *Frappe) Get(doctype, name string) (frappeDoc, error) {
	var out struct{ Data frappeDoc }
	err := f.do("GET", "/api/resource/"+url.PathEscape(doctype)+"/"+url.PathEscape(name), nil, nil, &out)
	return out.Data, err
}

func (f *Frappe) Insert(doctype string, doc frappeDoc) (frappeDoc, error) {
	doc["doctype"] = doctype
	var out struct{ Data frappeDoc }
	err := f.do("POST", "/api/resource/"+url.PathEscape(doctype), nil, doc, &out)
	return out.Data, err
}

func (f *Frappe) Update(doctype, name string, fields frappeDoc) (frappeDoc, error) {
	var out struct{ Data frappeDoc }
	err := f.do("PUT", "/api/resource/"+url.PathEscape(doctype)+"/"+url.PathEscape(name), nil, fields, &out)
	return out.Data, err
}

func (f *Frappe) Submit(doc frappeDoc) (frappeDoc, error) {
	var out struct{ Message frappeDoc }
	err := f.do("POST", "/api/method/frappe.client.submit", nil, map[string]any{"doc": doc}, &out)
	return out.Message, err
}

func (f *Frappe) Cancel(doctype, name string) error {
	return f.do("POST", "/api/method/frappe.client.cancel", nil, map[string]any{"doctype": doctype, "name": name}, nil)
}

func str(d frappeDoc, k string) string {
	if v, ok := d[k]; ok && v != nil {
		return fmt.Sprint(v)
	}
	return ""
}

func docstatus(d frappeDoc) int {
	if f, ok := d["docstatus"].(float64); ok {
		return int(f)
	}
	return 0
}

// ---------------- master sync (Frappe -> SQLite) ----------------

func (a *App) masterScheduler(ctx context.Context) {
	t := time.NewTicker(time.Hour)
	defer t.Stop()
	for {
		if a.frappe.Enabled() {
			last, _ := time.Parse(time.RFC3339, a.setting("last_master_sync"))
			if time.Since(last) >= 7*24*time.Hour {
				if _, err := a.masterSync(); err != nil {
					log.Printf("scheduled master sync: %v", err)
				}
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func (a *App) masterSync() (map[string]any, error) {
	if !a.frappe.Enabled() {
		return nil, errNotConfigured
	}
	start := time.Now()
	res, err := a.doMasterSync()
	if err != nil {
		a.setSetting("last_master_result", "failed: "+err.Error())
		return nil, err
	}
	res["duration_ms"] = time.Since(start).Milliseconds()
	b, _ := json.Marshal(res)
	a.setSetting("last_master_sync", nowStr())
	a.setSetting("last_master_result", string(b))
	return res, nil
}

type pulledStudent struct {
	frappeID, name, roll, admission, group string
}

func (a *App) doMasterSync() (map[string]any, error) {
	f := a.frappe
	groups, err := f.List("Student Group", []string{"name", "student_group_name", "disabled"}, nil)
	if err != nil {
		return nil, err
	}
	admission := map[string]string{}
	if a.cfg.AdmissionFld != "" {
		if rows, err := f.List("Student", []string{"name", a.cfg.AdmissionFld}, nil); err == nil {
			for _, r := range rows {
				admission[str(r, "name")] = str(r, a.cfg.AdmissionFld)
			}
		} // field may not exist on this site: fall back to the Student ID
	}
	var students []pulledStudent
	groupNames := map[string]bool{}
	for _, g := range groups {
		if docstatus(g) == 2 || str(g, "disabled") == "1" {
			continue
		}
		name := str(g, "name")
		full, err := f.Get("Student Group", name)
		if err != nil {
			return nil, err
		}
		groupNames[name] = true
		kids, _ := full["students"].([]any)
		for _, k := range kids {
			m, _ := k.(map[string]any)
			if m == nil || str(m, "active") == "0" {
				continue
			}
			sid := str(m, "student")
			adm := admission[sid]
			if adm == "" {
				adm = sid
			}
			students = append(students, pulledStudent{sid, str(m, "student_name"), str(m, "group_roll_number"), adm, name})
		}
	}
	courses, err := f.List("Course", []string{"name"}, nil)
	if err != nil {
		return nil, err
	}

	// Instructor -> Employee mapping, used to derive teacher allocations by email
	type instr struct {
		email string
		logs  []map[string]any
	}
	var instructors []instr
	if rows, err := f.List("Instructor", []string{"name", "employee"}, nil); err == nil {
		for _, r := range rows {
			emp := str(r, "employee")
			email := ""
			if emp != "" {
				if e, err := f.Get("Employee", emp); err == nil {
					email = normIdent(firstNonEmpty(str(e, "user_id"), str(e, "prefered_email"), str(e, "personal_email")))
				}
			}
			full, err := f.Get("Instructor", str(r, "name"))
			if err != nil || email == "" {
				continue
			}
			var logs []map[string]any
			if l, ok := full["instructor_log"].([]any); ok {
				for _, x := range l {
					if m, ok := x.(map[string]any); ok {
						logs = append(logs, m)
					}
				}
			}
			instructors = append(instructors, instr{email, logs})
		}
	}

	tx, err := a.begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	seen := map[string]bool{}
	var added, updated int
	for _, s := range students {
		class, section := splitGroup(s.group)
		var id int64
		var oname, oroll, oadm string
		var oactive int
		err := tx.QueryRow(`SELECT id,name,roll_no,admission_no,active FROM students WHERE student_group=? AND frappe_id=?`, s.group, s.frappeID).Scan(&id, &oname, &oroll, &oadm, &oactive)
		switch {
		case err == sql.ErrNoRows:
			_, err = tx.Exec(`INSERT INTO students(frappe_id,admission_no,roll_no,roll_sort,name,student_group,class_name,section) VALUES(?,?,?,?,?,?,?,?)`,
				s.frappeID, s.admission, s.roll, rollSort(s.roll), s.name, s.group, class, section)
			added++
		case err != nil:
			return nil, err
		default:
			if oname != s.name || oroll != s.roll || oadm != s.admission || oactive == 0 {
				updated++
			}
			_, err = tx.Exec(`UPDATE students SET admission_no=?,roll_no=?,roll_sort=?,name=?,class_name=?,section=?,active=1 WHERE id=?`,
				s.admission, s.roll, rollSort(s.roll), s.name, class, section, id)
		}
		if err != nil {
			return nil, err
		}
		seen[s.group+"\x00"+s.frappeID] = true
	}
	// students no longer present in any active group are retired (kept for history, hidden from rosters)
	rows, err := tx.Query(`SELECT id, student_group, frappe_id FROM students WHERE active=1`)
	if err != nil {
		return nil, err
	}
	var retire []int64
	for rows.Next() {
		var id int64
		var g, fid string
		rows.Scan(&id, &g, &fid)
		if !seen[g+"\x00"+fid] {
			retire = append(retire, id)
		}
	}
	rows.Close()
	for _, id := range retire {
		tx.Exec(`UPDATE students SET active=0 WHERE id=?`, id)
	}
	for _, c := range courses {
		tx.Exec(`INSERT OR IGNORE INTO courses(name) VALUES(?)`, str(c, "name"))
	}
	allocs := 0
	for _, in := range instructors {
		var uid int64
		if err := tx.QueryRow(`SELECT id FROM users WHERE email=? AND active=1`, in.email).Scan(&uid); err != nil {
			continue
		}
		tx.Exec(`DELETE FROM allocations WHERE user_id=? AND source='frappe'`, uid)
		for _, l := range in.logs {
			g, course := str(l, "student_group"), str(l, "course")
			if g == "" || course == "" || !groupNames[g] {
				continue
			}
			c, s := splitGroup(g)
			tx.Exec(`INSERT OR IGNORE INTO courses(name) VALUES(?)`, course)
			if _, err := tx.Exec(`INSERT OR IGNORE INTO allocations(user_id,class_name,section,subject,source) VALUES(?,?,?,?,'frappe')`, uid, c, s, course); err == nil {
				allocs++
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return map[string]any{"groups": len(groupNames), "students_seen": len(students), "students_added": added, "students_updated": updated,
		"students_retired": len(retire), "courses": len(courses), "allocations_from_erp": allocs}, nil
}

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		if s != "" {
			return s
		}
	}
	return ""
}

// ---------------- outbox worker (SQLite -> Frappe) ----------------

func (a *App) syncWorker(ctx context.Context) {
	for {
		worked := a.runDueJobs()
		if worked {
			continue
		}
		select {
		case <-ctx.Done():
			return
		case <-a.kick:
		case <-time.After(3 * time.Second):
		}
	}
}

type job struct {
	id, examID, studentID, corrID int64
	kind, subject                 string
	attempts                      int
}

func (a *App) runDueJobs() bool {
	var j job
	err := a.db.QueryRow(`SELECT id,kind,exam_id,student_id,subject,correction_id,attempts FROM jobs WHERE state='pending' AND next_try<=? ORDER BY id LIMIT 1`, nowStr()).
		Scan(&j.id, &j.kind, &j.examID, &j.studentID, &j.subject, &j.corrID, &j.attempts)
	if err != nil {
		return false
	}
	a.db.Exec(`UPDATE jobs SET state='running' WHERE id=?`, j.id)
	var runErr error
	switch j.kind {
	case "exam":
		runErr = a.syncExam(j.examID)
	case "correction":
		runErr = a.syncCorrection(j)
	default:
		runErr = fmt.Errorf("unknown job kind %q", j.kind)
	}
	if runErr == nil {
		a.db.Exec(`UPDATE jobs SET state='done', last_error='' WHERE id=?`, j.id)
		return true
	}
	if errors.Is(runErr, sql.ErrNoRows) { // exam was discarded meanwhile
		a.db.Exec(`UPDATE jobs SET state='done', last_error='target no longer exists' WHERE id=?`, j.id)
		return true
	}
	att := j.attempts + 1
	delay := time.Duration(5<<min(att, 7)) * time.Second // 10s .. ~10min
	if delay > 10*time.Minute {
		delay = 10 * time.Minute
	}
	msg := runErr.Error()
	if len(msg) > 500 {
		msg = msg[:500]
	}
	log.Printf("sync job %d (%s exam %d) failed (attempt %d): %s", j.id, j.kind, j.examID, att, msg)
	a.db.Exec(`UPDATE jobs SET state='pending', attempts=?, next_try=?, last_error=? WHERE id=?`, att, time.Now().Add(delay).UTC().Format(time.RFC3339), msg, j.id)
	return true
}

func resultHash(status, comps string) string {
	h := sha1.Sum([]byte(status + "|" + comps))
	return hex.EncodeToString(h[:])
}

func (f *Frappe) ensure(doctype, name string, doc frappeDoc) error {
	if _, err := f.Get(doctype, name); err == nil {
		return nil
	} else if fe, ok := err.(*frappeError); !ok || fe.Status != 404 {
		return err
	}
	_, err := f.Insert(doctype, doc)
	return err
}

func (a *App) syncExam(examID int64) error {
	e, err := a.loadExam(examID)
	if err != nil {
		return err
	}
	f := a.frappe
	if !f.Enabled() {
		return errNotConfigured
	}
	if err := f.ensure("Assessment Group", e.Category, frappeDoc{"assessment_group_name": e.Category, "parent_assessment_group": "All Assessment Groups", "is_group": 1}); err != nil {
		return err
	}
	if err := f.ensure("Assessment Group", e.SubType, frappeDoc{"assessment_group_name": e.SubType, "parent_assessment_group": e.Category, "is_group": 0}); err != nil {
		return err
	}
	var criteria []frappeDoc
	for _, c := range e.Components {
		if err := f.ensure("Assessment Criteria", c.Name, frappeDoc{"assessment_criteria": c.Name}); err != nil {
			return err
		}
		criteria = append(criteria, frappeDoc{"assessment_criteria": c.Name, "maximum_score": c.Max})
	}
	locked := e.Status == "locked"
	for _, subj := range e.Subjects {
		plan, err := a.ensurePlan(e, subj, criteria, locked)
		if err != nil {
			return err
		}
		rows, err := a.db.Query(`SELECT r.student_id,s.frappe_id,r.status,r.comps,r.total,r.frappe_name,r.frappe_docstatus,r.synced_hash
			FROM results r JOIN students s ON s.id=r.student_id WHERE r.exam_id=? AND r.subject=?`, examID, subj)
		if err != nil {
			return err
		}
		type pending struct {
			studentID, docstatus                int64
			frappeID, status, comps, name, hash string
			total                               sql.NullFloat64
		}
		var todo []pending
		for rows.Next() {
			var p pending
			rows.Scan(&p.studentID, &p.frappeID, &p.status, &p.comps, &p.total, &p.name, &p.docstatus, &p.hash)
			if !p.total.Valid { // incomplete rows stay local until finished
				continue
			}
			want := int64(0)
			if locked {
				want = 1
			}
			if p.hash == resultHash(p.status, p.comps) && p.docstatus == want {
				continue
			}
			todo = append(todo, p)
		}
		rows.Close()
		for _, p := range todo {
			var comps []*float64
			json.Unmarshal([]byte(p.comps), &comps)
			name, ds, err := a.pushResult(e, plan, p.frappeID, p.name, p.status, comps, locked)
			if err != nil {
				return fmt.Errorf("student %s: %w", p.frappeID, err)
			}
			a.db.Exec(`UPDATE results SET frappe_name=?,frappe_docstatus=?,synced_hash=? WHERE exam_id=? AND student_id=? AND subject=?`,
				name, ds, resultHash(p.status, p.comps), examID, p.studentID, subj)
		}
	}
	if locked {
		var already int
		a.db.QueryRow(`SELECT COUNT(*) FROM timeline WHERE exam_id=? AND kind='erp_synced'`, examID).Scan(&already)
		if already == 0 {
			addTimeline(a.db, examID, "System", "erp_synced", map[string]any{"message": "All results submitted to Frappe Education (docstatus=1)"})
		}
	}
	return nil
}

func (a *App) ensurePlan(e *Exam, subject string, criteria []frappeDoc, submit bool) (string, error) {
	f := a.frappe
	rows, err := f.List("Assessment Plan", []string{"name", "docstatus"}, [][]any{
		{"student_group", "=", e.StudentGroup}, {"course", "=", subject}, {"assessment_group", "=", e.SubType},
		{"schedule_date", "=", e.Date}, {"docstatus", "<", 2}})
	if err != nil {
		return "", err
	}
	var name string
	var ds int
	if len(rows) > 0 {
		name, ds = str(rows[0], "name"), docstatus(rows[0])
	} else {
		title := e.SubType + " " + subject
		if e.Topic != "" {
			title += " – " + e.Topic
		}
		doc, err := f.Insert("Assessment Plan", frappeDoc{
			"assessment_name": title, "student_group": e.StudentGroup, "course": subject, "assessment_group": e.SubType,
			"grading_scale": a.cfg.GradingScale, "schedule_date": e.Date, "from_time": "09:00:00", "to_time": "10:00:00",
			"maximum_assessment_score": e.MaxTotal, "assessment_criteria": criteria})
		if err != nil {
			return "", err
		}
		name, ds = str(doc, "name"), docstatus(doc)
	}
	if submit && ds == 0 {
		doc, err := f.Get("Assessment Plan", name)
		if err != nil {
			return "", err
		}
		if _, err := f.Submit(doc); err != nil && !strings.Contains(err.Error(), "not submittable") {
			return "", err
		}
	}
	return name, nil
}

// pushResult upserts one Assessment Result and (when wantSubmit) submits it. Submitted results are
// amended (cancel + new doc linked via amended_from) because Frappe forbids editing docstatus=1 documents.
func (a *App) pushResult(e *Exam, plan, studentFrappeID, existing, status string, comps []*float64, wantSubmit bool) (string, int64, error) {
	f := a.frappe
	var details []frappeDoc
	for i, c := range e.Components {
		score := 0.0
		if status == "" && i < len(comps) && comps[i] != nil {
			score = *comps[i]
		}
		details = append(details, frappeDoc{"assessment_criteria": c.Name, "maximum_score": c.Max, "score": score})
	}
	doc := frappeDoc{"assessment_plan": plan, "student": studentFrappeID, "student_group": e.StudentGroup, "details": details}
	if a.cfg.StatusField != "" {
		doc[a.cfg.StatusField] = status
	}
	if existing == "" {
		rows, err := f.List("Assessment Result", []string{"name", "docstatus"}, [][]any{{"assessment_plan", "=", plan}, {"student", "=", studentFrappeID}, {"docstatus", "<", 2}})
		if err != nil {
			return "", 0, err
		}
		if len(rows) > 0 {
			existing = str(rows[0], "name")
		}
	}
	var cur frappeDoc
	var err error
	if existing != "" {
		if cur, err = f.Get("Assessment Result", existing); err != nil {
			return "", 0, err
		}
		if docstatus(cur) == 2 { // cancelled remotely: start a fresh document
			existing, cur = "", nil
		}
	}
	switch {
	case cur == nil:
		cur, err = f.Insert("Assessment Result", doc)
	case docstatus(cur) == 1:
		if err = f.Cancel("Assessment Result", existing); err != nil {
			return "", 0, err
		}
		doc["amended_from"] = existing
		cur, err = f.Insert("Assessment Result", doc)
	default:
		cur, err = f.Update("Assessment Result", existing, doc)
	}
	if err != nil {
		return "", 0, err
	}
	name := str(cur, "name")
	ds := int64(docstatus(cur))
	if wantSubmit && ds == 0 {
		full, err := f.Get("Assessment Result", name)
		if err != nil {
			return "", 0, err
		}
		if _, err := f.Submit(full); err != nil {
			return "", 0, err
		}
		ds = 1
	}
	return name, ds, nil
}

func (a *App) syncCorrection(j job) error {
	e, err := a.loadExam(j.examID)
	if err != nil {
		return err
	}
	if !a.frappe.Enabled() {
		return errNotConfigured
	}
	var fid, status, comps, name string
	err = a.db.QueryRow(`SELECT s.frappe_id,r.status,r.comps,r.frappe_name FROM results r JOIN students s ON s.id=r.student_id
		WHERE r.exam_id=? AND r.student_id=? AND r.subject=?`, j.examID, j.studentID, j.subject).Scan(&fid, &status, &comps, &name)
	if err != nil {
		return err
	}
	var criteria []frappeDoc
	for _, c := range e.Components {
		criteria = append(criteria, frappeDoc{"assessment_criteria": c.Name, "maximum_score": c.Max})
	}
	plan, err := a.ensurePlan(e, j.subject, criteria, true)
	if err != nil {
		return err
	}
	var cs []*float64
	json.Unmarshal([]byte(comps), &cs)
	newName, ds, err := a.pushResult(e, plan, fid, name, status, cs, true)
	if err != nil {
		return err
	}
	a.db.Exec(`UPDATE results SET frappe_name=?,frappe_docstatus=?,synced_hash=? WHERE exam_id=? AND student_id=? AND subject=?`,
		newName, ds, resultHash(status, comps), j.examID, j.studentID, j.subject)
	var stName, roll string
	a.db.QueryRow(`SELECT name, roll_no FROM students WHERE id=?`, j.studentID).Scan(&stName, &roll)
	return addTimeline(a.db, j.examID, "System", "correction_synced", map[string]any{
		"correction_id": j.corrID, "student": stName, "roll_no": roll, "subject": j.subject, "erp_document": newName,
		"message": "Assessment Result re-synced to Frappe Education"})
}
