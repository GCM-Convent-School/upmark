package main

import (
	"database/sql"
	"fmt"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
)

// ---------- in-app notifications ----------

// notify queues an in-app notification for one user (shown on the teacher's Today screen and the bell).
func notify(tx execer, userID int64, kind, title, body string, examID int64) error {
	if userID == 0 {
		return nil
	}
	_, err := tx.Exec(`INSERT INTO notifications(user_id,kind,title,body,exam_id,created_at) VALUES(?,?,?,?,?,?)`, userID, kind, title, body, examID, nowStr())
	return err
}

func (a *App) apiNotifications(w http.ResponseWriter, r *http.Request, u *User) error {
	rows, err := a.db.Query(`SELECT id,kind,title,body,exam_id,created_at,read_at FROM notifications WHERE user_id=? ORDER BY id DESC LIMIT 40`, u.ID)
	if err != nil {
		return err
	}
	defer rows.Close()
	type note struct {
		ID      int64  `json:"id"`
		Kind    string `json:"kind"`
		Title   string `json:"title"`
		Body    string `json:"body"`
		ExamID  int64  `json:"exam_id"`
		Created string `json:"created_at"`
		Read    bool   `json:"read"`
	}
	out := []note{}
	unread := 0
	for rows.Next() {
		var n note
		var read string
		rows.Scan(&n.ID, &n.Kind, &n.Title, &n.Body, &n.ExamID, &n.Created, &read)
		n.Read = read != ""
		if !n.Read {
			unread++
		}
		out = append(out, n)
	}
	writeJSON(w, 200, map[string]any{"notifications": out, "unread": unread})
	return nil
}

func (a *App) apiNotificationsRead(w http.ResponseWriter, r *http.Request, u *User) error {
	if _, err := a.db.Exec(`UPDATE notifications SET read_at=? WHERE user_id=? AND read_at=''`, nowStr(), u.ID); err != nil {
		return err
	}
	writeJSON(w, 200, map[string]any{"ok": true})
	return nil
}

// ---------- admin: remarks, returning a sheet, nudges ----------

