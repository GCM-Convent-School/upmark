package main

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
)

func (a *App) adminRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/admin/overview", a.wrap("admin", a.adminOverview))
	mux.HandleFunc("GET /api/admin/exams", a.wrap("admin", a.adminExams))
	mux.HandleFunc("GET /api/admin/classes", a.wrap("admin", a.adminClasses))
	mux.HandleFunc("GET /api/admin/allocations", a.wrap("admin", a.adminAllocMatrix))
	mux.HandleFunc("PUT /api/admin/allocations/assign", a.wrap("admin", a.adminAllocAssign))
	mux.HandleFunc("GET /api/admin/users", a.wrap("admin", a.adminUsers))
	mux.HandleFunc("POST /api/admin/users", a.wrap("admin", a.adminUserCreate))
	mux.HandleFunc("PUT /api/admin/users/{id}", a.wrap("admin", a.adminUserUpdate))
	mux.HandleFunc("GET /api/admin/users/{id}/allocations", a.wrap("admin", a.adminAllocGet))
	mux.HandleFunc("PUT /api/admin/users/{id}/allocations", a.wrap("admin", a.adminAllocPut))
	mux.HandleFunc("POST /api/admin/users/import", a.wrap("admin", a.adminUsersImport))
	mux.HandleFunc("POST /api/admin/users/dummy", a.wrap("admin", a.adminDummyTeachers))
	mux.HandleFunc("GET /api/admin/approvals", a.wrap("admin", a.adminApprovals))
	mux.HandleFunc("POST /api/admin/exams/{id}/approve", a.wrap("admin", a.adminApprove))
	mux.HandleFunc("GET /api/admin/analysis", a.wrap("admin", a.adminAnalysis))
	mux.HandleFunc("POST /api/admin/students/import", a.wrap("admin", a.adminStudentsImport))
	mux.HandleFunc("GET /api/admin/students", a.wrap("admin", a.adminStudents))
	mux.HandleFunc("POST /api/admin/sync/master", a.wrap("admin", a.adminMasterSync))
	mux.HandleFunc("GET /api/admin/corrections", a.wrap("admin", a.adminCorrections))
	mux.HandleFunc("POST /api/admin/corrections/{id}/{action}", a.wrap("admin", a.adminCorrectionDecide))
}

// syncHealth maps outbox state to the three Sync Health badges.
func (a *App) syncHealth(examID int64, status string) map[string]any {
	var pending, retrying int
	var lastErr string
	a.db.QueryRow(`SELECT COUNT(*), COALESCE(SUM(attempts>0),0) FROM jobs WHERE exam_id=? AND state IN('pending','running')`, examID).Scan(&pending, &retrying)
	if pending > 0 {
		a.db.QueryRow(`SELECT last_error FROM jobs WHERE exam_id=? AND state IN('pending','running') AND last_error<>'' ORDER BY id DESC LIMIT 1`, examID).Scan(&lastErr)
	}
	h := map[string]any{"pending_jobs": pending, "last_error": lastErr}
	var approvedAt string
	a.db.QueryRow(`SELECT approved_at FROM exams WHERE id=?`, examID).Scan(&approvedAt)
	switch {
	case status == "locked" && approvedAt == "":
		h["state"], h["label"] = "yellow", "Submitted: awaiting admin approval"
	case retrying > 0 || (status == "locked" && pending > 0):
		h["state"], h["label"] = "red", "Sync Pending (Auto-Retrying)"
	case status == "draft":
		h["state"], h["label"] = "yellow", "Saved Locally (Draft)"
	default:
		h["state"], h["label"] = "green", "Locked & Synced to ERP"
	}
	return h
}

func (a *App) adminOverview(w http.ResponseWriter, r *http.Request, u *User) error {
	out := map[string]any{"frappe_configured": a.frappe.Enabled()}
	counts := map[string]int{}
	for _, q := range []struct{ k, sql string }{
		{"students", `SELECT COUNT(*) FROM students WHERE active=1`},
		{"teachers", `SELECT COUNT(*) FROM users WHERE role='teacher' AND active=1`},
		{"drafts", `SELECT COUNT(*) FROM exams WHERE status='draft'`},
		{"locked", `SELECT COUNT(*) FROM exams WHERE status='locked'`},
		{"pending_approvals", `SELECT COUNT(*) FROM exams WHERE status='locked' AND approved_at=''`},
		{"pending_corrections", `SELECT COUNT(*) FROM corrections WHERE state='pending'`},
		{"pending_jobs", `SELECT COUNT(*) FROM jobs WHERE state IN('pending','running')`},
	} {
		var n int
		a.db.QueryRow(q.sql).Scan(&n)
		counts[q.k] = n
	}
	out["counts"] = counts
	out["last_master_sync"] = a.setting("last_master_sync")
	out["last_master_result"] = a.setting("last_master_result")
	writeJSON(w, 200, out)
	return nil
}

