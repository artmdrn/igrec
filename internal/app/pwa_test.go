package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestManifestIncludesPWAInstallMetadata(t *testing.T) {
	a := testApp(t)
	req := httptest.NewRequest(http.MethodGet, "/manifest.webmanifest", nil)
	w := httptest.NewRecorder()

	a.manifest(w, req)

	body := w.Body.String()
	if w.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, w.Code)
	}
	if contentType := w.Header().Get("Content-Type"); contentType != "application/manifest+json; charset=utf-8" {
		t.Fatalf("expected manifest content type, got %q", contentType)
	}
	for _, needle := range []string{
		`"id":"/write"`,
		`"start_url":"/write?source=pwa"`,
		`"scope":"/"`,
		`"display_override":["standalone","browser"]`,
		`"shortcuts":[{"name":"write","short_name":"write","url":"/write"}`,
	} {
		if !strings.Contains(body, needle) {
			t.Fatalf("expected manifest to contain %s, got %s", needle, body)
		}
	}
}

func TestServiceWorkerServesRootScopedWorker(t *testing.T) {
	a := testApp(t)
	req := httptest.NewRequest(http.MethodGet, "/service-worker.js", nil)
	w := httptest.NewRecorder()

	a.serviceWorker(w, req)

	body := w.Body.String()
	if w.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, w.Code)
	}
	if contentType := w.Header().Get("Content-Type"); contentType != "application/javascript; charset=utf-8" {
		t.Fatalf("expected javascript content type, got %q", contentType)
	}
	if allowed := w.Header().Get("Service-Worker-Allowed"); allowed != "/" {
		t.Fatalf("expected root scope header, got %q", allowed)
	}
	for _, needle := range []string{
		`const CACHE = "igrec-shell-v1";`,
		`"/write",`,
		`self.addEventListener("fetch", (event) => {`,
	} {
		if !strings.Contains(body, needle) {
			t.Fatalf("expected worker to contain %s, got %s", needle, body)
		}
	}
}

func TestLayoutRegistersPWAScript(t *testing.T) {
	a := testApp(t)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()

	a.firehose(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, w.Code)
	}
	if !strings.Contains(w.Body.String(), `/static/pwa.js?v=20260611-push`) {
		t.Fatalf("expected layout to include pwa registration script, got %s", w.Body.String())
	}
}

func TestSettingsShowsPushControlsWhenVAPIDConfigured(t *testing.T) {
	a := testApp(t)
	a.cfg.VAPIDPublic = "BExampleVapidPublicKey"
	user, err := a.db.CreateUser("pusher", "pusher@example.com")
	if err != nil {
		t.Fatal(err)
	}
	sessionToken, sessionHash, err := newToken()
	if err != nil {
		t.Fatal(err)
	}
	if err := a.db.CreateSession(sessionHash, user.ID, farFuture()); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/settings", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: sessionToken})
	w := httptest.NewRecorder()
	a.settings(w, req)

	body := w.Body.String()
	if w.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, w.Code)
	}
	for _, needle := range []string{
		`data-push-toggle`,
		`data-push-vapid="BExampleVapidPublicKey"`,
		`0 devices ready`,
	} {
		if !strings.Contains(body, needle) {
			t.Fatalf("expected settings page to contain %s, got %s", needle, body)
		}
	}
}

func TestPushSubscribeAndUnsubscribe(t *testing.T) {
	a := testApp(t)
	user, err := a.db.CreateUser("pusher", "pusher@example.com")
	if err != nil {
		t.Fatal(err)
	}
	sessionToken, sessionHash, err := newToken()
	if err != nil {
		t.Fatal(err)
	}
	if err := a.db.CreateSession(sessionHash, user.ID, farFuture()); err != nil {
		t.Fatal(err)
	}

	wGet := httptest.NewRecorder()
	reqGet := httptest.NewRequest(http.MethodGet, "/settings", nil)
	reqGet.AddCookie(&http.Cookie{Name: sessionCookie, Value: sessionToken})
	a.settings(wGet, reqGet)
	csrf := cookieByName(wGet.Result(), csrfCookie)
	if csrf == nil || csrf.Value == "" {
		t.Fatal("expected csrf cookie from GET /settings")
	}

	subscribeForm := url.Values{}
	subscribeForm.Set(csrfField, csrf.Value)
	subscribeForm.Set("endpoint", "https://push.example/device")
	subscribeForm.Set("p256dh", "p256dh-value")
	subscribeForm.Set("auth", "auth-value")
	reqSubscribe := httptest.NewRequest(http.MethodPost, "/push/subscribe", strings.NewReader(subscribeForm.Encode()))
	reqSubscribe.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	reqSubscribe.AddCookie(&http.Cookie{Name: sessionCookie, Value: sessionToken})
	reqSubscribe.AddCookie(csrf)
	wSubscribe := httptest.NewRecorder()
	a.pushSubscribe(wSubscribe, reqSubscribe)

	if wSubscribe.Code != http.StatusOK {
		t.Fatalf("expected subscribe status %d, got %d: %s", http.StatusOK, wSubscribe.Code, wSubscribe.Body.String())
	}
	var subscribePayload struct {
		SubscriptionCount int `json:"subscription_count"`
	}
	if err := json.Unmarshal(wSubscribe.Body.Bytes(), &subscribePayload); err != nil {
		t.Fatal(err)
	}
	if subscribePayload.SubscriptionCount != 1 {
		t.Fatalf("expected 1 stored subscription, got %d", subscribePayload.SubscriptionCount)
	}
	if subscriptions, err := a.db.PushSubscriptionsByUser(user.ID); err != nil || len(subscriptions) != 1 {
		t.Fatalf("expected one stored subscription, got %d err=%v", len(subscriptions), err)
	}

	unsubscribeForm := url.Values{}
	unsubscribeForm.Set(csrfField, csrf.Value)
	unsubscribeForm.Set("endpoint", "https://push.example/device")
	reqUnsubscribe := httptest.NewRequest(http.MethodPost, "/push/unsubscribe", strings.NewReader(unsubscribeForm.Encode()))
	reqUnsubscribe.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	reqUnsubscribe.AddCookie(&http.Cookie{Name: sessionCookie, Value: sessionToken})
	reqUnsubscribe.AddCookie(csrf)
	wUnsubscribe := httptest.NewRecorder()
	a.pushUnsubscribe(wUnsubscribe, reqUnsubscribe)

	if wUnsubscribe.Code != http.StatusOK {
		t.Fatalf("expected unsubscribe status %d, got %d: %s", http.StatusOK, wUnsubscribe.Code, wUnsubscribe.Body.String())
	}
	var unsubscribePayload struct {
		SubscriptionCount int `json:"subscription_count"`
	}
	if err := json.Unmarshal(wUnsubscribe.Body.Bytes(), &unsubscribePayload); err != nil {
		t.Fatal(err)
	}
	if unsubscribePayload.SubscriptionCount != 0 {
		t.Fatalf("expected 0 stored subscriptions, got %d", unsubscribePayload.SubscriptionCount)
	}
}
