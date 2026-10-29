package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

const combinedKey = "*COMBINED*"

var (
	formativeTypes = []string{"FA1", "FA2", "FA3", "FA4", "Weekly Test", "Surprise Test"}
	summativeTypes = []string{"SA1", "SA2"}
	dateRe         = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)
)

type Component struct {
	Name string  `json:"name"`
	Max  float64 `json:"max"`
}

type Exam struct {
	ID           int64       `json:"id"`
	ClassName    string      `json:"class_name"`
	Section      string      `json:"section"`
	StudentGroup string      `json:"student_group"`
	Category     string      `json:"category"`
	SubType      string      `json:"sub_type"`
	Date         string      `json:"date"`
	Topic        string      `json:"topic"`
	Combined     bool        `json:"combined"`
	Split        bool        `json:"split"`
	MaxTotal     float64     `json:"max_total"`
	Status       string      `json:"status"`
	OwnerID      int64       `json:"owner_id"`
	OwnerName    string      `json:"owner_name"`
	CreatedAt    string      `json:"created_at"`
	LockedAt     string      `json:"locked_at"`
	ApprovedAt   string      `json:"approved_at"`
	ParentsSent  string      `json:"parents_sent_at"`
	Subjects     []string    `json:"subjects"`
	Components   []Component `json:"components"`
	Label        string      `json:"label"`
}

type Entry struct {
	StudentID int64      `json:"student_id"`
	Subject   string     `json:"subject"`
	Status    string     `json:"status"`
	Comps     []*float64 `json:"comps"`
	Total     *float64   `json:"total"`
}

type Student struct {
	ID          int64  `json:"id"`
	AdmissionNo string `json:"admission_no"`
	RollNo      string `json:"roll_no"`
	RollSort    int    `json:"roll_sort"`
	Name        string `json:"name"`
	FrappeID    string `json:"-"`
}

func (a *App) wake() {
	select {
	case a.kick <- struct{}{}:
	default:
	}
}

func (a *App) routes(mux *http.ServeMux) {
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		if err := a.db.Ping(); err != nil {
			http.Error(w, "db", 500)
			return
		}
		w.Write([]byte("ok"))
	})
	mux.HandleFunc("POST /api/auth/request", a.otpRequest)
	mux.HandleFunc("POST /api/auth/verify", a.otpVerify)
	mux.HandleFunc("POST /api/auth/logout", a.wrap("", a.logout))
	mux.HandleFunc("GET /api/me", a.wrap("", a.apiMe))
	mux.HandleFunc("GET /api/scope", a.wrap("", a.apiScope))
	mux.HandleFunc("POST /api/exams/resolve", a.wrap("", func(w http.ResponseWriter, r *http.Request, u *User) error { return a.apiExamOpen(w, r, u, false) }))
	mux.HandleFunc("POST /api/exams", a.wrap("", func(w http.ResponseWriter, r *http.Request, u *User) error { return a.apiExamOpen(w, r, u, true) }))
	mux.HandleFunc("GET /api/exams", a.wrap("", a.apiExamList))
	mux.HandleFunc("GET /api/exams/{id}", a.wrap("", a.apiExamGet))
	mux.HandleFunc("PUT /api/exams/{id}/marks", a.wrap("", a.apiSaveMarks))
	mux.HandleFunc("POST /api/exams/{id}/finalize", a.wrap("", a.apiFinalize))
	mux.HandleFunc("DELETE /api/exams/{id}", a.wrap("", a.apiDiscard))
	mux.HandleFunc("GET /api/reports/students", a.wrap("", a.apiStudentReports))
	mux.HandleFunc("GET /api/roster", a.wrap("", a.apiRoster))
	mux.HandleFunc("POST /api/exams/{id}/notify-parents", a.wrap("", a.apiNotifyParents))
	mux.HandleFunc("GET /api/exams/{id}/parents-status", a.wrap("", a.apiParentsStatus))
	mux.HandleFunc("GET /api/exams/{id}/timeline", a.wrap("", a.apiTimeline))
	mux.HandleFunc("POST /api/exams/{id}/corrections", a.wrap("", a.apiCorrectionRequest))
	a.adminRoutes(mux)
}

// ---------- scope ----------

// allowedSubjects returns nil for admins (everything allowed) or the set of subjects for a teacher.
func (a *App) allowedSubjects(u *User, class, section string) (map[string]bool, error) {
	if u.IsAdmin() {
		return nil, nil
	}
	rows, err := a.db.Query(`SELECT subject FROM allocations WHERE user_id=? AND class_name=? AND section=?`, u.ID, class, section)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	m := map[string]bool{}
	for rows.Next() {
		var s string
		rows.Scan(&s)
		m[s] = true
	}
	return m, rows.Err()
}

func can(set map[string]bool, subj string) bool { return set == nil || set[subj] }