// adminExamNote handles remark / return / nudge on one test. Each leaves a timeline entry and notifies the teacher.
func (a *App) adminExamNote(w http.ResponseWriter, r *http.Request, u *User) error {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	action := r.PathValue("action")
	var in struct{ Note string }
	if err := decode(r, &in); err != nil {
		return err
	}
	in.Note = strings.TrimSpace(in.Note)
	if len(in.Note) > 1000 {
		return bad("The note is too long")
	}
	e, err := a.loadExam(id)
	if err != nil {
		return notFound("Test not found")
	}
	tx, err := a.begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	switch action {
	case "remark":
		if in.Note == "" {
			return bad("Write the remark for the teacher")
		}
		if err := addTimeline(tx, id, u.Name, "remark", map[string]any{"note": in.Note}); err != nil {
			return err
		}
		if err := notify(tx, e.OwnerID, "remark", "Remark on "+e.Label, in.Note, id); err != nil {
			return err
		}
	case "return":
		if e.Status != "locked" || e.ApprovedAt != "" {
			return conflict("Only a submitted test that is not yet approved can be sent back", nil)
		}
		if in.Note == "" {
			return bad("Tell the teacher what to fix")
		}
		if _, err := tx.Exec(`UPDATE exams SET status='draft', locked_at=NULL, locked_by=NULL WHERE id=? AND status='locked' AND approved_at=''`, id); err != nil {
			return err
		}
		if err := addTimeline(tx, id, u.Name, "returned", map[string]any{"note": in.Note}); err != nil {
			return err
		}
		if err := notify(tx, e.OwnerID, "returned", "Sent back: "+e.Label, in.Note, id); err != nil {
			return err
		}
	case "nudge":
		if e.Status != "draft" {
			return conflict("Only an unfinished sheet can be nudged", nil)
		}
		msg := in.Note
		if msg == "" {
			msg = "Please finish and lock this marksheet."
		}
		if err := notify(tx, e.OwnerID, "nudge", "Reminder: "+e.Label, msg, id); err != nil {
			return err
		}
	default:
		return notFound("Unknown action")
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	writeJSON(w, 200, map[string]any{"ok": true})
	return nil
}

// ---------- admin: review a submitted test before approving ----------

type examFlag struct {
	Level string `json:"level"` // warn | info | good
	Text  string `json:"text"`
}

type scoreRow struct {
	sid     int64
	name    string
	subject string
	status  string
	pct     float64
	total   float64
	scored  bool
}

func (a *App) examScores(e *Exam) ([]scoreRow, error) {
	rows, err := a.db.Query(`SELECT r.student_id, s.name, r.subject, r.status, r.total FROM results r JOIN students s ON s.id=r.student_id WHERE r.exam_id=?`, e.ID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []scoreRow
	for rows.Next() {
		var x scoreRow
		var t sql.NullFloat64
		rows.Scan(&x.sid, &x.name, &x.subject, &x.status, &t)
		if x.status == "" && t.Valid && e.MaxTotal > 0 {
			x.scored, x.total, x.pct = true, t.Float64, t.Float64/e.MaxTotal*100
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

// previousExam finds the latest earlier submitted test of the same class sharing a subject.
func (a *App) previousExam(e *Exam) *Exam {
	var id int64
	err := a.db.QueryRow(`SELECT e.id FROM exams e WHERE e.class_name=? AND e.section=? AND e.status='locked' AND e.id<>?
		AND (e.exam_date<? OR (e.exam_date=? AND e.id<?))
		AND EXISTS(SELECT 1 FROM exam_subjects s WHERE s.exam_id=e.id AND s.subject IN (SELECT subject FROM exam_subjects WHERE exam_id=?))
		ORDER BY e.exam_date DESC, e.id DESC LIMIT 1`, e.ClassName, e.Section, e.ID, e.Date, e.Date, e.ID, e.ID).Scan(&id)
	if err != nil {
		return nil
	}
	p, err := a.loadExam(id)
	if err != nil {
		return nil
	}
	return p
}

func avgPct(rows []scoreRow) (float64, int) {
	sum, n := 0.0, 0
	for _, r := range rows {
		if r.scored {
			sum += r.pct
			n++
		}
	}
	if n == 0 {
		return 0, 0
	}
	return sum / float64(n), n
}

// reviewExam builds the numbers an administrator checks before approving: spread, warnings and the change since last time.
func (a *App) reviewExam(e *Exam) (map[string]any, error) {
	cur, err := a.examScores(e)
	if err != nil {
		return nil, err
	}
	avg, graded := avgPct(cur)
	var absent, exempt, zeros, full int
	distinct := map[float64]bool{}
	for _, r := range cur {
		switch {
		case r.status == "AB":
			absent++
		case r.status == "EX":
			exempt++
		case r.scored:
			distinct[r.total] = true
			if r.total == 0 {
				zeros++
			}
			if r.total == e.MaxTotal {
				full++
			}
		}
	}
	bins := []map[string]any{}
	edges := []struct {
		lab    string
		lo, hi float64
	}{{"Under 40%", 0, 40}, {"40–59%", 40, 60}, {"60–74%", 60, 75}, {"75–89%", 75, 90}, {"90%+", 90, 1000}}
	for _, b := range edges {
		n := 0
		for _, r := range cur {
			if r.scored && r.pct >= b.lo && r.pct < b.hi {
				n++
			}
		}
		bins = append(bins, map[string]any{"label": b.lab, "n": n})
	}
	flags := []examFlag{}
	total := len(cur)
	if total > 0 && float64(absent)/float64(total) > 0.15 {
		flags = append(flags, examFlag{"warn", fmt.Sprintf("%d students marked absent (%d%%). Check against attendance.", absent, absent*100/total)})
	}
	if zeros > 0 {
		flags = append(flags, examFlag{"warn", fmt.Sprintf("%d student%s scored 0 without being marked absent.", zeros, plural(zeros))})
	}
	if graded >= 5 && len(distinct) == 1 {
		flags = append(flags, examFlag{"warn", "Every student has the same mark. Check that the right sheet was entered."})
	}
	if graded >= 5 && full*2 > graded {
		flags = append(flags, examFlag{"info", fmt.Sprintf("%d of %d students scored full marks.", full, graded)})
	}
	var prevAvg any
	if p := a.previousExam(e); p != nil {
		prev, err := a.examScores(p)
		if err == nil {
			pa, pn := avgPct(prev)
			if pn > 0 && graded > 0 {
				prevAvg = round1(pa)
				d := avg - pa
				if d <= -10 {
					flags = append(flags, examFlag{"warn", fmt.Sprintf("Class average fell %.0f points since %s.", -d, p.SubType+" on "+shortDate(p.Date))})
				} else if d >= 15 {
					flags = append(flags, examFlag{"info", fmt.Sprintf("Class average rose %.0f points since %s.", d, p.SubType+" on "+shortDate(p.Date))})
				}
			}
			before := map[string]float64{}
			for _, r := range prev {
				if r.scored {
					before[fmt.Sprint(r.sid, "|", r.subject)] = r.pct
				}
			}
			type drop struct {
				name string
				by   float64
			}
			var drops []drop
			for _, r := range cur {
				if b, ok := before[fmt.Sprint(r.sid, "|", r.subject)]; ok && r.scored && b-r.pct >= 25 {
					drops = append(drops, drop{r.name, b - r.pct})
				}
			}
			sort.Slice(drops, func(i, j int) bool { return drops[i].by > drops[j].by })
			for i, d := range drops {
				if i == 3 {
					flags = append(flags, examFlag{"warn", fmt.Sprintf("%d more students dropped 25 points or more.", len(drops)-3)})
					break
				}
				flags = append(flags, examFlag{"warn", fmt.Sprintf("%s dropped %.0f points since the last test.", d.name, d.by)})
			}
		}
	}
	return map[string]any{
		"exam": e, "graded": graded, "absent": absent, "exempt": exempt, "avg_pct": round1(avg), "prev_avg_pct": prevAvg,
		"bins": bins, "flags": flags,
	}, nil
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

func (a *App) adminExamReview(w http.ResponseWriter, r *http.Request, u *User) error {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	e, err := a.loadExam(id)
	if err != nil {
		return notFound("Test not found")
	}
	out, err := a.reviewExam(e)
	if err != nil {
		return err
	}
	writeJSON(w, 200, out)
	return nil
}

// percentileRank is the share of the class scoring below x, counting ties as half (0-100).
func percentileRank(x float64, all []float64) float64 {
	if len(all) == 0 {
		return 0
	}
	below, equal := 0, 0
	for _, v := range all {
		switch {
		case v < x-1e-9:
			below++
		case math.Abs(v-x) <= 1e-9:
			equal++
		}
	}
	return math.Round((float64(below) + 0.5*float64(equal)) / float64(len(all)) * 100)
}
