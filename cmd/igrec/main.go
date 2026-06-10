package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"igrec.net/igrec/internal/app"
	"igrec.net/igrec/internal/store"
)

func main() {
	cfg := app.Config{
		BaseURL:     env("BASE_URL", "http://localhost:8080"),
		Addr:        env("ADDR", ":8080"),
		DatabaseURL: env("DATABASE_URL", "sqlite://igrec.db"),
		AppSecret:   os.Getenv("APP_SECRET"),
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
		ApplePass: app.ApplePassConfig{
			PassTypeID:  os.Getenv("APPLE_PASS_TYPE_ID"),
			TeamID:      os.Getenv("APPLE_TEAM_ID"),
			CertPath:    os.Getenv("APPLE_PASS_CERT_PATH"),
			KeyPath:     os.Getenv("APPLE_PASS_KEY_PATH"),
			KeyPassword: os.Getenv("APPLE_PASS_KEY_PASSWORD"),
			WWDRPath:    os.Getenv("APPLE_WWDR_CERT_PATH"),
		},
	}
	if err := cfg.Validate(); err != nil {
		log.Fatal(err)
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
		case "daily-email-status":
			if err := printDailyEmailStatus(db); err != nil {
				log.Fatal(err)
			}
			return
		case "backup-sqlite":
			keep := 14
			if raw := os.Getenv("BACKUP_KEEP"); raw != "" {
				if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 {
					keep = parsed
				}
			}
			path, err := backupSQLite(db, cfg.DatabaseURL, env("BACKUP_DIR", "data/backups"), keep)
			if err != nil {
				log.Fatal(err)
			}
			log.Printf("sqlite backup written to %s", path)
			return
		case "retry-activitypub":
			limit := 100
			if raw := os.Getenv("ACTIVITYPUB_RETRY_LIMIT"); raw != "" {
				if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 {
					limit = parsed
				}
			}
			delivered, failed, err := app.NewAppForJobs(cfg, db).RetryActivityPubDeliveries(limit)
			if err != nil {
				log.Fatal(err)
			}
			log.Printf("activitypub retry delivered=%d failed=%d", delivered, failed)
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

	errs := make(chan error, 1)
	go func() {
		log.Printf("igrec listening on %s", cfg.Addr)
		errs <- srv.ListenAndServe()
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)

	select {
	case err := <-errs:
		log.Fatal(err)
	case sig := <-stop:
		log.Printf("received %s, shutting down", sig)
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := srv.Shutdown(ctx); err != nil {
			log.Printf("shutdown error: %v", err)
		}
	}
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