func (a *App) apiScope(w http.ResponseWriter, r *http.Request, u *User) error {
	type group struct {
		Class    string   `json:"class_name"`
		Section  string   `json:"section"`
		Label    string   `json:"label"`
		Subjects []string `json:"subjects"`
		Students int      `json:"students"`
	}
	var groups []group
	if u.IsAdmin() {
		var courses []string
		rows, err := a.db.Query(`SELECT name FROM courses ORDER BY name`)
		if err != nil {
			return err
		}
		for rows.Next() {
			var c string
			rows.Scan(&c)
			courses = append(courses, c)
		}
		rows.Close()
		gr, err := a.db.Query(`SELECT class_name, section, COUNT(*) FROM students WHERE active=1 GROUP BY class_name, section ORDER BY class_name, section`)
		if err != nil {
			return err
		}
		defer gr.Close()
		for gr.Next() {
			g := group{Subjects: courses}
			gr.Scan(&g.Class, &g.Section, &g.Students)
			g.Label = groupLabel(g.Class, g.Section)
			groups = append(groups, g)
		}
	} else {
		rows, err := a.db.Query(`SELECT a.class_name, a.section, a.subject,
			(SELECT COUNT(*) FROM students s WHERE s.class_name=a.class_name AND s.section=a.section AND s.active=1)
			FROM allocations a WHERE a.user_id=? ORDER BY a.class_name, a.section, a.subject`, u.ID)
		if err != nil {
			return err
		}
		defer rows.Close()
		idx := map[string]int{}
		for rows.Next() {
			var c, s, subj string
			var n int
			rows.Scan(&c, &s, &subj, &n)
			k := c + "\x00" + s
			i, ok := idx[k]
			if !ok {
				groups = append(groups, group{Class: c, Section: s, Label: groupLabel(c, s), Students: n})
				i = len(groups) - 1
				idx[k] = i
			}
			groups[i].Subjects = append(groups[i].Subjects, subj)
		}
	}
	if groups == nil {
		groups = []group{}
	}
	writeJSON(w, 200, map[string]any{"groups": groups, "formative": formativeTypes, "summative": summativeTypes})
	return nil
}

// scopedWhere restricts exam queries to what the user may see (everything for admins).
func scopedWhere(u *User) (string, []any) {
	if u.IsAdmin() {
		return "1=1", nil
	}
	return `EXISTS(SELECT 1 FROM exam_subjects s JOIN allocations al ON al.user_id=? AND al.class_name=e.class_name
		AND al.section=e.section AND al.subject=s.subject WHERE s.exam_id=e.id)`, []any{u.ID}
}

func (a *App) apiMe(w http.ResponseWriter, r *http.Request, u *User) error {
	exams, err := a.listExams(`e.owner_id=? AND e.status='draft'`, u.ID)
	if err != nil {
		return err
	}
	where, args := scopedWhere(u)
	recent, err := a.listExams(where+" ORDER BY e.id DESC LIMIT 8", args...)
	if err != nil {
		return err
	}
	var classes, subjects, locked, pendingCorr int
	a.db.QueryRow(`SELECT COUNT(DISTINCT class_name||'|'||section), COUNT(*) FROM allocations WHERE user_id=?`, u.ID).Scan(&classes, &subjects)
	a.db.QueryRow(`SELECT COUNT(*) FROM exams e WHERE e.status='locked' AND `+where, args...).Scan(&locked)
	a.db.QueryRow(`SELECT COUNT(*) FROM corrections WHERE requested_by=? AND state='pending'`, u.ID).Scan(&pendingCorr)
	stats := map[string]int{"classes": classes, "subjects": subjects, "drafts": len(exams), "locked": locked, "pending_corrections": pendingCorr}
	writeJSON(w, 200, map[string]any{"user": u, "drafts": exams, "draft_limit": 2, "recent": recent, "stats": stats})
	return nil
}

// apiExamList lists every test the user may see, optionally filtered by status and a text query.
func (a *App) apiExamList(w http.ResponseWriter, r *http.Request, u *User) error {
	where, args := scopedWhere(u)
	if st := r.URL.Query().Get("status"); st == "draft" || st == "locked" {
		where, args = where+" AND e.status=?", append(args, st)
	}
	exams, err := a.listExams(where+" ORDER BY e.id DESC LIMIT 300", args...)
	if err != nil {
		return err
	}
	q := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q")))
	out := []map[string]any{}
	for _, e := range exams {
		if q != "" && !strings.Contains(strings.ToLower(e.Label+" "+e.OwnerName+" "+e.Topic), q) {
			continue
		}
		var graded, total int
		a.db.QueryRow(`SELECT COUNT(*) FROM results WHERE exam_id=?`, e.ID).Scan(&graded)
		a.db.QueryRow(`SELECT COUNT(*)*? FROM students WHERE class_name=? AND section=? AND active=1`, len(e.Subjects), e.ClassName, e.Section).Scan(&total)
		out = append(out, map[string]any{"exam": e, "graded": graded, "total": total, "sync": a.syncHealth(e.ID, e.Status)})
	}
	writeJSON(w, 200, map[string]any{"exams": out})
	return nil
}

