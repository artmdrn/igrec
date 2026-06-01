package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestActivityPubActorIncludesPublicKeyAndMedia(t *testing.T) {
	a := testApp(t)
	if _, err := a.db.CreateUser("cc00ffee", "cc@example.com"); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/ap/users/cc00ffee", nil)
	w := httptest.NewRecorder()
	a.actor(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, w.Code)
	}
	var actor map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &actor); err != nil {
		t.Fatal(err)
	}
	publicKey, ok := actor["publicKey"].(map[string]any)
	if !ok {
		t.Fatalf("expected publicKey in actor: %#v", actor)
	}
	if got, _ := publicKey["id"].(string); got != "http://localhost:8080/ap/users/cc00ffee#main-key" {
		t.Fatalf("unexpected public key id %q", got)
	}
	if pem, _ := publicKey["publicKeyPem"].(string); !strings.Contains(pem, "BEGIN PUBLIC KEY") {
		t.Fatalf("expected public key pem, got %q", pem)
	}
	if _, ok := actor["icon"].(map[string]any); !ok {
		t.Fatalf("expected icon in actor: %#v", actor)
	}
	if _, ok := actor["image"].(map[string]any); !ok {
		t.Fatalf("expected image in actor: %#v", actor)
	}
	if got, _ := actor["name"].(string); got != "cc00ffee · igrec" {
		t.Fatalf("unexpected actor name %q", got)
	}
	if got, _ := actor["summary"].(string); got == "" {
		t.Fatalf("expected actor summary")
	}
	if got, _ := actor["followers"].(string); got != "http://localhost:8080/ap/users/cc00ffee/followers" {
		t.Fatalf("unexpected followers URL %q", got)
	}
	if got, _ := actor["following"].(string); got != "http://localhost:8080/ap/users/cc00ffee/following" {
		t.Fatalf("unexpected following URL %q", got)
	}
}

func TestActivityPubFollowersCollection(t *testing.T) {
	a := testApp(t)
	user, err := a.db.CreateUser("cc00ffee", "cc@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if err := a.db.UpsertActivityPubFollower(user.ID, "https://example.social/users/a", "https://example.social/inbox"); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/ap/users/cc00ffee/followers", nil)
	w := httptest.NewRecorder()
	a.actor(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, w.Code)
	}
	if !strings.Contains(w.Body.String(), `"totalItems":1`) {
		t.Fatalf("expected one follower, got %s", w.Body.String())
	}
}

func TestActivityPubFollowingCollection(t *testing.T) {
	a := testApp(t)
	if _, err := a.db.CreateUser("cc00ffee", "cc@example.com"); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/ap/users/cc00ffee/following", nil)
	w := httptest.NewRecorder()
	a.actor(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, w.Code)
	}
	if !strings.Contains(w.Body.String(), `"totalItems":0`) {
		t.Fatalf("expected zero following, got %s", w.Body.String())
	}
}
