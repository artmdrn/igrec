package app

import (
	"html/template"
	"net/http"
	"strings"

	"igrec.net/igrec/internal/store"
)

type Config struct {
	BaseURL        string
	Addr           string
	DatabaseURL    string
	AppSecret      string
	OperatorEmails []string
	UploadDir      string
	ResendAPIKey   string
	LoginEmailFrom string
	DailyEmailFrom string
	VAPIDPublic    string
	VAPIDPrivate   string
	ApplePass      ApplePassConfig
}

type ApplePassConfig struct {
	PassTypeID  string
	TeamID      string
	CertPath    string
	KeyPath     string
	KeyPassword string
	WWDRPath    string
}

type App struct {
	cfg            Config
	db             *store.DB
	templates      *template.Template
	operatorEmails map[string]struct{}
	limiter        *rateLimiter
}

func New(cfg Config, db *store.DB) http.Handler {
	app := NewAppForJobs(cfg, db)

	mux := http.NewServeMux()
	mux.Handle("/static/", staticHandler())
	mux.Handle("/uploads/", http.StripPrefix("/uploads/", http.FileServer(http.Dir(cfg.UploadDir))))
	mux.HandleFunc("/service-worker.js", app.serviceWorker)
	mux.HandleFunc("/", app.route)
	mux.HandleFunc("/healthz", app.healthz)
	mux.HandleFunc("/api/", app.api)
	mux.HandleFunc("/join", app.join)
	mux.HandleFunc("/login", app.login)
	mux.HandleFunc("/logout", app.logout)
	mux.HandleFunc("/friends", app.friends)
	mux.HandleFunc("/u/", app.shortUnsubscribeEmail)
	mux.HandleFunc("/email/unsubscribe", app.unsubscribeEmail)
	mux.HandleFunc("/auth/magic", app.magic)
	mux.HandleFunc("/auth/email", app.confirmEmail)
	mux.HandleFunc("/auth/passkeys/register/options", app.passkeyRegisterOptions)
	mux.HandleFunc("/auth/passkeys/register", app.passkeyRegister)
	mux.HandleFunc("/auth/passkeys/login/options", app.passkeyLoginOptions)
	mux.HandleFunc("/auth/passkeys/login", app.passkeyLogin)
	mux.HandleFunc("/write", app.write)
	mux.HandleFunc("/settings", app.settings)
	mux.HandleFunc("/settings/export", app.export)
	mux.HandleFunc("/wallet/apple.pkpass", app.appleWalletPass)
	mux.HandleFunc("/operator/invites", app.operatorInvites)
	mux.HandleFunc("/admin/invites", app.adminInvites)
	mux.HandleFunc("/inbound/email", app.inboundEmail)
	mux.HandleFunc("/og/", app.postPreviewCard)
	mux.HandleFunc("/.well-known/webfinger", app.webfinger)
	mux.HandleFunc("/ap/users/", app.actor)
	mux.HandleFunc("/manifest.webmanifest", app.manifest)
	return app.withRequestLogging(securityHeaders(mux))
}

func NewAppForJobs(cfg Config, db *store.DB) *App {
	app := &App{
		cfg:            cfg,
		db:             db,
		templates:      parseTemplates(),
		operatorEmails: make(map[string]struct{}),
		limiter:        &rateLimiter{buckets: make(map[string]rateBucket)},
	}
	for _, email := range cfg.OperatorEmails {
		normalized := strings.ToLower(strings.TrimSpace(email))
		if normalized != "" {
			app.operatorEmails[normalized] = struct{}{}
		}
	}
	return app
}
