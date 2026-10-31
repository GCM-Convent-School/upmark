package main

import "fmt"

var demoCourses = []string{"English", "Hindi", "Mathematics", "Punjabi", "Science", "Social Science"}

var demoFirst = []string{"Aarav", "Vivaan", "Aditya", "Arjun", "Sai", "Reyansh", "Ayaan", "Krishna", "Ishaan", "Shaurya", "Ananya", "Diya", "Saanvi", "Aadhya", "Kiara", "Pari", "Myra", "Riya", "Navya", "Ira", "Harpreet", "Gurpreet", "Simran", "Jasleen", "Manpreet", "Rohan", "Kabir", "Tanvi", "Meera", "Neha", "Yash", "Dev", "Zoya", "Aryan", "Isha", "Veer", "Anika", "Rudra", "Sara", "Om"}
var demoLast = []string{"Sharma", "Singh", "Kaur", "Gill", "Verma", "Mehta", "Bansal", "Chopra", "Sethi", "Arora", "Kapoor", "Malhotra"}

type demoStudent struct{ ID, Name, Roll, Admission, FatherMobile, MotherMobile string }

// demoRoster is shared by the local seed and the mock Frappe server so both describe the same students.
func demoRoster(code string, n int) []demoStudent {
	out := make([]demoStudent, n)
	for i := 0; i < n; i++ {
		out[i] = demoStudent{
			ID:        fmt.Sprintf("STU-%s-%03d", code, i+1),
			Name:      demoFirst[(i*7+len(code))%len(demoFirst)] + " " + demoLast[(i*5+i/len(demoFirst))%len(demoLast)],
			Roll:      fmt.Sprint(i + 1),
			Admission: fmt.Sprintf("ADM%d%04d", 2020+len(code)%3, (i*37+11)%n+1000),
			// demo parent numbers: unique last four digits per student so the parent sign-in can be tried
			FatherMobile: fmt.Sprintf("98%04d%04d", len(code)*111, (len(code)*1000+i*13+7)%10000),
			MotherMobile: fmt.Sprintf("97%04d%04d", len(code)*111, (len(code)*1000+i*13+5000)%10000),
		}
	}
	return out
}

var demoGroups = []struct {
	Name, Code string
	N          int
}{{"Grade 8-A", "8A", 35}, {"Grade 10-A", "10A", 38}}

func (a *App) seedDemo() error {
	var n int
	a.db.QueryRow(`SELECT COUNT(*) FROM users WHERE role='teacher'`).Scan(&n)
	if n > 0 {
		return nil
	}
	tx, err := a.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, c := range demoCourses {
		tx.Exec(`INSERT OR IGNORE INTO courses(name) VALUES(?)`, c)
	}
	for _, g := range demoGroups {
		class, section := splitGroup(g.Name)
		for _, s := range demoRoster(g.Code, g.N) {
			tx.Exec(`INSERT OR IGNORE INTO students(frappe_id,admission_no,roll_no,roll_sort,name,student_group,class_name,section,father_mobile,mother_mobile) VALUES(?,?,?,?,?,?,?,?,?,?)`,
				s.ID, s.Admission, s.Roll, rollSort(s.Roll), s.Name, g.Name, class, section, s.FatherMobile, s.MotherMobile)
		}
	}
	mk := func(name, mobile, email string) int64 {
		res, _ := tx.Exec(`INSERT INTO users(name,mobile,email,role,created_at) VALUES(?,?,?,'teacher',?)`, name, mobile, email, nowStr())
		id, _ := res.LastInsertId()
		return id
	}
	alloc := func(uid int64, group string, subjects ...string) {
		c, s := splitGroup(group)
		for _, sub := range subjects {
			tx.Exec(`INSERT OR IGNORE INTO allocations(user_id,class_name,section,subject,source) VALUES(?,?,?,?,'manual')`, uid, c, s, sub)
		}
	}
	ta := mk("Teacher A (Demo)", "9000000001", "teacher.a@school.test")
	alloc(ta, "Grade 8-A", "Hindi", "Punjabi", "Mathematics")
	alloc(ta, "Grade 10-A", "Mathematics")
	tb := mk("Teacher B (Demo)", "9000000002", "teacher.b@school.test")
	alloc(tb, "Grade 8-A", "English", "Science", "Hindi")
	alloc(tb, "Grade 10-A", "English", "Science")
	return tx.Commit()
}
