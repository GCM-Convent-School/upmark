package main

import (
	"bytes"
	"encoding/csv"
	"io"
	"net/http"
	"regexp"
	"strings"

	"github.com/ledongthuc/pdf"
)

var (
	emailRe  = regexp.MustCompile(`[A-Za-z0-9._%+\-]+@[A-Za-z0-9.\-]+\.[A-Za-z]{2,}`)
	mobileRe = regexp.MustCompile(`(?:\+?91[\s-]?)?([6-9][0-9]{4}[\s-]?[0-9]{5})`)
	leadNoRe = regexp.MustCompile(`^\s*\d{1,3}[.)]?\s+`)
)

type importRow struct {
	Name   string `json:"name"`
	Mobile string `json:"mobile"`
	Email  string `json:"email"`
	Status string `json:"status"` // new | exists | invalid
	Note   string `json:"note,omitempty"`
}

// parseTeacherLine pulls name / mobile / email out of one CSV record or one PDF text line.
// Returns ok=false for lines with neither a mobile nor an email (headers, titles, page numbers).
func parseTeacherLine(line string) (importRow, bool) {
	var r importRow
	if m := emailRe.FindString(line); m != "" {
		r.Email = strings.ToLower(m)
		line = strings.Replace(line, m, " ", 1)
	}
	if m := mobileRe.FindStringSubmatch(line); m != nil {
		r.Mobile = normIdent(m[1])
		line = strings.Replace(line, m[0], " ", 1)
	}
	if r.Mobile == "" && r.Email == "" {
		return r, false
	}
	line = leadNoRe.ReplaceAllString(line, "")
	line = strings.Map(func(c rune) rune {
		if c == ',' || c == ';' || c == '|' || c == '\t' || c == '"' {
			return ' '
		}
		return c
	}, line)
	r.Name = strings.Join(strings.Fields(line), " ")
	return r, true
}

// fileLines returns one text line per CSV record or PDF text row.
func fileLines(filename string, data []byte) ([]string, error) {
	var lines []string
	switch {
	case strings.HasSuffix(strings.ToLower(filename), ".pdf"):
		pr, err := pdf.NewReader(bytes.NewReader(data), int64(len(data)))
		if err != nil {
			return nil, bad("Could not read this PDF: " + err.Error())
		}
		for i := 1; i <= pr.NumPage(); i++ {
			p := pr.Page(i)
			if p.V.IsNull() {
				continue
			}
			rows, err := p.GetTextByRow()
			if err != nil {
				continue
			}
			for _, row := range rows {
				var parts []string
				for _, t := range row.Content {
					parts = append(parts, t.S)
				}
				lines = append(lines, strings.Join(parts, " "))
			}
		}
	default:
		cr := csv.NewReader(bytes.NewReader(bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))))
		cr.FieldsPerRecord = -1
		cr.LazyQuotes = true
		for {
			rec, err := cr.Read()
			if err == io.EOF {
				break
			}
			if err != nil {
				return nil, bad("Could not read this CSV: " + err.Error())
			}
			lines = append(lines, strings.Join(rec, " , "))
		}
	}
	return lines, nil
}

func extractTeacherRows(filename string, data []byte) ([]importRow, error) {
	lines, err := fileLines(filename, data)
	if err != nil {
		return nil, err
	}
	var out []importRow
	for _, l := range lines {
		if r, ok := parseTeacherLine(l); ok {
			out = append(out, r)
		}
	}
	if len(out) == 0 {
		return nil, bad("No teachers found. Each row needs a name and a 10-digit mobile number (or an email).")
	}
	return out, nil
}

// adminUsersImport reads a CSV/PDF teacher list. With ?dry=1 it only previews; otherwise new teachers are created.
func (a *App) adminUsersImport(w http.ResponseWriter, r *http.Request, u *User) error {
	r.Body = http.MaxBytesReader(w, r.Body, 10<<20)
	if err := r.ParseMultipartForm(10 << 20); err != nil {
		return bad("Upload a CSV or PDF file")
	}
	f, hdr, err := r.FormFile("file")
	if err != nil {
		return bad("Upload a CSV or PDF file")
	}
	defer f.Close()
	data, _ := io.ReadAll(f)
	rows, err := extractTeacherRows(hdr.Filename, data)
	if err != nil {
		return err
	}
	dry := r.URL.Query().Get("dry") == "1"
	seen := map[string]bool{}
	added := 0
	for i := range rows {
		row := &rows[i]
		in := userReq{Name: row.Name, Mobile: row.Mobile, Email: row.Email, Role: "teacher"}
		if err := in.clean(); err != nil {
			row.Status, row.Note = "invalid", err.(*apiErr).Msg
			continue
		}
		key := firstNonEmpty(in.Mobile, in.Email)
		if seen[key] {
			row.Status, row.Note = "invalid", "Duplicate row in file"
			continue
		}
		seen[key] = true
		var n int
		a.db.QueryRow(`SELECT COUNT(*) FROM users WHERE (mobile=? AND ?<>'') OR (email=? AND ?<>'')`, in.Mobile, in.Mobile, in.Email, in.Email).Scan(&n)
		if n > 0 {
			row.Status, row.Note = "exists", "Already a user"
			continue
		}
		row.Status = "new"
		if !dry {
			if _, err := a.db.Exec(`INSERT INTO users(name,mobile,email,role,created_at) VALUES(?,?,?,?,?)`,
				in.Name, nullIfEmpty(in.Mobile), nullIfEmpty(in.Email), "teacher", nowStr()); err != nil {
				row.Status, row.Note = "invalid", "Could not save"
				continue
			}
			added++
		}
	}
	writeJSON(w, 200, map[string]any{"rows": rows, "dry": dry, "added": added})
	return nil
}

