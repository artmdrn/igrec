package app

import (
	"errors"
	"html/template"
	"net/http"
	"net/mail"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"igrec.net/igrec/internal/activitypub"
	emailpkg "igrec.net/igrec/internal/email"
	"igrec.net/igrec/internal/store"
)

type operatorPulse struct {
	UserCount             int
	PostCount             int
	PostsToday            int
	PendingDeliveries     int
	DueDeliveries         int
	DailyEmailSubscribers int
}

type inviteView struct {
	Code string
	Link string
	Used bool
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
		relMeLinks, err := normalizeRelMeLinks(r.FormValue("rel_me"))
		if err != nil {
			a.render(w, r, "settings.html", a.withCSRF(w, r, a.settingsData(user, map[string]any{"Error": err.Error(), "RelMeText": strings.TrimSpace(r.FormValue("rel_me"))})))
			return
		}
		if err := a.db.UpdateSettingsProfile(user.ID, r.FormValue("timestamp_preference"), r.FormValue("daily") == "on", fediverseAcct, relMeLinks); err != nil {
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
			"rel_me":               mustRelMeLinks(a.db.RelMeLinksByUser(user.ID)),
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

func (a *App) settingsData(user store.User, extra map[string]any) map[string]any {
	data := map[string]any{"User": user, "VAPIDPublic": a.cfg.VAPIDPublic}
	profileURL := strings.TrimRight(a.cfg.BaseURL, "/") + "/@" + url.PathEscape(user.Username)
	badgeURL := profileURL + "/badge.svg"
	data["ProfileURL"] = profileURL
	data["BadgeURL"] = badgeURL
	data["BadgeMarkdown"] = "[![" + user.Username + " on igrec](" + badgeURL + ")](" + profileURL + ")"
	data["BadgeHTML"] = `<a href="` + profileURL + `"><img src="` + badgeURL + `" alt="@` + template.HTMLEscapeString(user.Username) + ` on igrec"></a>`
	data["AppleWalletReady"] = a.appleWalletConfigured()
	data["AppleWalletURL"] = "/wallet/apple.pkpass"
	count, _ := a.db.PasskeyCount(user.ID)
	data["PasskeyCount"] = count
	if streakCount := a.userStreak(user.ID); streakCount > 1 {
		data["Streak"] = streakCount
	}
	if relMeLinks, err := a.db.RelMeLinksByUser(user.ID); err == nil {
		data["RelMeLinks"] = relMeLinks
		data["RelMeText"] = strings.Join(relMeLinks, "\n")
	}

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

func normalizeRelMeLinks(raw string) ([]string, error) {
	lines := strings.Split(strings.ReplaceAll(raw, "\r\n", "\n"), "\n")
	links := make([]string, 0, len(lines))
	seen := make(map[string]struct{}, len(lines))
	for _, line := range lines {
		value := strings.TrimSpace(line)
		if value == "" {
			continue
		}
		parsed, err := url.Parse(value)
		if err != nil || !parsed.IsAbs() || !strings.EqualFold(parsed.Scheme, "https") || parsed.Host == "" || parsed.User != nil {
			return nil, errors.New("rel=me links must be full https URLs")
		}
		parsed.Scheme = "https"
		parsed.Host = strings.ToLower(parsed.Host)
		parsed.Fragment = ""
		normalized := parsed.String()
		if _, ok := seen[normalized]; ok {
			continue
		}
		seen[normalized] = struct{}{}
		links = append(links, normalized)
	}
	return links, nil
}

func mustRelMeLinks(links []string, err error) []string {
	if err != nil {
		return nil
	}
	return links
}
