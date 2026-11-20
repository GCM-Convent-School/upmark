package main

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Backups keep UpMark's own data (teachers, tests, approvals, logs, settings) safe from a lost volume.
// Every hour, when something changed, a gzipped snapshot is written next to the database (48 kept) and,
// when the SIS is connected, uploaded to it as a private File (24 kept). A brand-new empty database
// restores the newest SIS copy before it opens, so even losing the whole volume costs at most an hour.

const (
	backupPrefix   = "upmark-backup-"
	keepLocal      = 48
	keepSIS        = 24
	backupInterval = time.Hour
)

func backupDir(dbPath string) string { return filepath.Join(filepath.Dir(dbPath), "backups") }

// snapshot returns a consistent gzipped copy of the live database (VACUUM INTO never blocks readers).
func (a *App) snapshot() ([]byte, error) {
	dir := backupDir(a.cfg.DBPath)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, err
	}
	tmp := filepath.Join(dir, fmt.Sprintf(".snap-%d.db", time.Now().UnixNano()))
	defer os.Remove(tmp)
	if _, err := a.db.Exec(`VACUUM INTO ?`, tmp); err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(tmp)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	zw, _ := gzip.NewWriterLevel(&buf, gzip.BestCompression)
	zw.Write(raw)
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// fingerprint changes whenever data that matters changes (sign-ins and one-time codes are left out,
// so an idle school does not upload a new copy every hour).
func (a *App) fingerprint() string {
	h := sha256.New()
	for _, q := range []string{
		`SELECT COUNT(*)||':'||COALESCE(MAX(id),0)||':'||COALESCE(SUM(active),0) FROM users`,
		`SELECT COUNT(*)||':'||COALESCE(MAX(rowid),0) FROM allocations`,
		`SELECT COUNT(*)||':'||COALESCE(MAX(id),0)||':'||COALESCE(MAX(locked_at),'')||':'||COALESCE(MAX(approved_at),'')||':'||COALESCE(SUM(status='locked'),0) FROM exams`,
		`SELECT COUNT(*)||':'||COALESCE(MAX(updated_at),'') FROM results`,
		`SELECT COUNT(*)||':'||COALESCE(MAX(decided_at),'') FROM corrections`,
		`SELECT COUNT(*) FROM timeline`,
		`SELECT COUNT(*)||':'||COALESCE(SUM(state='deleted'),0) FROM conduct_points`,
		`SELECT COUNT(*) FROM weekly_reports`,
		`SELECT group_concat(k||'='||v, ';') FROM (SELECT k,v FROM settings WHERE k NOT LIKE 'last_%' AND k NOT LIKE 'backup_%' ORDER BY k)`,
	} {
		var s string
		a.db.QueryRow(q).Scan(&s)
		h.Write([]byte(s + "\n"))
	}
	return hex.EncodeToString(h.Sum(nil))
}

type backupResult struct {
	File       string `json:"file"`
	Bytes      int    `json:"bytes"`
	SIS        bool   `json:"sis"`
	Drive      bool   `json:"drive"`
	Skipped    bool   `json:"skipped"`
	Error      string `json:"error,omitempty"`
	DriveError string `json:"drive_error,omitempty"`
}

// backup writes one snapshot. force=false skips it when nothing changed since the last one, unless the
// daily copy to the Google upload link is due.
func (a *App) backup(force bool) backupResult {
	fp := a.fingerprint()
	changed := force || fp != a.setting("backup_fingerprint")
	driveDue := a.driveURL() != "" && (force || a.driveDue())
	if !changed && !driveDue {
		return backupResult{Skipped: true}
	}
	data, err := a.snapshot()
	if err != nil {
		a.setSetting("backup_error", err.Error())
		return backupResult{Error: err.Error()}
	}
	name := backupPrefix + time.Now().UTC().Format("20060102-150405") + ".db.gz"
	res := backupResult{File: name, Bytes: len(data)}
	// a database with nothing in it yet is never sent away: it would become the newest copy
	// and a later restore would bring back an empty UpMark
	var users, exams int
	a.db.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&users)
	a.db.QueryRow(`SELECT COUNT(*) FROM exams`).Scan(&exams)
	empty := users <= 1 && exams == 0
	server := false
	if changed {
		dir := backupDir(a.cfg.DBPath)
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o640); err != nil {
			res.Error = err.Error()
		} else {
			server = true
		}
		pruneLocal(dir, keepLocal)
		a.setSetting("backup_last", nowStr())
		a.setSetting("backup_last_file", name)
		if a.frappe.Enabled() && empty {
			res.Error = strings.TrimSpace(res.Error + " Not sent to the SIS: there is nothing in the database yet.")
		} else if a.frappe.Enabled() {
			if err := a.frappe.Upload(name, data); err != nil {
				res.Error = strings.TrimSpace(res.Error + " SIS: " + err.Error())
			} else {
				res.SIS = true
				a.setSetting("backup_last_sis", nowStr())
				a.frappe.pruneBackups(keepSIS)
			}
		}
		a.setSetting("backup_error", res.Error)
		if res.Error == "" {
			a.setSetting("backup_fingerprint", fp)
		}
	}
	if driveDue {
		if empty {
			res.DriveError = "Not sent to Google Drive: there is nothing in the database yet."
		} else if e := a.sendDrive(name, data); e != "" {
			res.DriveError = e
		} else {
			res.Drive = true
		}
	}
	// the backup history on the Frappe sync page
	a.db.Exec(`INSERT INTO backup_log(at,file,bytes,manual,server,sis,drive,problem) VALUES(?,?,?,?,?,?,?,?)`,
		nowStr(), name, len(data), b2i(force), b2i(server), b2i(res.SIS), b2i(res.Drive), strings.TrimSpace(res.Error+" "+res.DriveError))
	a.db.Exec(`DELETE FROM backup_log WHERE id NOT IN (SELECT id FROM backup_log ORDER BY id DESC LIMIT 500)`)
	return res
}