// adminDummyTeachers creates one test teacher on a real mobile number, so the WorkCode arrives on WhatsApp as usual.
func (a *App) adminDummyTeachers(w http.ResponseWriter, r *http.Request, u *User) error {
	var in struct {
		Name      string `json:"name"`
		Mobile    string `json:"mobile"`
		AllAccess bool   `json:"all_access"`
	}
	if err := decode(r, &in); err != nil {
		return err
	}
	req := userReq{Name: in.Name, Mobile: in.Mobile, Role: "teacher"}
	if req.Name == "" {
		req.Name = "Test Teacher"
	}
	if err := req.clean(); err != nil {
		return err
	}
	res, err := a.db.Exec(`INSERT INTO users(name,mobile,role,created_at) VALUES(?,?,?,?)`, req.Name, req.Mobile, "teacher", nowStr())
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return conflict("A user with this mobile number already exists", nil)
		}
		return err
	}
	id, _ := res.LastInsertId()
	if in.AllAccess {
		a.db.Exec(`INSERT OR IGNORE INTO allocations(user_id,class_name,section,subject,source)
			SELECT ?, g.class_name, g.section, c.name, 'manual' FROM (SELECT DISTINCT class_name, section FROM students WHERE active=1) g CROSS JOIN courses c`, id)
	}
	writeJSON(w, 200, map[string]any{"id": id, "name": req.Name, "mobile": req.Mobile})
	return nil
}

func (a *App) adminStudents(w http.ResponseWriter, r *http.Request, u *User) error {
	q := r.URL.Query()
	where, args := `active=1`, []any{}
	if c := q.Get("class_name"); c != "" {
		where += ` AND class_name=?`
		args = append(args, c)
		if q.Has("section") {
			where += ` AND section=?`
			args = append(args, q.Get("section"))
		}
	}
	if s := strings.TrimSpace(q.Get("q")); s != "" {
		where += ` AND (name LIKE ? OR admission_no LIKE ?)`
		args = append(args, "%"+s+"%", "%"+s+"%")
	}
	var total int
	a.db.QueryRow(`SELECT COUNT(*) FROM students WHERE `+where, args...).Scan(&total)
	rows, err := a.db.Query(`SELECT name,admission_no,roll_no,class_name,section,student_group FROM students WHERE `+where+
		` ORDER BY class_name,section,roll_sort,name LIMIT 500`, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var name, adm, roll, cl, sec, grp string
		rows.Scan(&name, &adm, &roll, &cl, &sec, &grp)
		out = append(out, map[string]any{"name": name, "admission_no": adm, "roll_no": roll, "class_name": cl, "section": sec, "group": grp})
	}
	writeJSON(w, 200, map[string]any{"students": out, "total": total})
	return nil
}

type studentRow struct {
	Name      string `json:"name"`
	Class     string `json:"class_name"`
	Section   string `json:"section"`
	Roll      string `json:"roll_no"`
	Admission string `json:"admission_no"`
	Status    string `json:"status"` // new | exists | invalid
	Note      string `json:"note,omitempty"`
}

var admissionRe = regexp.MustCompile(`^[A-Za-z]*[-/]?[A-Za-z0-9/\-]*\d{3,}[A-Za-z0-9/\-]*$`)

