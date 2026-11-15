package main

import (
	"database/sql"
	"errors"
	"net/http"
	"os"
	"strconv"
	"strings"
)

// Teachers are SIS Instructors. The SIS is the master list:
//   - pull (roster sync): every Active Instructor is an UpMark teacher (contact details from the linked
//     Employee), an Instructor marked Left (or whose Employee left) is switched off, and the Instructor's
//     log gives the classes and subjects. Teachers are linked by users.frappe_instructor; the first time,
//     by email, then by mobile number.
//   - push (outbox job "instructor", student_id = user id): adding a teacher, changing their name or
//     classes, or switching them off in UpMark updates the Instructor (created when missing). Each
//     Instructor is tied to an Employee: the one with the same email or mobile (its missing contact details,
//     gender, dates and department are filled in), or a new one in the Teaching department when the teacher
//     has a gender and date of birth (the SIS requires both). Employees cover every department; UpMark
//     never switches one off.
// While a push is waiting, the pull leaves that teacher alone so UpMark's newer change wins.

type sisInstructor struct {
	name, fullName, status, employee string
	email, mobile                    string
	gender, dob, doj                 string
	empLeft                          bool
	logs                             []map[string]any
}

func digits10(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	d := b.String()
	if len(d) > 10 {
		d = d[len(d)-10:]
	}
	if len(d) != 10 {
		return ""
	}
	return d
}

func employeeEmail(e frappeDoc) string {
	return normIdent(firstNonEmpty(str(e, "user_id"), str(e, "company_email"), str(e, "prefered_email"), str(e, "personal_email")))
}

func (a *App) fetchInstructors() ([]sisInstructor, error) {
	f := a.frappe
	rows, err := f.List("Instructor", []string{"name"}, nil)
	if err != nil {
		return nil, err
	}
	var out []sisInstructor
	for _, r := range rows {
		full, err := f.Get("Instructor", str(r, "name"))
		if err != nil {
			return nil, err // a partial list would switch teachers off by mistake
		}
		in := sisInstructor{name: str(full, "name"), fullName: strings.TrimSpace(str(full, "instructor_name")), status: str(full, "status"), employee: str(full, "employee")}
		if in.employee != "" {
			if e, err := f.Get("Employee", in.employee); err == nil {
				in.email = employeeEmail(e)
				in.mobile = digits10(str(e, "cell_number"))
				in.gender, in.dob, in.doj = str(e, "gender"), str(e, "date_of_birth"), str(e, "date_of_joining")
				st := str(e, "status")
				in.empLeft = st == "Left" || st == "Inactive" || st == "Suspended"
				if in.fullName == "" {
					in.fullName = str(e, "employee_name")
				}
			}
		}
		if l, ok := full["instructor_log"].([]any); ok {
			for _, x := range l {
				if m, ok := x.(map[string]any); ok {
					in.logs = append(in.logs, m)
				}
			}
		}
		out = append(out, in)
	}
	return out, nil
}

