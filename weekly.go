package main

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"net"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
)

// Parents read an automatic weekly report: every approved test of the week (Monday to Sunday), the child's
// average, and where that average sits in their own class section (percentile). No WhatsApp sender is needed.

func weekStart(t time.Time) time.Time {
	t = time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.Local)
	return t.AddDate(0, 0, -((int(t.Weekday()) + 6) % 7))
}

const dayFmt = "2006-01-02"

// termStart is the start of the academic year (1 April).
func termStart(t time.Time) time.Time {
	y := t.Year()
	if t.Month() < time.April {
		y--
	}
	return time.Date(y, time.April, 1, 0, 0, 0, 0, time.Local)
}

type weekTest struct {
	Date     string  `json:"date"`
	Test     string  `json:"test"`
	Subject  string  `json:"subject"`
	Status   string  `json:"status"`
	Score    float64 `json:"score"`
	Max      float64 `json:"max"`
	Pct      float64 `json:"pct"`
	ClassAvg float64 `json:"class_avg"`
}

type weekSubject struct {
	Subject  string  `json:"subject"`
	Pct      float64 `json:"pct"`
	ClassAvg float64 `json:"class_avg"`
	sum, cs  float64
	n        int
}

// classWeek holds one class section's approved results for one week.
type classWeek struct {
	avg   map[int64]float64 // student -> average % over scored tests
	tests map[int64][]weekTest
}

func (a *App) classWeek(class, section string, from time.Time) (*classWeek, error) {
	to := from.AddDate(0, 0, 6)
	rows, err := a.db.Query(`SELECT r.student_id, r.exam_id, e.exam_date, e.sub_type, e.topic, r.subject, r.status, r.total, e.max_total
		FROM results r JOIN exams e ON e.id=r.exam_id
		WHERE e.class_name=? AND e.section=? AND e.approved_at<>'' AND e.exam_date>=? AND e.exam_date<=?
		ORDER BY e.exam_date, e.id, r.subject`, class, section, from.Format(dayFmt), to.Format(dayFmt))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	type key struct {
		exam int64
		subj string
	}
	cw := &classWeek{avg: map[int64]float64{}, tests: map[int64][]weekTest{}}
	sum, cnt := map[int64]float64{}, map[int64]int{}
	csum, ccnt := map[key]float64{}, map[key]int{}
	type ref struct {
		sid int64
		i   int
		k   key
	}
	var refs []ref
	for rows.Next() {
		var sid, eid int64
		var date, sub, topic, subj, status string
		var total sql.NullFloat64
		var max float64
		rows.Scan(&sid, &eid, &date, &sub, &topic, &subj, &status, &total, &max)
		t := weekTest{Date: date, Test: sub, Subject: subj, Status: status, Max: max}
		if topic != "" {
			t.Test += " · " + topic
		}
		k := key{eid, subj}
		if status == "" && total.Valid && max > 0 {
			t.Score, t.Pct = total.Float64, round1(total.Float64/max*100)
			sum[sid] += total.Float64 / max * 100
			cnt[sid]++
			csum[k] += total.Float64 / max * 100
			ccnt[k]++
		}
		cw.tests[sid] = append(cw.tests[sid], t)
		refs = append(refs, ref{sid, len(cw.tests[sid]) - 1, k})
	}
	for _, r := range refs {
		if ccnt[r.k] > 0 {
			cw.tests[r.sid][r.i].ClassAvg = round1(csum[r.k] / float64(ccnt[r.k]))
		}
	}
	for sid, n := range cnt {
		cw.avg[sid] = sum[sid] / float64(n)
	}
	return cw, rows.Err()
}

func (cw *classWeek) percentile(sid int64) (float64, bool) {
	x, ok := cw.avg[sid]
	if !ok {
		return 0, false
	}
	all := make([]float64, 0, len(cw.avg))
	for _, v := range cw.avg {
		all = append(all, v)
	}
	return percentileRank(x, all), true
}

// ---------- parent sign-in: admission number + last four digits of a parent's mobile ----------

var parentTries = struct {
	sync.Mutex
	m map[string][]time.Time
}{m: map[string][]time.Time{}}

