package main

import (
	"net/http"
	"sort"
	"strconv"
	"time"
)

// Performance tags, by percentage of the test's maximum marks.
const (
	belowAvgBelow = 40.0 // < 40%  => Below Average
	excellentFrom = 75.0 // >= 75% => Excellent; everything between is Average
)

func perfTag(pct float64) string {
	switch {
	case pct < belowAvgBelow:
		return "below"
	case pct >= excellentFrom:
		return "excellent"
	}
	return "average"
}

type tagCount struct {
	Label     string  `json:"label"`
	Below     int     `json:"below"`
	Average   int     `json:"average"`
	Excellent int     `json:"excellent"`
	Absent    int     `json:"absent"`
	Avg       float64 `json:"avg"`
	sum       float64
}

func (t *tagCount) add(pct float64) {
	switch perfTag(pct) {
	case "below":
		t.Below++
	case "excellent":
		t.Excellent++
	default:
		t.Average++
	}
	t.sum += pct
}

func (t *tagCount) finish() {
	if n := t.Below + t.Average + t.Excellent; n > 0 {
		t.Avg = float64(int(t.sum/float64(n)*10+0.5)) / 10
	}
}

// adminAnalysis summarises tests locked in the last N days (default 7): tags per class, subject, teacher and day.
func (a *App) adminAnalysis(w http.ResponseWriter, r *http.Request, u *User) error {
	days, _ := strconv.Atoi(r.URL.Query().Get("days"))
	if days < 1 || days > 366 {
		days = 7
	}
	from := time.Now().UTC().AddDate(0, 0, -days)
	rows, err := a.db.Query(`SELECT e.id, e.class_name, e.section, e.category, e.exam_date, e.locked_at, COALESCE(u.name,''),
			r.subject, r.status, r.total, e.max_total, s.name, s.roll_no
		FROM exams e JOIN results r ON r.exam_id=e.id JOIN students s ON s.id=r.student_id LEFT JOIN users u ON u.id=e.owner_id
		WHERE e.status='locked' AND e.locked_at>=? ORDER BY e.locked_at`, from.Format(time.RFC3339))
	if err != nil {
		return err
	}
	defer rows.Close()

	overall := &tagCount{Label: "All"}
	byClass := map[string]*tagCount{}
	bySubject := map[string]*tagCount{}
	byTeacher := map[string]*tagCount{}
	teacherTests := map[string]map[int64]bool{}
	tests := map[int64]bool{}
	perDay := map[string]map[int64]bool{}
	type weak struct {
		Name    string  `json:"name"`
		Roll    string  `json:"roll"`
		Class   string  `json:"class"`
		Subject string  `json:"subject"`
		Pct     float64 `json:"pct"`
	}
	var weakList []weak
	get := func(m map[string]*tagCount, k string) *tagCount {
		if m[k] == nil {
			m[k] = &tagCount{Label: k}
		}
		return m[k]
	}
	for rows.Next() {
		var eid int64
		var cl, sec, cat, date, lockedAt, owner, subject, status, sname, roll string
		var total *float64
		var max float64
		rows.Scan(&eid, &cl, &sec, &cat, &date, &lockedAt, &owner, &subject, &status, &total, &max, &sname, &roll)
		label := groupLabel(cl, sec)
		tests[eid] = true
		if teacherTests[owner] == nil {
			teacherTests[owner] = map[int64]bool{}
		}
		teacherTests[owner][eid] = true
		day := lockedAt
		if len(day) >= 10 {
			day = day[:10]
		}
		if perDay[day] == nil {
			perDay[day] = map[int64]bool{}
		}
		perDay[day][eid] = true
		if status != "" || total == nil || max <= 0 {
			for _, t := range []*tagCount{overall, get(byClass, label), get(bySubject, subject), get(byTeacher, owner)} {
				t.Absent++
			}
			continue
		}
		pct := *total / max * 100
		for _, t := range []*tagCount{overall, get(byClass, label), get(bySubject, subject), get(byTeacher, owner)} {
			t.add(pct)
		}
		if perfTag(pct) == "below" {
			weakList = append(weakList, weak{sname, roll, label, subject, float64(int(pct*10+0.5)) / 10})
		}
	}
	flat := func(m map[string]*tagCount) []*tagCount {
		out := make([]*tagCount, 0, len(m))
		for _, t := range m {
			t.finish()
			out = append(out, t)
		}
		sort.Slice(out, func(i, j int) bool { return out[i].Label < out[j].Label })
		return out
	}
	overall.finish()
	teachers := []map[string]any{}
	for _, t := range flat(byTeacher) {
		teachers = append(teachers, map[string]any{"name": t.Label, "tests": len(teacherTests[t.Label]), "avg": t.Avg,
			"below": t.Below, "average": t.Average, "excellent": t.Excellent})
	}
	daily := []map[string]any{}
	for i := days - 1; i >= 0; i-- {
		d := time.Now().UTC().AddDate(0, 0, -i).Format("2006-01-02")
		daily = append(daily, map[string]any{"date": d, "tests": len(perDay[d])})
	}
	sort.Slice(weakList, func(i, j int) bool { return weakList[i].Pct < weakList[j].Pct })
	totalWeak := len(weakList)
	if len(weakList) > 40 {
		weakList = weakList[:40]
	}
	if weakList == nil {
		weakList = []weak{}
	}
	writeJSON(w, 200, map[string]any{
		"days": days, "tests": len(tests), "overall": overall, "by_class": flat(byClass), "by_subject": flat(bySubject),
		"teachers": teachers, "daily": daily, "weak": weakList, "weak_total": totalWeak,
		"rules": map[string]any{"below_under": belowAvgBelow, "excellent_from": excellentFrom},
	})
	return nil
}
