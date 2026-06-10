package app

import (
	"errors"
	"fmt"
	"html/template"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"igrec.net/igrec/internal/store"
	"igrec.net/igrec/internal/word"
)

type postView struct {
	store.Post
	DisplayTime string
	MachineTime string
	HasImage    bool
	CaptionCSS  template.CSS
	FocusCSS    template.CSS
	URL         string
	PreviewURL  string
}

type archiveMonth struct {
	Year  string
	Month string
	Href  string
	Label string
	Count int
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
		post, err := postByPathSegment(a.db, user.Username, value)
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
	if relMeLinks, err := a.db.RelMeLinksByUser(user.ID); err == nil && len(relMeLinks) > 0 {
		data["RelMeLinks"] = relMeLinks
	}
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
		postPath := "/@" + url.PathEscape(post.Username) + "/" + url.PathEscape(canonicalPostSlug(post))
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

func postURL(baseURL string, post store.Post) string {
	return strings.TrimRight(baseURL, "/") + "/@" + url.PathEscape(post.Username) + "/" + url.PathEscape(canonicalPostSlug(post))
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
		views[i].URL = postURL(a.cfg.BaseURL, views[i].Post)
		views[i].PreviewURL = previewCardURL(a.cfg.BaseURL, views[i].Post)
		if !views[i].HasImage {
			continue
		}
		views[i].CaptionCSS = a.captionStyleForPost(views[i].Post)
		views[i].FocusCSS = imageFocusCSS(views[i].Post)
	}
	return views
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
