package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// A daily copy of the database also goes to an upload link outside the server and the SIS: a Google Apps
// Script web app that saves what it receives (to Drive). The request is the one the school's other apps use:
//   base64 db.gz | curl --data-binary @- "<link>?app=upmark&file=<name>"
// The link comes from the Database card (setting backup_drive_url) or BACKUP_UPLOAD_URL.

const driveEvery = 24 * time.Hour

func (a *App) driveURL() string {
	switch u := a.setting("backup_drive_url"); u {
	case "off": // turned off on the Database card, even when BACKUP_UPLOAD_URL is set
		return ""
	case "":
		return os.Getenv("BACKUP_UPLOAD_URL")
	default:
		return u
	}
}

// validDriveURL accepts only an https Apps Script web app link.
func validDriveURL(s string) bool {
	u, err := url.Parse(s)
	return err == nil && u.Scheme == "https" && u.Host == "script.google.com" && strings.HasPrefix(u.Path, "/macros/s/") && strings.HasSuffix(u.Path, "/exec")
}

var driveClient = &http.Client{Timeout: 3 * time.Minute}

// uploadDrive sends one gzipped snapshot, base64-encoded, to the upload link.
func uploadDrive(link, name string, gz []byte) error {
	u, err := url.Parse(link)
	if err != nil {
		return err
	}
	q := u.Query()
	q.Set("app", "upmark")
	q.Set("file", name)
	u.RawQuery = q.Encode()
	body := base64.StdEncoding.EncodeToString(gz)
	// Apps Script answers a POST with a redirect to its result page; the client follows it with a GET, as curl -L does
	resp, err := driveClient.Post(u.String(), "application/x-www-form-urlencoded", strings.NewReader(body))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	text := strings.TrimSpace(string(raw))
	if resp.StatusCode >= 300 {
		return fmt.Errorf("upload link answered HTTP %d", resp.StatusCode)
	}
	if strings.HasPrefix(strings.ToLower(text), "<!doctype") || strings.HasPrefix(strings.ToLower(text), "<html") {
		return fmt.Errorf("upload link answered with a web page, not a result (is the script deployed for \"Anyone\"?)")
	}
	var j map[string]any
	if json.Unmarshal(raw, &j) == nil {
		if ok, has := j["ok"].(bool); has && !ok {
			return fmt.Errorf("upload link refused it: %v", firstNonNil(j["error"], j["message"], text))
		}
		if e, has := j["error"]; has && e != nil && e != "" {
			return fmt.Errorf("upload link refused it: %v", e)
		}
	}
	return nil
}

func firstNonNil(v ...any) any {
	for _, x := range v {
		if x != nil && x != "" {
			return x
		}
	}
	return ""
}

// driveDue says whether today's copy has not been sent yet.
func (a *App) driveDue() bool { return driveDueAt(a.setting("backup_last_drive"), time.Now()) }

// driveDueAt: never sent, or the last copy is a day old (a few minutes early, so the hourly check
// does not slip by an hour every day).
func driveDueAt(last string, now time.Time) bool {
	t, err := time.Parse(time.RFC3339, last)
	return err != nil || now.Sub(t) >= driveEvery-5*time.Minute
}

// sendDrive uploads data to the link and records the outcome ("" when there is no link).
func (a *App) sendDrive(name string, data []byte) string {
	link := a.driveURL()
	if link == "" {
		return ""
	}
	if err := uploadDrive(link, name, data); err != nil {
		a.setSetting("backup_drive_error", err.Error())
		return "Google Drive: " + err.Error()
	}
	a.setSetting("backup_last_drive", nowStr())
	a.setSetting("backup_last_drive_file", name)
	a.setSetting("backup_drive_error", "")
	return ""
}
