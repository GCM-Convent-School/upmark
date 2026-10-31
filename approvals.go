package main

import (
	"net/http"
	"strconv"
)

// adminApprovals lists submitted (locked) tests, pending ones first.
func (a *App) adminApprovals(w http.ResponseWriter, r *http.Request, u *User) error {
	where := `e.status='locked'`
	if r.URL.Query().Get("state") == "pending" {
		where += ` AND e.approved_at=''`
	}
	exams, err := a.listExams(where + ` ORDER BY (e.approved_at='') DESC, e.locked_at DESC LIMIT 200`)
	if err != nil {
		return err
	}
	out := []map[string]any{}
	for _, e := range exams {
		var graded, absent int
		var sum float64
		a.db.QueryRow(`SELECT COUNT(*), COALESCE(SUM(status<>''),0), COALESCE(SUM(CASE WHEN status='' THEN total END),0) FROM results WHERE exam_id=?`, e.ID).Scan(&graded, &absent, &sum)
		avg := 0.0
		if n := graded - absent; n > 0 && e.MaxTotal > 0 {
			avg = float64(int(sum/float64(n)/e.MaxTotal*1000+0.5)) / 10
		}
		row := map[string]any{"exam": e, "graded": graded, "absent": absent, "avg_pct": avg, "approved": e.ApprovedAt != ""}
		if e.ApprovedAt == "" {
			if rv, err := a.reviewExam(&e); err == nil {
				n := 0
				for _, f := range rv["flags"].([]examFlag) {
					if f.Level == "warn" {
						n++
					}
				}
				row["warnings"] = n
			}
		}
		out = append(out, row)
	}
	writeJSON(w, 200, map[string]any{"exams": out})
	return nil
}

// adminApprove approves a submitted test: it is queued for push to Frappe, counts in the parents' weekly report,
// and the teacher is told.
func (a *App) adminApprove(w http.ResponseWriter, r *http.Request, u *User) error {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	e, err := a.loadExam(id)
	if err != nil {
		return notFound("Test not found")
	}
	if e.Status != "locked" {
		return conflict("Only submitted tests can be approved", nil)
	}
	if e.ApprovedAt != "" {
		return conflict("This test is already approved", nil)
	}
	tx, err := a.begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`UPDATE exams SET approved_at=?, approved_by=? WHERE id=? AND approved_at=''`, nowStr(), u.ID, id); err != nil {
		return err
	}
	if err := addTimeline(tx, id, u.Name, "approved", map[string]any{"label": e.Label}); err != nil {
		return err
	}
	if err := enqueue(tx, "exam", id, 0, "", 0, true); err != nil {
		return err
	}
	if err := notify(tx, e.OwnerID, "approved", "Approved: "+e.Label, "The marks now count in the parents' weekly report.", id); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	a.wake()
	writeJSON(w, 200, map[string]any{"ok": true})
	return nil
}
