package main

import (
	"log"
	"net/http"
	"os"
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
