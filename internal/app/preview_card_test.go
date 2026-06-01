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
	if _, err := a.db.CreatePost(user.ID, "Лекторий", nil); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/og/@mazine/%D0%9B%D0%B5%D0%BA%D1%82%D0%BE%D1%80%D0%B8%D0%B9.png", nil)
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

	req := httptest.NewRequest(http.MethodGet, "/@mazine/%D0%9B%D0%B5%D0%BA%D1%82%D0%BE%D1%80%D0%B8%D0%B9", nil)
	w := httptest.NewRecorder()
	a.profile(w, req)

	body := w.Body.String()
	if !strings.Contains(body, `property="og:image" content="http://localhost:8080/og/@mazine/%D0%9B%D0%B5%D0%BA%D1%82%D0%BE%D1%80%D0%B8%D0%B9.png"`) {
		t.Fatalf("expected generated preview image meta tag, got %s", body)
	}
	if !strings.Contains(body, `property="og:image:type" content="image/png"`) {
		t.Fatalf("expected image/png OG type, got %s", body)
	}
}