func (a *App) applyInstructors(tx *wtx, list []sisInstructor, groupNames map[string]bool) map[string]int {
	res := map[string]int{}
	pending := map[int64]bool{}
	if rows, err := tx.Query(`SELECT DISTINCT student_id FROM jobs WHERE kind='instructor' AND state IN('pending','running')`); err == nil {
		for rows.Next() {
			var id int64
			rows.Scan(&id)
			pending[id] = true
		}
		rows.Close()
	}
	seen := map[string]bool{}
	for _, in := range list {
		seen[in.name] = true
		left := in.status == "Left" || in.empLeft
		var uid int64
		var role string
		err := tx.QueryRow(`SELECT id,role FROM users WHERE frappe_instructor=?`, in.name).Scan(&uid, &role)
		if err == sql.ErrNoRows && in.email != "" {
			err = tx.QueryRow(`SELECT id,role FROM users WHERE email=? AND frappe_instructor=''`, in.email).Scan(&uid, &role)
		}
		if err == sql.ErrNoRows && in.mobile != "" {
			err = tx.QueryRow(`SELECT id,role FROM users WHERE mobile=? AND frappe_instructor=''`, in.mobile).Scan(&uid, &role)
		}
		if err == sql.ErrNoRows {
			if left || in.fullName == "" || (in.email == "" && in.mobile == "") {
				res["skipped"]++ // nobody to sign in as: shown to the admin as "no contact in the SIS"
				continue
			}
			r, err := tx.Exec(`INSERT INTO users(name,mobile,email,role,created_at,frappe_instructor,gender,dob,doj) VALUES(?,?,?,'teacher',?,?,?,?,?)`,
				in.fullName, nullIfEmpty(in.mobile), nullIfEmpty(in.email), nowStr(), in.name, in.gender, in.dob, in.doj)
			if err != nil {
				res["skipped"]++
				continue
			}
			uid, _ = r.LastInsertId()
			role = "teacher"
			res["added"]++
		} else if err != nil {
			continue
		} else if !pending[uid] {
			// link, take the SIS name, fill missing contact details, follow Active / Left (admins keep their access)
			var oname string
			var omob, oem sql.NullString
			var oactive int
			tx.QueryRow(`SELECT name,mobile,email,active FROM users WHERE id=?`, uid).Scan(&oname, &omob, &oem, &oactive)
			active := oactive
			if role == "teacher" {
				active = 1
				if left {
					active = 0
				}
			}
			name := oname
			if in.fullName != "" {
				name = in.fullName
			}
			tx.Exec(`UPDATE users SET name=?,active=?,frappe_instructor=? WHERE id=?`, name, active, in.name, uid)
			if !omob.Valid && in.mobile != "" {
				tx.Exec(`UPDATE users SET mobile=? WHERE id=? AND NOT EXISTS(SELECT 1 FROM users WHERE mobile=?)`, in.mobile, uid, in.mobile)
			}
			if !oem.Valid && in.email != "" {
				tx.Exec(`UPDATE users SET email=? WHERE id=? AND NOT EXISTS(SELECT 1 FROM users WHERE email=?)`, in.email, uid, in.email)
			}
			for col, v := range map[string]string{"gender": in.gender, "dob": in.dob, "doj": in.doj} {
				if v != "" {
					tx.Exec(`UPDATE users SET `+col+`=? WHERE id=? AND `+col+`=''`, v, uid)
				}
			}
			if active == 0 && oactive == 1 {
				tx.Exec(`DELETE FROM sessions WHERE user_id=?`, uid)
				res["left"]++
			} else if name != oname || active != oactive {
				res["updated"]++
			}
		}
		res["linked"]++
		if pending[uid] || left {
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
			// a hand-made allocation the SIS now also has becomes an SIS one
			tx.Exec(`DELETE FROM allocations WHERE user_id=? AND class_name=? AND section=? AND subject=? AND source='manual'`, uid, c, s, course)
			if r, err := tx.Exec(`INSERT OR IGNORE INTO allocations(user_id,class_name,section,subject,source) VALUES(?,?,?,?,'frappe')`, uid, c, s, course); err == nil {
				if n, _ := r.RowsAffected(); n > 0 {
					res["allocations"]++
				}
			}
		}
	}
	// Instructors deleted in the SIS: their teachers are switched off (kept for history)
	rows, err := tx.Query(`SELECT id,frappe_instructor FROM users WHERE frappe_instructor<>'' AND role='teacher'`)
	if err == nil {
		var gone []int64
		for rows.Next() {
			var id int64
			var n string
			rows.Scan(&id, &n)
			if !seen[n] && !pending[id] {
				gone = append(gone, id)
			}
		}
		rows.Close()
		for _, id := range gone {
			tx.Exec(`UPDATE users SET active=0, frappe_instructor='' WHERE id=?`, id)
			tx.Exec(`DELETE FROM sessions WHERE user_id=?`, id)
			res["left"]++
		}
	}
	return res
}

