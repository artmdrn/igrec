package app

import (
	"bytes"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAPICreateWordWithToken(t *testing.T) {
	a := testApp(t)
	user, err := a.db.CreateUser("apiuser", "api@example.com")
	if err != nil {
		t.Fatal(err)
	}
	token, tokenHash, err := newToken()
	if err != nil {
		t.Fatal(err)
	}
	if err := a.db.CreateAPIToken(user.ID, tokenHash, token[:10], "test"); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(map[string]string{"word": "stillness"})
	req := httptest.NewRequest(http.MethodPost, "/api/words", bytes.NewReader(raw))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	a.api(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d: %s", http.StatusOK, w.Code, w.Body.String())
	}
	posts, err := a.db.PostsByUser(user.Username, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(posts) != 1 || posts[0].Word != "stillness" {
		t.Fatalf("expected api-created post, got %#v", posts)
	}
	var payload struct {
		Word struct {
			URL string `json:"url"`
		} `json:"word"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Word.URL != "http://localhost:8080/@apiuser/1-stillness" {
		t.Fatalf("expected canonical post url, got %q", payload.Word.URL)
	}
}

func TestAPICreateWordRejectsMissingToken(t *testing.T) {
	a := testApp(t)
	req := httptest.NewRequest(http.MethodPost, "/api/words", bytes.NewReader([]byte(`{"word":"nope"}`)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	a.api(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected status %d, got %d", http.StatusUnauthorized, w.Code)
	}
}

func TestAPICreateWordWithMultipartImage(t *testing.T) {
	a := testApp(t)
	a.cfg.UploadDir = t.TempDir()
	user, err := a.db.CreateUser("imageapi", "imageapi@example.com")
	if err != nil {
		t.Fatal(err)
	}
	token, tokenHash, err := newToken()
	if err != nil {
		t.Fatal(err)
	}
	if err := a.db.CreateAPIToken(user.ID, tokenHash, token[:10], "test"); err != nil {
		t.Fatal(err)
	}

	var imageData bytes.Buffer
	src := image.NewRGBA(image.Rect(0, 0, 80, 80))
	for y := 0; y < 80; y++ {
		for x := 0; x < 80; x++ {
			src.Set(x, y, color.RGBA{R: uint8(x), G: uint8(y), B: 180, A: 255})
		}
	}
	if err := png.Encode(&imageData, src); err != nil {
		t.Fatal(err)
	}

	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	if err := form.WriteField("word", "frame"); err != nil {
		t.Fatal(err)
	}
	if err := form.WriteField("focus_x", "0.25"); err != nil {
		t.Fatal(err)
	}
	if err := form.WriteField("focus_y", "0.75"); err != nil {
		t.Fatal(err)
	}
	part, err := form.CreateFormFile("image_file", "frame.png")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(imageData.Bytes()); err != nil {
		t.Fatal(err)
	}
	if err := form.Close(); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/words", &body)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", form.FormDataContentType())
	w := httptest.NewRecorder()

	a.api(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d: %s", http.StatusOK, w.Code, w.Body.String())
	}
	posts, err := a.db.PostsByUser(user.Username, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(posts) != 1 || posts[0].Word != "frame" {
		t.Fatalf("expected api-created post, got %#v", posts)
	}
	if !posts[0].ImageURL.Valid || !strings.HasPrefix(posts[0].ImageURL.String, "/uploads/") {
		t.Fatalf("expected stored upload URL, got %#v", posts[0].ImageURL)
	}
	if posts[0].FocusX != 0.25 || posts[0].FocusY != 0.75 {
		t.Fatalf("expected stored focus point, got %0.2f %0.2f", posts[0].FocusX, posts[0].FocusY)
	}
	storedPath := filepath.Join(a.cfg.UploadDir, strings.TrimPrefix(posts[0].ImageURL.String, "/uploads/"))
	if _, err := os.Stat(storedPath); err != nil {
		t.Fatalf("expected stored image at %s: %v", storedPath, err)
	}

	var payload struct {
		Word struct {
			ImageURL string `json:"image_url"`
		} `json:"word"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Word.ImageURL != posts[0].ImageURL.String {
		t.Fatalf("expected response image URL %q, got %q", posts[0].ImageURL.String, payload.Word.ImageURL)
	}
}
