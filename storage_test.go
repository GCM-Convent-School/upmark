package main

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestClassifyMount(t *testing.T) {
	base := "100 90 0:50 / / rw,relatime master:1 - overlay overlay rw\n" +
		"101 100 0:51 / /proc rw - proc proc rw\n"
	anon := base + "120 100 8:1 /var/lib/docker/volumes/3f5c0a9e1b2d4c6f8a0b1c2d3e4f5a6b7c8d9e0f1a2b3c4d5e6f7a8b9c0d1e2f/_data /data rw,relatime - ext4 /dev/sda1 rw\n"
	named := base + "120 100 8:1 /var/lib/docker/volumes/bn5dcijugnhqesktf78bwv1m-upmark-data/_data /data rw,relatime - ext4 /dev/sda1 rw\n"
	bind := base + "120 100 8:1 /srv/upmark /data rw,relatime - ext4 /dev/sda1 rw\n"
	for _, c := range []struct {
		name, info, dir string
		ok              bool
	}{
		{"inside the container", base, "/data", false},
		{"unnamed volume (Dockerfile VOLUME only)", anon, "/data", false},
		{"named volume", named, "/data", true},
		{"bind mount", bind, "/data", true},
		{"sub-folder of a named volume", named, "/data/db", true},
		{"similar prefix is not the mount", named, "/database", false},
	} {
		if got := classifyMount(c.info, c.dir); got.OK != c.ok {
			t.Errorf("%s: ok=%v want %v (%s)", c.name, got.OK, c.ok, got.Detail)
		}
	}
}

func TestFreeSlot(t *testing.T) {
	plan := func(f, to string) frappeDoc { return frappeDoc{"from_time": f, "to_time": to} }
	for _, c := range []struct {
		busy     []frappeDoc
		from, to string
	}{
		{nil, "09:00:00", "10:00:00"},
		{[]frappeDoc{plan("09:00:00", "10:00:00")}, "10:00:00", "11:00:00"},
		{[]frappeDoc{plan("9:00:00", "10:00:00"), plan("10:00:00.000000", "11:30:00")}, "11:30:00", "12:30:00"},
		{[]frappeDoc{plan("09:30:00", "10:15:00")}, "10:15:00", "11:15:00"},
	} {
		if f, to := freeSlot(c.busy); f != c.from || to != c.to {
			t.Errorf("busy %v: got %s-%s want %s-%s", c.busy, f, to, c.from, c.to)
		}
	}
}

func TestDriveDue(t *testing.T) {
	now := time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC)
	at := func(d time.Duration) string { return now.Add(-d).Format(time.RFC3339) }
	for _, c := range []struct {
		last string
		due  bool
	}{{"", true}, {"garbage", true}, {at(2 * time.Hour), false}, {at(23 * time.Hour), false}, {at(23*time.Hour + 56*time.Minute), true}, {at(30 * time.Hour), true}} {
		if got := driveDueAt(c.last, now); got != c.due {
			t.Errorf("last %q: due=%v want %v", c.last, got, c.due)
		}
	}
	for s, ok := range map[string]bool{
		"https://script.google.com/macros/s/AKfy123/exec": true,
		"http://script.google.com/macros/s/AKfy123/exec":  false,
		"https://evil.test/macros/s/AKfy123/exec":         false,
		"https://script.google.com/macros/s/AKfy123/dev":  false,
	} {
		if validDriveURL(s) != ok {
			t.Errorf("validDriveURL(%q) want %v", s, ok)
		}
	}
}

func TestSessionRenewal(t *testing.T) {
	// sessionTTL for admin vs teacher
	if got := sessionTTL(&User{Role: "admin"}); got != 3*24*time.Hour {
		t.Errorf("sessionTTL(admin) = %v; want %v", got, 3*24*time.Hour)
	}
	if got := sessionTTL(&User{Role: "teacher"}); got != 15*24*time.Hour {
		t.Errorf("sessionTTL(teacher) = %v; want %v", got, 15*24*time.Hour)
	}
	if got := sessionTTL(nil); got != 15*24*time.Hour {
		t.Errorf("sessionTTL(nil) = %v; want %v", got, 15*24*time.Hour)
	}

	t0 := time.Date(2026, 11, 27, 12, 0, 0, 0, time.UTC)
	teacherTTL := 15 * 24 * time.Hour
	adminTTL := 3 * 24 * time.Hour

	// 1. Fresh session (no rewrite)
	storedTeacher := t0.Add(teacherTTL)
	if got, rewrite := renewedExpiry(storedTeacher, t0, teacherTTL); rewrite || !got.Equal(storedTeacher) {
		t.Errorf("fresh session: rewrite=%v got=%v want %v", rewrite, got, storedTeacher)
	}

	// 2. A session 2 days into a 15-day teacher ttl (rewrite)
	now2d := t0.Add(2 * 24 * time.Hour)
	want2d := now2d.Add(teacherTTL)
	if got, rewrite := renewedExpiry(storedTeacher, now2d, teacherTTL); !rewrite || !got.Equal(want2d) {
		t.Errorf("2 days into session: rewrite=%v got=%v want %v", rewrite, got, want2d)
	}

	// 3. 12h into it (no rewrite)
	now12h := t0.Add(12 * time.Hour)
	if got, rewrite := renewedExpiry(storedTeacher, now12h, teacherTTL); rewrite || !got.Equal(storedTeacher) {
		t.Errorf("12h into session: rewrite=%v got=%v want %v", rewrite, got, storedTeacher)
	}

	// 4. A teacher-length session after the role became admin (shrink)
	nowShrink := t0.Add(1 * time.Hour)
	wantShrink := nowShrink.Add(adminTTL)
	if got, rewrite := renewedExpiry(storedTeacher, nowShrink, adminTTL); !rewrite || !got.Equal(wantShrink) {
		t.Errorf("teacher session shrunk to admin: rewrite=%v got=%v want %v", rewrite, got, wantShrink)
	}
}

