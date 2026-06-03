package app

import (
	"bytes"
	"context"
	"crypto"
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/x509"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"html/template"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"image/png"
	"io"
	"log"
	"math"
	"mime/multipart"
	"net"
	"net/http"
	"net/mail"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/golang/freetype"
	"github.com/golang/freetype/truetype"
	xfont "golang.org/x/image/font"
	"golang.org/x/image/font/gofont/gomono"

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
	OperatorEmails []string
	UploadDir      string
	ResendAPIKey   string
	LoginEmailFrom string
	DailyEmailFrom string
	VAPIDPublic    string
	VAPIDPrivate   string
}

type App struct {
	cfg            Config
	db             *store.DB
	templates      *template.Template
	operatorEmails map[string]struct{}
	limiter        *rateLimiter
}

type postView struct {
	store.Post
	DisplayTime string
	MachineTime string
	HasImage    bool
	CaptionCSS  template.CSS
	FocusCSS    template.CSS
}

type operatorPulse struct {
	UserCount             int
	PostCount             int
	PostsToday            int
	PendingDeliveries     int
	DueDeliveries         int
	DailyEmailSubscribers int
}

type inboundAttachment struct {
	Filename    string `json:"filename"`
	ContentType string `json:"content_type"`
	Data        string `json:"data"`
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

type uploadStorageStats struct {
	FileCount        int
	Bytes            int64
	FormattedBytes   string
	AverageBytes     int64
	FormattedAverage string
	WatchLevel       string
	WatchMessage     string
}

const sessionCookie = "igrec_session"
const csrfCookie = "igrec_csrf"
const csrfField = "csrf_token"
const maxUploadBytes = 8 << 20
const activityPubPublic = "https://www.w3.org/ns/activitystreams#Public"

var usernamePattern = regexp.MustCompile(`^[A-Za-z0-9_]{1,32}$`)

type activityPubActivity struct {
	Context any    `json:"@context,omitempty"`
	ID      string `json:"id,omitempty"`
	Type    string `json:"type"`
	Actor   string `json:"actor,omitempty"`
	Object  any    `json:"object,omitempty"`
	To      any    `json:"to,omitempty"`
	CC      any    `json:"cc,omitempty"`
}

type remoteActor struct {
	ID            string `json:"id"`
	Type          string `json:"type"`
	Inbox         string `json:"inbox"`
	PreferredName string `json:"preferredUsername"`
	Endpoints     struct {
		SharedInbox string `json:"sharedInbox"`
	} `json:"endpoints"`
}

type rateLimiter struct {
	mu      sync.Mutex
	buckets map[string]rateBucket
}

type rateBucket struct {
	ResetAt time.Time
	Count   int
}

func New(cfg Config, db *store.DB) http.Handler {
	app := NewAppForJobs(cfg, db)

	mux := http.NewServeMux()
	mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.Dir("web/static"))))
	mux.Handle("/uploads/", http.StripPrefix("/uploads/", http.FileServer(http.Dir(cfg.UploadDir))))
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
	mux.HandleFunc("/operator/invites", app.operatorInvites)
	mux.HandleFunc("/admin/invites", app.adminInvites)
	mux.HandleFunc("/inbound/email", app.inboundEmail)
	mux.HandleFunc("/og/", app.postPreviewCard)
	mux.HandleFunc("/.well-known/webfinger", app.webfinger)
	mux.HandleFunc("/ap/users/", app.actor)
	mux.HandleFunc("/manifest.webmanifest", app.manifest)
	return app.withRequestLogging(mux)
}

func NewAppForJobs(cfg Config, db *store.DB) *App {
	app := &App{
		cfg:            cfg,
		db:             db,
		templates:      template.Must(template.ParseGlob("web/templates/*.html")),
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

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

func (a *App) withRequestLogging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		defer func() {
			if panicValue := recover(); panicValue != nil {
				log.Printf("panic method=%s path=%s remote=%s err=%v", r.Method, r.URL.Path, r.RemoteAddr, panicValue)
				http.Error(rec, "internal server error", http.StatusInternalServerError)
			}
			log.Printf("request method=%s path=%s status=%d duration_ms=%d remote=%s", r.Method, r.URL.Path, rec.status, time.Since(started).Milliseconds(), r.RemoteAddr)
		}()
		next.ServeHTTP(rec, r)
	})
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
	if r.URL.Path == "/today" {
		a.today(w, r)
		return
	}
	if strings.HasPrefix(r.URL.Path, "/@") {
		a.profile(w, r)
		return
	}
	http.NotFound(w, r)
}

