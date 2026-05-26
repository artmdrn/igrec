package app

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"net/mail"
	"net/url"
	"regexp"
	"strings"
	"time"

	"igrec.net/igrec/internal/activitypub"
	emailpkg "igrec.net/igrec/internal/email"
	"igrec.net/igrec/internal/store"
	"igrec.net/igrec/internal/word"
)

type Config struct {
	BaseURL        string
	Addr           string
	DatabaseURL    string
	AppSecret      string
	ResendAPIKey   string
	LoginEmailFrom string
	DailyEmailFrom string
	VAPIDPublic    string
	VAPIDPrivate   string
}

type App struct {
	cfg       Config
	db        *store.DB
	templates *template.Template
}

type postView struct {
	store.Post
	DisplayTime string
	MachineTime string
}

type archiveMonth struct {
	Year  string
	Month string
	Href  string
	Label string
	Count int
}

type inviteView struct {
	Code string
	Link string
	Used bool
}

type apiPostView struct {
	ID        int64   `json:"id"`
	Word      string  `json:"word"`
	URL       string  `json:"url"`
	ImageURL  *string `json:"image_url,omitempty"`
	CreatedAt string  `json:"created_at"`
}

const sessionCookie = "igrec_session"
const csrfCookie = "igrec_csrf"
const csrfField = "csrf_token"

var usernamePattern = regexp.MustCompile(`^[A-Za-z0-9_]{1,32}$`)

func New(cfg Config, db *store.DB) http.Handler {
	app := &App{
		cfg:       cfg,
		db:        db,
		templates: template.Must(template.ParseGlob("web/templates/*.html")),
	}

	mux := http.NewServeMux()
	mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.Dir("web/static"))))
	mux.HandleFunc("/", app.route)
	mux.HandleFunc("/healthz", app.healthz)
	mux.HandleFunc("/api/", app.api)
	mux.HandleFunc("/join", app.join)
	mux.HandleFunc("/login", app.login)
	mux.HandleFunc("/logout", app.logout)
	mux.HandleFunc("/auth/magic", app.magic)
	mux.HandleFunc("/auth/email", app.confirmEmail)
	mux.HandleFunc("/auth/passkeys/register/options", app.passkeyRegisterOptions)
	mux.HandleFunc("/auth/passkeys/register", app.passkeyRegister)
	mux.HandleFunc("/auth/passkeys/login/options", app.passkeyLoginOptions)
	mux.HandleFunc("/auth/passkeys/login", app.passkeyLogin)
	mux.HandleFunc("/write", app.write)
	mux.HandleFunc("/settings", app.settings)
	mux.HandleFunc("/settings/export", app.export)
	mux.HandleFunc("/admin/invites", app.adminInvites)
	mux.HandleFunc("/inbound/email", app.inboundEmail)
	mux.HandleFunc("/.well-known/webfinger", app.webfinger)
	mux.HandleFunc("/ap/users/", app.actor)
	mux.HandleFunc("/manifest.webmanifest", app.manifest)
	return mux
}

func (a *App) healthz(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := a.db.Ping(); err != nil {
		http.Error(w, "database unavailable", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte("ok\n"))
}

func (a *App) route(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/" {
		a.firehose(w, r)
		return
	}
	if strings.HasPrefix(r.URL.Path, "/@") {
		a.profile(w, r)
		return
	}
	http.NotFound(w, r)
}

func (a *App) api(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/@"), "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] != "words" {
		http.NotFound(w, r)
		return
	}
	username, err := url.PathUnescape(parts[0])
	if err != nil || username == "" {
		http.NotFound(w, r)
		return
	}
	user, err := a.db.UserByUsername(username)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	posts, err := a.db.AllPostsByUser(user.Username)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, "application/json; charset=utf-8", map[string]any{
		"user": map[string]any{
			"username":             user.Username,
			"url":                  strings.TrimRight(a.cfg.BaseURL, "/") + "/@" + url.PathEscape(user.Username),
			"fediverse":            "@" + user.Username + "@igrec.net",
			"timestamp_preference": user.TimestampPreference,
		},
		"words": apiPostViews(a.cfg.BaseURL, posts),
	})
}

