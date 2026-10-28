package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"math/big"
	"net/http"
	"net/smtp"
	"strings"
	"time"
)

type User struct {
	ID     int64  `json:"id"`
	Name   string `json:"name"`
	Mobile string `json:"mobile"`
	Email  string `json:"email"`
	Role   string `json:"role"`
	Active bool   `json:"active"`
}

func (u *User) IsAdmin() bool { return u.Role == "admin" }

type ctxKey struct{}

func normIdent(s string) string {
	s = strings.TrimSpace(strings.ToLower(s))
	if strings.Contains(s, "@") {
		return s
	}
	var d []rune
	for _, r := range s {
		if r >= '0' && r <= '9' {
			d = append(d, r)
		}
	}
	if len(d) > 10 {
		d = d[len(d)-10:]
	}
	return string(d)
}

func (a *App) findUser(ident string) (*User, error) {
	ident = normIdent(ident)
	if ident == "" {
		return nil, sql.ErrNoRows
	}
	u := &User{}
	var mob, em sql.NullString
	err := a.db.QueryRow(`SELECT id,name,mobile,email,role,active FROM users WHERE (mobile=? OR email=?) AND active=1`, ident, ident).
		Scan(&u.ID, &u.Name, &mob, &em, &u.Role, &u.Active)
	u.Mobile, u.Email = mob.String, em.String
	return u, err
}

func (a *App) bootstrapAdmin() error {
	var n int
	if err := a.db.QueryRow(`SELECT COUNT(*) FROM users WHERE role='admin'`).Scan(&n); err != nil || n > 0 {
		return err
	}
	if a.cfg.AdminMobile == "" && a.cfg.AdminEmail == "" {
		log.Printf("WARNING: no admin exists; set ADMIN_MOBILE or ADMIN_EMAIL to create the first administrator")
		return nil
	}
	_, err := a.db.Exec(`INSERT INTO users(name,mobile,email,role,created_at) VALUES(?,?,?,?,?)`,
		a.cfg.AdminName, nullIfEmpty(normIdent(a.cfg.AdminMobile)), nullIfEmpty(normIdent(a.cfg.AdminEmail)), "admin", nowStr())
	return err
}

func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func (a *App) deliverOTP(u *User, ident, code string) {
	if a.cfg.DevOTP {
		log.Printf("DEV OTP for %s: %s", ident, code)
	}
	msg := fmt.Sprintf("Your marks-entry login code is %s (valid 5 minutes).", code)
	if a.cfg.OTPWebhookURL != "" {
		body, _ := json.Marshal(map[string]string{"to": ident, "message": msg, "code": code})
		go func() {
			resp, err := http.Post(a.cfg.OTPWebhookURL, "application/json", bytes.NewReader(body))
			if err != nil {
				log.Printf("otp webhook: %v", err)
				return
			}
			resp.Body.Close()
		}()
	}
	if a.cfg.SMTPHost != "" && u.Email != "" {
		go func() {
			var auth smtp.Auth
			if a.cfg.SMTPUser != "" {
				auth = smtp.PlainAuth("", a.cfg.SMTPUser, a.cfg.SMTPPass, strings.Split(a.cfg.SMTPHost, ":")[0])
			}
			m := "From: " + a.cfg.SMTPFrom + "\r\nTo: " + u.Email + "\r\nSubject: Your login code\r\n\r\n" + msg
			if err := smtp.SendMail(a.cfg.SMTPHost, auth, a.cfg.SMTPFrom, []string{u.Email}, []byte(m)); err != nil {
				log.Printf("otp smtp: %v", err)
			}
		}()
	}
}

func (a *App) otpRequest(w http.ResponseWriter, r *http.Request) {
	var in struct{ Identifier string }
	if err := decode(r, &in); err != nil {
		writeErr(w, err)
		return
	}
	ident := normIdent(in.Identifier)
	out := map[string]any{"ok": true}
	u, err := a.findUser(ident)
	if err == nil {
		var sent string
		if e := a.db.QueryRow(`SELECT sent_at FROM otps WHERE identifier=?`, ident).Scan(&sent); e == nil {
			if t, _ := time.Parse(time.RFC3339, sent); time.Since(t) < 30*time.Second && !a.cfg.DevOTP {
				writeErr(w, &apiErr{Code: 429, Msg: "Please wait 30 seconds before requesting another code"})
				return
			}
		}
		n, _ := rand.Int(rand.Reader, big.NewInt(1000000))
		code := fmt.Sprintf("%06d", n.Int64())
		_, err = a.db.Exec(`INSERT INTO otps(identifier,code_hash,expires_at,attempts,sent_at) VALUES(?,?,?,0,?)
			ON CONFLICT(identifier) DO UPDATE SET code_hash=excluded.code_hash, expires_at=excluded.expires_at, attempts=0, sent_at=excluded.sent_at`,
			ident, sha(ident+":"+code), time.Now().Add(5*time.Minute).UTC().Format(time.RFC3339), nowStr())
		if err != nil {
			writeErr(w, err)
			return
		}
		a.deliverOTP(u, ident, code)
		if a.cfg.DevOTP {
			out["dev_code"] = code
		}
	}
	writeJSON(w, 200, out)
}