// allowTry is a light brake on guessing: 30 sign-in attempts per address per 10 minutes.
func allowTry(r *http.Request) bool {
	ip, _, _ := net.SplitHostPort(r.RemoteAddr)
	if f := r.Header.Get("X-Forwarded-For"); f != "" {
		ip = strings.TrimSpace(strings.Split(f, ",")[0])
	}
	parentTries.Lock()
	defer parentTries.Unlock()
	cut := time.Now().Add(-10 * time.Minute)
	keep := parentTries.m[ip][:0]
	for _, t := range parentTries.m[ip] {
		if t.After(cut) {
			keep = append(keep, t)
		}
	}
	if len(keep) >= 30 {
		parentTries.m[ip] = keep
		return false
	}
	parentTries.m[ip] = append(keep, time.Now())
	return true
}

func (a *App) parentRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/parent/login", a.parentLogin)
	mux.HandleFunc("POST /api/parent/logout", a.parentWrap(func(w http.ResponseWriter, r *http.Request, sid int64) error {
		if c, err := r.Cookie("rea_parent"); err == nil {
			a.db.Exec(`DELETE FROM parent_sessions WHERE token_hash=?`, sha(c.Value))
		}
		http.SetCookie(w, &http.Cookie{Name: "rea_parent", Value: "", Path: "/", MaxAge: -1, HttpOnly: true})
		writeJSON(w, 200, map[string]any{"ok": true})
		return nil
	}))
	mux.HandleFunc("GET /api/parent/report", a.parentWrap(a.parentReport))
	mux.HandleFunc("POST /api/parent/seen", a.parentWrap(a.parentSeen))
}

func (a *App) parentLogin(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("X-Requested-With") != "rea" {
		writeErr(w, forbidden("missing CSRF header"))
		return
	}
	var in struct {
		Admission string `json:"admission_no"`
		Last4     string `json:"last4"`
	}
	if err := decode(r, &in); err != nil {
		writeErr(w, err)
		return
	}
	if !allowTry(r) {
		writeErr(w, &apiErr{Code: 429, Msg: "Too many tries. Please wait a few minutes."})
		return
	}
	adm := strings.ToUpper(strings.TrimSpace(in.Admission))
	last4 := strings.TrimSpace(in.Last4)
	if adm == "" || len(last4) != 4 || strings.Trim(last4, "0123456789") != "" {
		writeErr(w, bad("Enter the admission number and the last 4 digits of a parent's mobile number"))
		return
	}
	rows, err := a.db.Query(`SELECT id, father_mobile, mother_mobile FROM students WHERE UPPER(TRIM(admission_no))=? AND active=1`, adm)
	if err != nil {
		writeErr(w, err)
		return
	}
	var sid int64
	for rows.Next() {
		var id int64
		var fm, mm string
		rows.Scan(&id, &fm, &mm)
		for _, m := range []string{normIdent(fm), normIdent(mm)} {
			if len(m) >= 4 && m[len(m)-4:] == last4 {
				sid = id
			}
		}
	}
	rows.Close()
	if sid == 0 {
		writeErr(w, &apiErr{Code: 401, Msg: "These details do not match the school's records. Check the admission number and use the mobile number the school has on file."})
		return
	}
	tok := randHex(32)
	exp := time.Now().Add(30 * 24 * time.Hour)
	if _, err := a.db.Exec(`INSERT INTO parent_sessions(token_hash,student_id,expires_at) VALUES(?,?,?)`, sha(tok), sid, exp.UTC().Format(time.RFC3339)); err != nil {
		writeErr(w, err)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: "rea_parent", Value: tok, Path: "/", HttpOnly: true, SameSite: http.SameSiteLaxMode,
		Secure: a.cfg.CookieSecure, MaxAge: 30 * 24 * 3600})
	writeJSON(w, 200, map[string]any{"ok": true})
}

func (a *App) parentWrap(h func(http.ResponseWriter, *http.Request, int64) error) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Header.Get("X-Requested-With") != "rea" {
			writeErr(w, forbidden("missing CSRF header"))
			return
		}
		c, err := r.Cookie("rea_parent")
		var sid int64
		var exp string
		if err == nil {
			a.db.QueryRow(`SELECT student_id, expires_at FROM parent_sessions WHERE token_hash=?`, sha(c.Value)).Scan(&sid, &exp)
		}
		if t, _ := time.Parse(time.RFC3339, exp); sid == 0 || time.Now().After(t) {
			writeErr(w, &apiErr{Code: 401, Msg: "Please sign in"})
			return
		}
		if err := h(w, r, sid); err != nil {
			writeErr(w, err)
		}
	}
}

