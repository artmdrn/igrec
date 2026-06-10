package app

import (
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/mail"
	"strings"
	"time"

	"igrec.net/igrec/internal/store"
	"igrec.net/igrec/internal/word"
)

type inboundAttachment struct {
	Filename    string `json:"filename"`
	ContentType string `json:"content_type"`
	Data        string `json:"data"`
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
		if value, err := word.Normalize(line); err == nil {
			return value, nil
		}
	}
	return "", errors.New("no word found")
}