func TestSessionRenewWorkflow(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	db, err := openDB(dbPath)
	if err != nil {
		t.Fatalf("openDB: %v", err)
	}
	defer db.Close()

	a := &App{db: db, cfg: Config{CookieSecure: false}}

	// Insert active user
	res, err := db.Exec(`INSERT INTO users(name, mobile, email, role, active, created_at) VALUES('Teacher Alice', '9999999999', 'alice@school.test', 'teacher', 1, ?)`, nowStr())
	if err != nil {
		t.Fatalf("insert user: %v", err)
	}
	uid, _ := res.LastInsertId()

	// 1. Fresh session
	tok := "testtoken1234567890123456789012"
	now := time.Now().UTC()
	initExp := now.Add(15 * 24 * time.Hour)
	_, err = db.Exec(`INSERT INTO sessions(token_hash, user_id, expires_at) VALUES(?, ?, ?)`, sha(tok), uid, initExp.Format(time.RFC3339))
	if err != nil {
		t.Fatalf("insert session: %v", err)
	}

	req := httptest.NewRequest("GET", "/api/test", nil)
	req.AddCookie(&http.Cookie{Name: "rea_session", Value: tok})

	// userFromRequest reads s.expires_at
	u := a.userFromRequest(req)
	if u == nil {
		t.Fatalf("userFromRequest returned nil")
	}
	if u.ExpiresAt.IsZero() {
		t.Errorf("expected u.ExpiresAt to be set, got zero time")
	}
	if u.ExpiresAt.Unix() != initExp.Unix() {
		t.Errorf("u.ExpiresAt = %v, want %v", u.ExpiresAt, initExp)
	}

	// userFromRequestRenew on fresh session should not rewrite or set cookie
	rec := httptest.NewRecorder()
	uRenew := a.userFromRequestRenew(rec, req)
	if uRenew == nil {
		t.Fatalf("userFromRequestRenew returned nil")
	}
	if rec.Header().Get("Set-Cookie") != "" {
		t.Errorf("fresh session should not set cookie, got: %v", rec.Header().Get("Set-Cookie"))
	}

	// 2. Simulate 2 days elapsed by setting stored expires_at to 13 days in future (< now + 15d - 24h)
	simStored := now.Add(13 * 24 * time.Hour)
	_, err = db.Exec(`UPDATE sessions SET expires_at=? WHERE token_hash=?`, simStored.Format(time.RFC3339), sha(tok))
	if err != nil {
		t.Fatalf("update session: %v", err)
	}

	rec2 := httptest.NewRecorder()
	uRenew2 := a.userFromRequestRenew(rec2, req)
	if uRenew2 == nil {
		t.Fatalf("userFromRequestRenew returned nil")
	}
	cookieHdr := rec2.Header().Get("Set-Cookie")
	if !strings.Contains(cookieHdr, "rea_session="+tok) || !strings.Contains(cookieHdr, "Max-Age=1296000") {
		t.Errorf("expected renewed Set-Cookie header with Max-Age=1296000, got: %s", cookieHdr)
	}

	// Check DB was updated to approx now + 15d
	var newExpStr string
	err = db.QueryRow(`SELECT expires_at FROM sessions WHERE token_hash=?`, sha(tok)).Scan(&newExpStr)
	if err != nil {
		t.Fatalf("query updated session: %v", err)
	}
	newExp, _ := time.Parse(time.RFC3339, newExpStr)
	if newExp.Before(now.Add(14 * 24 * time.Hour)) {
		t.Errorf("expected updated expires_at to be renewed (~15 days), got %v", newExp)
	}
}