func (a *App) setting(k string) string {
	var v string
	a.db.QueryRow(`SELECT v FROM settings WHERE k=?`, k).Scan(&v)
	return v
}

func (a *App) setSetting(k, v string) {
	a.db.Exec(`INSERT INTO settings(k,v) VALUES(?,?) ON CONFLICT(k) DO UPDATE SET v=excluded.v`, k, v)
}

func (a *App) adminExams(w http.ResponseWriter, r *http.Request, u *User) error {
	q := r.URL.Query()
	where, args := []string{"1=1"}, []any{}
	if v := q.Get("class_name"); v != "" {
		where, args = append(where, "e.class_name=?"), append(args, v)
		if sec := q.Get("section"); sec != "" {
			where, args = append(where, "e.section=?"), append(args, sec)
		}
	} else if v := q.Get("class"); v != "" {
		c, s := splitGroup(v)
		where, args = append(where, "e.class_name=? AND e.section=?"), append(args, c, s)
	}
	if v := q.Get("status"); v == "draft" || v == "locked" {
		where, args = append(where, "e.status=?"), append(args, v)
	}
	if v := q.Get("owner"); v != "" {
		where, args = append(where, "e.owner_id=?"), append(args, v)
	}
	exams, err := a.listExams(strings.Join(where, " AND ")+" ORDER BY e.id DESC LIMIT 200", args...)
	if err != nil {
		return err
	}
	out := []map[string]any{}
	for _, e := range exams {
		var graded, total int
		a.db.QueryRow(`SELECT COUNT(*) FROM results WHERE exam_id=?`, e.ID).Scan(&graded)
		a.db.QueryRow(`SELECT COUNT(*)*? FROM students WHERE class_name=? AND section=? AND active=1`, len(e.Subjects), e.ClassName, e.Section).Scan(&total)
		out = append(out, map[string]any{"exam": e, "graded": graded, "total": total, "sync": a.syncHealth(e.ID, e.Status)})
	}
	writeJSON(w, 200, map[string]any{"exams": out})
	return nil
}

func (a *App) adminUsers(w http.ResponseWriter, r *http.Request, u *User) error {
	rows, err := a.db.Query(`SELECT u.id,u.name,u.mobile,u.email,u.role,u.active,
		(SELECT COUNT(*) FROM allocations WHERE user_id=u.id) FROM users u ORDER BY u.role, u.name`)
	if err != nil {
		return err
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id int64
		var name, role string
		var mob, em sql.NullString
		var active, n int
		rows.Scan(&id, &name, &mob, &em, &role, &active, &n)
		out = append(out, map[string]any{"id": id, "name": name, "mobile": mob.String, "email": em.String, "role": role, "active": active == 1, "allocations": n})
	}
	writeJSON(w, 200, map[string]any{"users": out})
	return nil
}

type userReq struct {
	Name   string `json:"name"`
	Mobile string `json:"mobile"`
	Email  string `json:"email"`
	Role   string `json:"role"`
	Active *bool  `json:"active"`
}

func (in *userReq) clean() error {
	in.Name = strings.TrimSpace(in.Name)
	in.Mobile = normIdent(in.Mobile)
	in.Email = normIdent(in.Email)
	if in.Name == "" {
		return bad("Name is required")
	}
	if in.Mobile == "" && in.Email == "" {
		return bad("A mobile number or email is required for OTP login")
	}
	if in.Mobile != "" && len(in.Mobile) != 10 {
		return bad("Mobile number must have 10 digits")
	}
	if in.Email != "" && !strings.Contains(in.Email, "@") {
		return bad("Invalid email address")
	}
	if in.Role == "" {
		in.Role = "teacher"
	}
	if in.Role != "teacher" && in.Role != "admin" {
		return bad("Role must be teacher or admin")
	}
	return nil
}

func (a *App) adminUserCreate(w http.ResponseWriter, r *http.Request, u *User) error {
	var in userReq
	if err := decode(r, &in); err != nil {
		return err
	}
	if err := in.clean(); err != nil {
		return err
	}
	res, err := a.db.Exec(`INSERT INTO users(name,mobile,email,role,created_at) VALUES(?,?,?,?,?)`,
		in.Name, nullIfEmpty(in.Mobile), nullIfEmpty(in.Email), in.Role, nowStr())
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return conflict("A user with this mobile number or email already exists", nil)
		}
		return err
	}
	id, _ := res.LastInsertId()
	writeJSON(w, 201, map[string]any{"id": id})
	return nil
}

