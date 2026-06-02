package app

import (
	"net/http/httptest"
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