// queueInstructor asks the sync worker to bring the SIS Instructor in line with this teacher.
func queueInstructor(tx execer, uid int64) error {
	_, err := tx.Exec(`INSERT INTO jobs(kind,exam_id,student_id,next_try,created_at)
		SELECT 'instructor',0,?,?,? WHERE NOT EXISTS(SELECT 1 FROM jobs WHERE kind='instructor' AND student_id=? AND state='pending')`, uid, nowStr(), nowStr(), uid)
	return err
}

// syncInstructor pushes one teacher to the SIS.
func (a *App) syncInstructor(uid int64) error {
	f := a.frappe
	if !f.Enabled() {
		return errNotConfigured
	}
	var name, role, link string
	var mob, em sql.NullString
	var active int
	var t teacherProfile
	if err := a.db.QueryRow(`SELECT name,mobile,email,role,active,frappe_instructor,gender,dob,doj FROM users WHERE id=?`, uid).Scan(&name, &mob, &em, &role, &active, &link, &t.gender, &t.dob, &t.doj); err != nil {
		return err
	}
	t.name, t.mobile, t.email = name, mob.String, em.String
	if role != "teacher" {
		return nil
	}
	year := a.setting("academic_year")
	if year == "" {
		year = a.refreshAcademicYear()
	}

	// the classes UpMark has for this teacher, as SIS student group + course
	type pair struct{ group, course string }
	want := map[pair]bool{}
	rows, err := a.db.Query(`SELECT DISTINCT s.student_group, al.subject FROM allocations al
		JOIN students s ON s.class_name=al.class_name AND s.section=al.section AND s.active=1 AND s.frappe_id NOT LIKE 'local-%'
		WHERE al.user_id=?`, uid)
	if err != nil {
		return err
	}
	for rows.Next() {
		var p pair
		rows.Scan(&p.group, &p.course)
		want[p] = true
	}
	rows.Close()
	groups := map[string]bool{}
	if rows, err := a.db.Query(`SELECT DISTINCT student_group FROM students WHERE active=1`); err == nil {
		for rows.Next() {
			var g string
			rows.Scan(&g)
			groups[g] = true
		}
		rows.Close()
	}

	// the SIS requires each Instructor Log row's Program: the one its Student Group belongs to
	programs := map[string]string{}
	logRow := func(p pair) frappeDoc {
		prog, ok := programs[p.group]
		if !ok {
			if g, err := f.Get("Student Group", p.group); err == nil {
				prog = str(g, "program")
			}
			programs[p.group] = prog
		}
		row := frappeDoc{"student_group": p.group, "course": p.course, "academic_year": year}
		if prog != "" {
			row["program"] = prog
		}
		return row
	}

	var cur frappeDoc
	if link != "" {
		cur, err = f.Get("Instructor", link)
		var fe *frappeError
		if errors.As(err, &fe) && fe.Status == 404 {
			cur, link = nil, ""
		} else if err != nil {
			return err
		}
	}
	if link == "" && active == 0 {
		return nil // never created for someone already switched off
	}
	if link == "" {
		// the teacher's Employee (found, or made in the Teaching department); an Instructor for it may
		// already exist, so link to that instead of making a second one
		emp, err := a.ensureEmployee(t, "")
		if err != nil {
			return err
		}
		if emp != "" {
			if rows, err := f.List("Instructor", []string{"name"}, [][]any{{"employee", "=", emp}}); err == nil && len(rows) > 0 {
				link = str(rows[0], "name")
				if cur, err = f.Get("Instructor", link); err != nil {
					return err
				}
			}
		}
		if link == "" {
			logs := []any{}
			for p := range want {
				logs = append(logs, logRow(p))
			}
			doc := frappeDoc{"instructor_name": name, "status": "Active", "instructor_log": logs}
			if emp != "" {
				doc["employee"] = emp
			}
			created, err := f.Insert("Instructor", doc)
			if err != nil {
				return err
			}
			link = str(created, "name")
			return a.afterInstructorPush(uid, link)
		}
	}

	// keep log rows for other years and other groups; replace this year's rows for active groups with UpMark's list
	var logs []any
	have := map[pair]bool{}
	if l, ok := cur["instructor_log"].([]any); ok {
		for _, x := range l {
			m, ok := x.(map[string]any)
			if !ok {
				continue
			}
			p := pair{str(m, "student_group"), str(m, "course")}
			managed := groups[p.group] && (str(m, "academic_year") == year || str(m, "academic_year") == "")
			if managed && !want[p] {
				continue
			}
			if managed {
				if have[p] {
					continue
				}
				have[p] = true
			}
			logs = append(logs, m)
		}
	}
	for p := range want {
		if !have[p] {
			logs = append(logs, logRow(p))
		}
	}
	status := "Active"
	if active == 0 {
		status = "Left"
	}
	upd := frappeDoc{"instructor_name": name, "status": status, "instructor_log": logs}
	if active == 1 {
		// keep the Employee in step (fill what it is missing), or give the Instructor one
		emp, err := a.ensureEmployee(t, str(cur, "employee"))
		if err != nil {
			return err
		}
		if emp != "" && str(cur, "employee") == "" {
			upd["employee"] = emp
		}
	}
	if _, err := f.Update("Instructor", link, upd); err != nil {
		return err
	}
	return a.afterInstructorPush(uid, link)
}