// parentReport returns one week's report for the signed-in child plus the term trend of weekly percentiles.
func (a *App) parentReport(w http.ResponseWriter, r *http.Request, sid int64) error {
	var name, roll, adm, class, section, father, mother string
	if err := a.db.QueryRow(`SELECT name, roll_no, admission_no, class_name, section, father_name, mother_name FROM students WHERE id=?`, sid).
		Scan(&name, &roll, &adm, &class, &section, &father, &mother); err != nil {
		return notFound("Student not found")
	}
	now := time.Now()
	thisWeek := weekStart(now)
	// weeks of this term in which the class has approved tests
	rows, err := a.db.Query(`SELECT DISTINCT exam_date FROM exams WHERE class_name=? AND section=? AND approved_at<>'' AND exam_date>=? ORDER BY exam_date`,
		class, section, termStart(now).Format(dayFmt))
	if err != nil {
		return err
	}
	seenWeek := map[string]bool{}
	var weeks []time.Time
	for rows.Next() {
		var d string
		rows.Scan(&d)
		if t, err := time.ParseInLocation(dayFmt, d, time.Local); err == nil {
			ws := weekStart(t)
			if !seenWeek[ws.Format(dayFmt)] {
				seenWeek[ws.Format(dayFmt)] = true
				weeks = append(weeks, ws)
			}
		}
	}
	rows.Close()

	type trendPt struct {
		Week       string   `json:"week"`
		Avg        *float64 `json:"avg"`
		Percentile *float64 `json:"percentile"`
	}
	trend := []trendPt{}
	cache := map[string]*classWeek{}
	for _, ws := range weeks {
		cw, err := a.classWeek(class, section, ws)
		if err != nil {
			return err
		}
		cache[ws.Format(dayFmt)] = cw
		pt := trendPt{Week: ws.Format(dayFmt)}
		if v, ok := cw.avg[sid]; ok {
			av := round1(v)
			p, _ := cw.percentile(sid)
			pt.Avg, pt.Percentile = &av, &p
		}
		trend = append(trend, pt)
	}

	week := r.URL.Query().Get("week")
	if _, ok := cache[week]; !ok {
		week = ""
		if len(weeks) > 0 {
			week = weeks[len(weeks)-1].Format(dayFmt)
		}
	}
	out := map[string]any{
		"student": map[string]any{"name": name, "roll_no": roll, "admission_no": adm, "class": groupLabel(class, section), "father_name": father, "mother_name": mother},
		"weeks":   trend, "week": week, "this_week": thisWeek.Format(dayFmt),
	}
	if week == "" {
		writeJSON(w, 200, out)
		return nil
	}
	cw := cache[week]
	tests := cw.tests[sid]
	if tests == nil {
		tests = []weekTest{}
	}
	subj := map[string]*weekSubject{}
	var list []*weekSubject
	absent := 0
	for _, t := range tests {
		if t.Status != "" {
			absent++
			continue
		}
		s := subj[t.Subject]
		if s == nil {
			s = &weekSubject{Subject: t.Subject}
			subj[t.Subject] = s
			list = append(list, s)
		}
		s.sum += t.Pct
		s.cs += t.ClassAvg
		s.n++
	}
	for _, s := range list {
		s.Pct, s.ClassAvg = round1(s.sum/float64(s.n)), round1(s.cs/float64(s.n))
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Pct > list[j].Pct })
	if list == nil {
		list = []*weekSubject{}
	}
	out["tests"], out["subjects"], out["absent"] = tests, list, absent
	if v, ok := cw.avg[sid]; ok {
		out["avg"] = round1(v)
		p, _ := cw.percentile(sid)
		out["percentile"] = p
		out["class_size"] = len(cw.avg)
		classSum := 0.0
		for _, x := range cw.avg {
			classSum += x
		}
		out["class_avg"] = round1(classSum / float64(len(cw.avg)))
	}
	// change since the previous week with data
	for i := len(trend) - 1; i > 0; i-- {
		if trend[i].Week == week {
			for j := i - 1; j >= 0; j-- {
				if trend[j].Percentile != nil {
					out["prev_avg"], out["prev_percentile"] = *trend[j].Avg, *trend[j].Percentile
					break
				}
			}
			break
		}
	}
	var seenAt string
	a.db.QueryRow(`SELECT seen_at FROM report_seen WHERE student_id=? AND week=?`, sid, week).Scan(&seenAt)
	out["seen_at"] = seenAt
	out["complete"] = week < thisWeek.Format(dayFmt)
	writeJSON(w, 200, out)
	return nil
}

