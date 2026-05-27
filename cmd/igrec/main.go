package main

import (
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"igrec.net/igrec/internal/app"
	"igrec.net/igrec/internal/store"
)

func main() {
	cfg := app.Config{
		BaseURL:      env("BASE_URL", "http://localhost:8080"),
		Addr:         env("ADDR", ":8080"),
		DatabaseURL:  env("DATABASE_URL", "sqlite://igrec.db"),
		AppSecret:    os.Getenv("APP_SECRET"),
		OperatorEmails: splitCSV(
			os.Getenv("OPERATOR_EMAILS"),
		),
		UploadDir:    env("UPLOAD_DIR", "data/uploads"),
		ResendAPIKey: os.Getenv("RESEND_API_KEY"),
		LoginEmailFrom: envFallback(
			[]string{"LOGIN_EMAIL_FROM", "EMAIL_FROM"},
			"Y <!@igrec.net>",
		),
		DailyEmailFrom: envFallback(
			[]string{"DAILY_EMAIL_FROM", "EMAIL_FROM"},
			"Y <_@igrec.net>",
		),
		VAPIDPublic:  os.Getenv("VAPID_PUBLIC_KEY"),
		VAPIDPrivate: os.Getenv("VAPID_PRIVATE_KEY"),
	}

	db, err := store.Open(cfg.DatabaseURL)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	if err := db.Migrate(); err != nil {
		log.Fatal(err)
	}

	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "send-daily-email":
			sent, err := sendDailyEmails(cfg, db)
			if err != nil {
				log.Fatal(err)
			}
			log.Printf("daily email sent to %d users", sent)
			return
		default:
			log.Fatalf("unknown command %q", os.Args[1])
		}
	}

	srv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           app.New(cfg, db),
		ReadHeaderTimeout: 5 * time.Second,
	}

	log.Printf("igrec listening on %s", cfg.Addr)
	log.Fatal(srv.ListenAndServe())
}

func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func envFallback(keys []string, fallback string) string {
	for _, key := range keys {
		if value := os.Getenv(key); value != "" {
			return value
		}
	}
	return fallback
}

func splitCSV(raw string) []string {
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	values := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			values = append(values, part)
		}
	}
	return values
}