func pruneLocal(dir string, keep int) {
	m, _ := filepath.Glob(filepath.Join(dir, backupPrefix+"*.db.gz"))
	sort.Strings(m)
	for len(m) > keep {
		os.Remove(m[0])
		m = m[1:]
	}
}

func (a *App) backupScheduler(ctx context.Context) {
	// the first run waits a few minutes so a crash-looping deploy never overwrites good copies
	t := time.NewTimer(5 * time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		r := a.backup(false)
		if r.Error != "" {
			log.Printf("backup: %s", r.Error)
		}
		if r.DriveError != "" {
			log.Printf("backup: %s", r.DriveError)
		}
		t.Reset(backupInterval)
	}
}

// ---------- SIS file storage ----------

// Upload stores data as a private File in the SIS (Frappe's upload_file).
func (f *Frappe) Upload(name string, data []byte) error {
	if !f.Enabled() {
		return errNotConfigured
	}
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	mw.WriteField("is_private", "1")
	mw.WriteField("folder", "Home")
	mw.WriteField("file_name", name)
	fw, _ := mw.CreateFormFile("file", name)
	fw.Write(data)
	mw.Close()
	req, _ := http.NewRequest("POST", f.base+"/api/method/upload_file", &body)
	req.Header.Set("Authorization", "token "+f.key+":"+f.secret)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", mw.FormDataContentType())
	hc := &http.Client{Timeout: 2 * time.Minute}
	resp, err := hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 300 {
		return &frappeError{Status: resp.StatusCode, Body: string(b)}
	}
	return nil
}

// Download fetches a (private) file by its file_url.
func (f *Frappe) Download(fileURL string) ([]byte, error) {
	req, _ := http.NewRequest("GET", f.base+fileURL, nil)
	req.Header.Set("Authorization", "token "+f.key+":"+f.secret)
	hc := &http.Client{Timeout: 2 * time.Minute}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 512<<20))
	if resp.StatusCode >= 300 {
		return nil, &frappeError{Status: resp.StatusCode, Body: string(b)}
	}
	return b, err
}

// backups lists UpMark backups in the SIS, oldest first (the timestamp is in the name).
func (f *Frappe) backups() ([]frappeDoc, error) {
	rows, err := f.List("File", []string{"name", "file_name", "file_url"}, [][]any{{"file_name", "like", backupPrefix + "%"}})
	if err != nil {
		return nil, err
	}
	sort.Slice(rows, func(i, j int) bool { return str(rows[i], "file_name") < str(rows[j], "file_name") })
	return rows, nil
}

func (f *Frappe) pruneBackups(keep int) {
	rows, err := f.backups()
	if err != nil {
		return
	}
	for len(rows) > keep {
		f.do("DELETE", "/api/resource/File/"+url.PathEscape(str(rows[0], "name")), nil, nil, nil)
		rows = rows[1:]
	}
}

