package main

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"
)

//go:embed web
var webFS embed.FS

type Config struct {
	Addr          string
	DBPath        string
	DevOTP        bool
	SeedDemo      bool
	MockFrappe    string
	FrappeURL     string
	FrappeKey     string
	FrappeSecret  string
	GradingScale  string
	StatusField   string // custom field on Assessment Result holding AB / EX
	AdmissionFld  string // field on Student holding the admission number
	AdminName     string
	AdminMobile   string
	AdminEmail    string
	SMTPHost      string
	SMTPUser      string
	SMTPPass      string
	SMTPFrom      string
	OTPWebhookURL string
	CookieSecure  bool
}

func env(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}

func loadConfig() Config {
	return Config{
		Addr:          env("ADDR", ":8080"),
		DBPath:        env("DB_PATH", "./data/rapid.db"),
		DevOTP:        os.Getenv("DEV_OTP") == "1",
		SeedDemo:      os.Getenv("SEED_DEMO") == "1",
		MockFrappe:    os.Getenv("MOCK_FRAPPE_ADDR"),
		FrappeURL:     strings.TrimRight(os.Getenv("FRAPPE_URL"), "/"),
		FrappeKey:     os.Getenv("FRAPPE_API_KEY"),
		FrappeSecret:  os.Getenv("FRAPPE_API_SECRET"),
		GradingScale:  env("FRAPPE_GRADING_SCALE", "CBSE"),
		StatusField:   env("FRAPPE_STATUS_FIELD", "custom_marks_status"),
		AdmissionFld:  env("FRAPPE_ADMISSION_FIELD", "admission_number"),
		AdminName:     env("ADMIN_NAME", "Exam Coordinator"),
		AdminMobile:   os.Getenv("ADMIN_MOBILE"),
		AdminEmail:    os.Getenv("ADMIN_EMAIL"),
		SMTPHost:      os.Getenv("SMTP_HOST"),
		SMTPUser:      os.Getenv("SMTP_USER"),
		SMTPPass:      os.Getenv("SMTP_PASS"),
		SMTPFrom:      os.Getenv("SMTP_FROM"),
		OTPWebhookURL: os.Getenv("OTP_WEBHOOK_URL"),
		CookieSecure:  os.Getenv("COOKIE_SECURE") == "1",
	}
}

type App struct {
	wmu    sync.Mutex
	db     *sql.DB
	cfg    Config
	frappe *Frappe
	kick   chan struct{}
}

func main() {
	cfg := loadConfig()
	db, err := openDB(cfg.DBPath)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	if cfg.MockFrappe != "" {
		go runMockFrappe(cfg.MockFrappe)
		if cfg.FrappeURL == "" {
			cfg.FrappeURL = "http://127.0.0.1" + cfg.MockFrappe
			cfg.FrappeKey, cfg.FrappeSecret = "mock", "mock"
		}
	}
	a := &App{db: db, cfg: cfg, frappe: newFrappe(cfg), kick: make(chan struct{}, 1)}
	if err := a.bootstrapAdmin(); err != nil {
		log.Fatal(err)
	}
	if cfg.SeedDemo {
		if err := a.seedDemo(); err != nil {
			log.Fatal(err)
		}
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go a.syncWorker(ctx)
	go a.masterScheduler(ctx)

	sub, _ := fs.Sub(webFS, "web")
	mux := http.NewServeMux()
	a.routes(mux)
	files := http.FileServer(http.FS(sub))
	mux.Handle("/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-cache")
		files.ServeHTTP(w, r)
	}))
	srv := &http.Server{Addr: cfg.Addr, Handler: securityHeaders(mux), ReadHeaderTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		sc, c := context.WithTimeout(context.Background(), 5*time.Second)
		defer c()
		_ = srv.Shutdown(sc)
	}()
	log.Printf("rapid-entry listening on %s (frappe=%v)", cfg.Addr, a.frappe.Enabled())
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:")
		next.ServeHTTP(w, r)
	})
}