func (a *App) afterInstructorPush(uid int64, link string) error {
	tx, err := a.begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	tx.Exec(`UPDATE users SET frappe_instructor=? WHERE id=?`, link, uid)
	// what reached the SIS is now an SIS allocation (local-only sections stay hand-made)
	tx.Exec(`UPDATE allocations SET source='frappe' WHERE user_id=? AND EXISTS(SELECT 1 FROM students s
		WHERE s.class_name=allocations.class_name AND s.section=allocations.section AND s.active=1 AND s.frappe_id NOT LIKE 'local-%')`, uid)
	return tx.Commit()
}

type teacherProfile struct {
	name, mobile, email, gender, dob, doj string
}

// ensureEmployee returns the teacher's Employee: emp when given, else the one whose email or mobile matches,
// else a new one in the Teaching department (only when gender and date of birth are known, which the SIS
// requires). Details the Employee is missing are filled in from UpMark; nothing it has is overwritten.
func (a *App) ensureEmployee(t teacherProfile, emp string) (string, error) {
	f := a.frappe
	rows, err := f.List("Employee", []string{"name", "user_id", "company_email", "prefered_email", "personal_email", "cell_number", "status", "company"}, nil)
	if err != nil {
		return "", err
	}
	if emp == "" && t.email != "" {
		for _, e := range rows {
			if str(e, "status") != "Left" && employeeHasEmail(e, t.email) {
				emp = str(e, "name")
				break
			}
		}
	}
	if emp == "" && t.mobile != "" {
		for _, e := range rows {
			if str(e, "status") != "Left" && digits10(str(e, "cell_number")) == t.mobile {
				emp = str(e, "name")
				break
			}
		}
	}
	// the school's own email domain: an address there is a company email, any other a personal one
	emailField := "personal_email"
	if at := strings.LastIndex(t.email, "@"); at > 0 {
		dom := t.email[at:]
		for _, e := range rows {
			if strings.HasSuffix(normIdent(str(e, "company_email")), dom) {
				emailField = "company_email"
				break
			}
		}
	}
	dept := a.teachingDepartment()
	if emp == "" {
		if t.gender == "" || t.dob == "" {
			return "", nil // the Instructor is made without an Employee until these are filled in
		}
		first, last := t.name, ""
		if i := strings.IndexByte(t.name, ' '); i > 0 {
			first, last = t.name[:i], strings.TrimSpace(t.name[i+1:])
		}
		doj := t.doj
		if doj == "" {
			doj = today()
		}
		doc := frappeDoc{"first_name": first, "gender": t.gender, "date_of_birth": t.dob, "date_of_joining": doj, "status": "Active"}
		if last != "" {
			doc["last_name"] = last
		}
		if c := mostCommon(rows, "company"); c != "" {
			doc["company"] = c
		}
		if dept != "" {
			doc["department"] = dept
		}
		if t.mobile != "" {
			doc["cell_number"] = t.mobile
		}
		if t.email != "" {
			doc[emailField] = t.email
		}
		created, err := f.Insert("Employee", doc)
		if err != nil {
			return "", err
		}
		return str(created, "name"), nil
	}
	cur, err := f.Get("Employee", emp)
	if err != nil {
		return "", err
	}
	patch := frappeDoc{}
	if dept != "" && str(cur, "department") == "" {
		patch["department"] = dept
	}
	if t.mobile != "" && str(cur, "cell_number") == "" {
		patch["cell_number"] = t.mobile
	}
	if t.email != "" && employeeEmail(cur) == "" {
		patch[emailField] = t.email
	}
	for field, v := range map[string]string{"gender": t.gender, "date_of_birth": t.dob, "date_of_joining": t.doj} {
		if v != "" && str(cur, field) == "" {
			patch[field] = v
		}
	}
	if len(patch) > 0 {
		if _, err := f.Update("Employee", emp, patch); err != nil {
			return "", err
		}
	}
	return emp, nil
}