func (a *App) parentSeen(w http.ResponseWriter, r *http.Request, sid int64) error {
	var in struct{ Week string }
	if err := decode(r, &in); err != nil {
		return err
	}
	if _, err := time.Parse(dayFmt, in.Week); err != nil {
		return bad("Unknown week")
	}
	if _, err := a.db.Exec(`INSERT OR IGNORE INTO report_seen(student_id,week,seen_at) VALUES(?,?,?)`, sid, in.Week, nowStr()); err != nil {
		return err
	}
	writeJSON(w, 200, map[string]any{"ok": true})
	return nil
}

// parentReadStats is the share of students whose parents marked the latest published weekly report as seen.
func (a *App) parentReadStats() map[string]any {
	var week string
	a.db.QueryRow(`SELECT COALESCE(MAX(week),'') FROM weekly_reports`).Scan(&week)
	if week == "" {
		return map[string]any{"week": "", "students": 0, "seen": 0}
	}
	var students, seen int
	a.db.QueryRow(`SELECT COUNT(*) FROM students s JOIN weekly_reports w ON w.class_name=s.class_name AND w.section=s.section AND w.week=? WHERE s.active=1`, week).Scan(&students)
	a.db.QueryRow(`SELECT COUNT(*) FROM report_seen WHERE week=?`, week).Scan(&seen)
	return map[string]any{"week": week, "students": students, "seen": seen}
}

// publishWeeklyReports marks each finished week's report as sent for every class with approved tests that week,
// and tells the class's teachers. Reports themselves are computed live, so late approvals still show up.
func (a *App) publishWeeklyReports() error {
	cur := weekStart(time.Now())
	from := cur.AddDate(0, 0, -28)
	rows, err := a.db.Query(`SELECT DISTINCT class_name, section, exam_date FROM exams WHERE approved_at<>'' AND exam_date>=? AND exam_date<?`,
		from.Format(dayFmt), cur.Format(dayFmt))
	if err != nil {
		return err
	}
	type cw struct{ class, section, week string }
	todo := map[cw]bool{}
	for rows.Next() {
		var c, s, d string
		rows.Scan(&c, &s, &d)
		if t, err := time.ParseInLocation(dayFmt, d, time.Local); err == nil {
			todo[cw{c, s, weekStart(t).Format(dayFmt)}] = true
		}
	}
	rows.Close()
	for k := range todo {
		tx, err := a.begin()
		if err != nil {
			return err
		}
		res, err := tx.Exec(`INSERT OR IGNORE INTO weekly_reports(class_name,section,week,published_at) VALUES(?,?,?,?)`, k.class, k.section, k.week, nowStr())
		if err != nil {
			tx.Rollback()
			return err
		}
		if n, _ := res.RowsAffected(); n == 1 {
			var students int
			tx.QueryRow(`SELECT COUNT(*) FROM students WHERE class_name=? AND section=? AND active=1`, k.class, k.section).Scan(&students)
			ws, _ := time.Parse(dayFmt, k.week)
			body := fmt.Sprintf("Parents of %d students can now read the report for the week of %s.", students, ws.Format("2 Jan"))
			trows, err := tx.Query(`SELECT DISTINCT user_id FROM allocations WHERE class_name=? AND section=?`, k.class, k.section)
			if err == nil {
				var ids []int64
				for trows.Next() {
					var id int64
					trows.Scan(&id)
					ids = append(ids, id)
				}
				trows.Close()
				for _, id := range ids {
					notify(tx, id, "weekly_report", "Weekly reports sent: "+groupLabel(k.class, k.section), body, 0)
				}
			}
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

func (a *App) weeklyScheduler(ctx context.Context) {
	t := time.NewTicker(time.Hour)
	defer t.Stop()
	for {
		if err := a.publishWeeklyReports(); err != nil {
			log.Printf("weekly reports: %v", err)
		}
		a.db.Exec(`DELETE FROM parent_sessions WHERE expires_at<?`, nowStr())
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