// parseStudentLine reads "roll admission name class section" style rows; the class/section label must match a known
// group (e.g. "5TH A", "5TH-A", "2ND BLOSSOM"), which is how it is told apart from the name.
func parseStudentLine(line string, groups [][2]string) (studentRow, bool) {
	var r studentRow
	norm := strings.ToUpper(strings.NewReplacer(",", " ", ";", " ", "|", " ", "\t", " ", "\"", " ").Replace(line))
	norm = " " + strings.Join(strings.Fields(norm), " ") + " "
	best := -1
	for i, g := range groups {
		for _, sep := range []string{" ", "-"} {
			lbl := " " + g[0] + sep + g[1] + " "
			if g[1] == "" {
				lbl = " " + g[0] + " "
			}
			if strings.Contains(norm, lbl) && (best < 0 || len(groups[i][0])+len(groups[i][1]) > len(groups[best][0])+len(groups[best][1])) {
				best = i
				r.Class, r.Section = g[0], g[1]
				norm = strings.Replace(norm, lbl, " ", 1)
			}
		}
	}
	if best < 0 {
		return r, false
	}
	toks := strings.Fields(line)
	var rest []string
	for _, t := range toks {
		t = strings.Trim(t, ",;|\"")
		if t == "" {
			continue
		}
		rest = append(rest, t)
	}
	// drop the group tokens from the original-case line
	clean := " " + strings.Join(rest, " ") + " "
	for _, sep := range []string{" ", "-"} {
		lbl := r.Class + sep + r.Section
		if r.Section == "" {
			lbl = r.Class
		}
		clean = caseInsensitiveReplace(clean, " "+lbl+" ", " ")
	}
	var name []string
	for i, t := range strings.Fields(clean) {
		switch {
		case i == 0 && regexp.MustCompile(`^\d{1,3}$`).MatchString(t) && r.Roll == "":
			r.Roll = t
		case r.Admission == "" && admissionRe.MatchString(t) && strings.ContainsAny(t, "0123456789"):
			r.Admission = t
		default:
			name = append(name, t)
		}
	}
	r.Name = strings.Join(name, " ")
	return r, true
}

func caseInsensitiveReplace(s, old, repl string) string {
	if i := strings.Index(strings.ToUpper(s), strings.ToUpper(old)); i >= 0 {
		return s[:i] + repl + s[i+len(old):]
	}
	return s
}

func (a *App) knownGroups() [][2]string {
	rows, err := a.db.Query(`SELECT DISTINCT class_name, section FROM students WHERE active=1`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out [][2]string
	for rows.Next() {
		var c, s string
		rows.Scan(&c, &s)
		out = append(out, [2]string{strings.ToUpper(c), strings.ToUpper(s)})
	}
	return out
}

// adminStudentsImport adds students from a CSV/PDF list. ?dry=1 previews. Imported students live in this app only
// until they exist in Frappe; an admission number equal to a Frappe Student ID is matched by the next roster sync.
func (a *App) adminStudentsImport(w http.ResponseWriter, r *http.Request, u *User) error {
	r.Body = http.MaxBytesReader(w, r.Body, 10<<20)
	if err := r.ParseMultipartForm(10 << 20); err != nil {
		return bad("Upload a CSV or PDF file")
	}
	f, hdr, err := r.FormFile("file")
	if err != nil {
		return bad("Upload a CSV or PDF file")
	}
	defer f.Close()
	data, _ := io.ReadAll(f)
	lines, err := fileLines(hdr.Filename, data)
	if err != nil {
		return err
	}
	groups := a.knownGroups()
	if len(groups) == 0 {
		return bad("Fetch students from Frappe first so the classes and sections are known")
	}
	dry := r.URL.Query().Get("dry") == "1"
	var rows []studentRow
	added := 0
	for _, l := range lines {
		row, ok := parseStudentLine(l, groups)
		if !ok {
			continue
		}
		if row.Name == "" {
			row.Status, row.Note = "invalid", "Name missing"
			rows = append(rows, row)
			continue
		}
		group := row.Class
		if row.Section != "" {
			group = row.Class + " " + row.Section
		}
		var n int
		a.db.QueryRow(`SELECT COUNT(*) FROM students WHERE class_name=? AND section=? AND active=1 AND (LOWER(name)=LOWER(?) OR (?<>'' AND admission_no=?))`,
			row.Class, row.Section, row.Name, row.Admission, row.Admission).Scan(&n)
		if n > 0 {
			row.Status, row.Note = "exists", "Already in this class"
			rows = append(rows, row)
			continue
		}
		row.Status = "new"
		if !dry {
			fid := row.Admission
			if fid == "" {
				fid = "local-" + randHex(6)
			}
			if _, err := a.db.Exec(`INSERT INTO students(frappe_id,admission_no,roll_no,roll_sort,name,student_group,class_name,section) VALUES(?,?,?,?,?,?,?,?)`,
				fid, row.Admission, row.Roll, rollSort(row.Roll), row.Name, group, row.Class, row.Section); err != nil {
				row.Status, row.Note = "invalid", "Could not save (duplicate admission number?)"
			} else {
				added++
			}
		}
		rows = append(rows, row)
	}
	if len(rows) == 0 {
		return bad("No students found. Each row needs a name and a class/section that exists (e.g. 5TH A).")
	}
	writeJSON(w, 200, map[string]any{"rows": rows, "dry": dry, "added": added})
	return nil
}
