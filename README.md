# Rapid-Entry Assessment Kiosk (Phase 1)

Single-purpose marks-entry edge app for a Frappe Education ERP. One Go binary, pure-Go SQLite (WAL), and an
embedded vanilla-JS SPA (no build step). Teachers grade in a flashcard or table view; every mark is validated
and stored locally first, then pushed to Frappe by a background outbox worker.

## Run

```bash
go build -o rapid-entry .
# local demo: seeded demo teachers + an in-process mock Frappe server
DEV_OTP=1 SEED_DEMO=1 ADMIN_MOBILE=9000000000 MOCK_FRAPPE_ADDR=:8099 ./rapid-entry
# open http://localhost:8080  — log in as 9000000000 (admin), 9000000001 (Teacher A), 9000000002 (Teacher B)
# DEV_OTP=1 shows the OTP on the login screen and in the log. Never enable it in production.
```

Tests (need Node + Playwright for the `ui-*` suites): `tests/run-all.sh` — each suite gets a fresh DB and mock Frappe.
Demo parents: any Grade 8-A admission number with the last 4 digits of `father_mobile` or `mother_mobile` from My classes.

## Spaces

The app is branded **GCM UPMARK** ("Bold School" look, light and dark, phone first). It installs to a phone's home
screen from Chrome and keeps working offline. Three spaces:

* **Teacher space** (WorkCode sign-in): Today, New test (one screen: class, test, date, subject, out of), My tests,
  Updates, Weekly reports, My classes. Marking is in a card view with a keypad, or a table with **quick type**
  (`17 14.5 ab 19` fills a row of students in one go). Teachers lock a sheet and send it to the admin; they never
  send anything to parents. Teachers are notified when a sheet is approved, sent back, remarked on, when a
  correction is decided, and when their classes' weekly reports go out.
* **Admin space**: Pulse, Approvals (one inbox for locked sheets and teachers' correction requests, with checks
  before approving: spread, zeros not marked absent, high absence, identical marks, drops against the previous test),
  Marks explorer, Weekly analysis, Student reports, Corrections log, Classes, Students, Teacher allocation,
  Teachers, Frappe sync.
* **Parent portal** (`/#/parent`): sign in with the admission number and the last 4 digits of the father's or the
  mother's mobile number. Shows the weekly report built from approved marks (Monday to Sunday): average, class-section
  percentile, subject bars against the class average, the term trend of weekly percentiles, and an "I've seen this"
  button. English, Hindi and Punjabi, picked from the phone's language and switchable. Weekly reports are published
  automatically each week; there is no WhatsApp or SMS sending of results.

Offline: the shell is cached by a service worker, open sheets are kept on the phone, and unsaved marks sync when the
connection returns. Locking a sheet needs a connection.

Routing is by role on both the client and the API (`/api/admin/*` returns 403 for teachers, parent sessions reach only
`/api/parent/*`).

## Configuration (environment)

| Variable | Purpose |
|---|---|
| `ADDR` (`:8080`), `DB_PATH` (`./data/rapid.db`) | listen address, SQLite file |
| `ADMIN_MOBILE` / `ADMIN_EMAIL` / `ADMIN_NAME` | creates the first administrator when none exists |
| `FRAPPE_URL`, `FRAPPE_API_KEY`, `FRAPPE_API_SECRET` | Frappe REST access (`Authorization: token key:secret`) |
| `FRAPPE_GRADING_SCALE` (`CBSE`) | Grading Scale used on auto-created Assessment Plans |
| `FRAPPE_STATUS_FIELD` (`custom_marks_status`) | custom Data field on **Assessment Result** that stores `AB` / `EX` |
| `FRAPPE_ADMISSION_FIELD` (`admission_number`) | field on **Student** holding the admission number (falls back to Student ID) |
| `SMTP_HOST` (`host:port`), `SMTP_USER`, `SMTP_PASS`, `SMTP_FROM` | email OTP delivery |
| `OTP_WEBHOOK_URL` | POSTs `{to, message, code}` to the staff sign-in code gateway of your choice |
| `COOKIE_SECURE=1` | set behind HTTPS |

## Architecture

* `exams.go` — scoping, the 7 business rules, sanity gate, draft/lock/discard, corrections.
* `admin.go` — admin console API (users, allocations, explorer, corrections inbox, sync health).
* `frappe.go` — Frappe REST client, 7-day/on-demand master pull, outbox worker (exponential back-off 10 s → 10 min).
* `db.go` — schema; the `timeline` table is append-only (DB triggers reject UPDATE/DELETE).
* `web/` — SPA (`go:embed`). Local auto-stash = `localStorage` key `rea:stash:<examId>`, cleared after a successful save.
* Sync health: 🟡 draft · 🟢 locked and all outbox jobs done · 🔴 any job retrying, or a locked test not yet pushed.
* Draft saves are debounced into one Frappe push ~8 s later; locking and approved corrections push immediately.
* Writes are serialised in-process (one writer, WAL), which keeps handler latency flat under 20 concurrent teachers.

## UAT status (against the mock Frappe)

| # | Test | Status |
|---|---|---|
| 1 | Footprint & speed | RSS ≈ 23 MB (app + mock Frappe in one process). Handler p95 ≈ 2 ms with 20 teachers saving every ~0.5 s; a zero-think-time burst of 20 clients gives p95 ≈ 20 ms (queueing). **Re-measure on target hardware.** |
| 2 | Scoping & OTP | automated |
| 3 | 2-draft ceiling | automated |
| 4 | Same-day subject rule (+ combined overlap) | automated |
| 5 | CBSE split & sanity gate (UI + API) | automated |
| 6 | 3-way sort & view toggle, no lost marks | automated |
| 7 | Crash recovery | automated (tab closed, test reopened) |
| 8 | 100 % roster lock gate | automated |
| 9 | Surgical correction & timeline | automated (UI + ERP amend) |
| 10 | Master sync & health badges | automated against the mock; the 7-day scheduler itself is time-based and **not** exercised |

## Must be verified against a real Frappe Education site before go-live

These are assumptions the mock cannot prove:

1. **Custom field** `custom_marks_status` (Data) on *Assessment Result* must exist, or set `FRAPPE_STATUS_FIELD=""` to skip it.
2. **Student Group naming**: class/section are derived from names like `Grade 8-A` (class `Grade 8`, section `A`). Other schemes need a change to `splitGroup`.
3. **Assessment Plan** is created with a fixed 09:00–10:00 slot, and submitted on lock; **Assessment Result** corrections use cancel + amend (`amended_from`). Check these against your Frappe version's validations (grading scale, plan uniqueness, required fields).
4. **Instructor → allocations** mapping matches Instructor → Employee → `user_id`/`prefered_email` to the teacher's email here; teachers with only a mobile number need manual allocations.
5. **API key permissions** for Student Group, Student, Course, Instructor, Employee, Assessment Group/Criteria/Plan/Result.

## Design decisions to confirm

* A combined multi-subject test shares one component layout/max across its subjects.
* A teacher assigned only some subjects of a combined test cannot lock it (admin or a teacher assigned all subjects can).
* Uniqueness follows the spec literally: `(class/section + category + date + subject)`, so FA1 and Weekly Test for the same subject on one date collide.
* Future-dated tests are rejected; past dates are allowed.
* Student rows no longer present in Frappe are retired (hidden, history kept), never deleted.