func (a *App) adminUserUpdate(w http.ResponseWriter, r *http.Request, u *User) error {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	var in userReq
	if err := decode(r, &in); err != nil {
		return err
	}
	if err := in.clean(); err != nil {
		return err
	}
	active := 1
	if in.Active != nil && !*in.Active {
		active = 0
	}
	if id == u.ID && (active == 0 || in.Role != "admin") {
		return bad("You cannot deactivate or demote your own account")
	}
	_, err := a.db.Exec(`UPDATE users SET name=?,mobile=?,email=?,role=?,active=? WHERE id=?`,
		in.Name, nullIfEmpty(in.Mobile), nullIfEmpty(in.Email), in.Role, active, id)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return conflict("Another user already has this mobile number or email", nil)
		}
		return err
	}
	if active == 0 {
		a.db.Exec(`DELETE FROM sessions WHERE user_id=?`, id)
	}
	writeJSON(w, 200, map[string]any{"ok": true})
	return nil
}

type allocation struct {
	ClassName string `json:"class_name"`
	Section   string `json:"section"`
	Subject   string `json:"subject"`
	Source    string `json:"source,omitempty"`
}

func (a *App) adminAllocGet(w http.ResponseWriter, r *http.Request, u *User) error {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	rows, err := a.db.Query(`SELECT class_name,section,subject,source FROM allocations WHERE user_id=? ORDER BY class_name,section,subject`, id)
	if err != nil {
		return err
	}
	defer rows.Close()
	out := []allocation{}
	for rows.Next() {
		var al allocation
		rows.Scan(&al.ClassName, &al.Section, &al.Subject, &al.Source)
		out = append(out, al)
	}
	writeJSON(w, 200, map[string]any{"allocations": out})
	return nil
}

