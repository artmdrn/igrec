package app

import (
	"errors"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

type rateLimiter struct {
	mu      sync.Mutex
	buckets map[string]rateBucket
}

type rateBucket struct {
	ResetAt time.Time
	Count   int
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

func (a *App) withRequestLogging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		defer func() {
			if panicValue := recover(); panicValue != nil {
				log.Printf("panic method=%s path=%s remote=%s err=%v", r.Method, r.URL.Path, r.RemoteAddr, panicValue)
				http.Error(rec, "internal server error", http.StatusInternalServerError)
			}
			log.Printf("request method=%s path=%s status=%d duration_ms=%d remote=%s", r.Method, r.URL.Path, rec.status, time.Since(started).Milliseconds(), r.RemoteAddr)
		}()
		next.ServeHTTP(rec, r)
	})
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
		h.Set("Permissions-Policy", "camera=(self), microphone=(), geolocation=()")
		h.Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data:; style-src 'self' 'unsafe-inline'; script-src 'self'; connect-src 'self'; base-uri 'self'; form-action 'self'; frame-ancestors 'none'")
		next.ServeHTTP(w, r)
	})
}

func (a *App) healthz(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := a.db.Ping(); err != nil {
		http.Error(w, "database unavailable", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte("ok\n"))
}

func (a *App) render(w http.ResponseWriter, r *http.Request, name string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if page, ok := data.(map[string]any); ok {
		if user, authenticated := a.currentUser(r); authenticated {
			page["CurrentUser"] = user
		}
	}
	if err := a.templates.ExecuteTemplate(w, name, data); err != nil && !errors.Is(err, http.ErrHandlerTimeout) {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (a *App) allowRate(key string, limit int, window time.Duration) bool {
	if a.limiter == nil {
		return true
	}
	return a.limiter.allow(key, limit, window)
}

func (l *rateLimiter) allow(key string, limit int, window time.Duration) bool {
	if limit <= 0 || window <= 0 {
		return true
	}
	now := time.Now()
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.buckets) > 10000 {
		for key, bucket := range l.buckets {
			if !bucket.ResetAt.After(now) {
				delete(l.buckets, key)
			}
		}
	}
	bucket := l.buckets[key]
	if !bucket.ResetAt.After(now) {
		bucket = rateBucket{ResetAt: now.Add(window)}
	}
	if bucket.Count >= limit {
		l.buckets[key] = bucket
		return false
	}
	bucket.Count++
	l.buckets[key] = bucket
	return true
}

func clientKey(r *http.Request) string {
	for _, header := range []string{"CF-Connecting-IP", "X-Real-IP"} {
		if value := strings.TrimSpace(r.Header.Get(header)); value != "" {
			return value
		}
	}
	if forwarded := strings.TrimSpace(r.Header.Get("X-Forwarded-For")); forwarded != "" {
		if first := strings.TrimSpace(strings.Split(forwarded, ",")[0]); first != "" {
			return first
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil && host != "" {
		return host
	}
	if r.RemoteAddr != "" {
		return r.RemoteAddr
	}
	return "unknown"
}
