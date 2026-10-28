package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
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
	down  bool
}

func newMock() *mockFrappe {
	m := &mockFrappe{docs: map[string]map[string]frappeDoc{}, order: map[string][]string{}, seq: map[string]int{}}
	for _, c := range demoCourses {
		m.put("Course", frappeDoc{"name": c, "course_name": c})
	}
	m.put("Assessment Group", frappeDoc{"name": "All Assessment Groups", "is_group": 1})
	for _, g := range demoGroups {
		var kids []any
		for _, s := range demoRoster(g.Code, g.N) {
			m.put("Student", frappeDoc{"name": s.ID, "student_name": s.Name, "admission_number": s.Admission})
			kids = append(kids, map[string]any{"student": s.ID, "student_name": s.Name, "group_roll_number": s.Roll, "active": 1})
		}
		m.put("Student Group", frappeDoc{"name": g.Name, "student_group_name": g.Name, "disabled": 0, "students": kids})
	}
	m.put("Employee", frappeDoc{"name": "EMP-0001", "user_id": "teacher.a@school.test"})
	m.put("Instructor", frappeDoc{"name": "INS-0001", "employee": "EMP-0001", "instructor_log": []any{
		map[string]any{"student_group": "Grade 8-A", "course": "Hindi"},
		map[string]any{"student_group": "Grade 8-A", "course": "Punjabi"},
		map[string]any{"student_group": "Grade 8-A", "course": "Mathematics"},
		map[string]any{"student_group": "Grade 10-A", "course": "Mathematics"}}})
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
		switch dt {
		case "Assessment Group":
			d["name"] = str(d, "assessment_group_name")
		case "Assessment Criteria":
			d["name"] = str(d, "assessment_criteria")
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
		if msg := m.validate(dt, in); msg != "" {
			jerr(w, 417, msg)
			return
		}
		for k, v := range in {
			d[k] = v
		}
		writeJSON(w, 200, map[string]any{"data": d})
	default:
		jerr(w, 405, "method not allowed")
	}
}

func (m *mockFrappe) validate(dt string, d frappeDoc) string {
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
