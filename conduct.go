package main

import (
	"database/sql"
	"errors"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Merits and demerits ("conduct points"). The SIS is the record: categories come from its Conduct Category
// doctype and every point is a Student Conduct Point there. UpMark saves a point here first and the sync
// worker pushes it, so a point given with no signal or while Frappe is down is not lost. Points entered
// directly in ERPNext are pulled back, so totals match in both places. Points are always positive; the
// category's type (Merit / Demerit) decides the sign, exactly as in the SIS.

type conductCategory struct {
	Name        string `json:"name"`
	Type        string `json:"point_type"`
	Default     int    `json:"default_points"`
	Description string `json:"description"`
}

// defaultConductCategories mirrors the SIS seed (ERP-Next "merits and demerits/categories.csv"), so the
// screens work before the first pull from Frappe.
var defaultConductCategories = []conductCategory{
	{"Academic Excellence", "Merit", 10, "Top score / outstanding result in a test or exam"},
	{"Class Participation", "Merit", 2, "Active and meaningful participation in class"},
	{"Homework Completed Consistently", "Merit", 2, "All homework on time for the week"},
	{"Helping Others / Kindness", "Merit", 3, "Helping classmates or staff without being asked"},
	{"Sports Achievement", "Merit", 5, "Winning or representing the school in sports"},
	{"Cultural / Co-curricular Achievement", "Merit", 5, "Competitions - debate / music / art / quiz"},
	{"Leadership", "Merit", 5, "Monitor / prefect duties done well or leading an activity"},
	{"Cleanliness & Discipline", "Merit", 2, "Exemplary neatness / discipline"},
	{"Perfect Attendance (Month)", "Merit", 5, "No absence in the month"},
	{"Late to School", "Demerit", 1, "Arrived after the bell"},
	{"Improper Uniform", "Demerit", 1, "Uniform / ID card / grooming not as per rules"},
	{"Homework Not Done", "Demerit", 2, "Homework missing or incomplete"},
	{"Disrupting Class", "Demerit", 3, "Talking / disturbing the class after warning"},
	{"Mobile Phone Misuse", "Demerit", 5, "Using a phone in school without permission"},
	{"Disrespectful Behaviour", "Demerit", 5, "Rude to staff or classmates"},
	{"Unexcused Absence", "Demerit", 2, "Absent without leave or note"},
	{"Bullying", "Demerit", 10, "Physical / verbal / online bullying"},
	{"Damage to School Property", "Demerit", 10, "Deliberate damage to school property"},
}

const (
	conductPointDT    = "Student Conduct Point"
	conductCategoryDT = "Conduct Category"
	maxConductPoints  = 100
)

func (a *App) conductRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/conduct/categories", a.wrap("", a.apiConductCategories))
	mux.HandleFunc("GET /api/conduct", a.wrap("", a.apiConductSection))
	mux.HandleFunc("POST /api/conduct", a.wrap("", a.apiConductAward))
	mux.HandleFunc("DELETE /api/conduct/{id}", a.wrap("admin", a.apiConductDelete))
	mux.HandleFunc("GET /api/admin/conduct", a.wrap("admin", a.apiConductSchool))
}

// seedConductCategories fills the category list on a fresh database; the pull from Frappe replaces it.
func (a *App) seedConductCategories() error {
	var n int
	a.db.QueryRow(`SELECT COUNT(*) FROM conduct_categories`).Scan(&n)
	if n > 0 {
		return nil
	}
	for _, c := range defaultConductCategories {
		if _, err := a.db.Exec(`INSERT OR IGNORE INTO conduct_categories(name,point_type,default_points,description) VALUES(?,?,?,?)`, c.Name, c.Type, c.Default, c.Description); err != nil {
			return err
		}
	}
	return nil
}

func (a *App) academicYear() string { return a.setting("academic_year") }

// conductLive is the SQL condition for points that count (not deleted) in the current academic year.
const conductLive = `cp.state<>'deleted' AND (?='' OR cp.academic_year=?)`

