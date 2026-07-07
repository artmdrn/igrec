package app

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestMastodonStartRegistersAppAndRedirects(t *testing.T) {
	a := testApp(t)
	oldClient := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.Method != http.MethodPost || req.URL.String() != "https://mastodon.example/api/v1/apps" {
			t.Fatalf("unexpected app registration request %s %s", req.Method, req.URL.String())
		}
		body, err := io.ReadAll(req.Body)
		if err != nil {
			t.Fatal(err)
		}
		values, err := url.ParseQuery(string(body))
		if err != nil {
			t.Fatal(err)
		}
		if values.Get("redirect_uris") != "http://localhost:8080/auth/mastodon/callback" {
			t.Fatalf("unexpected redirect_uris %q", values.Get("redirect_uris"))
		}
		if values.Get("scopes") != "read:accounts" {
			t.Fatalf("unexpected scopes %q", values.Get("scopes"))
		}
		return textResponse(200, `{"client_id":"client-id","client_secret":"client-secret"}`, http.Header{"Content-Type": {"application/json"}}), nil
	})}
	t.Cleanup(func() { http.DefaultClient = oldClient })

	start := postMastodonStart(t, a, "mastodon.example", "/friends")
	location, err := url.Parse(start.Header.Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(location.String(), "https://mastodon.example/oauth/authorize") {
		t.Fatalf("expected redirect to Mastodon authorize endpoint, got %q", location.String())
	}
	if location.Query().Get("client_id") != "client-id" {
		t.Fatalf("expected client id in redirect, got %q", location.Query().Get("client_id"))
	}
	if location.Query().Get("scope") != "read:accounts" {
		t.Fatalf("expected read:accounts scope, got %q", location.Query().Get("scope"))
	}
	if cookieByName(start, mastodonOAuthStateCookie) == nil {
		t.Fatal("expected Mastodon state cookie")
	}
	if cookieByName(start, mastodonOAuthClientSecretCookie) == nil {
		t.Fatal("expected Mastodon client secret cookie")
	}
}

func TestMastodonCallbackStartsSessionForLinkedIdentity(t *testing.T) {
	a := testApp(t)
	user, err := a.db.CreateUser("masto", "masto@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if err := a.db.LinkAuthIdentity(user.ID, "mastodon", "alice@mastodon.example"); err != nil {
		t.Fatal(err)
	}
	oldClient := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: mastodonOAuthTransport(t, "alice")}
	t.Cleanup(func() { http.DefaultClient = oldClient })

	start := postMastodonStart(t, a, "mastodon.example", "/friends")
	location, err := url.Parse(start.Header.Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/auth/mastodon/callback?code=good-code&state="+url.QueryEscape(location.Query().Get("state")), nil)
	for _, cookie := range start.Cookies() {
		req.AddCookie(cookie)
	}
	w := httptest.NewRecorder()

	a.mastodonCallback(w, req)

	if w.Code != http.StatusSeeOther {
		t.Fatalf("expected status %d, got %d", http.StatusSeeOther, w.Code)
	}
	if got := w.Header().Get("Location"); got != "/friends" {
		t.Fatalf("expected redirect to /friends, got %q", got)
	}
	session := cookieByName(w.Result(), sessionCookie)
	if session == nil || session.Value == "" {
		t.Fatal("expected session cookie")
	}
	signedIn, err := a.db.UserBySessionHash(hashToken(session.Value))
	if err != nil {
		t.Fatal(err)
	}
	if signedIn.ID != user.ID {
		t.Fatalf("expected signed-in user %d, got %d", user.ID, signedIn.ID)
	}
}

