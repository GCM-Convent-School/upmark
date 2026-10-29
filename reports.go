package main

import (
	"net/http"
	"sort"
	"strconv"
	"time"
)

type reportTest struct {
	Date     string  `json:"date"`
	Test     string  `json:"test"`
	Subject  string  `json:"subject"`
	Status   string  `json:"status"` // "", AB, EX
	Score    float64 `json:"score"`
	Max      float64 `json:"max"`
	Pct      float64 `json:"pct"`
	ClassAvg float64 `json:"class_avg"`
	Tag      string  `json:"tag"`
}

type reportSubject struct {
	Subject  string  `json:"subject"`
	Avg      float64 `json:"avg"`
	ClassAvg float64 `json:"class_avg"`
	Tests    int     `json:"tests"`
	Tag      string  `json:"tag"`
	sum, cs  float64
	n        int
}

type studentReport struct {
	ID        int64            `json:"id"`
	Name      string           `json:"name"`
	Roll      string           `json:"roll_no"`
	Admission string           `json:"admission_no"`
	Father    string           `json:"father_name"`
	Mother    string           `json:"mother_name"`
	Avg       float64          `json:"avg"`
	Tag       string           `json:"tag"`
	Tests     int              `json:"tests"`
	Absent    int              `json:"absent"`
	Subjects  []*reportSubject `json:"subjects"`
	Detail    []reportTest     `json:"detail"`
}

func round1(f float64) float64 { return float64(int(f*10+0.5)) / 10 }

// apiStudentReports builds weekly (or N-day) progress reports for one class, or one student of it.
// Only approved tests count. Teachers see just the subjects they are assigned to.
func (a *App) apiStudentReports(w http.ResponseWriter, r *http.Request, u *User) error {
	q := r.URL.Query()
	class, section := q.Get("class_name"), q.Get("section")
	days, _ := strconv.Atoi(q.Get("days"))
	if days < 1 || days > 366 {
		days = 7
	}
	only, _ := strconv.ParseInt(q.Get("student_id"), 10, 64)
	set, err := a.allowedSubjects(u, class, section)
	if err != nil {
		return err
	}
	if set != nil && len(set) == 0 {
		return forbidden("This class is not assigned to you")
	}
	to := time.Now()
	from := to.AddDate(0, 0, -(days - 1))
	fromS, toS := from.Format("2006-01-02"), to.Format("2006-01-02")

	// class averages per test+subject (absent / exempt excluded)
	type key struct {
		exam    int64
		subject string
	}
	classAvg := map[key]float64{}
	cr, err := a.db.Query(`SELECT r.exam_id, r.subject, AVG(r.total*100.0/e.max_total) FROM results r JOIN exams e ON e.id=r.exam_id
		WHERE e.class_name=? AND e.section=? AND e.approved_at<>'' AND e.exam_date>=? AND e.exam_date<=? AND r.status='' AND r.total IS NOT NULL AND e.max_total>0
		GROUP BY r.exam_id, r.subject`, class, section, fromS, toS)
	if err != nil {
		return err
	}
	for cr.Next() {
		var k key
		var v float64
		cr.Scan(&k.exam, &k.subject, &v)
		classAvg[k] = v
	}
	cr.Close()

	reports := map[int64]*studentReport{}
	order := []int64{}
	sr, err := a.db.Query(`SELECT id,name,roll_no,admission_no,father_name,mother_name FROM students WHERE class_name=? AND section=? AND active=1 ORDER BY roll_sort,name`, class, section)
	if err != nil {
		return err
	}
	for sr.Next() {
		x := &studentReport{Subjects: []*reportSubject{}, Detail: []reportTest{}}
		sr.Scan(&x.ID, &x.Name, &x.Roll, &x.Admission, &x.Father, &x.Mother)
		if only != 0 && x.ID != only {
			continue
		}
		reports[x.ID] = x
		order = append(order, x.ID)
	}
	sr.Close()

	rr, err := a.db.Query(`SELECT r.student_id, r.exam_id, e.exam_date, e.sub_type, e.topic, r.subject, r.status, r.total, e.max_total
		FROM results r JOIN exams e ON e.id=r.exam_id WHERE e.class_name=? AND e.section=? AND e.approved_at<>'' AND e.exam_date>=? AND e.exam_date<=?
		ORDER BY e.exam_date, e.id, r.subject`, class, section, fromS, toS)
	if err != nil {
		return err
	}
	defer rr.Close()
	subj := map[int64]map[string]*reportSubject{}
	sums := map[int64]float64{}
	counts := map[int64]int{}
	for rr.Next() {
		var sid, eid int64
		var date, sub, topic, subject, status string
		var total *float64
		var max float64
		rr.Scan(&sid, &eid, &date, &sub, &topic, &subject, &status, &total, &max)
		x := reports[sid]
		if x == nil || !can(set, subject) {
			continue
		}
		t := reportTest{Date: date, Test: sub, Subject: subject, Status: status, Max: max}
		if topic != "" {
			t.Test += " (" + topic + ")"
		}
		if ca, ok := classAvg[key{eid, subject}]; ok {
			t.ClassAvg = round1(ca)
		}
		if status != "" || total == nil || max <= 0 {
			x.Absent++
			x.Detail = append(x.Detail, t)
			continue
		}
		t.Score, t.Pct = *total, round1(*total/max*100)
		t.Tag = perfTag(*total / max * 100)
		x.Tests++
		x.Detail = append(x.Detail, t)
		sums[sid] += *total / max * 100
		counts[sid]++
		if subj[sid] == nil {
			subj[sid] = map[string]*reportSubject{}
		}
		s := subj[sid][subject]
		if s == nil {
			s = &reportSubject{Subject: subject}
			subj[sid][subject] = s
			x.Subjects = append(x.Subjects, s)
		}
		s.sum += *total / max * 100
		s.cs += classAvg[key{eid, subject}]
		s.n++
	}
	out := []*studentReport{}
	for _, id := range order {
		x := reports[id]
		if counts[id] > 0 {
			x.Avg = round1(sums[id] / float64(counts[id]))
			x.Tag = perfTag(x.Avg)
		}
		for _, s := range x.Subjects {
			s.Avg, s.ClassAvg, s.Tests = round1(s.sum/float64(s.n)), round1(s.cs/float64(s.n)), s.n
			s.Tag = perfTag(s.Avg)
		}
		sort.Slice(x.Subjects, func(i, j int) bool { return x.Subjects[i].Subject < x.Subjects[j].Subject })
		out = append(out, x)
	}
	writeJSON(w, 200, map[string]any{"students": out, "from": fromS, "to": toS, "days": days, "class": groupLabel(class, section),
		"rules": map[string]any{"below_under": belowAvgBelow, "excellent_from": excellentFrom}})
	return nil
}