// ---------- exam loading ----------

func (a *App) listExams(where string, args ...any) ([]Exam, error) {
	rows, err := a.db.Query(`SELECT e.id FROM exams e WHERE `+where, args...)
	if err != nil {
		return nil, err
	}
	var ids []int64
	for rows.Next() {
		var id int64
		rows.Scan(&id)
		ids = append(ids, id)
	}
	rows.Close()
	out := []Exam{}
	for _, id := range ids {
		e, err := a.loadExam(id)
		if err != nil {
			return nil, err
		}
		out = append(out, *e)
	}
	return out, nil
}

func (a *App) loadExam(id int64) (*Exam, error) {
	e := &Exam{}
	var combined, split int
	var locked sql.NullString
	err := a.db.QueryRow(`SELECT e.id,e.class_name,e.section,e.student_group,e.category,e.sub_type,e.exam_date,e.topic,e.combined,e.split,
		e.max_total,e.status,e.owner_id,COALESCE(u.name,''),e.created_at,e.locked_at,e.approved_at,e.parents_sent_at FROM exams e LEFT JOIN users u ON u.id=e.owner_id WHERE e.id=?`, id).
		Scan(&e.ID, &e.ClassName, &e.Section, &e.StudentGroup, &e.Category, &e.SubType, &e.Date, &e.Topic, &combined, &split,
			&e.MaxTotal, &e.Status, &e.OwnerID, &e.OwnerName, &e.CreatedAt, &locked, &e.ApprovedAt, &e.ParentsSent)
	if err != nil {
		return nil, err
	}
	e.Combined, e.Split, e.LockedAt = combined == 1, split == 1, locked.String
	e.Subjects = []string{}
	rows, err := a.db.Query(`SELECT subject FROM exam_subjects WHERE exam_id=? ORDER BY subject`, id)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var s string
		rows.Scan(&s)
		e.Subjects = append(e.Subjects, s)
	}
	rows.Close()
	e.Components = []Component{}
	rows, err = a.db.Query(`SELECT name,max_marks FROM exam_components WHERE exam_id=? ORDER BY idx`, id)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var c Component
		rows.Scan(&c.Name, &c.Max)
		e.Components = append(e.Components, c)
	}
	rows.Close()
	subj := strings.Join(e.Subjects, " + ")
	if e.Combined {
		subj = "Combined: " + subj
	}
	e.Label = fmt.Sprintf("%s • %s • %s • %s", groupLabel(e.ClassName, e.Section), e.SubType, subj, shortDate(e.Date))
	return e, nil
}

func shortDate(d string) string {
	t, err := time.Parse("2006-01-02", d)
	if err != nil {
		return d
	}
	return t.Format("2 Jan")
}

func (a *App) rosterFor(class, section string) ([]Student, error) {
	rows, err := a.db.Query(`SELECT id,admission_no,roll_no,roll_sort,name,frappe_id FROM students WHERE class_name=? AND section=? AND active=1
		ORDER BY roll_sort, name`, class, section)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Student{}
	for rows.Next() {
		var s Student
		rows.Scan(&s.ID, &s.AdmissionNo, &s.RollNo, &s.RollSort, &s.Name, &s.FrappeID)
		out = append(out, s)
	}
	return out, rows.Err()
}

