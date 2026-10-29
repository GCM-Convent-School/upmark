package main

import (
	"database/sql"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"
)

type classStudent struct {
	ID           int64  `json:"id"`
	RollNo       string `json:"roll_no"`
	AdmissionNo  string `json:"admission_no"`
	Name         string `json:"name"`
	Gender       string `json:"gender"`
	DOB          string `json:"dob"`
	FatherName   string `json:"father_name"`
	FatherMobile string `json:"father_mobile"`
	MotherName   string `json:"mother_name"`
	MotherMobile string `json:"mother_mobile"`
}

// apiRoster returns the Frappe details of one class's students to a teacher assigned to it (or an admin).
func (a *App) apiRoster(w http.ResponseWriter, r *http.Request, u *User) error {
	class, section := r.URL.Query().Get("class_name"), r.URL.Query().Get("section")
	set, err := a.allowedSubjects(u, class, section)
	if err != nil {
		return err
	}
	if set != nil && len(set) == 0 {
		return forbidden("This class is not assigned to you")
	}
	rows, err := a.db.Query(`SELECT id,roll_no,admission_no,name,gender,dob,father_name,father_mobile,mother_name,mother_mobile FROM students
		WHERE class_name=? AND section=? AND active=1 ORDER BY roll_sort,name`, class, section)
	if err != nil {
		return err
	}
	defer rows.Close()
	out := []classStudent{}
	for rows.Next() {
		var s classStudent
		rows.Scan(&s.ID, &s.RollNo, &s.AdmissionNo, &s.Name, &s.Gender, &s.DOB, &s.FatherName, &s.FatherMobile, &s.MotherName, &s.MotherMobile)
		out = append(out, s)
	}
	subjects := []string{}
	for k := range set {
		subjects = append(subjects, k)
	}
	writeJSON(w, 200, map[string]any{"students": out, "subjects": subjects, "last_sync": a.setting("last_master_sync"), "frappe_configured": a.frappe.Enabled()})
	return nil
}

// ---------- send test details to parents ----------

var sending sync.Map // exam id -> true while a send is running

