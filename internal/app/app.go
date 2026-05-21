package app

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"html/template"
	"net/http"
	"net/url"
	"strings"

	"igrec.net/igrec/internal/activitypub"
	"igrec.net/igrec/internal/store"
	"igrec.net/igrec/internal/word"
)

type Config struct {
	BaseURL      string
	Addr         string
	DatabaseURL  string
	AppSecret    string
	ResendAPIKey string
	VAPIDPublic  string
	VAPIDPrivate string
}

type App struct {
	cfg       Config
	db        *store.DB
	templates *template.Template
}

func New(cfg Config, db *store.DB) http.Handler {
	app := &App{
		cfg: cfg,
		db:  db,
		templates: template.Must(template.ParseGlob("web/templates/*.html")),
	}

	mux := http.NewServeMux()
	mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.Dir("web/static"))))
	mux.HandleFunc("/", app.route)
	mux.HandleFunc("/write", app.write)
	mux.HandleFunc("/settings", app.settings)
	mux.HandleFunc("/inbound/email", app.inboundEmail)
	mux.HandleFunc("/.well-known/webfinger", app.webfinger)
	mux.HandleFunc("/ap/users/", app.actor)
	mux.HandleFunc("/manifest.webmanifest", app.manifest)
	return mux
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
	a.render(w, "profile.html", map[string]any{"User": user, "Posts": posts})
}

func (a *App) write(w http.ResponseWriter, r *http.Request) {
	user, err := a.db.EnsureLocalUser("demo")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	switch r.Method {
	case http.MethodGet:
		a.render(w, "write.html", nil)
	case http.MethodPost:
		value, err := word.Normalize(r.FormValue("word"))
		if err != nil {
			a.render(w, "write.html", map[string]any{"Error": err.Error(), "Word": r.FormValue("word")})
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
	a.render(w, "settings.html", map[string]any{"VAPIDPublic": a.cfg.VAPIDPublic})
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
		Text string `json:"text"`
	}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	fields := strings.Fields(payload.Text)
	if len(fields) == 0 {
		http.Error(w, "no word found", http.StatusBadRequest)
		return
	}
	value, err := word.Normalize(fields[0])
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	user, err := a.db.EnsureLocalUser("email")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
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
		"icons":            []any{},
	})
}

func (a *App) render(w http.ResponseWriter, name string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := a.templates.ExecuteTemplate(w, name, data); err != nil && !errors.Is(err, http.ErrHandlerTimeout) {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func writeJSON(w http.ResponseWriter, contentType string, data any) {
	w.Header().Set("Content-Type", contentType)
	_ = json.NewEncoder(w).Encode(data)
}
