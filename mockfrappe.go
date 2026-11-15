package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
)

// mockFrappe is a tiny in-memory stand-in for a Frappe Education site, used for local development and
// automated end-to-end tests (MOCK_FRAPPE_ADDR=:8099). It models just enough of the REST surface that
// the sync worker uses, including docstatus transitions and score validation.
type mockFrappe struct {
	mu    sync.Mutex
	docs  map[string]map[string]frappeDoc
	order map[string][]string
	seq   map[string]int
	files map[string][]byte // private file contents by file_url
	down  bool
}

func newMock() *mockFrappe {
	m := &mockFrappe{docs: map[string]map[string]frappeDoc{}, order: map[string][]string{}, seq: map[string]int{}, files: map[string][]byte{}}
	for _, c := range demoCourses {
		m.put("Course", frappeDoc{"name": c, "course_name": c})
	}
	m.put("Assessment Group", frappeDoc{"name": "All Assessment Groups", "is_group": 1})
	for _, g := range demoGroups {
		var kids []any
		for _, s := range demoRoster(g.Code, g.N) {
			m.put("Student", frappeDoc{"name": s.ID, "student_name": s.Name, "admission_number": s.Admission,
				"custom_father_mobile_number": s.FatherMobile, "custom_mother_mobile_number": s.MotherMobile})
			kids = append(kids, map[string]any{"student": s.ID, "student_name": s.Name, "group_roll_number": s.Roll, "active": 1})
		}
		prog, _ := splitGroup(g.Name)
		m.put("Program", frappeDoc{"name": prog})
		m.put("Student Group", frappeDoc{"name": g.Name, "student_group_name": g.Name, "program": prog, "disabled": 0, "students": kids})
	}
	// merits and demerits, as installed by ERP-Next "merits and demerits/setup_conduct_points.py"
	m.put("Education Settings", frappeDoc{"name": "Education Settings", "current_academic_year": "2026-27"})
	for _, c := range defaultConductCategories {
		m.put("Conduct Category", frappeDoc{"name": c.Name, "category_name": c.Name, "point_type": c.Type, "default_points": float64(c.Default), "enabled": float64(1), "description": c.Description})
	}
	m.put("User", frappeDoc{"name": "teacher.a@school.test", "email": "teacher.a@school.test"})
	// Employees cover every department; teachers are the ones with an Instructor
	m.put("Company", frappeDoc{"name": "GCM Convent School"})
	for _, d := range []string{"All Departments", "Marketing - GCM", "Teaching - GCM"} {
		m.put("Department", frappeDoc{"name": d})
	}
	m.put("Employee", frappeDoc{"name": "EMP-0001", "employee_name": "Teacher A (Demo)", "user_id": "teacher.a@school.test", "cell_number": "+91 90000 00001", "status": "Active",
		"company": "GCM Convent School", "gender": "Female", "date_of_birth": "1990-05-05", "date_of_joining": "2020-04-01"})
	m.put("Instructor", frappeDoc{"name": "INS-0001", "instructor_name": "Teacher A (Demo)", "status": "Active", "employee": "EMP-0001", "instructor_log": []any{
		map[string]any{"student_group": "Grade 8-A", "course": "Hindi", "academic_year": "2026-27", "program": "Grade 8"},
		map[string]any{"student_group": "Grade 8-A", "course": "Punjabi", "academic_year": "2026-27", "program": "Grade 8"},
		map[string]any{"student_group": "Grade 8-A", "course": "Mathematics", "academic_year": "2026-27", "program": "Grade 8"},
		map[string]any{"student_group": "Grade 10-A", "course": "Mathematics", "academic_year": "2026-27", "program": "Grade 10"}}})
	return m
}

func (m *mockFrappe) put(dt string, d frappeDoc) {
	if m.docs[dt] == nil {
		m.docs[dt] = map[string]frappeDoc{}
	}
	n := str(d, "name")
	if _, ok := m.docs[dt][n]; !ok {
		m.order[dt] = append(m.order[dt], n)
	}
	if _, ok := d["docstatus"]; !ok {
		d["docstatus"] = float64(0)
	}
	m.docs[dt][n] = d
}

