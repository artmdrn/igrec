package app

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestIndieAuthStartDiscoversEndpointAndRedirects(t *testing.T) {
	a := testApp(t)
	oldClient := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.Method != http.MethodGet || req.URL.String() != "https://example.com/" {
			t.Fatalf("unexpected discovery request %s %s", req.Method, req.URL.String())
		}
		return textResponse(200, "ok", http.Header{"Link": {`</auth>; rel="authorization_endpoint"`}}), nil
	})}
	t.Cleanup(func() { http.DefaultClient = oldClient })

	csrf := "csrf-token"
	form := url.Values{}
	form.Set(csrfField, csrf)
	form.Set("me", "https://example.com")
	form.Set("next", "/friends")
	req := httptest.NewRequest(http.MethodPost, "/auth/indieauth/start", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: csrfCookie, Value: csrf})
	w := httptest.NewRecorder()

	a.indieAuthStart(w, req)

	if w.Code != http.StatusSeeOther {
		t.Fatalf("expected status %d, got %d", http.StatusSeeOther, w.Code)
	}
	location, err := url.Parse(w.Header().Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(location.String(), "https://example.com/auth") {
		t.Fatalf("expected redirect to provider, got %q", location.String())
	}
	if location.Query().Get("response_type") != "code" {
		t.Fatalf("expected code response_type, got %q", location.Query().Get("response_type"))
	}
	if location.Query().Get("me") != "https://example.com/" {
		t.Fatalf("expected normalized me URL, got %q", location.Query().Get("me"))
	}
	if cookieByName(w.Result(), indieAuthStateCookie) == nil {
		t.Fatal("expected IndieAuth state cookie")
	}
	if cookieByName(w.Result(), indieAuthEndpointCookie) == nil {
		t.Fatal("expected IndieAuth endpoint cookie")
	}
}

func TestIndieAuthCallbackStartsSessionForVerifiedDomain(t *testing.T) {
	a := testApp(t)
	oldClient := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		switch req.Method {
		case http.MethodGet:
			if req.URL.String() != "https://example.com/" {
				t.Fatalf("unexpected discovery request %q", req.URL.String())
			}
			return textResponse(200, `<link rel="authorization_endpoint" href="/auth">`, http.Header{"Content-Type": {"text/html; charset=utf-8"}}), nil
		case http.MethodPost:
			if req.URL.String() != "https://example.com/auth" {
				t.Fatalf("expected token exchange at /auth, got %s", req.URL.String())
			}
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
			return textResponse(200, `{"me":"https://example.com/profile"}`, http.Header{"Content-Type": {"application/json"}}), nil
		default:
			t.Fatalf("unexpected method %s", req.Method)
		}
		return nil, nil
	})}
	t.Cleanup(func() { http.DefaultClient = oldClient })

	user, err := a.db.CreateUser("domainuser", "domain@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if err := a.db.LinkAuthIdentity(user.ID, "indieauth_domain", "example.com"); err != nil {
		t.Fatal(err)
	}

	start := postIndieAuthStart(t, a, "https://example.com", "/friends")
	location, err := url.Parse(start.Header.Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/auth/indieauth/callback?code=good-code&state="+url.QueryEscape(location.Query().Get("state")), nil)
	for _, cookie := range start.Cookies() {
		req.AddCookie(cookie)
	}
	w := httptest.NewRecorder()

	a.indieAuthCallback(w, req)

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

func TestIndieAuthCallbackRejectsUnknownDomain(t *testing.T) {
	a := testApp(t)
	oldClient := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.Method == http.MethodPost {
			return textResponse(200, "me="+url.QueryEscape("https://unknown.example/profile"), http.Header{"Content-Type": {"application/x-www-form-urlencoded"}}), nil
		}
		return textResponse(200, "ok", http.Header{"Link": {`</auth>; rel="authorization_endpoint"`}}), nil
	})}
	t.Cleanup(func() { http.DefaultClient = oldClient })

	start := postIndieAuthStart(t, a, "https://unknown.example", "/write")
	location, err := url.Parse(start.Header.Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/auth/indieauth/callback?code=good-code&state="+url.QueryEscape(location.Query().Get("state")), nil)
	for _, cookie := range start.Cookies() {
		req.AddCookie(cookie)
	}
	w := httptest.NewRecorder()

	a.indieAuthCallback(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, w.Code)
	}
	if cookieByName(w.Result(), sessionCookie) != nil {
		t.Fatal("did not expect session cookie for unknown domain")
	}
}

func postIndieAuthStart(t *testing.T, a *App, me, next string) *http.Response {
	t.Helper()
	csrf := "csrf-token"
	form := url.Values{}
	form.Set(csrfField, csrf)
	form.Set("me", me)
	form.Set("next", next)
	req := httptest.NewRequest(http.MethodPost, "/auth/indieauth/start", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: csrfCookie, Value: csrf})
	w := httptest.NewRecorder()
	a.indieAuthStart(w, req)
	if w.Code != http.StatusSeeOther {
		t.Fatalf("expected start status %d, got %d", http.StatusSeeOther, w.Code)
	}
	return w.Result()
}

func textResponse(status int, body string, header http.Header) *http.Response {
	if header == nil {
		header = http.Header{}
	}
	return &http.Response{
		StatusCode: status,
		Header:     header,
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}
