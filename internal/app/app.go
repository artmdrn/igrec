package app

import (
	"bytes"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
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
	"net/http"
	"net/mail"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
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
}

type postView struct {
	store.Post
	DisplayTime string
	MachineTime string
	HasImage    bool
	CaptionCSS  template.CSS
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

const sessionCookie = "igrec_session"
const csrfCookie = "igrec_csrf"
const csrfField = "csrf_token"
const maxUploadBytes = 8 << 20

var usernamePattern = regexp.MustCompile(`^[A-Za-z0-9_]{1,32}$`)

func New(cfg Config, db *store.DB) http.Handler {
	app := &App{
		cfg:            cfg,
		db:             db,
		templates:      template.Must(template.ParseGlob("web/templates/*.html")),
		operatorEmails: make(map[string]struct{}),
	}
	for _, email := range cfg.OperatorEmails {
		normalized := strings.ToLower(strings.TrimSpace(email))
		if normalized != "" {
			app.operatorEmails[normalized] = struct{}{}
		}
	}

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
	ttf, err := truetype.Parse(gomono.TTF)
	if err != nil {
		return nil, err
	}
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	fillRect(img, img.Bounds(), color.RGBA{246, 241, 222, 255})
	for y := 0; y < height; y += 8 {
		fillRect(img, image.Rect(0, y, width, y+1), color.RGBA{230, 220, 190, 120})
	}
	fillRect(img, image.Rect(38, 38, width-38, height-38), color.RGBA{255, 252, 242, 255})
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
	drawText(img, ttf, post.Word, wordSize, wordX-11, wordY, color.RGBA{0, 35, 149, 255})
	drawText(img, ttf, post.Word, wordSize, wordX+11, wordY, color.RGBA{237, 41, 57, 255})
	drawText(img, ttf, post.Word, wordSize, wordX, wordY, color.RGBA{5, 7, 12, 255})

	byline := "@" + post.Username
	drawText(img, ttf, byline, 34, centeredTextX(ttf, byline, 34, width), 466, color.RGBA{5, 7, 12, 255})
	drawText(img, ttf, post.CreatedAt.Format("2006-01-02 15:04"), 24, 80, 548, color.RGBA{5, 7, 12, 255})
	drawText(img, ttf, "igrec.net", 24, width-220, 548, color.RGBA{5, 7, 12, 255})
	fillRect(img, image.Rect(80, 492, width-80, 497), color.RGBA{5, 7, 12, 255})
	return img, nil
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
	data := map[string]any{"User": user, "Posts": a.styledPostViews(posts, user.TimestampPreference), "Months": months, "Title": title}
	if viewer, ok := a.currentUser(r); ok && viewer.ID != user.ID {
		follows, err := a.db.UserFollows(viewer.ID, user.ID)
		if err == nil {
			data["CanFriend"] = true
			data["IsFriend"] = follows
		}
	}
	a.render(w, r, "profile.html", a.withCSRF(w, r, data))
}

func (a *App) write(w http.ResponseWriter, r *http.Request) {
	user, ok := a.currentUser(r)
	if !ok {
		a.requireLogin(w, r)
		return
	}

	switch r.Method {
	case http.MethodGet:
		a.render(w, r, "write.html", a.withCSRF(w, r, nil))
	case http.MethodPost:
		if !a.validCSRF(r) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, maxUploadBytes+2<<20)
		value, err := word.Normalize(r.FormValue("word"))
		if err != nil {
			a.render(w, r, "write.html", a.withCSRF(w, r, map[string]any{"Error": err.Error(), "Word": r.FormValue("word")}))
			return
		}
		var imageURL *string
		imageFile, imageHeader, err := r.FormFile("image_file")
		if err == nil {
			defer imageFile.Close()
			uploaded, uploadErr := a.saveUploadedImage(imageFile, imageHeader)
			if uploadErr != nil {
				a.render(w, r, "write.html", a.withCSRF(w, r, map[string]any{
					"Error": uploadErr.Error(),
					"Word":  r.FormValue("word"),
				}))
				return
			}
			imageURL = &uploaded
		} else if !errors.Is(err, http.ErrMissingFile) {
			a.render(w, r, "write.html", a.withCSRF(w, r, map[string]any{
				"Error": "image upload failed",
				"Word":  r.FormValue("word"),
			}))
			return
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
		if err := a.db.UpdateSettings(user.ID, r.FormValue("timestamp_preference"), r.FormValue("daily") == "on"); err != nil {
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
			"actor":  activitypub.Actor(a.cfg.BaseURL, user),
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

func previewCardURL(baseURL string, post store.Post) string {
	return strings.TrimRight(baseURL, "/") + "/og/@" + url.PathEscape(post.Username) + "/" + url.PathEscape(post.Word) + ".png"
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
	}
	return views
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