func (a *App) otpVerify(w http.ResponseWriter, r *http.Request) {
	var in struct{ Identifier, Code string }
	if err := decode(r, &in); err != nil {
		writeErr(w, err)
		return
	}
	ident := normIdent(in.Identifier)
	invalid := &apiErr{Code: 401, Msg: "Invalid or expired code"}
	var hash, exp string
	var attempts int
	err := a.db.QueryRow(`SELECT code_hash,expires_at,attempts FROM otps WHERE identifier=?`, ident).Scan(&hash, &exp, &attempts)
	if err != nil {
		writeErr(w, invalid)
		return
	}
	t, _ := time.Parse(time.RFC3339, exp)
	if attempts >= 5 || time.Now().After(t) {
		a.db.Exec(`DELETE FROM otps WHERE identifier=?`, ident)
		writeErr(w, invalid)
		return
	}
	if subtle.ConstantTimeCompare([]byte(hash), []byte(sha(ident+":"+strings.TrimSpace(in.Code)))) != 1 {
		a.db.Exec(`UPDATE otps SET attempts=attempts+1 WHERE identifier=?`, ident)
		writeErr(w, invalid)
		return
	}
	u, err := a.findUser(ident)
	if err != nil {
		writeErr(w, invalid)
		return
	}
	a.db.Exec(`DELETE FROM otps WHERE identifier=?`, ident)
	tok := randHex(32)
	exp = time.Now().Add(12 * time.Hour).UTC().Format(time.RFC3339)
	if _, err := a.db.Exec(`INSERT INTO sessions(token_hash,user_id,expires_at) VALUES(?,?,?)`, sha(tok), u.ID, exp); err != nil {
		writeErr(w, err)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: "rea_session", Value: tok, Path: "/", HttpOnly: true, SameSite: http.SameSiteLaxMode,
		Secure: a.cfg.CookieSecure, MaxAge: 12 * 3600})
	writeJSON(w, 200, map[string]any{"ok": true, "user": u})
}

func (a *App) logout(w http.ResponseWriter, r *http.Request, u *User) error {
	if c, err := r.Cookie("rea_session"); err == nil {
		a.db.Exec(`DELETE FROM sessions WHERE token_hash=?`, sha(c.Value))
	}
	http.SetCookie(w, &http.Cookie{Name: "rea_session", Value: "", Path: "/", MaxAge: -1})
	writeJSON(w, 200, map[string]any{"ok": true})
	return nil
}

func (a *App) userFromRequest(r *http.Request) *User {
	c, err := r.Cookie("rea_session")
	if err != nil {
		return nil
	}
	u := &User{}
	var mob, em sql.NullString
	err = a.db.QueryRow(`SELECT u.id,u.name,u.mobile,u.email,u.role,u.active FROM sessions s JOIN users u ON u.id=s.user_id
		WHERE s.token_hash=? AND s.expires_at>? AND u.active=1`, sha(c.Value), nowStr()).Scan(&u.ID, &u.Name, &mob, &em, &u.Role, &u.Active)
	if err != nil {
		return nil
	}
	u.Mobile, u.Email = mob.String, em.String
	return u
}

type handler func(w http.ResponseWriter, r *http.Request, u *User) error

func (a *App) wrap(role string, h handler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Header.Get("X-Requested-With") != "rea" {
			writeErr(w, forbidden("missing CSRF header"))
			return
		}
		u := a.userFromRequest(r)
		if u == nil {
			writeErr(w, &apiErr{Code: 401, Msg: "Please sign in"})
			return
		}
		if role == "admin" && !u.IsAdmin() {
			writeErr(w, forbidden("Administrator access required"))
			return
		}
		r = r.WithContext(context.WithValue(r.Context(), ctxKey{}, u))
		tw := &timingWriter{ResponseWriter: w, start: time.Now()}
		if err := h(tw, r, u); err != nil {
			writeErr(w, err)
		}
	}
}

// timingWriter exposes handler duration (auth excluded) as a Server-Timing header for latency audits.
type timingWriter struct {
	http.ResponseWriter
	start time.Time
	sent  bool
}

func (t *timingWriter) WriteHeader(code int) {
	if !t.sent {
		t.sent = true
		t.Header().Set("Server-Timing", fmt.Sprintf("app;dur=%.2f", float64(time.Since(t.start).Microseconds())/1000))
	}
	t.ResponseWriter.WriteHeader(code)
}

func (t *timingWriter) Write(b []byte) (int, error) {
	if !t.sent {
		t.WriteHeader(200)
	}
	return t.ResponseWriter.Write(b)
}

func writeErr(w http.ResponseWriter, err error) {
	if ae, ok := err.(*apiErr); ok {
		body := map[string]any{"error": ae.Msg}
		for k, v := range ae.Extra {
			body[k] = v
		}
		writeJSON(w, ae.Code, body)
		return
	}
	log.Printf("internal error: %v", err)
	writeJSON(w, 500, map[string]any{"error": "Internal error"})
}