func (a *App) apiConductCategories(w http.ResponseWriter, r *http.Request, u *User) error {
	rows, err := a.db.Query(`SELECT name,point_type,default_points,description FROM conduct_categories WHERE enabled=1 ORDER BY point_type DESC, default_points DESC, name`)
	if err != nil {
		return err
	}
	defer rows.Close()
	out := []conductCategory{}
	for rows.Next() {
		var c conductCategory
		rows.Scan(&c.Name, &c.Type, &c.Default, &c.Description)
		out = append(out, c)
	}
	writeJSON(w, 200, map[string]any{"categories": out, "academic_year": a.academicYear()})
	return nil
}

type conductTotals struct {
	Merit   int `json:"merit"`
	Demerit int `json:"demerit"`
	Net     int `json:"net"`
	Entries int `json:"entries"`
}

type conductEntry struct {
	ID        int64  `json:"id"`
	StudentID int64  `json:"student_id"`
	Student   string `json:"student"`
	RollNo    string `json:"roll_no"`
	Group     string `json:"student_group"`
	Date      string `json:"date"`
	Category  string `json:"category"`
	Type      string `json:"point_type"`
	Points    int    `json:"points"`
	Remarks   string `json:"remarks"`
	By        string `json:"awarded_by"`
	State     string `json:"state"`
	Error     string `json:"error,omitempty"`
}