func employeeHasEmail(e frappeDoc, email string) bool {
	for _, k := range []string{"user_id", "company_email", "prefered_email", "personal_email"} {
		if normIdent(str(e, k)) == email {
			return true
		}
	}
	return false
}

func mostCommon(rows []frappeDoc, field string) string {
	n, best := map[string]int{}, ""
	for _, r := range rows {
		if v := str(r, field); v != "" {
			if n[v]++; n[v] > n[best] {
				best = v
			}
		}
	}
	return best
}

// teachingDepartment is the SIS Department teachers belong to ("Teaching - <company abbr>"), or "".
func (a *App) teachingDepartment() string {
	if d := os.Getenv("SIS_TEACHING_DEPARTMENT"); d != "" {
		return d
	}
	rows, err := a.frappe.List("Department", []string{"name"}, [][]any{{"name", "like", "Teaching%"}})
	if err != nil || len(rows) == 0 {
		return ""
	}
	return str(rows[0], "name")
}

// sisState describes a teacher's link to the SIS for the Teachers page.
func (a *App) sisState(uid int64, link string) (string, string) {
	var st, lastErr string
	a.db.QueryRow(`SELECT state,last_error FROM jobs WHERE kind='instructor' AND student_id=? ORDER BY id DESC LIMIT 1`, uid).Scan(&st, &lastErr)
	switch {
	case st == "pending" || st == "running":
		if lastErr != "" {
			return "error", lastErr
		}
		return "sending", ""
	case link != "":
		return "linked", ""
	}
	return "none", ""
}

func (a *App) instructorRoutes(mux *http.ServeMux) {
	// add one teacher, or every teacher not yet in the SIS, to it
	mux.HandleFunc("POST /api/admin/users/{id}/sis", a.wrap("admin", func(w http.ResponseWriter, r *http.Request, u *User) error {
		id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
		var n int
		a.db.QueryRow(`SELECT COUNT(*) FROM users WHERE id=? AND role='teacher'`, id).Scan(&n)
		if n == 0 {
			return notFound("Unknown teacher")
		}
		if !a.frappe.Enabled() {
			return bad("The SIS is not connected")
		}
		if err := queueInstructor(a.db, id); err != nil {
			return err
		}
		a.wake()
		writeJSON(w, 200, map[string]any{"queued": 1})
		return nil
	}))
	mux.HandleFunc("POST /api/admin/users/sis-all", a.wrap("admin", func(w http.ResponseWriter, r *http.Request, u *User) error {
		if !a.frappe.Enabled() {
			return bad("The SIS is not connected")
		}
		rows, err := a.db.Query(`SELECT id FROM users WHERE role='teacher' AND active=1 AND frappe_instructor=''`)
		if err != nil {
			return err
		}
		var ids []int64
		for rows.Next() {
			var id int64
			rows.Scan(&id)
			ids = append(ids, id)
		}
		rows.Close()
		for _, id := range ids {
			if err := queueInstructor(a.db, id); err != nil {
				return err
			}
		}
		a.wake()
		writeJSON(w, 200, map[string]any{"queued": len(ids)})
		return nil
	}))
}
