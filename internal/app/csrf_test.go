package app

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"html/template"

	"igrec.net/igrec/internal/store"
)

func testApp(t *testing.T) *App {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "igrec.db")
	db, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	tmpl, err := template.ParseGlob("../../web/templates/*.html")
	if err != nil {
		t.Fatal(err)
	}

	return &App{
		cfg: Config{BaseURL: "http://localhost:8080"},
		db:  db,
		templates: tmpl,
	}
}

func TestLoginPostRejectsMissingCSRF(t *testing.T) {
	a := testApp(t)
	req := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader("email=x%40example.com"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()

	a.login(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected status %d, got %d", http.StatusForbidden, w.Code)
	}
}

func TestLoginPostAcceptsValidCSRF(t *testing.T) {
	a := testApp(t)
	wGet := httptest.NewRecorder()
	reqGet := httptest.NewRequest(http.MethodGet, "/login", nil)
	a.login(wGet, reqGet)

	resp := wGet.Result()
	var csrf *http.Cookie
	for _, c := range resp.Cookies() {
		if c.Name == csrfCookie {
			csrf = c
			break
		}
	}
	if csrf == nil || csrf.Value == "" {
		t.Fatal("expected csrf cookie from GET /login")
	}

	form := url.Values{}
	form.Set("email", "missing@example.com")
	form.Set(csrfField, csrf.Value)
	reqPost := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(form.Encode()))
	reqPost.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	reqPost.AddCookie(csrf)
	wPost := httptest.NewRecorder()

	a.login(wPost, reqPost)

	if wPost.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, wPost.Code)
	}
}