// conductEntries lists points with the student they belong to; where is appended after the live condition.
func (a *App) conductEntries(where string, limit int, args ...any) ([]conductEntry, error) {
	y := a.academicYear()
	q := `SELECT cp.id, cp.student_id, COALESCE(s.name,''), COALESCE(s.roll_no,''), cp.student_group, cp.date, cp.category, cp.point_type, cp.points,
		cp.remarks, cp.awarded_by_name, cp.state, COALESCE((SELECT last_error FROM jobs j WHERE j.kind='conduct' AND j.correction_id=cp.id AND j.state IN('pending','running') ORDER BY j.id DESC LIMIT 1),'')
		FROM conduct_points cp LEFT JOIN students s ON s.id=cp.student_id WHERE ` + conductLive + ` AND ` + where + ` ORDER BY cp.date DESC, cp.id DESC LIMIT ` + strconv.Itoa(limit)
	rows, err := a.db.Query(q, append([]any{y, y}, args...)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []conductEntry{}
	for rows.Next() {
		var e conductEntry
		rows.Scan(&e.ID, &e.StudentID, &e.Student, &e.RollNo, &e.Group, &e.Date, &e.Category, &e.Type, &e.Points, &e.Remarks, &e.By, &e.State, &e.Error)
		out = append(out, e)
	}
	return out, rows.Err()
}

// conductTotalsFor sums the current year per Frappe student, so a student who changed section keeps their total.
func (a *App) conductTotalsFor(frappeIDs []string) (map[string]*conductTotals, error) {
	out := map[string]*conductTotals{}
	if len(frappeIDs) == 0 {
		return out, nil
	}
	y := a.academicYear()
	args := []any{y, y}
	for _, f := range frappeIDs {
		args = append(args, f)
	}
	rows, err := a.db.Query(`SELECT cp.frappe_student, cp.point_type, SUM(cp.points), COUNT(*) FROM conduct_points cp
		WHERE `+conductLive+` AND cp.frappe_student IN (?`+strings.Repeat(",?", len(frappeIDs)-1)+`) GROUP BY cp.frappe_student, cp.point_type`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var fid, typ string
		var sum, n int
		rows.Scan(&fid, &typ, &sum, &n)
		t := out[fid]
		if t == nil {
			t = &conductTotals{}
			out[fid] = t
		}
		if typ == "Merit" {
			t.Merit += sum
		} else {
			t.Demerit += sum
		}
		t.Entries += n
		t.Net = t.Merit - t.Demerit
	}
	return out, rows.Err()
}

// canConduct: admins everywhere, teachers in the sections they teach.
func (a *App) canConduct(u *User, class, section string) (bool, error) {
	set, err := a.allowedSubjects(u, class, section)
	return set == nil || len(set) > 0, err
}

// apiConductSection returns per-student totals and the latest entries for one section.
func (a *App) apiConductSection(w http.ResponseWriter, r *http.Request, u *User) error {
	q := r.URL.Query()
	class, section := q.Get("class_name"), q.Get("section")
	if ok, err := a.canConduct(u, class, section); err != nil {
		return err
	} else if !ok {
		return forbidden("This class is not assigned to you")
	}
	type row struct {
		ID        int64  `json:"id"`
		Roll      string `json:"roll_no"`
		Name      string `json:"name"`
		Admission string `json:"admission_no"`
		InSIS     bool   `json:"in_sis"`
		conductTotals
		fid string
	}
	rows, err := a.db.Query(`SELECT id,roll_no,name,admission_no,frappe_id FROM students WHERE class_name=? AND section=? AND active=1 ORDER BY roll_sort,name`, class, section)
	if err != nil {
		return err
	}
	var list []*row
	var fids []string
	for rows.Next() {
		x := &row{}
		rows.Scan(&x.ID, &x.Roll, &x.Name, &x.Admission, &x.fid)
		x.InSIS = !strings.HasPrefix(x.fid, "local-")
		list = append(list, x)
		fids = append(fids, x.fid)
	}
	rows.Close()
	tot, err := a.conductTotalsFor(fids)
	if err != nil {
		return err
	}
	out := []*row{}
	sum := conductTotals{}
	for _, x := range list {
		if t := tot[x.fid]; t != nil {
			x.conductTotals = *t
			sum.Merit, sum.Demerit, sum.Entries = sum.Merit+t.Merit, sum.Demerit+t.Demerit, sum.Entries+t.Entries
		}
		out = append(out, x)
	}
	sum.Net = sum.Merit - sum.Demerit
	recent, err := a.conductEntries(`cp.frappe_student IN (SELECT frappe_id FROM students WHERE class_name=? AND section=? AND active=1)`, 60, class, section)
	if err != nil {
		return err
	}
	writeJSON(w, 200, map[string]any{"students": out, "totals": sum, "recent": recent, "academic_year": a.academicYear(), "can_delete": u.IsAdmin()})
	return nil
}

type conductAward struct {
	StudentIDs []int64 `json:"student_ids"`
	Category   string  `json:"category"`
	Points     int     `json:"points"`
	Date       string  `json:"date"`
	Remarks    string  `json:"remarks"`
}

func (a *App) apiConductAward(w http.ResponseWriter, r *http.Request, u *User) error {
	var in conductAward
	if err := decode(r, &in); err != nil {
		return err
	}
	if len(in.StudentIDs) == 0 {
		return bad("Pick at least one student")
	}
	if len(in.StudentIDs) > 200 {
		return bad("Pick at most 200 students at a time")
	}
	var cat conductCategory
	if err := a.db.QueryRow(`SELECT name,point_type,default_points FROM conduct_categories WHERE name=? AND enabled=1`, in.Category).Scan(&cat.Name, &cat.Type, &cat.Default); err != nil {
		return bad("Pick a category")
	}
	if in.Points == 0 {
		in.Points = cat.Default
	}
	if in.Points < 1 || in.Points > maxConductPoints {
		return bad("Points must be between 1 and " + strconv.Itoa(maxConductPoints) + ". Merit or demerit comes from the category.")
	}
	if in.Date == "" {
		in.Date = time.Now().Format("2006-01-02")
	}
	d, err := time.Parse("2006-01-02", in.Date)
	if err != nil {
		return bad("Invalid date")
	}
	if d.After(time.Now().Add(24 * time.Hour)) {
		return bad("Points cannot be dated in the future")
	}
	if d.Before(time.Now().AddDate(-1, 0, 0)) {
		return bad("That date is more than a year ago")
	}
	in.Remarks = strings.TrimSpace(in.Remarks)
	if len(in.Remarks) > 500 {
		return bad("Remarks are too long")
	}
	type stu struct {
		id                               int64
		fid, group, class, section, name string
	}
	var list []stu
	seen := map[int64]bool{}
	allowed := map[string]bool{}
	for _, id := range in.StudentIDs {
		if seen[id] {
			continue
		}
		seen[id] = true
		var s stu
		err := a.db.QueryRow(`SELECT id,frappe_id,student_group,class_name,section,name FROM students WHERE id=? AND active=1`, id).Scan(&s.id, &s.fid, &s.group, &s.class, &s.section, &s.name)
		if errors.Is(err, sql.ErrNoRows) {
			return bad("A selected student is no longer on the roster. Reload and try again.")
		} else if err != nil {
			return err
		}
		k := s.class + "\x00" + s.section
		if _, ok := allowed[k]; !ok {
			can, err := a.canConduct(u, s.class, s.section)
			if err != nil {
				return err
			}
			allowed[k] = can
		}
		if !allowed[k] {
			return forbidden(groupLabel(s.class, s.section) + " is not assigned to you")
		}
		if strings.HasPrefix(s.fid, "local-") {
			return bad(s.name + " was added in UpMark only and is not in the SIS yet, so points cannot be recorded for them.")
		}
		list = append(list, s)
	}
	tx, err := a.begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now, year := nowStr(), a.academicYear()
	ids := []int64{}
	for _, s := range list {
		res, err := tx.Exec(`INSERT INTO conduct_points(student_id,frappe_student,student_group,academic_year,date,category,point_type,points,remarks,awarded_by,awarded_by_name,created_at)
			VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`, s.id, s.fid, s.group, year, in.Date, cat.Name, cat.Type, in.Points, in.Remarks, u.ID, u.Name, now)
		if err != nil {
			return err
		}
		id, _ := res.LastInsertId()
		if _, err := tx.Exec(`INSERT INTO jobs(kind,exam_id,correction_id,next_try,created_at) VALUES('conduct',0,?,?,?)`, id, now, now); err != nil {
			return err
		}
		ids = append(ids, id)
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	a.wake()
	writeJSON(w, 201, map[string]any{"created": len(ids), "ids": ids, "point_type": cat.Type, "points": in.Points, "category": cat.Name})
	return nil
}

// apiConductDelete removes a point here and in the SIS. Only admins delete, as in the SIS.
func (a *App) apiConductDelete(w http.ResponseWriter, r *http.Request, u *User) error {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	tx, err := a.begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var state string
	if err := tx.QueryRow(`SELECT state FROM conduct_points WHERE id=?`, id).Scan(&state); err != nil {
		return notFound("Entry not found")
	}
	if state == "deleted" {
		return notFound("Entry already deleted")
	}
	now := nowStr()
	if _, err := tx.Exec(`UPDATE conduct_points SET state='deleted', deleted_by=?, deleted_at=? WHERE id=?`, u.Name, now, id); err != nil {
		return err
	}
	if _, err := tx.Exec(`INSERT INTO jobs(kind,exam_id,correction_id,next_try,created_at) VALUES('conduct',0,?,?,?)`, id, now, now); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	a.wake()
	writeJSON(w, 200, map[string]any{"deleted": id})
	return nil
}

// apiConductSchool is the admin's view: totals per section, the leaders, the most demerits, and the latest entries.
func (a *App) apiConductSchool(w http.ResponseWriter, r *http.Request, u *User) error {
	y := a.academicYear()
	type section struct {
		Class   string `json:"class_name"`
		Section string `json:"section"`
		Label   string `json:"label"`
		conductTotals
	}
	type person struct {
		ID    int64  `json:"id"`
		Name  string `json:"name"`
		Label string `json:"class"`
		conductTotals
	}
	rows, err := a.db.Query(`SELECT s.id, s.name, s.class_name, s.section, cp.point_type, SUM(cp.points), COUNT(*)
		FROM conduct_points cp JOIN students s ON s.frappe_id=cp.frappe_student AND s.active=1
		WHERE `+conductLive+` GROUP BY s.id, cp.point_type`, y, y)
	if err != nil {
		return err
	}
	people := map[int64]*person{}
	secs := map[string]*section{}
	var tot conductTotals
	for rows.Next() {
		var id int64
		var name, class, sec, typ string
		var sum, n int
		rows.Scan(&id, &name, &class, &sec, &typ, &sum, &n)
		p := people[id]
		if p == nil {
			p = &person{ID: id, Name: name, Label: groupLabel(class, sec)}
			people[id] = p
		}
		k := class + "\x00" + sec
		s := secs[k]
		if s == nil {
			s = &section{Class: class, Section: sec, Label: groupLabel(class, sec)}
			secs[k] = s
		}
		for _, t := range []*conductTotals{&p.conductTotals, &s.conductTotals, &tot} {
			if typ == "Merit" {
				t.Merit += sum
			} else {
				t.Demerit += sum
			}
			t.Entries += n
			t.Net = t.Merit - t.Demerit
		}
	}
	rows.Close()
	sections := []*section{}
	for _, s := range secs {
		sections = append(sections, s)
	}
	sort.Slice(sections, func(i, j int) bool {
		return sections[i].Net > sections[j].Net || sections[i].Net == sections[j].Net && sections[i].Label < sections[j].Label
	})
	var all []*person
	for _, p := range people {
		all = append(all, p)
	}
	top := []*person{} // only students who are ahead overall
	for _, p := range all {
		if p.Net > 0 {
			top = append(top, p)
		}
	}
	sort.Slice(top, func(i, j int) bool {
		return top[i].Net > top[j].Net || top[i].Net == top[j].Net && top[i].Name < top[j].Name
	})
	watch := []*person{}
	for _, p := range all {
		if p.Demerit > 0 {
			watch = append(watch, p)
		}
	}
	sort.Slice(watch, func(i, j int) bool {
		return watch[i].Demerit > watch[j].Demerit || watch[i].Demerit == watch[j].Demerit && watch[i].Name < watch[j].Name
	})
	if len(top) > 10 {
		top = top[:10]
	}
	if len(watch) > 10 {
		watch = watch[:10]
	}
	recent, err := a.conductEntries("1=1", 40)
	if err != nil {
		return err
	}
	var pending int
	a.db.QueryRow(`SELECT COUNT(*) FROM jobs WHERE kind='conduct' AND state IN('pending','running')`).Scan(&pending)
	writeJSON(w, 200, map[string]any{"totals": tot, "sections": sections, "top": top, "watch": watch, "recent": recent,
		"academic_year": y, "pending_sync": pending, "frappe_configured": a.frappe.Enabled()})
	return nil
}

// ---------------- SIS sync ----------------

// syncConduct pushes one point to the SIS, or removes it there once it was deleted here.
func (a *App) syncConduct(id int64) error {
	var p struct {
		fid, name, group, year, date, category, remarks, by, state, frappeName string
		points                                                                 int
		byUser                                                                 int64
	}
	err := a.db.QueryRow(`SELECT cp.frappe_student, COALESCE(s.name,''), cp.student_group, cp.academic_year, cp.date, cp.category, cp.remarks, cp.awarded_by_name,
		cp.state, cp.frappe_name, cp.points, cp.awarded_by FROM conduct_points cp LEFT JOIN students s ON s.id=cp.student_id WHERE cp.id=?`, id).
		Scan(&p.fid, &p.name, &p.group, &p.year, &p.date, &p.category, &p.remarks, &p.by, &p.state, &p.frappeName, &p.points, &p.byUser)
	if err != nil {
		return err
	}
	f := a.frappe
	if !f.Enabled() {
		return errNotConfigured
	}
	if p.state == "deleted" {
		if p.frappeName != "" {
			if err := f.Delete(conductPointDT, p.frappeName); err != nil {
				return err
			}
		}
		return nil
	}
	if p.frappeName != "" { // already in the SIS
		return nil
	}
	if p.year == "" {
		p.year = a.refreshAcademicYear()
	}
	remarks := p.remarks
	if p.by != "" {
		if remarks != "" {
			remarks += " · "
		}
		remarks += "Given by " + p.by + " via UpMark"
	}
	doc := frappeDoc{"student": p.fid, "student_name": p.name, "student_group": p.group, "date": p.date, "category": p.category,
		"points": p.points, "remarks": remarks}
	if p.year != "" {
		doc["academic_year"] = p.year
	}
	// Awarded By is a link to a Frappe User; set it when the teacher has an account there with the same email.
	var email string
	a.db.QueryRow(`SELECT COALESCE(email,'') FROM users WHERE id=?`, p.byUser).Scan(&email)
	if email != "" {
		doc["awarded_by"] = email
	}
	out, err := f.Insert(conductPointDT, cloneDoc(doc))
	var fe *frappeError
	if err != nil && email != "" && errors.As(err, &fe) && fe.Status >= 400 && fe.Status < 500 && fe.Status != 404 {
		delete(doc, "awarded_by") // no such Frappe user: the remark still names the teacher
		out, err = f.Insert(conductPointDT, cloneDoc(doc))
	}
	if err != nil {
		return err
	}
	_, err = a.db.Exec(`UPDATE conduct_points SET frappe_name=?, academic_year=?, state=CASE WHEN state='deleted' THEN state ELSE 'synced' END WHERE id=?`, str(out, "name"), p.year, id)
	if err == nil {
		var state string
		if a.db.QueryRow(`SELECT state FROM conduct_points WHERE id=?`, id).Scan(&state); state == "deleted" { // deleted while it was being sent
			a.db.Exec(`INSERT INTO jobs(kind,exam_id,correction_id,next_try,created_at) VALUES('conduct',0,?,?,?)`, id, nowStr(), nowStr())
		}
	}
	return err
}

func cloneDoc(d frappeDoc) frappeDoc {
	c := frappeDoc{}
	for k, v := range d {
		c[k] = v
	}
	return c
}

func (f *Frappe) Delete(doctype, name string) error {
	err := f.do("DELETE", "/api/resource/"+url.PathEscape(doctype)+"/"+url.PathEscape(name), nil, nil, nil)
	var fe *frappeError
	if errors.As(err, &fe) && fe.Status == 404 { // already gone
		return nil
	}
	return err
}

// refreshAcademicYear reads the SIS's current academic year (Education Settings) and remembers it.
func (a *App) refreshAcademicYear() string {
	if d, err := a.frappe.Get("Education Settings", "Education Settings"); err == nil {
		if y := str(d, "current_academic_year"); y != "" {
			a.setSetting("academic_year", y)
			return y
		}
	}
	return a.academicYear()
}

// pullConduct copies categories and this year's points from the SIS, so entries made in ERPNext show here and
// entries deleted there disappear. A site without the conduct doctypes is left alone.
func (a *App) pullConduct() (map[string]any, error) {
	f := a.frappe
	if !f.Enabled() {
		return nil, errNotConfigured
	}
	cats, err := f.List(conductCategoryDT, []string{"name", "category_name", "point_type", "default_points", "enabled", "description"}, nil)
	if err != nil {
		var fe *frappeError
		if errors.As(err, &fe) && (fe.Status == 404 || fe.Status == 403) {
			return map[string]any{"conduct": "not installed in the SIS"}, nil
		}
		return nil, err
	}
	year := a.refreshAcademicYear()
	var filters [][]any
	if year != "" {
		filters = [][]any{{"academic_year", "=", year}}
	}
	points, err := f.List(conductPointDT, []string{"name", "student", "student_group", "academic_year", "date", "category", "point_type", "points", "remarks", "awarded_by"}, filters)
	if err != nil {
		return nil, err
	}
	tx, err := a.begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	for _, c := range cats {
		name := firstNonEmpty(str(c, "category_name"), str(c, "name"))
		typ := str(c, "point_type")
		if typ != "Merit" && typ != "Demerit" {
			continue
		}
		def, _ := strconv.Atoi(strings.TrimSuffix(str(c, "default_points"), ".0"))
		enabled := 1
		if s := str(c, "enabled"); s == "0" || s == "false" {
			enabled = 0
		}
		if _, err := tx.Exec(`INSERT INTO conduct_categories(name,point_type,default_points,enabled,description) VALUES(?,?,?,?,?)
			ON CONFLICT(name) DO UPDATE SET point_type=excluded.point_type, default_points=excluded.default_points, enabled=excluded.enabled, description=excluded.description`,
			name, typ, def, enabled, str(c, "description")); err != nil {
			return nil, err
		}
	}
	inSIS := map[string]bool{}
	added, updated := 0, 0
	for _, p := range points {
		name, fid := str(p, "name"), str(p, "student")
		inSIS[name] = true
		pts, _ := strconv.Atoi(strings.TrimSuffix(str(p, "points"), ".0"))
		typ := str(p, "point_type")
		if typ == "" {
			tx.QueryRow(`SELECT point_type FROM conduct_categories WHERE name=?`, str(p, "category")).Scan(&typ)
		}
		if pts < 1 || (typ != "Merit" && typ != "Demerit") {
			continue
		}
		var id int64
		var state string
		err := tx.QueryRow(`SELECT id,state FROM conduct_points WHERE frappe_name=?`, name).Scan(&id, &state)
		switch {
		case err == nil:
			if state == "deleted" { // deleted here; the delete job removes it there
				continue
			}
			res, _ := tx.Exec(`UPDATE conduct_points SET date=?, category=?, point_type=?, points=?, academic_year=?, state='synced'
				WHERE id=? AND (date<>? OR category<>? OR point_type<>? OR points<>? OR academic_year<>?)`,
				str(p, "date"), str(p, "category"), typ, pts, str(p, "academic_year"), id, str(p, "date"), str(p, "category"), typ, pts, str(p, "academic_year"))
			if n, _ := res.RowsAffected(); n > 0 {
				updated++
			}
		case errors.Is(err, sql.ErrNoRows):
			var sid int64
			if tx.QueryRow(`SELECT id FROM students WHERE frappe_id=? ORDER BY active DESC, id DESC LIMIT 1`, fid).Scan(&sid) != nil {
				continue // not on any roster here
			}
			by := str(p, "awarded_by")
			remarks := str(p, "remarks")
			if i := strings.LastIndex(remarks, "Given by "); i >= 0 && strings.HasSuffix(remarks, " via UpMark") {
				by = strings.TrimSuffix(remarks[i+len("Given by "):], " via UpMark")
				remarks = strings.TrimSuffix(strings.TrimSpace(remarks[:i]), " ·")
			}
			if _, err := tx.Exec(`INSERT INTO conduct_points(student_id,frappe_student,student_group,academic_year,date,category,point_type,points,remarks,awarded_by_name,created_at,frappe_name,state)
				VALUES(?,?,?,?,?,?,?,?,?,?,?,?,'synced')`, sid, fid, str(p, "student_group"), str(p, "academic_year"), str(p, "date"), str(p, "category"), typ, pts,
				remarks, by, nowStr(), name); err != nil {
				return nil, err
			}
			added++
		default:
			return nil, err
		}
	}
	// points that were in the SIS and are gone now were deleted there
	removed := 0
	rows, err := tx.Query(`SELECT id, frappe_name FROM conduct_points WHERE state='synced' AND frappe_name<>'' AND (?='' OR academic_year=?)`, year, year)
	if err != nil {
		return nil, err
	}
	var gone []int64
	for rows.Next() {
		var id int64
		var name string
		rows.Scan(&id, &name)
		if !inSIS[name] {
			gone = append(gone, id)
		}
	}
	rows.Close()
	for _, id := range gone {
		tx.Exec(`UPDATE conduct_points SET state='deleted', deleted_by='SIS', deleted_at=? WHERE id=?`, nowStr(), id)
		removed++
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	a.setSetting("last_conduct_pull", nowStr())
	return map[string]any{"conduct_added": added, "conduct_updated": updated, "conduct_removed": removed, "categories": len(cats)}, nil
}