func jerr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]any{"exception": msg, "_server_messages": msg})
}

func matches(d frappeDoc, filters [][]any) bool {
	for _, f := range filters {
		if len(f) < 3 {
			continue
		}
		field, op, want := fmt.Sprint(f[0]), fmt.Sprint(f[1]), f[2]
		got := d[field]
		switch op {
		case "=":
			if fmt.Sprint(got) != fmt.Sprint(want) {
				if gf, ok := got.(float64); ok {
					if wf, ok := want.(float64); ok && gf == wf {
						continue
					}
				}
				return false
			}
		case "like":
			pat := "^" + strings.ReplaceAll(regexp.QuoteMeta(fmt.Sprint(want)), "%", ".*") + "$"
			if ok, _ := regexp.MatchString(pat, fmt.Sprint(got)); !ok {
				return false
			}
		case "<":
			gf, _ := got.(float64)
			wf, _ := want.(float64)
			if !(gf < wf) {
				return false
			}
		}
	}
	return true
}

func (m *mockFrappe) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p := r.URL.Path
	switch {
	case p == "/__down":
		m.down = true
		writeJSON(w, 200, map[string]any{"down": true})
		return
	case p == "/__up":
		m.down = false
		writeJSON(w, 200, map[string]any{"down": false})
		return
	case p == "/__addstudent":
		var in struct{ Group, ID, Name, Roll, Admission string }
		json.NewDecoder(r.Body).Decode(&in)
		g := m.docs["Student Group"][in.Group]
		kids, _ := g["students"].([]any)
		g["students"] = append(kids, map[string]any{"student": in.ID, "student_name": in.Name, "group_roll_number": in.Roll, "active": 1})
		m.put("Student", frappeDoc{"name": in.ID, "student_name": in.Name, "admission_number": in.Admission})
		writeJSON(w, 200, map[string]any{"ok": true})
		return
	case p == "/__dump":
		dt := r.URL.Query().Get("doctype")
		var out []frappeDoc
		for _, n := range m.order[dt] {
			out = append(out, m.docs[dt][n])
		}
		writeJSON(w, 200, out)
		return
	}
	if m.down {
		jerr(w, 503, "mock frappe is down")
		return
	}
	if p == "/api/method/upload_file" {
		if err := r.ParseMultipartForm(64 << 20); err != nil {
			jerr(w, 400, err.Error())
			return
		}
		f, hdr, err := r.FormFile("file")
		if err != nil {
			jerr(w, 417, "No file attached")
			return
		}
		data, _ := io.ReadAll(f)
		name := r.FormValue("file_name")
		if name == "" {
			name = hdr.Filename
		}
		m.seq["File"]++
		dir := "/files/"
		if r.FormValue("is_private") == "1" {
			dir = "/private/files/"
		}
		d := frappeDoc{"name": fmt.Sprintf("FILE-%05d", m.seq["File"]), "file_name": name, "file_url": dir + name, "is_private": r.FormValue("is_private"), "file_size": float64(len(data))}
		m.files[dir+name] = data
		m.put("File", d)
		writeJSON(w, 200, map[string]any{"message": d})
		return
	}
	if strings.HasPrefix(p, "/private/files/") || strings.HasPrefix(p, "/files/") {
		data, ok := m.files[p]
		if !ok {
			jerr(w, 404, "file not found")
			return
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Write(data)
		return
	}
	if p == "/api/method/frappe.client.submit" || p == "/api/method/frappe.client.cancel" {
		var in struct {
			Doc           frappeDoc
			Doctype, Name string
		}
		json.NewDecoder(r.Body).Decode(&in)
		dt, name := in.Doctype, in.Name
		if in.Doc != nil {
			dt, name = str(in.Doc, "doctype"), str(in.Doc, "name")
		}
		d := m.docs[dt][name]
		if d == nil {
			jerr(w, 404, "not found")
			return
		}
		if strings.HasSuffix(p, "submit") {
			if docstatus(d) != 0 {
				jerr(w, 417, "Cannot submit a document that is not in draft")
				return
			}
			d["docstatus"] = float64(1)
		} else {
			if docstatus(d) != 1 {
				jerr(w, 417, "Only submitted documents can be cancelled")
				return
			}
			d["docstatus"] = float64(2)
		}
		writeJSON(w, 200, map[string]any{"message": d})
		return
	}
	rest, ok := strings.CutPrefix(p, "/api/resource/")
	if !ok {
		jerr(w, 404, "no such route")
		return
	}
	parts := strings.SplitN(rest, "/", 2)
	dt, _ := url.PathUnescape(parts[0])
	name := ""
	if len(parts) == 2 {
		name, _ = url.PathUnescape(parts[1])
	}
	switch {
	case r.Method == "GET" && name == "":
		var filters [][]any
		json.Unmarshal([]byte(r.URL.Query().Get("filters")), &filters)
		var fields []string
		json.Unmarshal([]byte(r.URL.Query().Get("fields")), &fields)
		out := []frappeDoc{}
		for _, n := range m.order[dt] {
			d := m.docs[dt][n]
			if !matches(d, filters) {
				continue
			}
			row := frappeDoc{"name": n}
			for _, f := range fields {
				row[f] = d[f]
			}
			out = append(out, row)
		}
		writeJSON(w, 200, map[string]any{"data": out})
	case r.Method == "GET":
		d := m.docs[dt][name]
		if d == nil {
			jerr(w, 404, dt+" "+name+" not found")
			return
		}
		writeJSON(w, 200, map[string]any{"data": d})
	case r.Method == "POST":
		var d frappeDoc
		json.NewDecoder(r.Body).Decode(&d)
		if msg := m.validate(dt, d); msg != "" {
			jerr(w, 417, msg)
			return
		}
		if dt == "Assessment Plan" { // as in education.utils.validate_overlap
			f1, _ := clockMin(d["from_time"])
			t1, _ := clockMin(d["to_time"])
			for _, n := range m.order[dt] {
				o := m.docs[dt][n]
				f2, _ := clockMin(o["from_time"])
				t2, _ := clockMin(o["to_time"])
				if docstatus(o) < 2 && str(o, "student_group") == str(d, "student_group") && str(o, "schedule_date") == str(d, "schedule_date") && f1 < t2 && f2 < t1 {
					jerr(w, 417, "education.education.utils.OverlapError: This Assessment Plan conflicts with "+n+" for Student Group "+str(d, "student_group"))
					return
				}
			}
		}
		switch dt {
		case "Assessment Group":
			d["name"] = str(d, "assessment_group_name")
		case "Assessment Criteria":
			d["name"] = str(d, "assessment_criteria")
		case "Student Conduct Point":
			m.seq[dt]++
			d["name"] = fmt.Sprintf("SCP-2026-%05d", m.seq[dt])
			if c := m.docs["Conduct Category"][str(d, "category")]; c != nil {
				d["point_type"] = c["point_type"]
			}
			if str(d, "awarded_by") == "" {
				d["awarded_by"] = "api@school.test"
			}
		default:
			m.seq[dt]++
			d["name"] = fmt.Sprintf("%s-%04d", strings.ToUpper(strings.ReplaceAll(dt, " ", ""))[:6], m.seq[dt])
			if from := str(d, "amended_from"); from != "" {
				d["name"] = from + "-1"
			}
		}
		if _, exists := m.docs[dt][str(d, "name")]; exists {
			jerr(w, 409, "Duplicate entry "+str(d, "name"))
			return
		}
		d["docstatus"] = float64(0)
		m.put(dt, d)
		writeJSON(w, 200, map[string]any{"data": d})
	case r.Method == "PUT":
		d := m.docs[dt][name]
		if d == nil {
			jerr(w, 404, "not found")
			return
		}
		if docstatus(d) != 0 {
			jerr(w, 417, "Cannot edit a submitted document")
			return
		}
		var in frappeDoc
		json.NewDecoder(r.Body).Decode(&in)
		check := in
		if dt == "Employee" { // mandatory fields are checked on the whole record
			check = frappeDoc{}
			for k, v := range d {
				check[k] = v
			}
			for k, v := range in {
				check[k] = v
			}
		}
		if msg := m.validate(dt, check); msg != "" {
			jerr(w, 417, msg)
			return
		}
		for k, v := range in {
			d[k] = v
		}
		if dt == "Employee" {
			d["employee_name"] = check["employee_name"]
		}
		writeJSON(w, 200, map[string]any{"data": d})
	case r.Method == "DELETE":
		if m.docs[dt][name] == nil {
			jerr(w, 404, dt+" "+name+" not found")
			return
		}
		delete(m.docs[dt], name)
		for i, n := range m.order[dt] {
			if n == name {
				m.order[dt] = append(m.order[dt][:i], m.order[dt][i+1:]...)
				break
			}
		}
		writeJSON(w, 202, map[string]any{"message": "ok"})
	default:
		jerr(w, 405, "method not allowed")
	}
}

