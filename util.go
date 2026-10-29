package main

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
)

func nowStr() string { return time.Now().UTC().Format(time.RFC3339) }

func randHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func sha(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

// apiErr is returned by handlers to produce a JSON error response.
type apiErr struct {
	Code  int
	Msg   string
	Extra map[string]any
}

func (e *apiErr) Error() string { return e.Msg }

func bad(msg string) error                        { return &apiErr{Code: 400, Msg: msg} }
func forbidden(msg string) error                  { return &apiErr{Code: 403, Msg: msg} }
func notFound(msg string) error                   { return &apiErr{Code: 404, Msg: msg} }
func conflict(msg string, x map[string]any) error { return &apiErr{Code: 409, Msg: msg, Extra: x} }

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func decode(r *http.Request, v any) error {
	r.Body = http.MaxBytesReader(nil, r.Body, 4<<20)
	d := json.NewDecoder(r.Body)
	if err := d.Decode(v); err != nil {
		return bad("invalid JSON body")
	}
	return nil
}

func isHalf(v float64) bool { return math.Abs(v*2-math.Round(v*2)) < 1e-9 }

var sectionRe = regexp.MustCompile(`^(.+?)\s*[-–/ ]\s*([A-Za-z])$`)

// splitGroup derives (class, section) from a Frappe Student Group name such as "Grade 10-A".
func splitGroup(name string) (string, string) {
	name = strings.TrimSpace(name)
	if m := sectionRe.FindStringSubmatch(name); m != nil {
		return strings.TrimSpace(m[1]), strings.ToUpper(m[2])
	}
	// Named sections, e.g. "2ND BLOSSOM" -> class "2ND", section "BLOSSOM".
	if i := strings.Index(name, " "); i > 0 {
		return name[:i], strings.ToUpper(strings.TrimSpace(name[i+1:]))
	}
	return name, ""
}

func groupLabel(class, section string) string {
	if section == "" {
		return class
	}
	return class + "-" + section
}

func rollSort(s string) int {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		return 1 << 30
	}
	return n
}

var errNotConfigured = errors.New("frappe is not configured (set FRAPPE_URL, FRAPPE_API_KEY, FRAPPE_API_SECRET)")