func (a *App) firehose(w http.ResponseWriter, r *http.Request) {
	posts, err := a.db.Firehose(100)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	a.render(w, "index.html", map[string]any{"Posts": posts})
}

func (a *App) profile(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/@"), "/")
	username, err := url.PathUnescape(parts[0])
	if err != nil || username == "" {
		http.NotFound(w, r)
		return
	}
	user, err := a.db.UserByUsername(username)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	posts, err := a.db.PostsByUser(user.Username, 100)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	months := archiveMonths(user.Username, posts)
	title := ""
	if len(parts) > 1 && isArchiveYear(parts[1]) {
		var ok bool
		posts, title, ok = filterArchivePosts(posts, parts)
		if !ok {
			http.NotFound(w, r)
			return
		}
	}
	a.render(w, "profile.html", map[string]any{"User": user, "Posts": profilePostViews(posts, user.TimestampPreference), "Months": months, "Title": title})
}

func (a *App) write(w http.ResponseWriter, r *http.Request) {
	user, ok := a.currentUser(r)
	if !ok {
		a.requireLogin(w, r)
		return
	}

	switch r.Method {
	case http.MethodGet:
		a.render(w, "write.html", a.withCSRF(w, r, nil))
	case http.MethodPost:
		if !a.validCSRF(r) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		value, err := word.Normalize(r.FormValue("word"))
		if err != nil {
			a.render(w, "write.html", a.withCSRF(w, r, map[string]any{"Error": err.Error(), "Word": r.FormValue("word")}))
			return
		}
		var imageURL *string
		if raw := strings.TrimSpace(r.FormValue("image_url")); raw != "" {
			imageURL = &raw
		}
		post, err := a.db.CreatePost(user.ID, value, imageURL)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		http.Redirect(w, r, "/@"+url.PathEscape(post.Username)+"/"+url.PathEscape(post.Word), http.StatusSeeOther)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (a *App) settings(w http.ResponseWriter, r *http.Request) {
	user, ok := a.currentUser(r)
	if !ok {
		a.requireLogin(w, r)
		return
	}

	switch r.Method {
	case http.MethodGet:
		a.render(w, "settings.html", a.withCSRF(w, r, a.settingsData(user, nil)))
	case http.MethodPost:
		if !a.validCSRF(r) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		if r.FormValue("action") == "invite" {
			created, err := a.db.InviteCountByInviter(user.ID)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			if created >= 3 {
				a.render(w, "settings.html", a.withCSRF(w, r, a.settingsData(user, map[string]any{"Error": "all invites are already made"})))
				return
			}
			code, err := newInviteCode()
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			if err := a.db.CreateInviteForUser(code, user.ID); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			a.render(w, "settings.html", a.withCSRF(w, r, a.settingsData(user, map[string]any{"Notice": "invite made"})))
			return
		}
		emailNotice := ""
		email := strings.ToLower(strings.TrimSpace(r.FormValue("email")))
		if email != "" && !strings.EqualFold(email, user.Email) {
			if _, err := mail.ParseAddress(email); err != nil {
				a.render(w, "settings.html", a.withCSRF(w, r, a.settingsData(user, map[string]any{"Error": "email is not valid"})))
				return
			}
			token, tokenHash, err := newToken()
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			if err := a.db.CreateEmailChangeToken(tokenHash, user.ID, email, time.Now().Add(30*time.Minute)); err != nil {
				a.render(w, "settings.html", a.withCSRF(w, r, a.settingsData(user, map[string]any{"Error": "email is already used"})))
				return
			}
			link := strings.TrimRight(a.cfg.BaseURL, "/") + "/auth/email?token=" + url.QueryEscape(token)
			body := "confirm this email for igrec:\n\n" + link + "\n\nthis link expires in 30 minutes.\n"
			err = (emailpkg.Resend{APIKey: a.cfg.ResendAPIKey, From: a.cfg.LoginEmailFrom}).SendPlain(email, "confirm igrec email", body)
			if err != nil {
				a.render(w, "settings.html", a.withCSRF(w, r, a.settingsData(user, map[string]any{"Error": err.Error()})))
				return
			}
			emailNotice = "check email to confirm"
		}
		if err := a.db.UpdateSettings(user.ID, r.FormValue("timestamp_preference"), r.FormValue("daily") == "on"); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if emailNotice != "" {
			user, _ = a.db.UserByUsername(user.Username)
			a.render(w, "settings.html", a.withCSRF(w, r, a.settingsData(user, map[string]any{"Notice": emailNotice})))
			return
		}
		http.Redirect(w, r, "/settings", http.StatusSeeOther)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (a *App) export(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	user, ok := a.currentUser(r)
	if !ok {
		a.requireLogin(w, r)
		return
	}
	posts, err := a.db.AllPostsByUser(user.Username)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Disposition", `attachment; filename="igrec-export-`+user.Username+`.json"`)
	writeJSON(w, "application/json; charset=utf-8", map[string]any{
		"exported_at": time.Now().UTC().Format(time.RFC3339),
		"user": map[string]any{
			"username":             user.Username,
			"url":                  strings.TrimRight(a.cfg.BaseURL, "/") + "/@" + url.PathEscape(user.Username),
			"fediverse":            "@" + user.Username + "@igrec.net",
			"domain":               user.Domain,
			"fediverse_acct":       user.FediverseAcct,
			"timestamp_preference": user.TimestampPreference,
			"created_at":           user.CreatedAt.UTC().Format(time.RFC3339),
		},
		"activitypub": map[string]any{
			"actor":  activitypub.Actor(a.cfg.BaseURL, user),
			"outbox": activitypubOutbox(a.cfg.BaseURL, posts),
		},
		"words": apiPostViews(a.cfg.BaseURL, posts),
	})
}

func (a *App) join(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		a.render(w, "join.html", a.withCSRF(w, r, map[string]any{"Invite": r.URL.Query().Get("invite")}))
	case http.MethodPost:
		if !a.validCSRF(r) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		inviteCode := strings.TrimSpace(r.FormValue("invite"))
		username, usernameErr := normalizeSignupUsername(r.FormValue("username"))
		email := strings.ToLower(strings.TrimSpace(r.FormValue("email")))
		if usernameErr != nil {
			a.render(w, "join.html", a.withCSRF(w, r, map[string]any{"Error": usernameErr.Error(), "Invite": inviteCode, "Username": r.FormValue("username"), "Email": email}))
			return
		}
		if _, err := mail.ParseAddress(email); err != nil {
			a.render(w, "join.html", a.withCSRF(w, r, map[string]any{"Error": "email is not valid", "Invite": inviteCode, "Username": username, "Email": email}))
			return
		}
		invite, err := a.db.InviteByCode(inviteCode)
		if err != nil || invite.UsedAt.Valid {
			a.render(w, "join.html", a.withCSRF(w, r, map[string]any{"Error": "invite is not valid", "Invite": inviteCode, "Username": username, "Email": email}))
			return
		}
		user, err := a.db.CreateUser(username, email)
		if err != nil {
			a.render(w, "join.html", a.withCSRF(w, r, map[string]any{"Error": "username or email is already taken", "Invite": inviteCode, "Username": username, "Email": email}))
			return
		}
		if err := a.db.UseInvite(invite.Code, user.ID); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if invite.InviterID.Valid {
			_ = a.db.CreateUserFollow(user.ID, invite.InviterID.Int64)
		}
		if err := a.startSession(w, user.ID); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		http.Redirect(w, r, "/write", http.StatusSeeOther)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (a *App) login(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		a.render(w, "login.html", a.withCSRF(w, r, map[string]any{"Next": r.URL.Query().Get("next")}))
	case http.MethodPost:
		if !a.validCSRF(r) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		email := strings.ToLower(strings.TrimSpace(r.FormValue("email")))
		next := safeNext(r.FormValue("next"))
		user, err := a.db.UserByEmail(email)
		if err != nil {
			a.render(w, "login.html", a.withCSRF(w, r, map[string]any{"Error": "no account uses that email yet", "Email": email, "Next": next}))
			return
		}
		token, tokenHash, err := newToken()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if err := a.db.CreateLoginToken(tokenHash, user.ID, time.Now().Add(20*time.Minute)); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		link := strings.TrimRight(a.cfg.BaseURL, "/") + "/auth/magic?token=" + url.QueryEscape(token) + "&next=" + url.QueryEscape(next)
		body := "sign in to igrec:\n\n" + link + "\n\nthis link expires in 20 minutes.\n"
		err = (emailpkg.Resend{APIKey: a.cfg.ResendAPIKey, From: a.cfg.LoginEmailFrom}).SendPlain(user.Email, "igrec sign in", body)
		if err != nil {
			a.render(w, "login.html", a.withCSRF(w, r, map[string]any{"Error": err.Error(), "Email": email, "Next": next}))
			return
		}
		a.render(w, "login_sent.html", map[string]any{"Email": email})
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (a *App) magic(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("token")
	if token == "" {
		http.NotFound(w, r)
		return
	}
	user, err := a.db.UseLoginToken(hashToken(token))
	if err != nil {
		a.render(w, "login.html", map[string]any{"Error": "login link is invalid or expired"})
		return
	}
	if err := a.startSession(w, user.ID); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, safeNext(r.URL.Query().Get("next")), http.StatusSeeOther)
}

func (a *App) confirmEmail(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("token")
	if token == "" {
		http.NotFound(w, r)
		return
	}
	user, err := a.db.UseEmailChangeToken(hashToken(token))
	if err != nil {
		a.render(w, "settings.html", map[string]any{"User": user, "Error": "email link is invalid or expired"})
		return
	}
	if _, ok := a.currentUser(r); !ok {
		if err := a.startSession(w, user.ID); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}
	http.Redirect(w, r, "/settings", http.StatusSeeOther)
}

func (a *App) logout(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie(sessionCookie); err == nil {
		_ = a.db.DeleteSession(hashToken(cookie.Value))
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: a.secureCookies()})
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (a *App) adminInvites(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		a.render(w, "admin_invites.html", a.withCSRF(w, r, nil))
		return
	}
	if !a.validCSRF(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if a.cfg.AppSecret == "" || subtle.ConstantTimeCompare([]byte(r.FormValue("secret")), []byte(a.cfg.AppSecret)) != 1 {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	code, err := newInviteCode()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := a.db.CreateInvite(code); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	link := strings.TrimRight(a.cfg.BaseURL, "/") + "/join?invite=" + url.QueryEscape(code)
	a.render(w, "admin_invites.html", a.withCSRF(w, r, map[string]any{"InviteLink": link}))
}

func (a *App) inboundEmail(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if a.cfg.AppSecret != "" && subtle.ConstantTimeCompare([]byte(r.Header.Get("X-Igrec-Secret")), []byte(a.cfg.AppSecret)) != 1 {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	var payload struct {
		From string `json:"from"`
		To   string `json:"to"`
		Text string `json:"text"`
	}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	value, err := firstInboundWord(payload.Text)
	if err != nil {
		http.Error(w, "no word found", http.StatusBadRequest)
		return
	}
	user, err := a.inboundUser(payload.From, payload.To)
	if err != nil {
		http.Error(w, "sender is not an igrec user", http.StatusNotFound)
		return
	}
	post, err := a.db.CreatePost(user.ID, value, nil)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, "application/json; charset=utf-8", map[string]any{"ok": true, "post": post})
}

func (a *App) webfinger(w http.ResponseWriter, r *http.Request) {
	resource := r.URL.Query().Get("resource")
	if !strings.HasPrefix(resource, "acct:") {
		http.NotFound(w, r)
		return
	}
	name := strings.TrimPrefix(strings.Split(strings.TrimPrefix(resource, "acct:"), "@")[0], "@")
	if name == "" {
		http.NotFound(w, r)
		return
	}
	if _, err := a.db.UserByUsername(name); err != nil {
		http.NotFound(w, r)
		return
	}
	writeJSON(w, "application/jrd+json; charset=utf-8", activitypub.WebFinger(a.cfg.BaseURL, name))
}

func (a *App) actor(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/ap/users/")
	parts := strings.Split(rest, "/")
	username, err := url.PathUnescape(parts[0])
	if err != nil || username == "" {
		http.NotFound(w, r)
		return
	}
	user, err := a.db.UserByUsername(username)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if len(parts) > 1 && parts[1] == "outbox" {
		posts, err := a.db.PostsByUser(user.Username, 20)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		items := make([]any, 0, len(posts))
		for _, post := range posts {
			items = append(items, activitypub.Note(a.cfg.BaseURL, post))
		}
		writeJSON(w, "application/activity+json; charset=utf-8", map[string]any{"@context": "https://www.w3.org/ns/activitystreams", "type": "OrderedCollection", "orderedItems": items})
		return
	}
	writeJSON(w, "application/activity+json; charset=utf-8", activitypub.Actor(a.cfg.BaseURL, user))
}

func (a *App) manifest(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, "application/manifest+json; charset=utf-8", map[string]any{
		"name":             "igrec",
		"short_name":       "igrec",
		"start_url":        "/write",
		"display":          "standalone",
		"background_color": "#f7f0df",
		"theme_color":      "#111111",
		"icons": []any{
			map[string]string{"src": "/static/icon-192.png?v=20260521-french", "sizes": "192x192", "type": "image/png"},
			map[string]string{"src": "/static/icon-512.png?v=20260521-french", "sizes": "512x512", "type": "image/png"},
		},
	})
}

func (a *App) render(w http.ResponseWriter, name string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := a.templates.ExecuteTemplate(w, name, data); err != nil && !errors.Is(err, http.ErrHandlerTimeout) {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (a *App) withCSRF(w http.ResponseWriter, r *http.Request, data map[string]any) map[string]any {
	if data == nil {
		data = map[string]any{}
	}
	token, err := a.ensureCSRFToken(w, r)
	if err == nil {
		data["CSRFToken"] = token
	}
	return data
}

func (a *App) ensureCSRFToken(w http.ResponseWriter, r *http.Request) (string, error) {
	if cookie, err := r.Cookie(csrfCookie); err == nil && cookie.Value != "" {
		return cookie.Value, nil
	}
	token, _, err := newToken()
	if err != nil {
		return "", err
	}
	http.SetCookie(w, &http.Cookie{
		Name:     csrfCookie,
		Value:    token,
		Path:     "/",
		HttpOnly: false,
		SameSite: http.SameSiteLaxMode,
		Secure:   a.secureCookies(),
	})
	return token, nil
}

func (a *App) validCSRF(r *http.Request) bool {
	cookie, err := r.Cookie(csrfCookie)
	if err != nil || cookie.Value == "" {
		return false
	}
	formToken := r.FormValue(csrfField)
	if formToken == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(cookie.Value), []byte(formToken)) == 1
}

func (a *App) settingsData(user store.User, extra map[string]any) map[string]any {
	data := map[string]any{"User": user, "VAPIDPublic": a.cfg.VAPIDPublic}
	count, _ := a.db.PasskeyCount(user.ID)
	data["PasskeyCount"] = count

	invites, _ := a.db.InvitesByInviter(user.ID)
	views := make([]inviteView, 0, len(invites))
	for _, invite := range invites {
		views = append(views, inviteView{
			Code: invite.Code,
			Link: strings.TrimRight(a.cfg.BaseURL, "/") + "/join?invite=" + url.QueryEscape(invite.Code),
			Used: invite.UsedAt.Valid,
		})
	}
	data["Invites"] = views
	remaining := 3 - len(invites)
	if remaining < 0 {
		remaining = 0
	}
	data["InviteRemaining"] = remaining

	for key, value := range extra {
		data[key] = value
	}
	return data
}

func writeJSON(w http.ResponseWriter, contentType string, data any) {
	w.Header().Set("Content-Type", contentType)
	_ = json.NewEncoder(w).Encode(data)
}

func apiPostViews(baseURL string, posts []store.Post) []apiPostView {
	views := make([]apiPostView, 0, len(posts))
	for _, post := range posts {
		var imageURL *string
		if post.ImageURL.Valid {
			value := post.ImageURL.String
			imageURL = &value
		}
		views = append(views, apiPostView{
			ID:        post.ID,
			Word:      post.Word,
			URL:       postURL(baseURL, post),
			ImageURL:  imageURL,
			CreatedAt: post.CreatedAt.UTC().Format(time.RFC3339),
		})
	}
	return views
}

func activitypubOutbox(baseURL string, posts []store.Post) map[string]any {
	items := make([]any, 0, len(posts))
	for _, post := range posts {
		items = append(items, activitypub.Note(baseURL, post))
	}
	return map[string]any{
		"@context":     "https://www.w3.org/ns/activitystreams",
		"type":         "OrderedCollection",
		"totalItems":   len(items),
		"orderedItems": items,
	}
}

func postURL(baseURL string, post store.Post) string {
	return strings.TrimRight(baseURL, "/") + "/@" + url.PathEscape(post.Username) + "/" + url.PathEscape(post.Word)
}

func (a *App) currentUser(r *http.Request) (store.User, bool) {
	cookie, err := r.Cookie(sessionCookie)
	if err != nil || cookie.Value == "" {
		return store.User{}, false
	}
	user, err := a.db.UserBySessionHash(hashToken(cookie.Value))
	return user, err == nil
}

func (a *App) requireLogin(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, "/login?next="+url.QueryEscape(r.URL.RequestURI()), http.StatusSeeOther)
}

func (a *App) startSession(w http.ResponseWriter, userID int64) error {
	token, tokenHash, err := newToken()
	if err != nil {
		return err
	}
	expires := time.Now().Add(30 * 24 * time.Hour)
	if err := a.db.CreateSession(tokenHash, userID, expires); err != nil {
		return err
	}
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    token,
		Path:     "/",
		Expires:  expires,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   a.secureCookies(),
	})
	return nil
}

func (a *App) secureCookies() bool {
	return strings.HasPrefix(a.cfg.BaseURL, "https://")
}

func newToken() (string, string, error) {
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", "", err
	}
	token := base64.RawURLEncoding.EncodeToString(raw[:])
	return token, hashToken(token), nil
}

func newInviteCode() (string, error) {
	token, _, err := newToken()
	if err != nil {
		return "", err
	}
	return strings.TrimRight(base64.RawURLEncoding.EncodeToString([]byte(token))[:22], "="), nil
}

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return fmt.Sprintf("%x", sum[:])
}

func safeNext(next string) string {
	if next == "" || !strings.HasPrefix(next, "/") || strings.HasPrefix(next, "//") {
		return "/write"
	}
	return next
}

func senderEmail(raw string) string {
	addr, err := mail.ParseAddress(raw)
	if err == nil {
		return strings.ToLower(strings.TrimSpace(addr.Address))
	}
	return strings.ToLower(strings.TrimSpace(raw))
}

func (a *App) inboundUser(from, to string) (store.User, error) {
	if user, err := a.db.UserByEmail(senderEmail(from)); err == nil {
		return user, nil
	}
	for _, recipient := range strings.Split(to, ",") {
		username := taggedDailyRecipient(recipient)
		if username == "" {
			continue
		}
		if user, err := a.db.UserByUsername(username); err == nil {
			return user, nil
		}
	}
	return store.User{}, errors.New("sender is not an igrec user")
}

func taggedDailyRecipient(raw string) string {
	addr, err := mail.ParseAddress(raw)
	value := strings.TrimSpace(raw)
	if err == nil {
		value = addr.Address
	}
	value = strings.ToLower(strings.TrimSpace(value))
	local, domain, ok := strings.Cut(value, "@")
	if !ok || domain != "igrec.net" || !strings.HasPrefix(local, "_+") {
		return ""
	}
	return strings.TrimPrefix(local, "_+")
}

func firstInboundWord(text string) (string, error) {
	for _, line := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		line = strings.TrimSpace(line)
		if line == "" ||
			strings.HasPrefix(line, ">") ||
			strings.HasPrefix(line, "--") ||
			strings.Contains(line, ":") ||
			strings.HasPrefix(strings.ToLower(line), "on ") {
			continue
		}
		for _, field := range strings.Fields(line) {
			if value, err := word.Normalize(field); err == nil {
				return value, nil
			}
		}
	}
	return "", errors.New("no word found")
}