// restoreFromSIS runs before the database opens: when there is no database file yet, the newest SIS
// backup becomes the database. Returns the backup's name, or "" when there was nothing to restore.
func restoreFromSIS(cfg Config, f *Frappe) (string, error) {
	if os.Getenv("AUTO_RESTORE") == "0" || !f.Enabled() {
		return "", nil
	}
	if _, err := os.Stat(cfg.DBPath); err == nil || !errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	var rows []frappeDoc
	var err error
	for i := 0; i < 30; i++ { // the SIS may be restarting too; give it half a minute before starting empty
		if rows, err = f.backups(); err == nil {
			break
		}
		time.Sleep(time.Second)
	}
	if err != nil {
		return "", fmt.Errorf("listing SIS backups: %w", err)
	}
	for i := len(rows) - 1; i >= 0; i-- { // newest first; skip a copy that will not open
		gz, err := f.Download(str(rows[i], "file_url"))
		if err != nil {
			log.Printf("restore: %s: %v", str(rows[i], "file_name"), err)
			continue
		}
		zr, err := gzip.NewReader(bytes.NewReader(gz))
		if err != nil {
			continue
		}
		raw, err := io.ReadAll(zr)
		if err != nil || !bytes.HasPrefix(raw, []byte("SQLite format 3\x00")) {
			continue
		}
		if err := os.MkdirAll(filepath.Dir(cfg.DBPath), 0o750); err != nil {
			return "", err
		}
		tmp := cfg.DBPath + ".restoring"
		if err := os.WriteFile(tmp, raw, 0o640); err != nil {
			return "", err
		}
		if err := os.Rename(tmp, cfg.DBPath); err != nil {
			return "", err
		}
		return str(rows[i], "file_name"), nil
	}
	return "", nil
}

// ---------- admin API ----------

func (a *App) backupRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/admin/backup", a.wrap("admin", func(w http.ResponseWriter, r *http.Request, u *User) error {
		res := a.backup(true)
		code := 200
		if res.Error != "" || res.DriveError != "" {
			code = 502
		}
		writeJSON(w, code, res)
		return nil
	}))
	mux.HandleFunc("GET /api/admin/backup/log", a.wrap("admin", func(w http.ResponseWriter, r *http.Request, u *User) error {
		rows, err := a.db.Query(`SELECT at,file,bytes,manual,server,sis,drive,problem FROM backup_log ORDER BY id DESC LIMIT 60`)
		if err != nil {
			return err
		}
		defer rows.Close()
		out := []map[string]any{}
		for rows.Next() {
			var at, file, problem string
			var bytes, manual, server, sis, drive int
			rows.Scan(&at, &file, &bytes, &manual, &server, &sis, &drive, &problem)
			out = append(out, map[string]any{"at": at, "file": file, "bytes": bytes, "manual": manual == 1, "server": server == 1, "sis": sis == 1, "drive": drive == 1, "problem": problem})
		}
		writeJSON(w, 200, map[string]any{"backups": out})
		return nil
	}))
	// the Google upload link for the daily copy ("" turns it off)
	mux.HandleFunc("PUT /api/admin/backup/drive", a.wrap("admin", func(w http.ResponseWriter, r *http.Request, u *User) error {
		var in struct {
			URL string `json:"url"`
		}
		if err := decode(r, &in); err != nil {
			return err
		}
		in.URL = strings.TrimSpace(in.URL)
		if in.URL != "" && !validDriveURL(in.URL) {
			return bad("Use the web app link from Apps Script: https://script.google.com/macros/s/…/exec")
		}
		if in.URL == "" {
			in.URL = "off"
		}
		a.setSetting("backup_drive_url", in.URL)
		a.setSetting("backup_drive_error", "")
		writeJSON(w, 200, map[string]any{"ok": true})
		return nil
	}))
	mux.HandleFunc("GET /api/admin/backup/download", a.wrap("admin", func(w http.ResponseWriter, r *http.Request, u *User) error {
		data, err := a.snapshot()
		if err != nil {
			return err
		}
		w.Header().Set("Content-Type", "application/gzip")
		w.Header().Set("Content-Disposition", `attachment; filename="`+backupPrefix+time.Now().Format("20060102-1504")+`.db.gz"`)
		w.Write(data)
		return nil
	}))
}

func (a *App) backupStatus() map[string]any {
	n := 0
	if m, err := filepath.Glob(filepath.Join(backupDir(a.cfg.DBPath), backupPrefix+"*.db.gz")); err == nil {
		n = len(m)
	}
	return map[string]any{"last": a.setting("backup_last"), "last_sis": a.setting("backup_last_sis"), "file": a.setting("backup_last_file"),
		"error": a.setting("backup_error"), "local_count": n, "restored_from": a.setting("restored_from"), "restored_at": a.setting("restored_at"),
		"sis": a.frappe.Enabled(), "drive": a.driveURL() != "", "drive_link": maskLink(a.driveURL()), "last_drive": a.setting("backup_last_drive"),
		"drive_error": a.setting("backup_drive_error")}
}

// maskLink shows enough of the upload link to recognise it without handing out the whole of it.
func maskLink(s string) string {
	i := strings.Index(s, "/macros/s/")
	if i < 0 || len(s) < i+20 {
		return s
	}
	id := s[i+10:]
	return s[:i+10] + id[:6] + "…" + id[len(id)-10:]
}