func (m *mockFrappe) validate(dt string, d frappeDoc) string {
	if dt == "Employee" { // as HRMS: these are mandatory, and links must exist
		for _, f := range []string{"first_name", "gender", "date_of_birth", "date_of_joining", "company"} {
			if str(d, f) == "" {
				return "frappe.exceptions.MandatoryError: [Employee]: " + f
			}
		}
		if m.docs["Company"][str(d, "company")] == nil {
			return "Could not find Company: " + str(d, "company")
		}
		if dep := str(d, "department"); dep != "" && m.docs["Department"][dep] == nil {
			return "Could not find Department: " + dep
		}
		d["employee_name"] = strings.TrimSpace(str(d, "first_name") + " " + str(d, "last_name"))
		return ""
	}
	if dt == "Instructor" { // Instructor Log rows need an academic year
		if e := str(d, "employee"); e != "" && m.docs["Employee"][e] == nil {
			return "Could not find Employee: " + e
		}
		if st, ok := d["status"]; ok && st != "Active" && st != "Left" {
			return "Status must be Active or Left"
		}
		rows, _ := d["instructor_log"].([]any)
		for _, r := range rows {
			row, _ := r.(map[string]any)
			if str(row, "academic_year") == "" {
				return "Instructor Log: Academic Year is mandatory"
			}
			if str(row, "program") == "" { // as the school's SIS
				return "frappe.exceptions.MandatoryError: [Instructor]: program"
			}
		}
		return ""
	}
	if dt == "Student Conduct Point" {
		if m.docs["Student"][str(d, "student")] == nil {
			return "Could not find Student: " + str(d, "student")
		}
		if m.docs["Conduct Category"][str(d, "category")] == nil {
			return "Could not find Category: " + str(d, "category")
		}
		if p, _ := d["points"].(float64); p <= 0 {
			return "Points must be greater than zero"
		}
		if u := str(d, "awarded_by"); u != "" && m.docs["User"][u] == nil {
			return "Could not find Awarded By: " + u
		}
		return ""
	}
	if dt != "Assessment Result" {
		return ""
	}
	rows, _ := d["details"].([]any)
	for _, r := range rows {
		row, _ := r.(map[string]any)
		s, _ := row["score"].(float64)
		mx, _ := row["maximum_score"].(float64)
		if s > mx {
			return "Score cannot exceed maximum score"
		}
	}
	return ""
}

func runMockFrappe(addr string) {
	log.Printf("mock frappe listening on %s", addr)
	if err := http.ListenAndServe(addr, newMock()); err != nil {
		log.Printf("mock frappe: %v", err)
	}
}