func normalizeSignupUsername(raw string) (string, error) {
	value := strings.TrimSpace(raw)
	value = strings.TrimPrefix(value, "@")
	if strings.Contains(value, "@") {
		parts := strings.Split(value, "@")
		if len(parts) != 2 || !strings.EqualFold(parts[1], "igrec.net") {
			return "", errors.New("choose a handle at igrec.net")
		}
		value = parts[0]
	}
	if !usernamePattern.MatchString(value) {
		return "", errors.New("handle can use letters, numbers, and underscore")
	}
	return value, nil
}

func profilePostViews(posts []store.Post, preference string) []postView {
	counts := make(map[string]int)
	for _, post := range posts {
		counts[dayKey(post.CreatedAt)]++
	}

	views := make([]postView, 0, len(posts))
	for _, post := range posts {
		views = append(views, postView{
			Post:        post,
			DisplayTime: displayTime(post.CreatedAt, preference, counts[dayKey(post.CreatedAt)]),
			MachineTime: post.CreatedAt.Format(time.RFC3339),
		})
	}
	return views
}

func archiveMonths(username string, posts []store.Post) []archiveMonth {
	seen := make(map[string]int)
	order := make([]string, 0)
	for _, post := range posts {
		key := post.CreatedAt.Format("2006/01")
		if seen[key] == 0 {
			order = append(order, key)
		}
		seen[key]++
	}

	months := make([]archiveMonth, 0, len(order))
	for _, key := range order {
		parts := strings.Split(key, "/")
		months = append(months, archiveMonth{
			Year:  parts[0],
			Month: parts[1],
			Href:  "/@" + url.PathEscape(username) + "/" + parts[0] + "/" + parts[1],
			Label: parts[0] + "." + parts[1],
			Count: seen[key],
		})
	}
	return months
}

