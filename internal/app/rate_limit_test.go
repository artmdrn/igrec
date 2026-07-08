package app

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestRateLimiterAllowsWithinLimit(t *testing.T) {
	limiter := &rateLimiter{buckets: map[string]rateBucket{}}
	if !limiter.allow("k", 2, time.Minute) {
		t.Fatal("first request should pass")
	}
	if !limiter.allow("k", 2, time.Minute) {
		t.Fatal("second request should pass")
	}
	if limiter.allow("k", 2, time.Minute) {
		t.Fatal("third request should be limited")
	}
}

func TestClientKeyPrefersCloudflareHeader(t *testing.T) {
	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "127.0.0.1:1234"
	req.Header.Set("CF-Connecting-IP", "203.0.113.8")
	if got := clientKey(req); got != "203.0.113.8" {
		t.Fatalf("clientKey = %q", got)
	}
}

func TestLoginPostRateLimitsAuthByIP(t *testing.T) {
	a := testApp(t)
	csrf := "csrf-token"
	for i := 0; i < 30; i++ {
		form := url.Values{}
		form.Set(csrfField, csrf)
		form.Set("email", "missing"+time.Duration(i).String()+"@example.com")
		req := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.AddCookie(&http.Cookie{Name: csrfCookie, Value: csrf})
		req.RemoteAddr = "203.0.113.10:1234"
		w := httptest.NewRecorder()
		a.login(w, req)
		if w.Code == http.StatusTooManyRequests {
			t.Fatalf("request %d was limited too early", i+1)
		}
	}

	form := url.Values{}
	form.Set(csrfField, csrf)
	form.Set("email", "missing-final@example.com")
	req := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: csrfCookie, Value: csrf})
	req.RemoteAddr = "203.0.113.10:1234"
	w := httptest.NewRecorder()
	a.login(w, req)

	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("expected status %d, got %d", http.StatusTooManyRequests, w.Code)
	}
}

func TestIndieAuthStartRateLimitsBeforeDiscovery(t *testing.T) {
	a := testApp(t)
	csrf := "csrf-token"
	for i := 0; i < 20; i++ {
		form := url.Values{}
		form.Set(csrfField, csrf)
		form.Set("me", "")
		req := httptest.NewRequest(http.MethodPost, "/auth/indieauth/start", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.AddCookie(&http.Cookie{Name: csrfCookie, Value: csrf})
		req.RemoteAddr = "203.0.113.20:1234"
		w := httptest.NewRecorder()
		a.indieAuthStart(w, req)
		if w.Code == http.StatusTooManyRequests {
			t.Fatalf("request %d was limited too early", i+1)
		}
	}

	form := url.Values{}
	form.Set(csrfField, csrf)
	form.Set("me", "")
	req := httptest.NewRequest(http.MethodPost, "/auth/indieauth/start", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: csrfCookie, Value: csrf})
	req.RemoteAddr = "203.0.113.20:1234"
	w := httptest.NewRecorder()
	a.indieAuthStart(w, req)

	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("expected status %d, got %d", http.StatusTooManyRequests, w.Code)
	}
}

func TestPasskeyLoginOptionsRateLimitsByIP(t *testing.T) {
	a := testApp(t)
	for i := 0; i < 30; i++ {
		req := httptest.NewRequest(http.MethodPost, "/auth/passkeys/login/options", nil)
		req.RemoteAddr = "203.0.113.30:1234"
		w := httptest.NewRecorder()
		a.passkeyLoginOptions(w, req)
		if w.Code == http.StatusTooManyRequests {
			t.Fatalf("request %d was limited too early", i+1)
		}
	}

	req := httptest.NewRequest(http.MethodPost, "/auth/passkeys/login/options", nil)
	req.RemoteAddr = "203.0.113.30:1234"
	w := httptest.NewRecorder()
	a.passkeyLoginOptions(w, req)

	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("expected status %d, got %d", http.StatusTooManyRequests, w.Code)
	}
}
