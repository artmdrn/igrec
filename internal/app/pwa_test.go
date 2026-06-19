package app

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	webpush "github.com/SherClockHolmes/webpush-go"
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
		`const CACHE = "igrec-shell-` + assetsVersion + `";`,
		`const NOTIFICATION_URL = "/write?source=push&focus=1";`,
		`"/write",`,
		assetPath("igrec.css"),
		`self.addEventListener("fetch", (event) => {`,
		`self.addEventListener("push", (event) => {`,
		`self.registration.showNotification(title, {`,
		`self.addEventListener("notificationclick", (event) => {`,
		`self.clients.openWindow(target)`,
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
	if !strings.Contains(w.Body.String(), assetPath("pwa.js")) {
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
	pair := mustVAPIDPair(t)
	a.cfg.VAPIDPublic = pair.public
	a.cfg.VAPIDPrivate = pair.private
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

func TestPushSubscribeRejectsWhenVAPIDNotConfigured(t *testing.T) {
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

	if wSubscribe.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected subscribe status %d, got %d: %s", http.StatusServiceUnavailable, wSubscribe.Code, wSubscribe.Body.String())
	}
	if subscriptions, err := a.db.PushSubscriptionsByUser(user.ID); err != nil || len(subscriptions) != 0 {
		t.Fatalf("expected no stored subscriptions, got %d err=%v", len(subscriptions), err)
	}
}

