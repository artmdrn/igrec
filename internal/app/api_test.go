package app

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
