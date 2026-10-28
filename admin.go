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
	mux.HandleFunc("GET /api/admin/users", a.wrap("admin", a.adminUsers))
	mux.HandleFunc("POST /api/admin/users", a.wrap("admin", a.adminUserCreate))
	mux.HandleFunc("PUT /api/admin/users/{id}", a.wrap("admin", a.adminUserUpdate))
	mux.HandleFunc("GET /api/admin/users/{id}/allocations", a.wrap("admin", a.adminAllocGet))
	mux.HandleFunc("PUT /api/admin/users/{id}/allocations", a.wrap("admin", a.adminAllocPut))
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
	switch {
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
	if v := q.Get("class"); v != "" {
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