func TestFollowingUserSendsPushNotificationOnce(t *testing.T) {
	a := testApp(t)
	pair := mustVAPIDPair(t)
	a.cfg.VAPIDPublic = pair.public
	a.cfg.VAPIDPrivate = pair.private

	follower, err := a.db.CreateUser("reader", "reader@example.com")
	if err != nil {
		t.Fatal(err)
	}
	followed, err := a.db.CreateUser("author", "author@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if err := a.db.UpsertPushSubscription(followed.ID, "https://push.example/author", "p256dh-author", "auth-author"); err != nil {
		t.Fatal(err)
	}
	sessionToken, sessionHash, err := newToken()
	if err != nil {
		t.Fatal(err)
	}
	if err := a.db.CreateSession(sessionHash, follower.ID, farFuture()); err != nil {
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

	type payload struct {
		Title string `json:"title"`
		Body  string `json:"body"`
		URL   string `json:"url"`
		Tag   string `json:"tag"`
	}
	var delivered []payload
	original := sendBrowserPushNotification
	sendBrowserPushNotification = func(message []byte, sub *webpush.Subscription, options *webpush.Options) (*http.Response, error) {
		var got payload
		if err := json.Unmarshal(message, &got); err != nil {
			t.Fatalf("unmarshal push payload: %v", err)
		}
		delivered = append(delivered, got)
		return &http.Response{
			StatusCode: http.StatusCreated,
			Status:     "201 Created",
			Body:       io.NopCloser(strings.NewReader("")),
		}, nil
	}
	defer func() { sendBrowserPushNotification = original }()

	form := url.Values{}
	form.Set(csrfField, csrf.Value)
	form.Set("username", followed.Username)
	form.Set("next", "/@"+followed.Username)
	reqFollow := httptest.NewRequest(http.MethodPost, "/friends", strings.NewReader(form.Encode()))
	reqFollow.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	reqFollow.AddCookie(&http.Cookie{Name: sessionCookie, Value: sessionToken})
	reqFollow.AddCookie(csrf)
	wFollow := httptest.NewRecorder()
	a.friends(wFollow, reqFollow)

	if wFollow.Code != http.StatusSeeOther {
		t.Fatalf("expected follow redirect status %d, got %d", http.StatusSeeOther, wFollow.Code)
	}
	if len(delivered) != 1 {
		t.Fatalf("expected one delivered push, got %#v", delivered)
	}
	if delivered[0].Title != "igrec" || delivered[0].Body != "@reader followed you" || delivered[0].URL != "/@reader" || delivered[0].Tag != "follow-reader" {
		t.Fatalf("unexpected push payload %#v", delivered[0])
	}
	if follows, err := a.db.UserFollows(follower.ID, followed.ID); err != nil || !follows {
		t.Fatalf("expected stored follow, got follows=%v err=%v", follows, err)
	}

	wRepeat := httptest.NewRecorder()
	reqRepeat := httptest.NewRequest(http.MethodPost, "/friends", strings.NewReader(form.Encode()))
	reqRepeat.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	reqRepeat.AddCookie(&http.Cookie{Name: sessionCookie, Value: sessionToken})
	reqRepeat.AddCookie(csrf)
	a.friends(wRepeat, reqRepeat)

	if wRepeat.Code != http.StatusSeeOther {
		t.Fatalf("expected repeat follow redirect status %d, got %d", http.StatusSeeOther, wRepeat.Code)
	}
	if len(delivered) != 1 {
		t.Fatalf("expected no duplicate push on repeat follow, got %#v", delivered)
	}
}

func TestInviteJoinSendsPushNotificationToInviter(t *testing.T) {
	a := testApp(t)
	pair := mustVAPIDPair(t)
	a.cfg.VAPIDPublic = pair.public
	a.cfg.VAPIDPrivate = pair.private

	inviter, err := a.db.CreateUser("maker", "maker@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if err := a.db.CreateInviteForUser("join-123", inviter.ID); err != nil {
		t.Fatal(err)
	}
	if err := a.db.UpsertPushSubscription(inviter.ID, "https://push.example/maker", "p256dh-maker", "auth-maker"); err != nil {
		t.Fatal(err)
	}

	wGet := httptest.NewRecorder()
	reqGet := httptest.NewRequest(http.MethodGet, "/join?invite=join-123", nil)
	a.join(wGet, reqGet)
	csrf := cookieByName(wGet.Result(), csrfCookie)
	if csrf == nil || csrf.Value == "" {
		t.Fatal("expected csrf cookie from GET /join")
	}

	type payload struct {
		Title string `json:"title"`
		Body  string `json:"body"`
		URL   string `json:"url"`
		Tag   string `json:"tag"`
	}
	var delivered []payload
	original := sendBrowserPushNotification
	sendBrowserPushNotification = func(message []byte, sub *webpush.Subscription, options *webpush.Options) (*http.Response, error) {
		var got payload
		if err := json.Unmarshal(message, &got); err != nil {
			t.Fatalf("unmarshal push payload: %v", err)
		}
		delivered = append(delivered, got)
		return &http.Response{
			StatusCode: http.StatusCreated,
			Status:     "201 Created",
			Body:       io.NopCloser(strings.NewReader("")),
		}, nil
	}
	defer func() { sendBrowserPushNotification = original }()

	form := url.Values{}
	form.Set(csrfField, csrf.Value)
	form.Set("invite", "join-123")
	form.Set("username", "newfriend")
	form.Set("email", "newfriend@example.com")
	reqJoin := httptest.NewRequest(http.MethodPost, "/join", strings.NewReader(form.Encode()))
	reqJoin.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	reqJoin.AddCookie(csrf)
	wJoin := httptest.NewRecorder()
	a.join(wJoin, reqJoin)

	if wJoin.Code != http.StatusSeeOther {
		t.Fatalf("expected join redirect status %d, got %d: %s", http.StatusSeeOther, wJoin.Code, wJoin.Body.String())
	}
	if location := wJoin.Header().Get("Location"); location != "/write" {
		t.Fatalf("expected join redirect to /write, got %q", location)
	}
	if len(delivered) != 1 {
		t.Fatalf("expected one delivered push, got %#v", delivered)
	}
	if delivered[0].Title != "igrec" || delivered[0].Body != "@newfriend joined with your invite" || delivered[0].URL != "/@newfriend" || delivered[0].Tag != "invite-used-newfriend" {
		t.Fatalf("unexpected push payload %#v", delivered[0])
	}
	joined, err := a.db.UserByUsername("newfriend")
	if err != nil {
		t.Fatalf("expected joined user, got err=%v", err)
	}
	if follows, err := a.db.UserFollows(joined.ID, inviter.ID); err != nil || !follows {
		t.Fatalf("expected joined user to follow inviter, got follows=%v err=%v", follows, err)
	}
}