func filterArchivePosts(posts []store.Post, parts []string) ([]store.Post, string, bool) {
	if len(parts) > 4 {
		return nil, "", false
	}
	year, err := parseArchivePart(parts[1], 1, 9999)
	if err != nil || len(parts[1]) != 4 {
		return nil, "", false
	}
	month := 0
	day := 0
	if len(parts) > 2 {
		month, err = parseArchivePart(parts[2], 1, 12)
		if err != nil || len(parts[2]) != 2 {
			return nil, "", false
		}
	}
	if len(parts) > 3 {
		day, err = parseArchivePart(parts[3], 1, 31)
		if err != nil || len(parts[3]) != 2 {
			return nil, "", false
		}
	}

	var filtered []store.Post
	for _, post := range posts {
		if post.CreatedAt.Year() != year {
			continue
		}
		if month != 0 && int(post.CreatedAt.Month()) != month {
			continue
		}
		if day != 0 && post.CreatedAt.Day() != day {
			continue
		}
		filtered = append(filtered, post)
	}

	title := parts[1]
	if month != 0 {
		title += "." + parts[2]
	}
	if day != 0 {
		title += "." + parts[3]
	}
	return filtered, title, true
}

func parseArchivePart(value string, min, max int) (int, error) {
	var parsed int
	if _, err := fmt.Sscanf(value, "%d", &parsed); err != nil {
		return 0, err
	}
	if parsed < min || parsed > max {
		return 0, errors.New("archive date out of range")
	}
	return parsed, nil
}

func isArchiveYear(value string) bool {
	_, err := parseArchivePart(value, 1, 9999)
	return err == nil && len(value) == 4
}

func dayKey(t time.Time) string {
	return t.Format("2006-01-02")
}

func displayTime(t time.Time, preference string, postsOnDay int) string {
	switch preference {
	case "date":
		return t.Format("Jan 02")
	case "datetime":
		return t.Format("Jan 02 15:04")
	default:
		if postsOnDay > 1 {
			return t.Format("Jan 02 15:04")
		}
		return t.Format("Jan 02")
	}
}