func (a *App) api(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost && r.URL.Path == "/api/words" {
		a.apiCreateWord(w, r)
		return
	}
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

func (a *App) apiCreateWord(w http.ResponseWriter, r *http.Request) {
	user, ok := a.apiUser(r)
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if !a.allowRate("api-write:user:"+strconv.FormatInt(user.ID, 10), 120, time.Hour) || !a.allowRate("api-write:ip:"+clientKey(r), 240, time.Hour) {
		http.Error(w, "rate limited", http.StatusTooManyRequests)
		return
	}
	var raw string
	if strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		var payload struct {
			Word string `json:"word"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, 64*1024)).Decode(&payload); err != nil {
			http.Error(w, "invalid json", http.StatusBadRequest)
			return
		}
		raw = payload.Word
	} else {
		if err := r.ParseForm(); err != nil {
			http.Error(w, "invalid form", http.StatusBadRequest)
			return
		}
		raw = r.FormValue("word")
	}
	value, err := word.Normalize(raw)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	post, err := a.db.CreatePost(user.ID, value, nil)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	go a.deliverPost(post)
	writeJSON(w, "application/json; charset=utf-8", map[string]any{
		"ok":   true,
		"word": apiPostViews(a.cfg.BaseURL, []store.Post{post})[0],
	})
}

func (a *App) apiUser(r *http.Request) (store.User, bool) {
	raw := strings.TrimSpace(r.Header.Get("Authorization"))
	if !strings.HasPrefix(strings.ToLower(raw), "bearer ") {
		return store.User{}, false
	}
	token := strings.TrimSpace(raw[len("Bearer "):])
	if token == "" {
		return store.User{}, false
	}
	user, err := a.db.UserByAPITokenHash(hashToken(token))
	return user, err == nil
}

func (a *App) postPreviewCard(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	username, value, ok := previewCardPathParts(r.URL.Path)
	if !ok {
		http.NotFound(w, r)
		return
	}
	post, err := a.db.PostByUserWord(username, value)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	img, err := renderPreviewCard(post)
	if err != nil {
		http.Error(w, "preview unavailable", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "public, max-age=14400")
	if r.Method == http.MethodHead {
		return
	}
	_ = png.Encode(w, img)
}

func previewCardPathParts(path string) (string, string, bool) {
	rest := strings.TrimPrefix(path, "/og/@")
	if rest == path || rest == "" {
		return "", "", false
	}
	usernamePart, wordPart, ok := strings.Cut(rest, "/")
	if !ok || usernamePart == "" || !strings.HasSuffix(wordPart, ".png") {
		return "", "", false
	}
	wordPart = strings.TrimSuffix(wordPart, ".png")
	username, err := url.PathUnescape(usernamePart)
	if err != nil {
		return "", "", false
	}
	value, err := url.PathUnescape(wordPart)
	if err != nil {
		return "", "", false
	}
	if username == "" || value == "" {
		return "", "", false
	}
	return username, value, true
}

func renderPreviewCard(post store.Post) (image.Image, error) {
	const width = 1200
	const height = 630
	colors := previewCardColors(post.Word)
	ttf, err := truetype.Parse(gomono.TTF)
	if err != nil {
		return nil, err
	}
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	fillRect(img, img.Bounds(), colors.paper)
	for y := 0; y < height; y += 8 {
		fillRect(img, image.Rect(0, y, width, y+1), colors.line)
	}
	fillRect(img, image.Rect(38, 38, width-38, height-38), colors.panel)
	fillRect(img, image.Rect(38, 38, width-38, 44), color.RGBA{5, 7, 12, 255})
	fillRect(img, image.Rect(38, height-44, width-38, height-38), color.RGBA{5, 7, 12, 255})
	fillRect(img, image.Rect(38, 38, 44, height-38), color.RGBA{5, 7, 12, 255})
	fillRect(img, image.Rect(width-44, 38, width-38, height-38), color.RGBA{5, 7, 12, 255})
	fillRect(img, image.Rect(58, 58, width-58, height-58), color.RGBA{255, 255, 255, 255})

	drawText(img, ttf, "Y", 34, 88, 102, color.RGBA{5, 7, 12, 255})
	fillRect(img, image.Rect(74, 66, 122, 114), color.RGBA{0, 35, 149, 255})
	fillRect(img, image.Rect(80, 72, 116, 108), color.RGBA{255, 255, 255, 255})
	drawText(img, ttf, "Y", 29, 84, 103, color.RGBA{5, 7, 12, 255})
	drawText(img, ttf, "IGREC", 46, 153, 108, color.RGBA{0, 35, 149, 255})
	drawText(img, ttf, "IGREC", 46, 161, 108, color.RGBA{237, 41, 57, 255})
	drawText(img, ttf, "IGREC", 46, 157, 108, color.RGBA{5, 7, 12, 255})

	wordSize := fittedFontSize(ttf, post.Word, 980, 156, 42)
	wordY := 355
	wordX := centeredTextX(ttf, post.Word, wordSize, width)
	drawText(img, ttf, post.Word, wordSize, wordX-11, wordY, colors.left)
	drawText(img, ttf, post.Word, wordSize, wordX+11, wordY, colors.right)
	drawText(img, ttf, post.Word, wordSize, wordX, wordY, color.RGBA{5, 7, 12, 255})

	byline := "@" + post.Username
	drawText(img, ttf, byline, 34, centeredTextX(ttf, byline, 34, width), 466, color.RGBA{5, 7, 12, 255})
	drawText(img, ttf, post.CreatedAt.Format("2006-01-02 15:04"), 24, 80, 548, color.RGBA{5, 7, 12, 255})
	drawText(img, ttf, "igrec.net", 24, width-220, 548, color.RGBA{5, 7, 12, 255})
	fillRect(img, image.Rect(80, 492, width-80, 497), color.RGBA{5, 7, 12, 255})
	return img, nil
}

type previewColors struct {
	paper color.RGBA
	panel color.RGBA
	line  color.RGBA
	left  color.RGBA
	right color.RGBA
}

func previewCardColors(value string) previewColors {
	palettes := []previewColors{
		{paper: color.RGBA{246, 238, 220, 255}, panel: color.RGBA{255, 252, 242, 255}, line: color.RGBA{230, 220, 190, 120}, left: color.RGBA{0, 35, 149, 255}, right: color.RGBA{237, 41, 57, 255}},
		{paper: color.RGBA{235, 241, 255, 255}, panel: color.RGBA{255, 255, 255, 255}, line: color.RGBA{198, 212, 245, 140}, left: color.RGBA{0, 35, 149, 255}, right: color.RGBA{237, 41, 57, 255}},
		{paper: color.RGBA{255, 236, 239, 255}, panel: color.RGBA{255, 253, 247, 255}, line: color.RGBA{238, 188, 194, 130}, left: color.RGBA{0, 35, 149, 255}, right: color.RGBA{190, 25, 38, 255}},
		{paper: color.RGBA{242, 244, 236, 255}, panel: color.RGBA{255, 255, 250, 255}, line: color.RGBA{207, 215, 196, 140}, left: color.RGBA{0, 35, 149, 255}, right: color.RGBA{237, 41, 57, 255}},
	}
	sum := sha256.Sum256([]byte(strings.ToLower(value)))
	return palettes[int(sum[0])%len(palettes)]
}

func fillRect(img draw.Image, r image.Rectangle, c color.Color) {
	draw.Draw(img, r, &image.Uniform{C: c}, image.Point{}, draw.Src)
}

func drawText(img draw.Image, ttf *truetype.Font, text string, size float64, x, y int, c color.Color) {
	ctx := freetype.NewContext()
	ctx.SetDPI(72)
	ctx.SetFont(ttf)
	ctx.SetFontSize(size)
	ctx.SetClip(img.Bounds())
	ctx.SetDst(img)
	ctx.SetSrc(image.NewUniform(c))
	_, _ = ctx.DrawString(text, freetype.Pt(x, y))
}

func fittedFontSize(ttf *truetype.Font, text string, maxWidth int, start, min float64) float64 {
	for size := start; size >= min; size -= 2 {
		if textWidth(ttf, text, size) <= maxWidth {
			return size
		}
	}
	return min
}

func centeredTextX(ttf *truetype.Font, text string, size float64, canvasWidth int) int {
	return (canvasWidth - textWidth(ttf, text, size)) / 2
}

func textWidth(ttf *truetype.Font, text string, size float64) int {
	face := truetype.NewFace(ttf, &truetype.Options{Size: size, DPI: 72})
	defer face.Close()
	return xfont.MeasureString(face, text).Ceil()
}

func (a *App) firehose(w http.ResponseWriter, r *http.Request) {
	before := parseIntQuery(r, "before")
	const pageSize = 30
	posts, err := a.db.FirehoseBefore(before, pageSize+1)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	a.render(w, r, "index.html", feedData(a.styledPostViews(posts, "datetime"), pageSize, "public", "/"))
}

func (a *App) today(w http.ResponseWriter, r *http.Request) {
	before := parseIntQuery(r, "before")
	const pageSize = 30
	posts, err := a.db.PostsSinceBefore(startOfToday(), before, pageSize+1)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	data := feedData(a.styledPostViews(posts, "datetime"), pageSize, "today", "/today")
	data["PageTitle"] = "today"
	a.render(w, r, "index.html", data)
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
	if len(parts) > 1 && parts[1] == "badge.svg" {
		a.badge(w, r, user)
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
	} else if len(parts) > 1 && parts[1] != "" {
		value, err := url.PathUnescape(parts[1])
		if err != nil {
			http.NotFound(w, r)
			return
		}
		post, err := a.db.PostByUserWord(user.Username, value)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		data := map[string]any{"Post": a.styledPostViews([]store.Post{post}, user.TimestampPreference)[0], "User": user}
		if echoes, err := a.db.PostsByWord(post.Word, post.ID, 6); err == nil && len(echoes) > 0 {
			data["Echoes"] = a.styledPostViews(echoes, "datetime")
		}
		data["PageTitle"] = post.Word + " by @" + post.Username
		data["OGURL"] = postURL(a.cfg.BaseURL, post)
		data["OGDescription"] = "@" + post.Username + " said: " + post.Word
		if post.ImageURL.Valid {
			data["OGImage"] = absoluteURL(a.cfg.BaseURL, post.ImageURL.String)
			data["OGImageType"] = "image/jpeg"
			if width, height, ok := a.imageDimensions(post.ImageURL.String); ok {
				data["OGImageWidth"] = width
				data["OGImageHeight"] = height
			}
		} else {
			data["OGImage"] = previewCardURL(a.cfg.BaseURL, post)
			data["OGImageType"] = "image/png"
			data["OGImageWidth"] = 1200
			data["OGImageHeight"] = 630
		}
		a.render(w, r, "post.html", data)
		return
	}
	data := map[string]any{"User": user, "Posts": a.styledPostViews(posts, user.TimestampPreference), "Months": months, "Title": title, "BadgeURL": badgeURL(a.cfg.BaseURL, user)}
	if inviter, err := a.db.InviterByUserID(user.ID); err == nil {
		data["Inviter"] = inviter
	}
	if viewer, ok := a.currentUser(r); ok && viewer.ID != user.ID {
		follows, err := a.db.UserFollows(viewer.ID, user.ID)
		if err == nil {
			data["CanFriend"] = true
			data["IsFriend"] = follows
		}
	}
	a.render(w, r, "profile.html", a.withCSRF(w, r, data))
}

func (a *App) badge(w http.ResponseWriter, r *http.Request, user store.User) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	post, err := a.db.LatestPostByUser(user.Username)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	svg := renderBadgeSVG(user, post)
	w.Header().Set("Content-Type", "image/svg+xml; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=300")
	if r.Method == http.MethodHead {
		return
	}
	_, _ = w.Write([]byte(svg))
}

func (a *App) write(w http.ResponseWriter, r *http.Request) {
	user, ok := a.currentUser(r)
	if !ok {
		a.requireLogin(w, r)
		return
	}

	switch r.Method {
	case http.MethodGet:
		a.render(w, r, "write.html", a.withCSRF(w, r, a.writeData(user, nil)))
	case http.MethodPost:
		if !a.validCSRF(r) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		if !a.allowRate("write:user:"+strconv.FormatInt(user.ID, 10), 60, time.Hour) || !a.allowRate("write:ip:"+clientKey(r), 120, time.Hour) {
			http.Error(w, "rate limited", http.StatusTooManyRequests)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, maxUploadBytes+2<<20)
		value, err := word.Normalize(r.FormValue("word"))
		if err != nil {
			a.render(w, r, "write.html", a.withCSRF(w, r, a.writeData(user, map[string]any{"Error": err.Error(), "Word": r.FormValue("word")})))
			return
		}
		focusX := parseUnitFloat(r.FormValue("focus_x"), 0.5)
		focusY := parseUnitFloat(r.FormValue("focus_y"), 0.5)
		var imageURL *string
		imageFile, imageHeader, err := r.FormFile("image_file")
		if err == nil {
			defer imageFile.Close()
			uploaded, uploadErr := a.saveUploadedImage(imageFile, imageHeader)
			if uploadErr != nil {
				a.render(w, r, "write.html", a.withCSRF(w, r, a.writeData(user, map[string]any{
					"Error": uploadErr.Error(),
					"Word":  r.FormValue("word"),
				})))
				return
			}
			imageURL = &uploaded
		} else if !errors.Is(err, http.ErrMissingFile) {
			a.render(w, r, "write.html", a.withCSRF(w, r, a.writeData(user, map[string]any{
				"Error": "image upload failed",
				"Word":  r.FormValue("word"),
			})))
			return
		}
		post, err := a.db.CreatePostWithFocus(user.ID, value, imageURL, focusX, focusY)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		go a.deliverPost(post)
		postPath := "/@" + url.PathEscape(post.Username) + "/" + url.PathEscape(post.Word)
		data := map[string]any{
			"PageTitle":      post.Word,
			"Post":           a.styledPostViews([]store.Post{post}, user.TimestampPreference)[0],
			"PostURL":        postPath,
			"RefreshURL":     postPath,
			"RefreshSeconds": 6,
		}
		a.render(w, r, "posted.html", a.withCSRF(w, r, data))
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (a *App) writeData(user store.User, extra map[string]any) map[string]any {
	data := map[string]any{}
	if posts, err := a.db.PostsByUser(user.Username, 10); err == nil {
		today := dayKey(time.Now())
		for _, post := range posts {
			if dayKey(post.CreatedAt) == today {
				data["TodayPost"] = a.styledPostViews([]store.Post{post}, user.TimestampPreference)[0]
				break
			}
		}
	}
	for key, value := range extra {
		data[key] = value
	}
	return data
}

func parseUnitFloat(raw string, fallback float64) float64 {
	value, err := strconv.ParseFloat(strings.TrimSpace(raw), 64)
	if err != nil || math.IsNaN(value) || math.IsInf(value, 0) {
		return fallback
	}
	if value < 0 {
		return 0
	}
	if value > 1 {
		return 1
	}
	return value
}

func (a *App) saveUploadedImage(file io.Reader, header *multipart.FileHeader) (string, error) {
	if header.Size > maxUploadBytes {
		return "", errors.New("image must be 8MB or smaller")
	}
	raw, err := io.ReadAll(io.LimitReader(file, maxUploadBytes+1))
	if err != nil {
		return "", errors.New("could not read image")
	}
	if int64(len(raw)) > maxUploadBytes {
		return "", errors.New("image must be 8MB or smaller")
	}
	return a.storeImageBytes(raw)
}

func (a *App) storeImageBytes(raw []byte) (string, error) {
	contentType := http.DetectContentType(raw)
	if contentType != "image/jpeg" && contentType != "image/png" {
		return "", errors.New("only JPEG and PNG are supported")
	}
	img, _, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		return "", errors.New("invalid image file")
	}
	if contentType == "image/jpeg" {
		img = applyJPEGOrientation(img, raw)
	}
	bounds := img.Bounds()
	if bounds.Dx() < 24 || bounds.Dy() < 24 {
		return "", errors.New("image is too small")
	}
	img = downscaleWithin(img, 1440)
	token, _, err := newToken()
	if err != nil {
		return "", errors.New("could not store image")
	}
	if err := os.MkdirAll(a.cfg.UploadDir, 0o755); err != nil {
		return "", errors.New("could not store image")
	}
	filename := token[:20] + ".jpg"
	target := filepath.Join(a.cfg.UploadDir, filename)
	out, err := os.Create(target)
	if err != nil {
		return "", errors.New("could not store image")
	}
	defer out.Close()
	if err := jpeg.Encode(out, img, &jpeg.Options{Quality: 86}); err != nil {
		return "", errors.New("could not store image")
	}
	url := "/uploads/" + filename
	return url, nil
}

func cropSquareAroundFocus(img image.Image, focusX, focusY float64) image.Image {
	b := img.Bounds()
	w := b.Dx()
	h := b.Dy()
	size := w
	if h < size {
		size = h
	}
	cx := int(focusX * float64(w))
	cy := int(focusY * float64(h))
	left := cx - size/2
	top := cy - size/2
	if left < 0 {
		left = 0
	}
	if top < 0 {
		top = 0
	}
	if left+size > w {
		left = w - size
	}
	if top+size > h {
		top = h - size
	}
	rect := image.Rect(b.Min.X+left, b.Min.Y+top, b.Min.X+left+size, b.Min.Y+top+size)
	dst := image.NewRGBA(image.Rect(0, 0, size, size))
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			dst.Set(x, y, img.At(rect.Min.X+x, rect.Min.Y+y))
		}
	}
	return dst
}

func downscaleWithin(img image.Image, maxEdge int) image.Image {
	b := img.Bounds()
	w := b.Dx()
	h := b.Dy()
	if w <= maxEdge && h <= maxEdge {
		return img
	}
	scale := float64(maxEdge) / float64(w)
	if h > w {
		scale = float64(maxEdge) / float64(h)
	}
	nw := int(float64(w) * scale)
	nh := int(float64(h) * scale)
	if nw < 1 {
		nw = 1
	}
	if nh < 1 {
		nh = 1
	}
	dst := image.NewRGBA(image.Rect(0, 0, nw, nh))
	for y := 0; y < nh; y++ {
		sy := b.Min.Y + int(float64(y)/scale)
		if sy >= b.Max.Y {
			sy = b.Max.Y - 1
		}
		for x := 0; x < nw; x++ {
			sx := b.Min.X + int(float64(x)/scale)
			if sx >= b.Max.X {
				sx = b.Max.X - 1
			}
			dst.Set(x, y, img.At(sx, sy))
		}
	}
	return dst
}

func applyJPEGOrientation(img image.Image, raw []byte) image.Image {
	orientation := jpegOrientation(raw)
	if orientation <= 1 || orientation > 8 {
		return img
	}
	b := img.Bounds()
	w := b.Dx()
	h := b.Dy()
	dw := w
	dh := h
	if orientation >= 5 {
		dw = h
		dh = w
	}
	dst := image.NewRGBA(image.Rect(0, 0, dw, dh))
	for y := 0; y < dh; y++ {
		for x := 0; x < dw; x++ {
			var sx, sy int
			switch orientation {
			case 2:
				sx, sy = w-1-x, y
			case 3:
				sx, sy = w-1-x, h-1-y
			case 4:
				sx, sy = x, h-1-y
			case 5:
				sx, sy = y, x
			case 6:
				sx, sy = y, h-1-x
			case 7:
				sx, sy = w-1-y, h-1-x
			case 8:
				sx, sy = w-1-y, x
			default:
				sx, sy = x, y
			}
			dst.Set(x, y, img.At(b.Min.X+sx, b.Min.Y+sy))
		}
	}
	return dst
}

func jpegOrientation(raw []byte) int {
	if len(raw) < 4 || raw[0] != 0xff || raw[1] != 0xd8 {
		return 1
	}
	for i := 2; i+4 <= len(raw); {
		if raw[i] != 0xff {
			return 1
		}
		for i < len(raw) && raw[i] == 0xff {
			i++
		}
		if i >= len(raw) {
			return 1
		}
		marker := raw[i]
		i++
		if marker == 0xd9 || marker == 0xda {
			return 1
		}
		if i+2 > len(raw) {
			return 1
		}
		size := int(binary.BigEndian.Uint16(raw[i : i+2]))
		i += 2
		if size < 2 || i+size-2 > len(raw) {
			return 1
		}
		segment := raw[i : i+size-2]
		i += size - 2
		if marker != 0xe1 || len(segment) < 14 || string(segment[:6]) != "Exif\x00\x00" {
			continue
		}
		if value := exifOrientation(segment[6:]); value >= 1 && value <= 8 {
			return value
		}
	}
	return 1
}

func exifOrientation(tiff []byte) int {
	if len(tiff) < 8 {
		return 1
	}
	var order binary.ByteOrder
	switch string(tiff[:2]) {
	case "II":
		order = binary.LittleEndian
	case "MM":
		order = binary.BigEndian
	default:
		return 1
	}
	if order.Uint16(tiff[2:4]) != 42 {
		return 1
	}
	offset := int(order.Uint32(tiff[4:8]))
	if offset < 8 || offset+2 > len(tiff) {
		return 1
	}
	count := int(order.Uint16(tiff[offset : offset+2]))
	entries := offset + 2
	for n := 0; n < count; n++ {
		entry := entries + n*12
		if entry+12 > len(tiff) {
			return 1
		}
		tag := order.Uint16(tiff[entry : entry+2])
		if tag != 0x0112 {
			continue
		}
		typ := order.Uint16(tiff[entry+2 : entry+4])
		if typ == 3 {
			return int(order.Uint16(tiff[entry+8 : entry+10]))
		}
		if typ == 4 {
			return int(order.Uint32(tiff[entry+8 : entry+12]))
		}
		return 1
	}
	return 1
}

func (a *App) settings(w http.ResponseWriter, r *http.Request) {
	user, ok := a.currentUser(r)
	if !ok {
		a.requireLogin(w, r)
		return
	}

	switch r.Method {
	case http.MethodGet:
		a.render(w, r, "settings.html", a.withCSRF(w, r, a.settingsData(user, nil)))
	case http.MethodPost:
		if !a.validCSRF(r) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		if r.FormValue("action") == "delete-account" {
			confirmation := strings.TrimSpace(r.FormValue("confirm_username"))
			if confirmation != user.Username {
				a.render(w, r, "settings.html", a.withCSRF(w, r, a.settingsData(user, map[string]any{"Error": "type your username to delete this account"})))
				return
			}
			if err := a.db.DeleteUser(user.ID); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: a.secureCookies()})
			http.Redirect(w, r, "/", http.StatusSeeOther)
			return
		}
		if r.FormValue("action") == "invite" {
			created, err := a.db.InviteCountByInviter(user.ID)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			limit, err := a.db.InviteLimitByInviter(user.ID)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			if created >= limit {
				a.render(w, r, "settings.html", a.withCSRF(w, r, a.settingsData(user, map[string]any{"Error": "all invites are already made"})))
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
			a.render(w, r, "settings.html", a.withCSRF(w, r, a.settingsData(user, map[string]any{"Notice": "invite made"})))
			return
		}
		if r.FormValue("action") == "api-token" {
			token, tokenHash, err := newToken()
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			name := strings.TrimSpace(r.FormValue("api_token_name"))
			prefix := token
			if len(prefix) > 10 {
				prefix = prefix[:10]
			}
			if err := a.db.CreateAPIToken(user.ID, tokenHash, prefix, name); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			a.render(w, r, "settings.html", a.withCSRF(w, r, a.settingsData(user, map[string]any{"Notice": "api token created", "NewAPIToken": token})))
			return
		}
		if r.FormValue("delete_api_token_id") != "" {
			tokenID, _ := strconv.ParseInt(r.FormValue("delete_api_token_id"), 10, 64)
			if tokenID > 0 {
				_ = a.db.DeleteAPIToken(user.ID, tokenID)
			}
			http.Redirect(w, r, "/settings", http.StatusSeeOther)
			return
		}
		emailNotice := ""
		email := strings.ToLower(strings.TrimSpace(r.FormValue("email")))
		if email != "" && !strings.EqualFold(email, user.Email) {
			if _, err := mail.ParseAddress(email); err != nil {
				a.render(w, r, "settings.html", a.withCSRF(w, r, a.settingsData(user, map[string]any{"Error": "email is not valid"})))
				return
			}
			token, tokenHash, err := newToken()
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			if err := a.db.CreateEmailChangeToken(tokenHash, user.ID, email, time.Now().Add(30*time.Minute)); err != nil {
				a.render(w, r, "settings.html", a.withCSRF(w, r, a.settingsData(user, map[string]any{"Error": "email is already used"})))
				return
			}
			link := strings.TrimRight(a.cfg.BaseURL, "/") + "/auth/email?token=" + url.QueryEscape(token)
			body := "confirm this email for igrec:\n\n" + link + "\n\nthis link expires in 30 minutes.\n"
			err = (emailpkg.Resend{APIKey: a.cfg.ResendAPIKey, From: a.cfg.LoginEmailFrom}).SendPlain(email, "confirm igrec email", body)
			if err != nil {
				a.render(w, r, "settings.html", a.withCSRF(w, r, a.settingsData(user, map[string]any{"Error": err.Error()})))
				return
			}
			emailNotice = "check email to confirm"
		}
		fediverseAcct, err := normalizeFediverseAcct(r.FormValue("fediverse"))
		if err != nil {
			a.render(w, r, "settings.html", a.withCSRF(w, r, a.settingsData(user, map[string]any{"Error": err.Error()})))
			return
		}
		if err := a.db.UpdateSettings(user.ID, r.FormValue("timestamp_preference"), r.FormValue("daily") == "on", fediverseAcct); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if emailNotice != "" {
			user, _ = a.db.UserByUsername(user.Username)
			a.render(w, r, "settings.html", a.withCSRF(w, r, a.settingsData(user, map[string]any{"Notice": emailNotice})))
			return
		}
		http.Redirect(w, r, "/settings", http.StatusSeeOther)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (a *App) friends(w http.ResponseWriter, r *http.Request) {
	user, ok := a.currentUser(r)
	if !ok {
		a.requireLogin(w, r)
		return
	}
	if r.Method == http.MethodPost {
		if !a.validCSRF(r) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		target, err := a.db.UserByUsername(strings.TrimPrefix(strings.TrimSpace(r.FormValue("username")), "@"))
		if err != nil || target.ID == user.ID {
			http.Redirect(w, r, safeNext(r.FormValue("next")), http.StatusSeeOther)
			return
		}
		if r.FormValue("action") == "unfriend" {
			_ = a.db.DeleteUserFollow(user.ID, target.ID)
		} else {
			_ = a.db.CreateUserFollow(user.ID, target.ID)
		}
		http.Redirect(w, r, safeNext(r.FormValue("next")), http.StatusSeeOther)
		return
	}
	before := parseIntQuery(r, "before")
	const pageSize = 30
	posts, err := a.db.FriendPostsBefore(user.ID, before, pageSize+1)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	a.render(w, r, "index.html", feedData(a.styledPostViews(posts, "datetime"), pageSize, "friends", "/friends"))
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
			"actor":  activitypub.Actor(a.cfg.BaseURL, user, ""),
			"outbox": activitypubOutbox(a.cfg.BaseURL, posts),
		},
		"words": apiPostViews(a.cfg.BaseURL, posts),
	})
}

func (a *App) join(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		a.render(w, r, "join.html", a.withCSRF(w, r, map[string]any{"Invite": r.URL.Query().Get("invite")}))
	case http.MethodPost:
		if !a.validCSRF(r) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		inviteCode := strings.TrimSpace(r.FormValue("invite"))
		username, usernameErr := normalizeSignupUsername(r.FormValue("username"))
		email := strings.ToLower(strings.TrimSpace(r.FormValue("email")))
		if usernameErr != nil {
			a.render(w, r, "join.html", a.withCSRF(w, r, map[string]any{"Error": usernameErr.Error(), "Invite": inviteCode, "Username": r.FormValue("username"), "Email": email}))
			return
		}
		if _, err := mail.ParseAddress(email); err != nil {
			a.render(w, r, "join.html", a.withCSRF(w, r, map[string]any{"Error": "email is not valid", "Invite": inviteCode, "Username": username, "Email": email}))
			return
		}
		invite, err := a.db.InviteByCode(inviteCode)
		if err != nil || invite.UsedAt.Valid {
			a.render(w, r, "join.html", a.withCSRF(w, r, map[string]any{"Error": "invite is not valid", "Invite": inviteCode, "Username": username, "Email": email}))
			return
		}
		user, err := a.db.CreateUser(username, email)
		if err != nil {
			a.render(w, r, "join.html", a.withCSRF(w, r, map[string]any{"Error": "username or email is already taken", "Invite": inviteCode, "Username": username, "Email": email}))
			return
		}
		if err := a.db.UseInvite(invite.Code, user.ID); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if invite.InviterID.Valid {
			_ = a.db.CreateUserFollow(user.ID, invite.InviterID.Int64)
		}
		if cc, err := a.db.UserByUsername("cc00ffee"); err == nil {
			_ = a.db.CreateUserFollow(user.ID, cc.ID)
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
		if _, ok := a.currentUser(r); ok {
			http.Redirect(w, r, safeNext(r.URL.Query().Get("next")), http.StatusSeeOther)
			return
		}
		a.render(w, r, "login.html", a.withCSRF(w, r, map[string]any{"Next": r.URL.Query().Get("next")}))
	case http.MethodPost:
		if !a.validCSRF(r) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		email := strings.ToLower(strings.TrimSpace(r.FormValue("email")))
		if !a.allowRate("login:"+clientKey(r)+":"+email, 5, 10*time.Minute) {
			http.Error(w, "too many login emails", http.StatusTooManyRequests)
			return
		}
		next := safeNext(r.FormValue("next"))
		user, err := a.db.UserByEmail(email)
		if err != nil {
			a.render(w, r, "login.html", a.withCSRF(w, r, map[string]any{"Error": "no account uses that email yet", "Email": email, "Next": next}))
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
			a.render(w, r, "login.html", a.withCSRF(w, r, map[string]any{"Error": err.Error(), "Email": email, "Next": next}))
			return
		}
		a.render(w, r, "login_sent.html", map[string]any{"Email": email})
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
		a.render(w, r, "login.html", map[string]any{"Error": "login link is invalid or expired"})
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
		a.render(w, r, "settings.html", map[string]any{"User": user, "Error": "email link is invalid or expired"})
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

func (a *App) unsubscribeEmail(w http.ResponseWriter, r *http.Request) {
	user, err := a.userFromEmailToken(r.URL.Query().Get("token"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if err := a.db.SetEmailOptIn(user.ID, false); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	a.render(w, r, "login_sent.html", map[string]any{"Notice": "daily email is off"})
}

func (a *App) shortUnsubscribeEmail(w http.ResponseWriter, r *http.Request) {
	token := strings.TrimPrefix(r.URL.Path, "/u/")
	if token == "" || strings.Contains(token, "/") {
		http.NotFound(w, r)
		return
	}
	user, err := a.db.UserByUnsubscribeToken(token)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if err := a.db.SetEmailOptIn(user.ID, false); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	a.render(w, r, "login_sent.html", map[string]any{"Notice": "daily email is off"})
}

func (a *App) adminInvites(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, "/operator/invites", http.StatusSeeOther)
}

func (a *App) operatorInvites(w http.ResponseWriter, r *http.Request) {
	user, ok := a.currentUser(r)
	if !ok {
		a.requireLogin(w, r)
		return
	}
	if _, ok := a.operatorEmails[strings.ToLower(user.Email)]; !ok {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	render := func(extra map[string]any) {
		invites, err := a.db.RecentInvites(25)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		views := make([]inviteView, 0, len(invites))
		for _, invite := range invites {
			views = append(views, inviteView{
				Code: invite.Code,
				Link: strings.TrimRight(a.cfg.BaseURL, "/") + "/join?invite=" + url.QueryEscape(invite.Code),
				Used: invite.UsedAt.Valid,
			})
		}
		data := map[string]any{"Invites": views}
		for key, value := range extra {
			data[key] = value
		}
		a.render(w, r, "operator_invites.html", a.withCSRF(w, r, data))
	}

	if r.Method != http.MethodPost {
		render(nil)
		return
	}
	if !a.validCSRF(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
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
	render(map[string]any{"InviteLink": link, "Notice": "invite made"})
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
	if !a.allowRate("inbound:"+clientKey(r), 60, time.Minute) {
		http.Error(w, "rate limited", http.StatusTooManyRequests)
		return
	}
	var payload struct {
		From        string              `json:"from"`
		To          string              `json:"to"`
		Text        string              `json:"text"`
		Attachments []inboundAttachment `json:"attachments"`
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
	var imageURL *string
	for _, attachment := range payload.Attachments {
		raw, err := base64.StdEncoding.DecodeString(attachment.Data)
		if err != nil {
			continue
		}
		if contentType := http.DetectContentType(raw); contentType != "image/jpeg" && contentType != "image/png" {
			continue
		}
		uploaded, err := a.storeImageBytes(raw)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		imageURL = &uploaded
		break
	}
	post, err := a.db.CreatePost(user.ID, value, imageURL)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	go a.deliverPost(post)
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
	if len(parts) > 1 && parts[1] == "inbox" {
		a.activityPubInbox(w, r, user)
		return
	}
	if len(parts) > 1 && parts[1] == "followers" {
		followers, err := a.db.ActivityPubFollowers(user.ID)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, "application/activity+json; charset=utf-8", map[string]any{
			"@context":   "https://www.w3.org/ns/activitystreams",
			"id":         activitypubActorID(a.cfg.BaseURL, user.Username) + "/followers",
			"type":       "OrderedCollection",
			"totalItems": len(followers),
		})
		return
	}
	if len(parts) > 1 && parts[1] == "following" {
		writeJSON(w, "application/activity+json; charset=utf-8", map[string]any{
			"@context":   "https://www.w3.org/ns/activitystreams",
			"id":         activitypubActorID(a.cfg.BaseURL, user.Username) + "/following",
			"type":       "OrderedCollection",
			"totalItems": 0,
		})
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
	publicKey, err := a.activityPubPublicKey(user)
	if err != nil {
		http.Error(w, "activitypub key unavailable", http.StatusInternalServerError)
		return
	}
	writeJSON(w, "application/activity+json; charset=utf-8", activitypub.Actor(a.cfg.BaseURL, user, publicKey))
}

func (a *App) activityPubInbox(w http.ResponseWriter, r *http.Request, user store.User) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !a.allowRate("ap-inbox:"+user.Username+":"+clientKey(r), 120, time.Minute) {
		http.Error(w, "rate limited", http.StatusTooManyRequests)
		return
	}
	var activity activityPubActivity
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&activity); err != nil {
		http.Error(w, "invalid activity", http.StatusBadRequest)
		return
	}
	switch activity.Type {
	case "Follow":
		a.acceptActivityPubFollow(w, r, user, activity)
	case "Undo":
		a.undoActivityPubFollow(w, user, activity)
	default:
		w.WriteHeader(http.StatusAccepted)
	}
}

func (a *App) acceptActivityPubFollow(w http.ResponseWriter, r *http.Request, user store.User, activity activityPubActivity) {
	if activity.Actor == "" || !sameActivityPubObject(activity.Object, activitypubActorID(a.cfg.BaseURL, user.Username)) {
		http.Error(w, "unsupported follow", http.StatusBadRequest)
		return
	}
	remote, err := fetchRemoteActor(r.Context(), activity.Actor)
	if err != nil {
		http.Error(w, "remote actor unavailable", http.StatusBadGateway)
		return
	}
	inbox := remote.Endpoints.SharedInbox
	if inbox == "" {
		inbox = remote.Inbox
	}
	if remote.ID != "" && remote.ID != activity.Actor {
		http.Error(w, "actor mismatch", http.StatusBadRequest)
		return
	}
	if inbox == "" {
		http.Error(w, "remote inbox unavailable", http.StatusBadRequest)
		return
	}
	if err := a.db.UpsertActivityPubFollower(user.ID, activity.Actor, inbox); err != nil {
		http.Error(w, "follow failed", http.StatusInternalServerError)
		return
	}
	accept := activityPubActivity{
		Context: "https://www.w3.org/ns/activitystreams",
		ID:      activitypubActorID(a.cfg.BaseURL, user.Username) + "/accept/" + randomID(),
		Type:    "Accept",
		Actor:   activitypubActorID(a.cfg.BaseURL, user.Username),
		Object:  activity,
		To:      []string{activity.Actor},
	}
	if err := a.deliverActivity(user, inbox, accept); err != nil {
		log.Printf("activitypub accept delivery failed user=%s inbox=%s err=%v", user.Username, inbox, err)
		a.enqueueActivityPubDelivery(user, inbox, accept, err)
	}
	go a.deliverRecentPostsToInbox(user, inbox, 5)
	w.WriteHeader(http.StatusAccepted)
}

func (a *App) undoActivityPubFollow(w http.ResponseWriter, user store.User, activity activityPubActivity) {
	actor := activity.Actor
	if nested, ok := activity.Object.(map[string]any); ok {
		if nestedType, _ := nested["type"].(string); nestedType == "Follow" {
			if nestedActor, _ := nested["actor"].(string); nestedActor != "" {
				actor = nestedActor
			}
		}
	}
	if actor != "" {
		_ = a.db.DeleteActivityPubFollower(user.ID, actor)
	}
	w.WriteHeader(http.StatusAccepted)
}

func (a *App) deliverPost(post store.Post) {
	followers, err := a.db.ActivityPubFollowers(post.UserID)
	if err != nil {
		log.Printf("activitypub followers failed post=%d err=%v", post.ID, err)
		return
	}
	if len(followers) == 0 {
		return
	}
	user := store.User{ID: post.UserID, Username: post.Username}
	create := activitypub.Create(a.cfg.BaseURL, post)
	for _, follower := range followers {
		if err := a.deliverActivity(user, follower.Inbox, create); err != nil {
			log.Printf("activitypub create delivery failed post=%d actor=%s inbox=%s err=%v", post.ID, follower.Actor, follower.Inbox, err)
			a.enqueueActivityPubDelivery(user, follower.Inbox, create, err)
		}
	}
}

func (a *App) deliverRecentPostsToInbox(user store.User, inbox string, limit int) {
	if limit <= 0 {
		return
	}
	posts, err := a.db.PostsByUser(user.Username, limit)
	if err != nil {
		log.Printf("activitypub backfill posts failed user=%s err=%v", user.Username, err)
		return
	}
	// Deliver oldest first so a new follower sees the arrival packet in natural order.
	for i := len(posts) - 1; i >= 0; i-- {
		post := posts[i]
		create := activitypub.Create(a.cfg.BaseURL, post)
		if err := a.deliverActivity(user, inbox, create); err != nil {
			log.Printf("activitypub backfill delivery failed user=%s post=%d inbox=%s err=%v", user.Username, post.ID, inbox, err)
			a.enqueueActivityPubDelivery(user, inbox, create, err)
		}
	}
}

func (a *App) enqueueActivityPubDelivery(user store.User, inbox string, activity any, cause error) {
	raw, err := json.Marshal(activity)
	if err != nil {
		log.Printf("activitypub queue marshal failed user=%s inbox=%s err=%v", user.Username, inbox, err)
		return
	}
	message := ""
	if cause != nil {
		message = cause.Error()
	}
	if err := a.db.EnqueueActivityPubDelivery(user.ID, inbox, raw, time.Now().Add(10*time.Minute), message); err != nil {
		log.Printf("activitypub queue insert failed user=%s inbox=%s err=%v", user.Username, inbox, err)
	}
}

func (a *App) RetryActivityPubDeliveries(limit int) (int, int, error) {
	deliveries, err := a.db.DueActivityPubDeliveries(time.Now(), limit)
	if err != nil {
		return 0, 0, err
	}
	delivered := 0
	failed := 0
	for _, delivery := range deliveries {
		user, err := a.db.UserByID(delivery.UserID)
		if err != nil {
			failed++
			_ = a.db.MarkActivityPubDeliveryFailed(delivery.ID, delivery.Attempts+1, nextActivityPubRetry(delivery.Attempts+1), err.Error())
			continue
		}
		if err := a.deliverActivityBytes(user, delivery.Inbox, delivery.Activity); err != nil {
			failed++
			_ = a.db.MarkActivityPubDeliveryFailed(delivery.ID, delivery.Attempts+1, nextActivityPubRetry(delivery.Attempts+1), err.Error())
			continue
		}
		if err := a.db.MarkActivityPubDeliveryDelivered(delivery.ID); err != nil {
			return delivered, failed, err
		}
		delivered++
	}
	if _, err := a.db.PruneDeliveredActivityPubDeliveries(time.Now().Add(-7 * 24 * time.Hour)); err != nil {
		return delivered, failed, err
	}
	return delivered, failed, nil
}

func nextActivityPubRetry(attempts int) time.Time {
	delays := []time.Duration{
		10 * time.Minute,
		30 * time.Minute,
		2 * time.Hour,
		8 * time.Hour,
		24 * time.Hour,
	}
	if attempts <= 0 {
		attempts = 1
	}
	idx := attempts - 1
	if idx >= len(delays) {
		idx = len(delays) - 1
	}
	return time.Now().Add(delays[idx])
}

func (a *App) deliverActivity(user store.User, inbox string, activity any) error {
	if !safeRemoteURL(inbox) {
		return fmt.Errorf("unsafe inbox url")
	}
	raw, err := json.Marshal(activity)
	if err != nil {
		return err
	}
	return a.deliverActivityBytes(user, inbox, raw)
}

func (a *App) deliverActivityBytes(user store.User, inbox string, raw []byte) error {
	if !safeRemoteURL(inbox) {
		return fmt.Errorf("unsafe inbox url")
	}
	key, err := a.activityPubPrivateKey(user)
	if err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodPost, inbox, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/activity+json")
	req.Header.Set("Accept", "application/activity+json")
	req.Header.Set("Date", time.Now().UTC().Format(http.TimeFormat))
	sum := sha256.Sum256(raw)
	req.Header.Set("Digest", "SHA-256="+base64.StdEncoding.EncodeToString(sum[:]))
	if err := signActivityPubRequest(req, key, activitypubActorID(a.cfg.BaseURL, user.Username)+"#main-key"); err != nil {
		return err
	}
	client := &http.Client{Timeout: 12 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return fmt.Errorf("remote status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return nil
}

func (a *App) activityPubPublicKey(user store.User) (string, error) {
	key, err := a.ensureActivityPubKey(user.ID)
	if err != nil {
		return "", err
	}
	return key.PublicKeyPEM, nil
}

func (a *App) activityPubPrivateKey(user store.User) (*rsa.PrivateKey, error) {
	key, err := a.ensureActivityPubKey(user.ID)
	if err != nil {
		return nil, err
	}
	block, _ := pem.Decode([]byte(key.PrivateKeyPEM))
	if block == nil {
		return nil, errors.New("invalid activitypub private key")
	}
	return x509.ParsePKCS1PrivateKey(block.Bytes)
}

func (a *App) ensureActivityPubKey(userID int64) (store.ActivityPubKey, error) {
	key, err := a.db.ActivityPubKey(userID)
	if err == nil {
		return key, nil
	}
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return store.ActivityPubKey{}, err
	}
	privatePEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(privateKey)})
	publicDER, err := x509.MarshalPKIXPublicKey(&privateKey.PublicKey)
	if err != nil {
		return store.ActivityPubKey{}, err
	}
	publicPEM := pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: publicDER})
	if err := a.db.CreateActivityPubKey(userID, string(privatePEM), string(publicPEM)); err != nil {
		return store.ActivityPubKey{}, err
	}
	return store.ActivityPubKey{UserID: userID, PrivateKeyPEM: string(privatePEM), PublicKeyPEM: string(publicPEM)}, nil
}

func signActivityPubRequest(req *http.Request, key *rsa.PrivateKey, keyID string) error {
	host := req.URL.Host
	target := strings.ToLower(req.Method) + " " + req.URL.RequestURI()
	signed := "(request-target): " + target + "\n" +
		"host: " + host + "\n" +
		"date: " + req.Header.Get("Date") + "\n" +
		"digest: " + req.Header.Get("Digest")
	digest := sha256.Sum256([]byte(signed))
	signature, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
	if err != nil {
		return err
	}
	req.Host = host
	req.Header.Set("Signature", fmt.Sprintf(`keyId="%s",algorithm="rsa-sha256",headers="(request-target) host date digest",signature="%s"`, keyID, base64.StdEncoding.EncodeToString(signature)))
	return nil
}

func fetchRemoteActor(ctx context.Context, actorURL string) (remoteActor, error) {
	if !safeRemoteURL(actorURL) {
		return remoteActor{}, errors.New("unsafe actor url")
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, actorURL, nil)
	if err != nil {
		return remoteActor{}, err
	}
	req.Header.Set("Accept", `application/activity+json, application/ld+json; profile="https://www.w3.org/ns/activitystreams"`)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return remoteActor{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return remoteActor{}, fmt.Errorf("remote actor status %d", resp.StatusCode)
	}
	var actor remoteActor
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&actor); err != nil {
		return remoteActor{}, err
	}
	return actor, nil
}

func safeRemoteURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Hostname() == "" {
		return false
	}
	host := strings.ToLower(u.Hostname())
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return false
	}
	if ip := net.ParseIP(host); ip != nil {
		return !ip.IsLoopback() && !ip.IsPrivate() && !ip.IsUnspecified()
	}
	return true
}

func sameActivityPubObject(value any, expected string) bool {
	switch object := value.(type) {
	case string:
		return object == expected
	case map[string]any:
		id, _ := object["id"].(string)
		return id == expected
	default:
		return false
	}
}

func activitypubActorID(baseURL, username string) string {
	return strings.TrimRight(baseURL, "/") + "/ap/users/" + url.PathEscape(username)
}

func randomID() string {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return strconv.FormatInt(time.Now().UnixNano(), 36)
	}
	return base64.RawURLEncoding.EncodeToString(raw[:])
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

func (a *App) render(w http.ResponseWriter, r *http.Request, name string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if page, ok := data.(map[string]any); ok {
		if user, authenticated := a.currentUser(r); authenticated {
			page["CurrentUser"] = user
		}
	}
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

func (a *App) allowRate(key string, limit int, window time.Duration) bool {
	if a.limiter == nil {
		return true
	}
	return a.limiter.allow(key, limit, window)
}

func (l *rateLimiter) allow(key string, limit int, window time.Duration) bool {
	if limit <= 0 || window <= 0 {
		return true
	}
	now := time.Now()
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.buckets) > 10000 {
		for key, bucket := range l.buckets {
			if !bucket.ResetAt.After(now) {
				delete(l.buckets, key)
			}
		}
	}
	bucket := l.buckets[key]
	if !bucket.ResetAt.After(now) {
		bucket = rateBucket{ResetAt: now.Add(window)}
	}
	if bucket.Count >= limit {
		l.buckets[key] = bucket
		return false
	}
	bucket.Count++
	l.buckets[key] = bucket
	return true
}

func clientKey(r *http.Request) string {
	for _, header := range []string{"CF-Connecting-IP", "X-Real-IP"} {
		if value := strings.TrimSpace(r.Header.Get(header)); value != "" {
			return value
		}
	}
	if forwarded := strings.TrimSpace(r.Header.Get("X-Forwarded-For")); forwarded != "" {
		if first := strings.TrimSpace(strings.Split(forwarded, ",")[0]); first != "" {
			return first
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil && host != "" {
		return host
	}
	if r.RemoteAddr != "" {
		return r.RemoteAddr
	}
	return "unknown"
}

func (a *App) settingsData(user store.User, extra map[string]any) map[string]any {
	data := map[string]any{"User": user, "VAPIDPublic": a.cfg.VAPIDPublic}
	profileURL := strings.TrimRight(a.cfg.BaseURL, "/") + "/@" + url.PathEscape(user.Username)
	badgeURL := profileURL + "/badge.svg"
	data["ProfileURL"] = profileURL
	data["BadgeURL"] = badgeURL
	data["BadgeMarkdown"] = "[![" + user.Username + " on igrec](" + badgeURL + ")](" + profileURL + ")"
	data["BadgeHTML"] = `<a href="` + profileURL + `"><img src="` + badgeURL + `" alt="@` + template.HTMLEscapeString(user.Username) + ` on igrec"></a>`
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
	limit, err := a.db.InviteLimitByInviter(user.ID)
	if err != nil {
		limit = 3
	}
	remaining := limit - len(invites)
	if remaining < 0 {
		remaining = 0
	}
	data["InviteRemaining"] = remaining
	if friends, err := a.db.UserFriends(user.ID); err == nil {
		data["Friends"] = friends
	}
	if tokens, err := a.db.APITokensByUser(user.ID); err == nil {
		data["APITokens"] = tokens
	}
	if _, ok := a.operatorEmails[strings.ToLower(user.Email)]; ok {
		data["IsOperator"] = true
		data["UploadStorage"] = a.uploadStorageStats()
		data["OperatorPulse"] = a.operatorPulse()
	}

	for key, value := range extra {
		data[key] = value
	}
	return data
}

func (a *App) operatorPulse() operatorPulse {
	var pulse operatorPulse
	pulse.UserCount, _ = a.db.CountUsers()
	pulse.PostCount, _ = a.db.CountPosts()
	pulse.PostsToday, _ = a.db.CountPostsSince(time.Now().Truncate(24 * time.Hour))
	pulse.PendingDeliveries, _ = a.db.PendingActivityPubDeliveryCount()
	pulse.DueDeliveries, _ = a.db.DueActivityPubDeliveryCount(time.Now())
	if candidates, err := a.db.DailyEmailCandidates(dayKey(time.Now()), 10000); err == nil {
		pulse.DailyEmailSubscribers = len(candidates)
	}
	return pulse
}

func (a *App) uploadStorageStats() uploadStorageStats {
	var stats uploadStorageStats
	err := filepath.WalkDir(a.cfg.UploadDir, func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return nil
		}
		stats.FileCount++
		stats.Bytes += info.Size()
		return nil
	})
	if err != nil {
		stats.WatchLevel = "error"
		stats.WatchMessage = "upload storage unavailable"
		return stats
	}
	if stats.FileCount > 0 {
		stats.AverageBytes = stats.Bytes / int64(stats.FileCount)
	}
	stats.FormattedBytes = formatBytes(stats.Bytes)
	stats.FormattedAverage = formatBytes(stats.AverageBytes)
	switch {
	case stats.Bytes >= 20*1024*1024*1024:
		stats.WatchLevel = "move"
		stats.WatchMessage = "move images to object storage"
	case stats.Bytes >= 5*1024*1024*1024:
		stats.WatchLevel = "watch"
		stats.WatchMessage = "prepare R2 migration"
	default:
		stats.WatchLevel = "ok"
		stats.WatchMessage = "local storage is fine"
	}
	return stats
}

func formatBytes(bytes int64) string {
	const unit = 1024
	if bytes < unit {
		return strconv.FormatInt(bytes, 10) + " B"
	}
	value := float64(bytes)
	for _, suffix := range []string{"KB", "MB", "GB", "TB"} {
		value /= unit
		if value < unit {
			return strconv.FormatFloat(value, 'f', 1, 64) + " " + suffix
		}
	}
	return strconv.FormatFloat(value, 'f', 1, 64) + " PB"
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

func previewCardURL(baseURL string, post store.Post) string {
	return strings.TrimRight(baseURL, "/") + "/og/@" + url.PathEscape(post.Username) + "/" + url.PathEscape(post.Word) + ".png"
}

func badgeURL(baseURL string, user store.User) string {
	return strings.TrimRight(baseURL, "/") + "/@" + url.PathEscape(user.Username) + "/badge.svg"
}

func absoluteURL(baseURL, value string) string {
	if strings.HasPrefix(value, "http://") || strings.HasPrefix(value, "https://") {
		return value
	}
	return strings.TrimRight(baseURL, "/") + "/" + strings.TrimLeft(value, "/")
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

func NewShortToken() (string, error) {
	var raw [12]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw[:]), nil
}

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return fmt.Sprintf("%x", sum[:])
}

func (a *App) emailToken(user store.User) string {
	return EmailToken(a.cfg, user)
}

func EmailToken(cfg Config, user store.User) string {
	secret := cfg.AppSecret
	if secret == "" {
		secret = cfg.DatabaseURL
	}
	payload := fmt.Sprintf("%d:%s", user.ID, strings.ToLower(user.Email))
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(payload))
	return base64.RawURLEncoding.EncodeToString([]byte(payload + ":" + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))))
}

func (a *App) userFromEmailToken(token string) (store.User, error) {
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		return store.User{}, err
	}
	parts := strings.Split(string(raw), ":")
	if len(parts) != 3 {
		return store.User{}, errors.New("invalid email token")
	}
	id, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return store.User{}, err
	}
	user, err := a.db.UserByID(id)
	if err != nil {
		return store.User{}, err
	}
	if !strings.EqualFold(parts[1], user.Email) {
		return store.User{}, errors.New("email token does not match user")
	}
	secret := a.cfg.AppSecret
	if secret == "" {
		secret = a.cfg.DatabaseURL
	}
	payload := fmt.Sprintf("%d:%s", user.ID, strings.ToLower(user.Email))
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(payload))
	expected := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	if subtle.ConstantTimeCompare([]byte(expected), []byte(parts[2])) != 1 {
		return store.User{}, errors.New("invalid email token")
	}
	return user, nil
}

func safeNext(next string) string {
	if next == "" || !strings.HasPrefix(next, "/") || strings.HasPrefix(next, "//") {
		return "/write"
	}
	return next
}

func parseIntQuery(r *http.Request, key string) int64 {
	value, err := strconv.ParseInt(strings.TrimSpace(r.URL.Query().Get(key)), 10, 64)
	if err != nil || value < 0 {
		return 0
	}
	return value
}

func feedData(posts []postView, pageSize int, title, path string) map[string]any {
	data := map[string]any{"Posts": posts, "FeedTitle": title}
	if len(posts) > pageSize {
		last := posts[pageSize-1]
		data["Posts"] = posts[:pageSize]
		data["MoreHref"] = path + "?before=" + strconv.FormatInt(last.ID, 10)
	}
	return data
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

func normalizeFediverseAcct(raw string) (string, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return "", nil
	}
	value = strings.TrimPrefix(value, "@")
	local, domain, ok := strings.Cut(value, "@")
	if !ok || local == "" || domain == "" {
		return "", errors.New("fediverse handle must look like @name@example.social")
	}
	if strings.ContainsAny(local, " \t\r\n/@") || strings.ContainsAny(domain, " \t\r\n/@:") {
		return "", errors.New("fediverse handle must look like @name@example.social")
	}
	labels := strings.Split(strings.ToLower(domain), ".")
	if len(labels) < 2 {
		return "", errors.New("fediverse handle must look like @name@example.social")
	}
	for _, label := range labels {
		if label == "" || strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") {
			return "", errors.New("fediverse handle must look like @name@example.social")
		}
		for _, r := range label {
			if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '-' {
				return "", errors.New("fediverse handle must look like @name@example.social")
			}
		}
	}
	return "@" + local + "@" + strings.Join(labels, "."), nil
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
			HasImage:    post.ImageURL.Valid && strings.TrimSpace(post.ImageURL.String) != "",
		})
	}
	return views
}

func (a *App) styledPostViews(posts []store.Post, preference string) []postView {
	views := profilePostViews(posts, preference)
	for i := range views {
		if !views[i].HasImage {
			continue
		}
		views[i].CaptionCSS = a.captionStyleForPost(views[i].Post)
		views[i].FocusCSS = imageFocusCSS(views[i].Post)
	}
	return views
}

func imageFocusCSS(post store.Post) template.CSS {
	x := strconv.FormatFloat(unitPercent(post.FocusX), 'f', 1, 64)
	y := strconv.FormatFloat(unitPercent(post.FocusY), 'f', 1, 64)
	return template.CSS("object-position: " + x + "% " + y + "%;")
}

func unitPercent(value float64) float64 {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return 50
	}
	if value < 0 {
		value = 0
	}
	if value > 1 {
		value = 1
	}
	return value * 100
}

func (a *App) captionStyleForPost(post store.Post) template.CSS {
	accent := "#ffd460"
	text := "#ffffff"
	if value, ok := a.imagePalette(post.ImageURL.String); ok {
		accent = value.accent
		text = value.text
	}
	return template.CSS("color: " + text + "; text-shadow: 0 1px 1px #000, 0 0 24px " + accent + ";")
}

type palette struct {
	accent string
	text   string
}

func (a *App) imagePalette(imageURL string) (palette, bool) {
	if !strings.HasPrefix(imageURL, "/uploads/") {
		return palette{}, false
	}
	filename := filepath.Base(imageURL)
	if filename == "." || filename == "/" || filename == "" {
		return palette{}, false
	}
	path := filepath.Join(a.cfg.UploadDir, filename)
	f, err := os.Open(path)
	if err != nil {
		return palette{}, false
	}
	defer f.Close()
	img, _, err := image.Decode(f)
	if err != nil {
		return palette{}, false
	}
	accent, luminance := sampledAccent(img)
	text := "#ffffff"
	if luminance > 0.62 {
		text = "#080a0f"
	}
	return palette{accent: accent, text: text}, true
}

func (a *App) imageDimensions(imageURL string) (int, int, bool) {
	if !strings.HasPrefix(imageURL, "/uploads/") {
		return 0, 0, false
	}
	filename := filepath.Base(imageURL)
	if filename == "." || filename == "/" || filename == "" {
		return 0, 0, false
	}
	path := filepath.Join(a.cfg.UploadDir, filename)
	f, err := os.Open(path)
	if err != nil {
		return 0, 0, false
	}
	defer f.Close()
	cfg, _, err := image.DecodeConfig(f)
	if err != nil {
		return 0, 0, false
	}
	return cfg.Width, cfg.Height, cfg.Width > 0 && cfg.Height > 0
}

func sampledAccent(img image.Image) (string, float64) {
	b := img.Bounds()
	stepX := maxInt(1, b.Dx()/24)
	stepY := maxInt(1, b.Dy()/24)
	var rSum, gSum, bSum float64
	var samples float64
	for y := b.Min.Y; y < b.Max.Y; y += stepY {
		for x := b.Min.X; x < b.Max.X; x += stepX {
			cr, cg, cb, _ := img.At(x, y).RGBA()
			r := float64(cr>>8) / 255.0
			g := float64(cg>>8) / 255.0
			bl := float64(cb>>8) / 255.0
			// Prefer pixels with meaningful saturation for a stronger accent.
			_, sat, _ := rgbToHSV(r, g, bl)
			weight := 0.35 + sat*0.65
			rSum += r * weight
			gSum += g * weight
			bSum += bl * weight
			samples += weight
		}
	}
	if samples == 0 {
		return "#ffd460", 0
	}
	r := clamp01(rSum / samples)
	g := clamp01(gSum / samples)
	bl := clamp01(bSum / samples)
	_, sat, val := rgbToHSV(r, g, bl)
	// Nudge toward a stylish accent: keep hue, boost sat/value slightly.
	sat = clamp01(sat*1.18 + 0.08)
	val = clamp01(val*1.08 + 0.06)
	ar, ag, ab := hsvToRGB(hueOf(r, g, bl), sat, val)
	luminance := relativeLuminance(ar, ag, ab)
	return rgbHex(ar, ag, ab), luminance
}

func rgbToHSV(r, g, b float64) (float64, float64, float64) {
	max := math.Max(r, math.Max(g, b))
	min := math.Min(r, math.Min(g, b))
	delta := max - min
	h := 0.0
	switch {
	case delta == 0:
		h = 0
	case max == r:
		h = math.Mod((g-b)/delta, 6.0)
	case max == g:
		h = (b-r)/delta + 2.0
	default:
		h = (r-g)/delta + 4.0
	}
	h *= 60
	if h < 0 {
		h += 360
	}
	s := 0.0
	if max > 0 {
		s = delta / max
	}
	return h, s, max
}

func hsvToRGB(h, s, v float64) (float64, float64, float64) {
	c := v * s
	x := c * (1 - math.Abs(math.Mod(h/60.0, 2)-1))
	m := v - c
	r, g, b := 0.0, 0.0, 0.0
	switch {
	case h < 60:
		r, g, b = c, x, 0
	case h < 120:
		r, g, b = x, c, 0
	case h < 180:
		r, g, b = 0, c, x
	case h < 240:
		r, g, b = 0, x, c
	case h < 300:
		r, g, b = x, 0, c
	default:
		r, g, b = c, 0, x
	}
	return r + m, g + m, b + m
}

func hueOf(r, g, b float64) float64 {
	h, _, _ := rgbToHSV(r, g, b)
	return h
}

func rgbHex(r, g, b float64) string {
	cr := uint8(clamp01(r) * 255)
	cg := uint8(clamp01(g) * 255)
	cb := uint8(clamp01(b) * 255)
	return fmt.Sprintf("#%02x%02x%02x", cr, cg, cb)
}

func relativeLuminance(r, g, b float64) float64 {
	conv := func(c float64) float64 {
		if c <= 0.03928 {
			return c / 12.92
		}
		return math.Pow((c+0.055)/1.055, 2.4)
	}
	return 0.2126*conv(r) + 0.7152*conv(g) + 0.0722*conv(b)
}

func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
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

func startOfToday() time.Time {
	now := time.Now()
	return time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
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

func renderBadgeSVG(user store.User, post store.Post) string {
	wordText := template.HTMLEscapeString(post.Word)
	userText := template.HTMLEscapeString("@" + user.Username)
	dateText := template.HTMLEscapeString(post.CreatedAt.Format("2006-01-02"))
	width := 260 + len([]rune(post.Word))*30
	if width < 520 {
		width = 520
	}
	if width > 980 {
		width = 980
	}
	wordSize := 74
	if len([]rune(post.Word)) > 10 {
		wordSize = 58
	}
	if len([]rune(post.Word)) > 18 {
		wordSize = 44
	}
	stripes := badgeStripePath(width)
	return fmt.Sprintf(`<svg xmlns="http://www.w3.org/2000/svg" width="%d" height="180" viewBox="0 0 %d 180" role="img" aria-label="%s said %s">
<rect width="100%%" height="100%%" fill="#f6eedc"/>
<path d="%s" stroke="#e4d8b8" stroke-width="1"/>
<rect x="10" y="10" width="%d" height="160" fill="#fffef7" stroke="#111" stroke-width="4"/>
<rect x="24" y="24" width="30" height="30" fill="#fff" stroke="#0055a4" stroke-width="3"/>
<text x="32" y="47" font-family="ui-monospace, SFMono-Regular, Menlo, monospace" font-size="24" font-weight="900" fill="#111">Y</text>
<text x="66" y="47" font-family="ui-monospace, SFMono-Regular, Menlo, monospace" font-size="22" font-weight="900" fill="#0055a4">IGREC</text>
<text x="70" y="47" font-family="ui-monospace, SFMono-Regular, Menlo, monospace" font-size="22" font-weight="900" fill="#ef4135">IGREC</text>
<text x="68" y="47" font-family="ui-monospace, SFMono-Regular, Menlo, monospace" font-size="22" font-weight="900" fill="#111">IGREC</text>
<text x="32" y="118" font-family="ui-monospace, SFMono-Regular, Menlo, monospace" font-size="%d" font-weight="900" fill="#0055a4">%s</text>
<text x="38" y="118" font-family="ui-monospace, SFMono-Regular, Menlo, monospace" font-size="%d" font-weight="900" fill="#ef4135">%s</text>
<text x="35" y="118" font-family="ui-monospace, SFMono-Regular, Menlo, monospace" font-size="%d" font-weight="900" fill="#111">%s</text>
<path d="M24 136H%d" stroke="#111" stroke-width="3"/>
<text x="24" y="158" font-family="ui-monospace, SFMono-Regular, Menlo, monospace" font-size="18" fill="#111">%s</text>
<text x="%d" y="158" text-anchor="end" font-family="ui-monospace, SFMono-Regular, Menlo, monospace" font-size="18" fill="#111">igrec.net · %s</text>
</svg>`, width, width, userText, wordText, stripes, width-20, wordSize, wordText, wordSize, wordText, wordSize, wordText, width-24, userText, width-24, dateText)
}

func badgeStripePath(width int) string {
	var b strings.Builder
	for y := 12; y <= 172; y += 8 {
		if b.Len() > 0 {
			b.WriteByte(' ')
		}
		b.WriteString("M0 ")
		b.WriteString(strconv.Itoa(y))
		b.WriteString("H")
		b.WriteString(strconv.Itoa(width))
	}
	return b.String()
}
