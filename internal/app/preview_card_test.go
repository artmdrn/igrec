package app

import (
	"image/png"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPostPreviewCardServesPNG(t *testing.T) {
	a := testApp(t)
	user, err := a.db.CreateUser("mazine", "mazine@example.com")
	if err != nil {
		t.Fatal(err)
	}
	post, err := a.db.CreatePost(user.ID, "Лекторий", nil)
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, strings.TrimPrefix(previewCardURL("http://localhost:8080", post), "http://localhost:8080"), nil)
	w := httptest.NewRecorder()
	a.postPreviewCard(w, req)

	resp := w.Result()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, resp.StatusCode)
	}
	if got := resp.Header.Get("Content-Type"); got != "image/png" {
		t.Fatalf("expected image/png content type, got %q", got)
	}
	cfg, err := png.DecodeConfig(resp.Body)
	if err != nil {
		t.Fatalf("expected valid png: %v", err)
	}
	if cfg.Width != 1200 || cfg.Height != 630 {
		t.Fatalf("expected 1200x630 image, got %dx%d", cfg.Width, cfg.Height)
	}
}

func TestPostPageUsesGeneratedPreviewForImagelessPost(t *testing.T) {
	a := testApp(t)
	user, err := a.db.CreateUser("mazine", "mazine@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.db.CreatePost(user.ID, "Лекторий", nil); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/@mazine/1-%D0%9B%D0%B5%D0%BA%D1%82%D0%BE%D1%80%D0%B8%D0%B9", nil)
	w := httptest.NewRecorder()
	a.profile(w, req)

	body := w.Body.String()
	if !strings.Contains(body, `property="og:image" content="http://localhost:8080/og/@mazine/1-%D0%9B%D0%B5%D0%BA%D1%82%D0%BE%D1%80%D0%B8%D0%B9.png"`) {
		t.Fatalf("expected generated preview image meta tag, got %s", body)
	}
	if !strings.Contains(body, `property="og:image:type" content="image/png"`) {
		t.Fatalf("expected image/png OG type, got %s", body)
	}
}

func TestPostPageShowsWordEchoes(t *testing.T) {
	a := testApp(t)
	first, err := a.db.CreateUser("first", "first@example.com")
	if err != nil {
		t.Fatal(err)
	}
	second, err := a.db.CreateUser("second", "second@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.db.CreatePost(first.ID, "ember", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := a.db.CreatePost(second.ID, "ember", nil); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/@second/ember", nil)
	w := httptest.NewRecorder()
	a.profile(w, req)

	body := w.Body.String()
	if !strings.Contains(body, "echoes") || !strings.Contains(body, "@first") {
		t.Fatalf("expected echo from first user, got %s", body)
	}
}

func TestProfileLinksDuplicateWordsToDistinctCanonicalPosts(t *testing.T) {
	a := testApp(t)
	user, err := a.db.CreateUser("again", "again@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.db.CreatePost(user.ID, "ember", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := a.db.CreatePost(user.ID, "ember", nil); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/@again", nil)
	w := httptest.NewRecorder()
	a.profile(w, req)

	body := w.Body.String()
	if !strings.Contains(body, `href="http://localhost:8080/@again/1-ember"`) {
		t.Fatalf("expected link for first duplicate post, got %s", body)
	}
	if !strings.Contains(body, `href="http://localhost:8080/@again/2-ember"`) {
		t.Fatalf("expected link for second duplicate post, got %s", body)
	}
}

func TestCanonicalDuplicatePostPathShowsOlderPost(t *testing.T) {
	a := testApp(t)
	user, err := a.db.CreateUser("again", "again@example.com")
	if err != nil {
		t.Fatal(err)
	}
	first, err := a.db.CreatePost(user.ID, "ember", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.db.CreatePost(user.ID, "ember", nil); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/@again/1-ember", nil)
	w := httptest.NewRecorder()
	a.profile(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, w.Code)
	}
	if !strings.Contains(w.Body.String(), "ember") {
		t.Fatalf("expected canonical older post to render, got %s", w.Body.String())
	}
	if first.ID != 1 {
		t.Fatalf("expected deterministic first post id, got %d", first.ID)
	}
}

func TestBadgeServesLatestWordSVG(t *testing.T) {
	a := testApp(t)
	user, err := a.db.CreateUser("badge", "badge@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.db.CreatePost(user.ID, "signal", nil); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/@badge/badge.svg", nil)
	w := httptest.NewRecorder()
	a.profile(w, req)

	resp := w.Result()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, resp.StatusCode)
	}
	if got := resp.Header.Get("Content-Type"); got != "image/svg+xml; charset=utf-8" {
		t.Fatalf("expected svg content type, got %q", got)
	}
	if body := w.Body.String(); !strings.Contains(body, "signal") || !strings.Contains(body, "@badge") {
		t.Fatalf("expected badge svg to include latest word and user, got %s", body)
	}
}

func TestProfileShowsRelMeLinks(t *testing.T) {
	a := testApp(t)
	user, err := a.db.CreateUser("links", "links@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if err := a.db.ReplaceRelMeLinks(user.ID, []string{
		"https://github.com/links",
		"https://social.example/@links",
	}); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/@links", nil)
	w := httptest.NewRecorder()
	a.profile(w, req)

	body := w.Body.String()
	if !strings.Contains(body, `rel="me" href="https://github.com/links"`) {
		t.Fatalf("expected github rel=me link, got %s", body)
	}
	if !strings.Contains(body, `rel="me" href="https://social.example/@links"`) {
		t.Fatalf("expected social rel=me link, got %s", body)
	}
}