func (a *App) entriesFor(examID int64) ([]Entry, error) {
	rows, err := a.db.Query(`SELECT student_id,subject,status,comps,total FROM results WHERE exam_id=?`, examID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Entry{}
	for rows.Next() {
		var e Entry
		var comps string
		var total sql.NullFloat64
		rows.Scan(&e.StudentID, &e.Subject, &e.Status, &comps, &total)
		json.Unmarshal([]byte(comps), &e.Comps)
		if total.Valid {
			e.Total = &total.Float64
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// ---------- validation ----------

// validateEntry enforces the sanity gate (0 <= score <= max, 0.5 increments) and computes the total.
func validateEntry(comps []Component, status string, in []*float64) (out []*float64, total *float64, complete bool, err error) {
	out = make([]*float64, len(comps))
	if status != "" && status != "AB" && status != "EX" {
		return nil, nil, false, fmt.Errorf("status must be AB or EX")
	}
	if status != "" {
		z := 0.0
		return out, &z, true, nil
	}
	if len(in) != len(comps) {
		return nil, nil, false, fmt.Errorf("expected %d score value(s)", len(comps))
	}
	sum, all := 0.0, true
	for i, v := range in {
		if v == nil {
			all = false
			continue
		}
		f := *v
		switch {
		case math.IsNaN(f) || math.IsInf(f, 0):
			return nil, nil, false, fmt.Errorf("%s: invalid number", comps[i].Name)
		case f < 0:
			return nil, nil, false, fmt.Errorf("%s: score cannot be below 0", comps[i].Name)
		case f > comps[i].Max:
			return nil, nil, false, fmt.Errorf("%s: score %s exceeds maximum %s", comps[i].Name, fmtNum(f), fmtNum(comps[i].Max))
		case !isHalf(f):
			return nil, nil, false, fmt.Errorf("%s: marks must be in steps of 0.5", comps[i].Name)
		}
		c := f
		out[i] = &c
		sum += f
	}
	if all {
		total = &sum
	}
	return out, total, all, nil
}

func fmtNum(f float64) string { return strconv.FormatFloat(f, 'f', -1, 64) }

func validateExamConfig(in *examReq) ([]Component, error) {
	if in.Split {
		if len(in.Components) < 2 || len(in.Components) > 8 {
			return nil, bad("Split mode needs between 2 and 8 components")
		}
		seen := map[string]bool{}
		sum := 0.0
		out := make([]Component, 0, len(in.Components))
		for _, c := range in.Components {
			c.Name = strings.TrimSpace(c.Name)
			if c.Name == "" || len(c.Name) > 60 {
				return nil, bad("Every component needs a name (max 60 characters)")
			}
			if seen[strings.ToLower(c.Name)] {
				return nil, bad("Component names must be unique: " + c.Name)
			}
			seen[strings.ToLower(c.Name)] = true
			if c.Max <= 0 || c.Max > 1000 || !isHalf(c.Max) {
				return nil, bad("Component maximum must be positive, in steps of 0.5: " + c.Name)
			}
			sum += c.Max
			out = append(out, c)
		}
		return out, nil
	}
	if in.MaxTotal <= 0 || in.MaxTotal > 1000 || !isHalf(in.MaxTotal) {
		return nil, bad("Max marks must be positive, in steps of 0.5")
	}
	return []Component{{Name: "Total Marks", Max: in.MaxTotal}}, nil
}

// ---------- resolve / create ----------

type examReq struct {
	ClassName  string      `json:"class_name"`
	Section    string      `json:"section"`
	Category   string      `json:"category"`
	SubType    string      `json:"sub_type"`
	Date       string      `json:"date"`
	Topic      string      `json:"topic"`
	Subjects   []string    `json:"subjects"`
	Combined   bool        `json:"combined"`
	Split      bool        `json:"split"`
	MaxTotal   float64     `json:"max_total"`
	Components []Component `json:"components"`
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func (a *App) apiExamOpen(w http.ResponseWriter, r *http.Request, u *User, create bool) error {
	var in examReq
	if err := decode(r, &in); err != nil {
		return err
	}
	switch in.Category {
	case "Formative":
		if !contains(formativeTypes, in.SubType) {
			return bad("Unknown formative test type")
		}
	case "Summative":
		if !contains(summativeTypes, in.SubType) {
			return bad("Unknown summative test type")
		}
	default:
		return bad("Category must be Formative or Summative")
	}
	if !dateRe.MatchString(in.Date) {
		return bad("Date must be YYYY-MM-DD")
	}
	d, err := time.Parse("2006-01-02", in.Date)
	if err != nil {
		return bad("Invalid date")
	}
	if d.After(time.Now().Add(24 * time.Hour)) {
		return bad("Tests cannot be dated in the future")
	}
	in.Topic = strings.TrimSpace(in.Topic)
	if in.SubType != "Weekly Test" && in.SubType != "Surprise Test" {
		in.Topic = ""
	}
	if len(in.Topic) > 120 {
		return bad("Topic is too long")
	}
	sort.Strings(in.Subjects)
	for i := 1; i < len(in.Subjects); i++ {
		if in.Subjects[i] == in.Subjects[i-1] {
			return bad("Duplicate subject")
		}
	}
	if in.Combined && len(in.Subjects) < 2 {
		return bad("A combined test needs at least two subjects")
	}
	if !in.Combined && len(in.Subjects) != 1 {
		return bad("Pick exactly one subject")
	}
	set, err := a.allowedSubjects(u, in.ClassName, in.Section)
	if err != nil {
		return err
	}
	for _, s := range in.Subjects {
		if !can(set, s) {
			return forbidden("You are not assigned to " + s + " for " + groupLabel(in.ClassName, in.Section))
		}
	}
	var group string
	if err := a.db.QueryRow(`SELECT student_group FROM students WHERE class_name=? AND section=? AND active=1 LIMIT 1`, in.ClassName, in.Section).Scan(&group); err != nil {
		return bad("No students found for this class; ask the administrator to sync the roster")
	}

	// existing test on the same (class, category, date, subject)?
	if id, conflictMsg, err := a.findExisting(&in); err != nil {
		return err
	} else if conflictMsg != "" {
		return conflict(conflictMsg, nil)
	} else if id != 0 {
		e, _ := a.loadExam(id)
		writeJSON(w, 200, map[string]any{"state": "existing", "exam_id": id, "status": e.Status, "label": e.Label})
		return nil
	}
	if !create {
		writeJSON(w, 200, map[string]any{"state": "new"})
		return nil
	}

	comps, err := validateExamConfig(&in)
	if err != nil {
		return err
	}
	total := 0.0
	for _, c := range comps {
		total += c.Max
	}
	key := in.Subjects[0]
	if in.Combined {
		key = combinedKey
	}
	tx, err := a.begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var drafts int
	tx.QueryRow(`SELECT COUNT(*) FROM exams WHERE owner_id=? AND status='draft'`, u.ID).Scan(&drafts)
	if drafts >= 2 {
		return conflict("You already have 2 active drafts. Finalize & Lock or discard one before starting a new test.", map[string]any{"code": "draft_limit"})
	}
	res, err := tx.Exec(`INSERT INTO exams(class_name,section,student_group,category,sub_type,exam_date,subject_key,topic,combined,split,max_total,status,owner_id,created_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,'draft',?,?)`, in.ClassName, in.Section, group, in.Category, in.SubType, in.Date, key, in.Topic,
		b2i(in.Combined), b2i(in.Split), total, u.ID, nowStr())
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return conflict("A test already exists for this class, category, date and subject; open it instead.", nil)
		}
		return err
	}
	id, _ := res.LastInsertId()
	for _, s := range in.Subjects {
		if _, err := tx.Exec(`INSERT INTO exam_subjects(exam_id,subject) VALUES(?,?)`, id, s); err != nil {
			return err
		}
	}
	for i, c := range comps {
		if _, err := tx.Exec(`INSERT INTO exam_components(exam_id,idx,name,max_marks) VALUES(?,?,?,?)`, id, i, c.Name, c.Max); err != nil {
			return err
		}
	}
	if err := addTimeline(tx, id, u.Name, "created", map[string]any{"subjects": in.Subjects, "max_total": total, "split": in.Split, "components": comps}); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	writeJSON(w, 201, map[string]any{"state": "created", "exam_id": id})
	return nil
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

// findExisting implements the (Class/Section + Category + Date + Subject) uniqueness rule.
func (a *App) findExisting(in *examReq) (int64, string, error) {
	ph := strings.TrimSuffix(strings.Repeat("?,", len(in.Subjects)), ",")
	args := []any{in.ClassName, in.Section, in.Category, in.Date}
	for _, s := range in.Subjects {
		args = append(args, s)
	}
	rows, err := a.db.Query(`SELECT DISTINCT e.id, e.subject_key, s.subject FROM exams e JOIN exam_subjects s ON s.exam_id=e.id
		WHERE e.class_name=? AND e.section=? AND e.category=? AND e.exam_date=? AND s.subject IN (`+ph+`) ORDER BY e.id`, args...)
	if err != nil {
		return 0, "", err
	}
	defer rows.Close()
	var firstID int64
	var overlap []string
	for rows.Next() {
		var id int64
		var key, subj string
		rows.Scan(&id, &key, &subj)
		if firstID == 0 {
			firstID = id
		}
		overlap = append(overlap, subj)
		if !in.Combined {
			return id, "", nil // same subject already has a test that day: upsert
		}
		if key == combinedKey {
			return id, "", nil
		}
	}
	if in.Combined && len(overlap) > 0 {
		return 0, "These subjects already have separate tests that day: " + strings.Join(overlap, ", ") + ". Open those tests instead.", nil
	}
	// a combined container may exist for the day even when the subjects selected differ
	if in.Combined {
		var id int64
		if err := a.db.QueryRow(`SELECT id FROM exams WHERE class_name=? AND section=? AND category=? AND exam_date=? AND subject_key=?`,
			in.ClassName, in.Section, in.Category, in.Date, combinedKey).Scan(&id); err == nil {
			return id, "", nil
		}
	}
	return 0, "", nil
}

type execer interface {
	Exec(query string, args ...any) (sql.Result, error)
}

func addTimeline(tx execer, examID int64, user, kind string, detail any) error {
	b, _ := json.Marshal(detail)
	_, err := tx.Exec(`INSERT INTO timeline(exam_id,ts,user_name,kind,detail) VALUES(?,?,?,?,?)`, examID, nowStr(), user, kind, string(b))
	return err
}

// enqueue adds an outbox job. Draft saves are debounced (coalesced into one push ~8s later) so rapid
// saves never hammer the ERP; locking and corrections go out immediately.
func enqueue(tx execer, kind string, examID, studentID int64, subject string, corrID int64, urgent ...bool) error {
	if kind == "exam" {
		due := time.Now().Add(8 * time.Second).UTC().Format(time.RFC3339)
		if len(urgent) > 0 && urgent[0] {
			due = nowStr()
			if _, err := tx.Exec(`UPDATE jobs SET next_try=? WHERE kind='exam' AND exam_id=? AND state='pending' AND attempts=0`, due, examID); err != nil {
				return err
			}
		}
		_, err := tx.Exec(`INSERT INTO jobs(kind,exam_id,next_try,created_at)
			SELECT 'exam',?,?,? WHERE NOT EXISTS(SELECT 1 FROM jobs WHERE kind='exam' AND exam_id=? AND state='pending')`, examID, due, nowStr(), examID)
		return err
	}
	_, err := tx.Exec(`INSERT INTO jobs(kind,exam_id,student_id,subject,correction_id,next_try,created_at) VALUES(?,?,?,?,?,?,?)`,
		kind, examID, studentID, subject, corrID, nowStr(), nowStr())
	return err
}

// ---------- fetch / save / finalize / discard ----------

func (a *App) examForUser(r *http.Request, u *User) (*Exam, map[string]bool, error) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		return nil, nil, notFound("Test not found")
	}
	e, err := a.loadExam(id)
	if err == sql.ErrNoRows {
		return nil, nil, notFound("Test not found")
	} else if err != nil {
		return nil, nil, err
	}
	set, err := a.allowedSubjects(u, e.ClassName, e.Section)
	if err != nil {
		return nil, nil, err
	}
	ok := set == nil
	for _, s := range e.Subjects {
		if can(set, s) {
			ok = true
		}
	}
	if !ok {
		return nil, nil, forbidden("This test is outside your assigned classes and subjects")
	}
	return e, set, nil
}

func (a *App) apiExamGet(w http.ResponseWriter, r *http.Request, u *User) error {
	e, set, err := a.examForUser(r, u)
	if err != nil {
		return err
	}
	roster, err := a.rosterFor(e.ClassName, e.Section)
	if err != nil {
		return err
	}
	entries, err := a.entriesFor(e.ID)
	if err != nil {
		return err
	}
	var visible []string
	for _, s := range e.Subjects {
		if can(set, s) {
			visible = append(visible, s)
		}
	}
	type pend struct {
		StudentID int64  `json:"student_id"`
		Subject   string `json:"subject"`
	}
	pending := []pend{}
	rows, err := a.db.Query(`SELECT student_id,subject FROM corrections WHERE exam_id=? AND state='pending'`, e.ID)
	if err != nil {
		return err
	}
	for rows.Next() {
		var p pend
		rows.Scan(&p.StudentID, &p.Subject)
		pending = append(pending, p)
	}
	rows.Close()
	writeJSON(w, 200, map[string]any{"exam": e, "visible_subjects": visible, "roster": roster, "entries": entries,
		"pending_corrections": pending, "sync": a.syncHealth(e.ID, e.Status)})
	return nil
}

func (a *App) apiSaveMarks(w http.ResponseWriter, r *http.Request, u *User) error {
	e, set, err := a.examForUser(r, u)
	if err != nil {
		return err
	}
	if e.Status != "draft" {
		return conflict("This test is locked. Only an administrator can edit it.", nil)
	}
	var in struct{ Entries []Entry }
	if err := decode(r, &in); err != nil {
		return err
	}
	roster, err := a.rosterFor(e.ClassName, e.Section)
	if err != nil {
		return err
	}
	inRoster := map[int64]bool{}
	for _, s := range roster {
		inRoster[s.ID] = true
	}
	type ferr struct {
		StudentID int64  `json:"student_id"`
		Subject   string `json:"subject"`
		Message   string `json:"message"`
	}
	var errs []ferr
	type prepared struct {
		Entry
		comps []*float64
		total *float64
		clear bool
	}
	var prep []prepared
	for _, en := range in.Entries {
		if !inRoster[en.StudentID] || !contains(e.Subjects, en.Subject) || !can(set, en.Subject) {
			errs = append(errs, ferr{en.StudentID, en.Subject, "Student or subject is outside this test"})
			continue
		}
		comps, total, _, verr := validateEntry(e.Components, en.Status, en.Comps)
		if verr != nil {
			errs = append(errs, ferr{en.StudentID, en.Subject, verr.Error()})
			continue
		}
		clear := en.Status == ""
		for _, c := range comps {
			if c != nil {
				clear = false
			}
		}
		prep = append(prep, prepared{Entry: en, comps: comps, total: total, clear: clear})
	}
	if len(errs) > 0 {
		return &apiErr{Code: 422, Msg: "Some marks were rejected: " + errs[0].Message, Extra: map[string]any{"errors": errs}}
	}
	tx, err := a.begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var st string
	if err := tx.QueryRow(`SELECT status FROM exams WHERE id=?`, e.ID).Scan(&st); err != nil || st != "draft" {
		return conflict("This test is locked.", nil)
	}
	for _, p := range prep {
		if p.clear {
			if _, err := tx.Exec(`DELETE FROM results WHERE exam_id=? AND student_id=? AND subject=?`, e.ID, p.StudentID, p.Subject); err != nil {
				return err
			}
			continue
		}
		cj, _ := json.Marshal(p.comps)
		var total any
		if p.total != nil {
			total = *p.total
		}
		if _, err := tx.Exec(`INSERT INTO results(exam_id,student_id,subject,status,comps,total,updated_at,updated_by) VALUES(?,?,?,?,?,?,?,?)
			ON CONFLICT(exam_id,student_id,subject) DO UPDATE SET status=excluded.status, comps=excluded.comps, total=excluded.total,
			updated_at=excluded.updated_at, updated_by=excluded.updated_by`, e.ID, p.StudentID, p.Subject, p.Status, string(cj), total, nowStr(), u.ID); err != nil {
			return err
		}
	}
	var prior int
	tx.QueryRow(`SELECT COUNT(*) FROM timeline WHERE exam_id=? AND kind IN('draft_saved','draft_updated')`, e.ID).Scan(&prior)
	kind := "draft_updated"
	if prior == 0 {
		kind = "draft_saved"
	}
	var graded int
	tx.QueryRow(`SELECT COUNT(*) FROM results WHERE exam_id=?`, e.ID).Scan(&graded)
	if err := addTimeline(tx, e.ID, u.Name, kind, map[string]any{"entries_saved": len(prep), "entries_total": graded}); err != nil {
		return err
	}
	if err := enqueue(tx, "exam", e.ID, 0, "", 0); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	a.wake()
	writeJSON(w, 200, map[string]any{"ok": true, "saved": len(prep)})
	return nil
}

func (a *App) apiFinalize(w http.ResponseWriter, r *http.Request, u *User) error {
	e, set, err := a.examForUser(r, u)
	if err != nil {
		return err
	}
	if e.Status != "draft" {
		return conflict("This test is already locked.", nil)
	}
	for _, s := range e.Subjects {
		if !can(set, s) && !u.IsAdmin() {
			// a teacher with only part of a combined test may not lock it; the other subjects belong to colleagues
			return forbidden("This combined test includes subjects assigned to other teachers; they must complete those subjects and any assigned teacher of all subjects (or an administrator) must finalize.")
		}
	}
	roster, err := a.rosterFor(e.ClassName, e.Section)
	if err != nil {
		return err
	}
	entries, err := a.entriesFor(e.ID)
	if err != nil {
		return err
	}
	done := map[string]Entry{}
	for _, en := range entries {
		done[fmt.Sprintf("%d|%s", en.StudentID, en.Subject)] = en
	}
	type miss struct {
		StudentID int64  `json:"student_id"`
		Subject   string `json:"subject"`
	}
	var missing []miss
	var graded, ab, ex int
	var sum, maxScore float64
	minScore := math.MaxFloat64
	for _, s := range roster {
		for _, subj := range e.Subjects {
			en, ok := done[fmt.Sprintf("%d|%s", s.ID, subj)]
			complete := ok && (en.Status != "" || en.Total != nil)
			if !complete {
				missing = append(missing, miss{s.ID, subj})
				continue
			}
			switch en.Status {
			case "AB":
				ab++
			case "EX":
				ex++
			default:
				graded++
				sum += *en.Total
				maxScore = math.Max(maxScore, *en.Total)
				minScore = math.Min(minScore, *en.Total)
			}
		}
	}
	if len(missing) > 0 {
		return conflict(fmt.Sprintf("%d entr%s still ungraded. Mark each student with a score, AB or EX before locking.", len(missing), map[bool]string{true: "y is", false: "ies are"}[len(missing) == 1]),
			map[string]any{"missing": missing})
	}
	stats := map[string]any{"students": len(roster), "graded": graded, "absent": ab, "exempt": ex, "max_total": e.MaxTotal}
	if graded > 0 {
		stats["average"] = math.Round(sum/float64(graded)*100) / 100
		stats["highest"], stats["lowest"] = maxScore, minScore
	}
	tx, err := a.begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.Exec(`UPDATE exams SET status='locked', locked_at=?, locked_by=? WHERE id=? AND status='draft'`, nowStr(), u.ID, e.ID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return conflict("This test is already locked.", nil)
	}
	if err := addTimeline(tx, e.ID, u.Name, "locked", stats); err != nil {
		return err
	}
	// The marksheet is pushed to Frappe only after an administrator approves it.
	if err := tx.Commit(); err != nil {
		return err
	}
	writeJSON(w, 200, map[string]any{"ok": true, "stats": stats})
	return nil
}

func (a *App) apiDiscard(w http.ResponseWriter, r *http.Request, u *User) error {
	e, _, err := a.examForUser(r, u)
	if err != nil {
		return err
	}
	if e.Status != "draft" {
		return conflict("Finalized tests cannot be deleted.", nil)
	}
	if e.OwnerID != u.ID && !u.IsAdmin() {
		return forbidden("Only the teacher who started the draft can discard it")
	}
	tx, err := a.begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := addTimeline(tx, e.ID, u.Name, "discarded", map[string]any{"label": e.Label}); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM jobs WHERE exam_id=? AND state='pending'`, e.ID); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM exams WHERE id=? AND status='draft'`, e.ID); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	writeJSON(w, 200, map[string]any{"ok": true})
	return nil
}

func (a *App) apiTimeline(w http.ResponseWriter, r *http.Request, u *User) error {
	e, _, err := a.examForUser(r, u)
	if err != nil {
		return err
	}
	rows, err := a.db.Query(`SELECT ts,user_name,kind,detail FROM timeline WHERE exam_id=? ORDER BY id`, e.ID)
	if err != nil {
		return err
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var ts, user, kind, detail string
		rows.Scan(&ts, &user, &kind, &detail)
		var d any
		json.Unmarshal([]byte(detail), &d)
		out = append(out, map[string]any{"ts": ts, "user": user, "kind": kind, "detail": d})
	}
	writeJSON(w, 200, map[string]any{"events": out, "label": e.Label})
	return nil
}

// ---------- corrections ----------

func (a *App) apiCorrectionRequest(w http.ResponseWriter, r *http.Request, u *User) error {
	if !u.IsAdmin() {
		return forbidden("This marksheet is locked. Only an administrator can edit it.")
	}
	e, set, err := a.examForUser(r, u)
	if err != nil {
		return err
	}
	if e.Status != "locked" {
		return conflict("Corrections apply only to locked tests; edit the draft directly.", nil)
	}
	var in struct {
		StudentID int64      `json:"student_id"`
		Subject   string     `json:"subject"`
		Status    string     `json:"status"`
		Comps     []*float64 `json:"comps"`
		Reason    string     `json:"reason"`
	}
	if err := decode(r, &in); err != nil {
		return err
	}
	in.Reason = strings.TrimSpace(in.Reason)
	if len(in.Reason) < 3 {
		return bad("A reason / evidence note is required")
	}
	if len(in.Reason) > 1000 {
		return bad("Reason is too long")
	}
	if !contains(e.Subjects, in.Subject) || !can(set, in.Subject) {
		return forbidden("You are not assigned to this subject")
	}
	comps, total, complete, verr := validateEntry(e.Components, in.Status, in.Comps)
	if verr != nil {
		return &apiErr{Code: 422, Msg: verr.Error()}
	}
	if !complete {
		return bad("A correction must supply a complete score, AB or EX")
	}
	var oldStatus, oldComps string
	var oldTotal sql.NullFloat64
	if err := a.db.QueryRow(`SELECT status,comps,total FROM results WHERE exam_id=? AND student_id=? AND subject=?`, e.ID, in.StudentID, in.Subject).
		Scan(&oldStatus, &oldComps, &oldTotal); err != nil {
		return notFound("Student has no recorded result in this test")
	}
	newJSON, _ := json.Marshal(comps)
	if oldStatus == in.Status && oldComps == string(newJSON) {
		return bad("The proposed result is identical to the current one")
	}
	var stName, roll string
	a.db.QueryRow(`SELECT name, roll_no FROM students WHERE id=?`, in.StudentID).Scan(&stName, &roll)
	tx, err := a.begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var dup int
	tx.QueryRow(`SELECT COUNT(*) FROM corrections WHERE exam_id=? AND student_id=? AND subject=? AND state='pending'`, e.ID, in.StudentID, in.Subject).Scan(&dup)
	if dup > 0 {
		return conflict("A correction request for this student is already awaiting approval.", nil)
	}
	// Administrator edits apply immediately; the correction row is kept as the audit record and pushed to Frappe.
	res, err := tx.Exec(`INSERT INTO corrections(exam_id,student_id,subject,old_status,old_comps,old_total,new_status,new_comps,new_total,reason,state,requested_by,requested_at,decided_by,decided_at,decision_note)
		VALUES(?,?,?,?,?,?,?,?,?,?,'approved',?,?,?,?,?)`, e.ID, in.StudentID, in.Subject, oldStatus, oldComps, nullF(oldTotal), in.Status, string(newJSON), *total, in.Reason,
		u.ID, nowStr(), u.ID, nowStr(), "Edited directly by administrator")
	if err != nil {
		return err
	}
	cid, _ := res.LastInsertId()
	if _, err := tx.Exec(`UPDATE results SET status=?,comps=?,total=?,updated_at=?,updated_by=? WHERE exam_id=? AND student_id=? AND subject=?`,
		in.Status, string(newJSON), *total, nowStr(), u.ID, e.ID, in.StudentID, in.Subject); err != nil {
		return err
	}
	if e.ApprovedAt != "" {
		if err := enqueue(tx, "correction", e.ID, in.StudentID, in.Subject, cid); err != nil {
			return err
		}
	}
	if err := addTimeline(tx, e.ID, u.Name, "correction_approved", map[string]any{
		"correction_id": cid, "student": stName, "roll_no": roll, "subject": in.Subject,
		"old": displayResult(oldStatus, oldTotal.Float64), "new": displayResult(in.Status, *total), "note": "Edited by administrator: " + in.Reason}); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	a.wake()
	writeJSON(w, 201, map[string]any{"ok": true, "correction_id": cid})
	return nil
}

func nullF(n sql.NullFloat64) any {
	if n.Valid {
		return n.Float64
	}
	return nil
}

func displayResult(status string, total float64) string {
	if status != "" {
		return status
	}
	return fmtNum(total)
}
