package app

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"igrec.net/igrec/internal/store"
	"igrec.net/igrec/internal/word"
)

type apiPostView struct {
	ID        int64   `json:"id"`
	Word      string  `json:"word"`
	URL       string  `json:"url"`
	ImageURL  *string `json:"image_url,omitempty"`
	CreatedAt string  `json:"created_at"`
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
	var imageURL *string
	focusX := 0.5
	focusY := 0.5
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
		r.Body = http.MaxBytesReader(w, r.Body, maxUploadBytes+2<<20)
		if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
			if err := r.ParseMultipartForm(maxUploadBytes + 2<<20); err != nil {
				http.Error(w, "invalid form", http.StatusBadRequest)
				return
			}
		} else {
			if err := r.ParseForm(); err != nil {
				http.Error(w, "invalid form", http.StatusBadRequest)
				return
			}
		}
		raw = r.FormValue("word")
		focusX = parseUnitFloat(r.FormValue("focus_x"), 0.5)
		focusY = parseUnitFloat(r.FormValue("focus_y"), 0.5)
		imageFile, imageHeader, err := r.FormFile("image_file")
		if err == nil {
			defer imageFile.Close()
			uploaded, uploadErr := a.saveUploadedImage(imageFile, imageHeader)
			if uploadErr != nil {
				http.Error(w, uploadErr.Error(), http.StatusBadRequest)
				return
			}
			imageURL = &uploaded
		} else if !errors.Is(err, http.ErrMissingFile) {
			http.Error(w, "image upload failed", http.StatusBadRequest)
			return
		}
	}
	value, err := word.Normalize(raw)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	post, err := a.db.CreatePostWithFocus(user.ID, value, imageURL, focusX, focusY)
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