func TestMastodonCallbackLinksLegacyFediverseHandle(t *testing.T) {
	a := testApp(t)
	user, err := a.db.CreateUser("legacy", "legacy@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if err := a.db.UpdateSettingsProfile(user.ID, "smart", true, "@alice@mastodon.example", "", nil); err != nil {
		t.Fatal(err)
	}
	oldClient := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: mastodonOAuthTransport(t, "alice")}
	t.Cleanup(func() { http.DefaultClient = oldClient })

	start := postMastodonStart(t, a, "mastodon.example", "/write")
	location, err := url.Parse(start.Header.Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/auth/mastodon/callback?code=good-code&state="+url.QueryEscape(location.Query().Get("state")), nil)
	for _, cookie := range start.Cookies() {
		req.AddCookie(cookie)
	}
	w := httptest.NewRecorder()

	a.mastodonCallback(w, req)

	if w.Code != http.StatusSeeOther {
		t.Fatalf("expected status %d, got %d", http.StatusSeeOther, w.Code)
	}
	found, err := a.db.UserByAuthIdentity("mastodon", "alice@mastodon.example")
	if err != nil {
		t.Fatal(err)
	}
	if found.ID != user.ID {
		t.Fatalf("expected linked identity for user %d, got %d", user.ID, found.ID)
	}
}

func TestMastodonCallbackRejectsUnknownAccount(t *testing.T) {
	a := testApp(t)
	oldClient := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: mastodonOAuthTransport(t, "unknown")}
	t.Cleanup(func() { http.DefaultClient = oldClient })

	start := postMastodonStart(t, a, "mastodon.example", "/write")
	location, err := url.Parse(start.Header.Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/auth/mastodon/callback?code=good-code&state="+url.QueryEscape(location.Query().Get("state")), nil)
	for _, cookie := range start.Cookies() {
		req.AddCookie(cookie)
	}
	w := httptest.NewRecorder()

	a.mastodonCallback(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, w.Code)
	}
	if cookieByName(w.Result(), sessionCookie) != nil {
		t.Fatal("did not expect session cookie for unknown Mastodon account")
	}
}

func postMastodonStart(t *testing.T, a *App, instance, next string) *http.Response {
	t.Helper()
	csrf := "csrf-token"
	form := url.Values{}
	form.Set(csrfField, csrf)
	form.Set("instance", instance)
	form.Set("next", next)
	req := httptest.NewRequest(http.MethodPost, "/auth/mastodon/start", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: csrfCookie, Value: csrf})
	w := httptest.NewRecorder()
	a.mastodonStart(w, req)
	if w.Code != http.StatusSeeOther {
		t.Fatalf("expected start status %d, got %d", http.StatusSeeOther, w.Code)
	}
	return w.Result()
}

func mastodonOAuthTransport(t *testing.T, acct string) http.RoundTripper {
	t.Helper()
	return roundTripFunc(func(req *http.Request) (*http.Response, error) {
		switch {
		case req.Method == http.MethodPost && req.URL.String() == "https://mastodon.example/api/v1/apps":
			return textResponse(200, `{"client_id":"client-id","client_secret":"client-secret"}`, http.Header{"Content-Type": {"application/json"}}), nil
		case req.Method == http.MethodPost && req.URL.String() == "https://mastodon.example/oauth/token":
			body, err := io.ReadAll(req.Body)
			if err != nil {
				t.Fatal(err)
			}
			values, err := url.ParseQuery(string(body))
			if err != nil {
				t.Fatal(err)
			}
			if values.Get("code") != "good-code" {
				t.Fatalf("expected good-code, got %q", values.Get("code"))
			}
			return textResponse(200, `{"access_token":"access-token"}`, http.Header{"Content-Type": {"application/json"}}), nil
		case req.Method == http.MethodGet && req.URL.String() == "https://mastodon.example/api/v1/accounts/verify_credentials":
			if req.Header.Get("Authorization") != "Bearer access-token" {
				t.Fatalf("unexpected Authorization header %q", req.Header.Get("Authorization"))
			}
			return textResponse(200, `{"acct":"`+acct+`"}`, http.Header{"Content-Type": {"application/json"}}), nil
		default:
			t.Fatalf("unexpected Mastodon request %s %s", req.Method, req.URL.String())
		}
		return nil, nil
	})
}