func (a *App) adminAllocPut(w http.ResponseWriter, r *http.Request, u *User) error {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	var in struct{ Allocations []allocation }
	if err := decode(r, &in); err != nil {
		return err
	}
	tx, err := a.begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM allocations WHERE user_id=?`, id); err != nil {
		return err
	}
	for _, al := range in.Allocations {
		if al.ClassName == "" || al.Subject == "" {
			return bad("Allocation needs a class and a subject")
		}
		if _, err := tx.Exec(`INSERT OR IGNORE INTO allocations(user_id,class_name,section,subject,source) VALUES(?,?,?,?,'manual')`, id, al.ClassName, al.Section, al.Subject); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	writeJSON(w, 200, map[string]any{"ok": true, "count": len(in.Allocations)})
	return nil
}

func (a *App) adminMasterSync(w http.ResponseWriter, r *http.Request, u *User) error {
	res, err := a.masterSync()
	if err != nil {
		return &apiErr{Code: 502, Msg: "Master sync failed: " + err.Error()}
	}
	writeJSON(w, 200, res)
	return nil
}

func (a *App) adminCorrections(w http.ResponseWriter, r *http.Request, u *User) error {
	state := r.URL.Query().Get("state")
	where, args := "1=1", []any{}
	if state != "" {
		where, args = "c.state=?", []any{state}
	}
	rows, err := a.db.Query(`SELECT c.id,c.exam_id,c.subject,c.old_status,c.old_total,c.new_status,c.new_total,c.reason,c.state,c.requested_at,
		COALESCE(ru.name,''),COALESCE(du.name,''),COALESCE(c.decided_at,''),c.decision_note,s.name,s.roll_no,s.admission_no
		FROM corrections c JOIN students s ON s.id=c.student_id LEFT JOIN users ru ON ru.id=c.requested_by LEFT JOIN users du ON du.id=c.decided_by
		WHERE `+where+` ORDER BY (c.state='pending') DESC, c.id DESC LIMIT 200`, args...)
	if err != nil {
		return err
	}
	type row struct {
		ID           int64  `json:"id"`
		ExamID       int64  `json:"exam_id"`
		Subject      string `json:"subject"`
		Old          string `json:"old"`
		New          string `json:"new"`
		Reason       string `json:"reason"`
		State        string `json:"state"`
		RequestedAt  string `json:"requested_at"`
		RequestedBy  string `json:"requested_by"`
		DecidedBy    string `json:"decided_by"`
		DecidedAt    string `json:"decided_at"`
		DecisionNote string `json:"decision_note"`
		Student      string `json:"student"`
		RollNo       string `json:"roll_no"`
		AdmissionNo  string `json:"admission_no"`
		Label        string `json:"label"`
	}
	var list []row
	for rows.Next() {
		var x row
		var os, ns string
		var ot, nt sql.NullFloat64
		rows.Scan(&x.ID, &x.ExamID, &x.Subject, &os, &ot, &ns, &nt, &x.Reason, &x.State, &x.RequestedAt, &x.RequestedBy, &x.DecidedBy, &x.DecidedAt, &x.DecisionNote, &x.Student, &x.RollNo, &x.AdmissionNo)
		x.Old, x.New = displayResult(os, ot.Float64), displayResult(ns, nt.Float64)
		list = append(list, x)
	}
	rows.Close()
	for i := range list {
		if e, err := a.loadExam(list[i].ExamID); err == nil {
			list[i].Label = e.Label
		}
	}
	if list == nil {
		list = []row{}
	}
	writeJSON(w, 200, map[string]any{"corrections": list})
	return nil
}

func (a *App) adminCorrectionDecide(w http.ResponseWriter, r *http.Request, u *User) error {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	action := r.PathValue("action")
	if action != "approve" && action != "reject" {
		return notFound("Unknown action")
	}
	var in struct{ Note string }
	_ = json.NewDecoder(r.Body).Decode(&in)
	tx, err := a.begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var examID, studentID int64
	var subject, state, newStatus, newComps, oldStatus string
	var newTotal, oldTotal sql.NullFloat64
	err = tx.QueryRow(`SELECT exam_id,student_id,subject,state,new_status,new_comps,new_total,old_status,old_total FROM corrections WHERE id=?`, id).
		Scan(&examID, &studentID, &subject, &state, &newStatus, &newComps, &newTotal, &oldStatus, &oldTotal)
	if err == sql.ErrNoRows {
		return notFound("Correction request not found")
	} else if err != nil {
		return err
	}
	if state != "pending" {
		return conflict("This request was already decided", nil)
	}
	var stName, roll string
	tx.QueryRow(`SELECT name, roll_no FROM students WHERE id=?`, studentID).Scan(&stName, &roll)
	detail := map[string]any{"correction_id": id, "student": stName, "roll_no": roll, "subject": subject,
		"old": displayResult(oldStatus, oldTotal.Float64), "new": displayResult(newStatus, newTotal.Float64), "note": in.Note}
	if action == "approve" {
		// surgical: only this student's row changes
		if _, err := tx.Exec(`UPDATE results SET status=?,comps=?,total=?,updated_at=?,updated_by=? WHERE exam_id=? AND student_id=? AND subject=?`,
			newStatus, newComps, nullF(newTotal), nowStr(), u.ID, examID, studentID, subject); err != nil {
			return err
		}
		if err := enqueue(tx, "correction", examID, studentID, subject, id); err != nil {
			return err
		}
	}
	st := map[string]string{"approve": "approved", "reject": "rejected"}[action]
	if _, err := tx.Exec(`UPDATE corrections SET state=?,decided_by=?,decided_at=?,decision_note=? WHERE id=?`, st, u.ID, nowStr(), in.Note, id); err != nil {
		return err
	}
	if err := addTimeline(tx, examID, u.Name, "correction_"+st, detail); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	a.wake()
	writeJSON(w, 200, map[string]any{"ok": true, "state": st})
	return nil
}

// adminClasses summarises every class/section: roster size, tests and teacher coverage.
func (a *App) adminClasses(w http.ResponseWriter, r *http.Request, u *User) error {
	rows, err := a.db.Query(`SELECT class_name, section, COUNT(*) FROM students WHERE active=1 GROUP BY class_name, section ORDER BY class_name, section`)
	if err != nil {
		return err
	}
	type grp struct {
		ClassName string `json:"class_name"`
		Section   string `json:"section"`
		Label     string `json:"label"`
		Students  int    `json:"students"`
		Drafts    int    `json:"drafts"`
		Locked    int    `json:"locked"`
		Teachers  int    `json:"teachers"`
		Subjects  int    `json:"subjects"`
	}
	var list []grp
	for rows.Next() {
		var g grp
		rows.Scan(&g.ClassName, &g.Section, &g.Students)
		g.Label = groupLabel(g.ClassName, g.Section)
		list = append(list, g)
	}
	rows.Close()
	for i := range list {
		g := &list[i]
		a.db.QueryRow(`SELECT COALESCE(SUM(status='draft'),0), COALESCE(SUM(status='locked'),0) FROM exams WHERE class_name=? AND section=?`, g.ClassName, g.Section).Scan(&g.Drafts, &g.Locked)
		a.db.QueryRow(`SELECT COUNT(DISTINCT user_id), COUNT(DISTINCT subject) FROM allocations al JOIN users u ON u.id=al.user_id AND u.active=1 WHERE class_name=? AND section=?`, g.ClassName, g.Section).Scan(&g.Teachers, &g.Subjects)
	}
	if list == nil {
		list = []grp{}
	}
	writeJSON(w, 200, map[string]any{"classes": list})
	return nil
}

// adminAllocMatrix returns, for one class/section, each subject with the teachers assigned to it.
func (a *App) adminAllocMatrix(w http.ResponseWriter, r *http.Request, u *User) error {
	class, section := r.URL.Query().Get("class_name"), r.URL.Query().Get("section")
	if class == "" {
		return bad("class_name is required")
	}
	type teacher struct {
		ID   int64  `json:"id"`
		Name string `json:"name"`
	}
	type subj struct {
		Subject  string    `json:"subject"`
		Teachers []teacher `json:"teachers"`
	}
	rows, err := a.db.Query(`SELECT c.name, COALESCE(u.id,0), COALESCE(u.name,'') FROM courses c
		LEFT JOIN allocations al ON al.subject=c.name AND al.class_name=? AND al.section=?
		LEFT JOIN users u ON u.id=al.user_id AND u.active=1 ORDER BY c.name, u.name`, class, section)
	if err != nil {
		return err
	}
	var subjects []subj
	for rows.Next() {
		var name, tn string
		var tid int64
		rows.Scan(&name, &tid, &tn)
		if len(subjects) == 0 || subjects[len(subjects)-1].Subject != name {
			subjects = append(subjects, subj{Subject: name, Teachers: []teacher{}})
		}
		if tid != 0 {
			s := &subjects[len(subjects)-1]
			s.Teachers = append(s.Teachers, teacher{tid, tn})
		}
	}
	rows.Close()
	tr, err := a.db.Query(`SELECT id, name || CASE WHEN COALESCE(mobile,'')<>'' THEN ' (' || mobile || ')' ELSE '' END FROM users WHERE role='teacher' AND active=1 ORDER BY name`)
	if err != nil {
		return err
	}
	defer tr.Close()
	teachers := []teacher{}
	for tr.Next() {
		var t teacher
		tr.Scan(&t.ID, &t.Name)
		teachers = append(teachers, t)
	}
	if subjects == nil {
		subjects = []subj{}
	}
	roster, _ := a.rosterFor(class, section)
	writeJSON(w, 200, map[string]any{"subjects": subjects, "teachers": teachers, "students": len(roster)})
	return nil
}

// adminAllocAssign assigns or revokes one (class, section, subject) for one teacher.
func (a *App) adminAllocAssign(w http.ResponseWriter, r *http.Request, u *User) error {
	var in struct {
		ClassName string `json:"class_name"`
		Section   string `json:"section"`
		Subject   string `json:"subject"`
		UserID    int64  `json:"user_id"`
		Assigned  bool   `json:"assigned"`
	}
	if err := decode(r, &in); err != nil {
		return err
	}
	var n int
	a.db.QueryRow(`SELECT COUNT(*) FROM students WHERE class_name=? AND section=? AND active=1`, in.ClassName, in.Section).Scan(&n)
	if n == 0 {
		return bad("Unknown class / section")
	}
	a.db.QueryRow(`SELECT COUNT(*) FROM courses WHERE name=?`, in.Subject).Scan(&n)
	if n == 0 {
		return bad("Unknown subject")
	}
	a.db.QueryRow(`SELECT COUNT(*) FROM users WHERE id=? AND role='teacher' AND active=1`, in.UserID).Scan(&n)
	if n == 0 {
		return bad("Unknown teacher")
	}
	var err error
	if in.Assigned {
		_, err = a.db.Exec(`INSERT OR IGNORE INTO allocations(user_id,class_name,section,subject,source) VALUES(?,?,?,?,'manual')`, in.UserID, in.ClassName, in.Section, in.Subject)
	} else {
		_, err = a.db.Exec(`DELETE FROM allocations WHERE user_id=? AND class_name=? AND section=? AND subject=?`, in.UserID, in.ClassName, in.Section, in.Subject)
	}
	if err != nil {
		return err
	}
	writeJSON(w, 200, map[string]any{"ok": true})
	return nil
}