// parentMessage builds the WhatsApp text for one student's results in a test.
func parentMessage(e *Exam, student string, lines []string) string {
	title := e.SubType
	if e.Topic != "" {
		title += " (" + e.Topic + ")"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Dear Parent,\nHere are %s's results for %s, class %s, held on %s:\n", student, title, groupLabel(e.ClassName, e.Section), fmtDate(e.Date))
	for _, l := range lines {
		b.WriteString("• " + l + "\n")
	}
	b.WriteString("_— GCM Convent School_")
	return b.String()
}

func fmtDate(d string) string {
	if t, err := time.Parse("2006-01-02", d); err == nil {
		return t.Format("02 Jan 2006")
	}
	return d
}

// apiNotifyParents sends each student's result of an approved test to the student's parents (father/mother numbers).
func (a *App) apiNotifyParents(w http.ResponseWriter, r *http.Request, u *User) error {
	e, set, err := a.examForUser(r, u)
	if err != nil {
		return err
	}
	if e.Status != "locked" || e.ApprovedAt == "" {
		return conflict("This test has not been approved by an administrator yet.", nil)
	}
	if !u.IsAdmin() {
		for _, s := range e.Subjects {
			if !can(set, s) {
				return forbidden("Only a teacher assigned to every subject of this test, or an administrator, can message parents")
			}
		}
	}
	if a.cfg.NotifyURL == "" || a.cfg.NotifyKey == "" {
		return &apiErr{Code: 503, Msg: "The WhatsApp gateway is not configured on this server"}
	}
	if _, busy := sending.LoadOrStore(e.ID, true); busy {
		return conflict("Messages for this test are already being sent.", nil)
	}
	type target struct {
		sid    int64
		mobile string
		msg    string
	}
	rows, err := a.db.Query(`SELECT s.id,s.name,s.father_mobile,s.mother_mobile,r.subject,r.status,r.total FROM results r JOIN students s ON s.id=r.student_id
		WHERE r.exam_id=? ORDER BY s.roll_sort,s.name,r.subject`, e.ID)
	if err != nil {
		sending.Delete(e.ID)
		return err
	}
	type stu struct {
		name, fm, mm string
		lines        []string
	}
	order := []int64{}
	by := map[int64]*stu{}
	for rows.Next() {
		var id int64
		var name, fm, mm, subj, status string
		var total sql.NullFloat64
		rows.Scan(&id, &name, &fm, &mm, &subj, &status, &total)
		x := by[id]
		if x == nil {
			x = &stu{name: name, fm: normIdent(fm), mm: normIdent(mm)}
			by[id] = x
			order = append(order, id)
		}
		switch {
		case status == "AB":
			x.lines = append(x.lines, subj+": Absent")
		case status == "EX":
			x.lines = append(x.lines, subj+": Exempted")
		case total.Valid:
			x.lines = append(x.lines, fmt.Sprintf("%s: %s / %s", subj, fmtNum(total.Float64), fmtNum(e.MaxTotal)))
		}
	}
	rows.Close()
	var targets []target
	noNumber := 0
	for _, id := range order {
		x := by[id]
		if len(x.lines) == 0 {
			continue
		}
		seen := map[string]bool{}
		got := false
		for _, m := range []string{x.fm, x.mm} {
			if len(m) == 10 && !seen[m] {
				seen[m] = true
				got = true
				targets = append(targets, target{id, m, parentMessage(e, x.name, x.lines)})
			}
		}
		if !got {
			noNumber++
		}
	}
	// already-delivered messages are skipped, so a second click only retries failures and new numbers
	var todo []target
	for _, t := range targets {
		var st string
		a.db.QueryRow(`SELECT status FROM parent_msgs WHERE exam_id=? AND student_id=? AND mobile=?`, e.ID, t.sid, t.mobile).Scan(&st)
		if st != "sent" {
			todo = append(todo, t)
		}
	}
	for _, t := range todo {
		a.db.Exec(`INSERT INTO parent_msgs(exam_id,student_id,mobile,status) VALUES(?,?,?,'pending') ON CONFLICT(exam_id,student_id,mobile) DO UPDATE SET status='pending',error=''`, e.ID, t.sid, t.mobile)
	}
	go func() {
		defer sending.Delete(e.ID)
		for _, t := range todo {
			err := a.notifySend(t.mobile, t.msg)
			st, msg := "sent", ""
			if err != nil {
				st, msg = "failed", err.Error()
				log.Printf("parent message (exam %d): %v", e.ID, err)
			}
			a.db.Exec(`UPDATE parent_msgs SET status=?,error=?,sent_at=? WHERE exam_id=? AND student_id=? AND mobile=?`, st, msg, nowStr(), e.ID, t.sid, t.mobile)
			time.Sleep(400 * time.Millisecond)
		}
		a.db.Exec(`UPDATE exams SET parents_sent_at=? WHERE id=?`, nowStr(), e.ID)
	}()
	writeJSON(w, 200, map[string]any{"queued": len(todo), "already_sent": len(targets) - len(todo), "no_number": noNumber})
	return nil
}

func (a *App) apiParentsStatus(w http.ResponseWriter, r *http.Request, u *User) error {
	e, _, err := a.examForUser(r, u)
	if err != nil {
		return err
	}
	var sent, failed, pending int
	a.db.QueryRow(`SELECT COALESCE(SUM(status='sent'),0), COALESCE(SUM(status='failed'),0), COALESCE(SUM(status='pending'),0) FROM parent_msgs WHERE exam_id=?`, e.ID).Scan(&sent, &failed, &pending)
	_, running := sending.Load(e.ID)
	writeJSON(w, 200, map[string]any{"sent": sent, "failed": failed, "pending": pending, "running": running, "sent_at": e.ParentsSent})
	return nil
}
