package app

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"
)

func (a *App) pushSubscribe(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !a.pushConfigured() {
		http.Error(w, "push notifications are not configured", http.StatusServiceUnavailable)
		return
	}
	user, ok := a.currentUser(r)
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if !a.validCSRF(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if !a.allowRate("push-subscribe:user:"+strconv.FormatInt(user.ID, 10), 30, time.Hour) || !a.allowRate("push-subscribe:ip:"+clientKey(r), 60, time.Hour) {
		http.Error(w, "rate limited", http.StatusTooManyRequests)
		return
	}
	endpoint := strings.TrimSpace(r.FormValue("endpoint"))
	p256dh := strings.TrimSpace(r.FormValue("p256dh"))
	auth := strings.TrimSpace(r.FormValue("auth"))
	if err := validatePushSubscription(endpoint, p256dh, auth); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := a.db.UpsertPushSubscription(user.ID, endpoint, p256dh, auth); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	count, _ := a.db.PushSubscriptionCountByUser(user.ID)
	writeJSON(w, "application/json; charset=utf-8", map[string]any{"ok": true, "subscription_count": count})
}

func (a *App) pushUnsubscribe(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	user, ok := a.currentUser(r)
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if !a.validCSRF(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if !a.allowRate("push-unsubscribe:user:"+strconv.FormatInt(user.ID, 10), 30, time.Hour) || !a.allowRate("push-unsubscribe:ip:"+clientKey(r), 60, time.Hour) {
		http.Error(w, "rate limited", http.StatusTooManyRequests)
		return
	}
	endpoint := strings.TrimSpace(r.FormValue("endpoint"))
	if endpoint == "" {
		http.Error(w, "endpoint is required", http.StatusBadRequest)
		return
	}
	if err := a.db.DeletePushSubscription(user.ID, endpoint); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	count, _ := a.db.PushSubscriptionCountByUser(user.ID)
	writeJSON(w, "application/json; charset=utf-8", map[string]any{"ok": true, "subscription_count": count})
}

func validatePushSubscription(endpoint, p256dh, auth string) error {
	if endpoint == "" {
		return errors.New("endpoint is required")
	}
	if len(endpoint) > 2000 {
		return errors.New("endpoint is too long")
	}
	if p256dh == "" || auth == "" {
		return errors.New("push keys are required")
	}
	if len(p256dh) > 512 || len(auth) > 512 {
		return errors.New("push keys are too long")
	}
	return nil
}

func (a *App) pushConfigured() bool {
	if strings.TrimSpace(a.cfg.VAPIDPublic) == "" || strings.TrimSpace(a.cfg.VAPIDPrivate) == "" {
		return false
	}
	return validateVAPIDKeys(a.cfg.VAPIDPublic, a.cfg.VAPIDPrivate) == nil
}
