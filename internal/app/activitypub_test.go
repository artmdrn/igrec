package app

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return fn(req)
}

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

func TestStartAccountMigrationQueuesMoveAndRedirectsActor(t *testing.T) {
	a := testApp(t)
	user, err := a.db.CreateUser("member", "member@example.com")
	if err != nil {
		t.Fatal(err)
	}
	target := "https://remote.example/users/member"
	if err := a.db.UpdateSettings(user.ID, "smart", false, "", target); err != nil {
		t.Fatal(err)
	}
	user, err = a.db.UserByID(user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.db.UpsertActivityPubFollower(user.ID, "https://follower.example/users/a", "https://follower.example/inbox"); err != nil {
		t.Fatal(err)
	}

	oldClient := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		body := `{"id":"` + target + `","type":"Person","alsoKnownAs":["http://localhost:8080/ap/users/member"]}`
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: req}, nil
	})}
	t.Cleanup(func() { http.DefaultClient = oldClient })

	if err := a.startAccountMigration(t.Context(), user); err != nil {
		t.Fatal(err)
	}
	migration, err := a.db.AccountMigrationByUser(user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if migration.TargetActor != target {
		t.Fatalf("expected migration target %q, got %q", target, migration.TargetActor)
	}
	deliveries, err := a.db.DueActivityPubDeliveries(farFuture(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(deliveries) != 1 || deliveries[0].Inbox != "https://follower.example/inbox" {
		t.Fatalf("expected one queued follower delivery, got %#v", deliveries)
	}
	var move map[string]any
	if err := json.Unmarshal(deliveries[0].Activity, &move); err != nil {
		t.Fatal(err)
	}
	if move["type"] != "Move" || move["object"] != "http://localhost:8080/ap/users/member" || move["target"] != target {
		t.Fatalf("unexpected Move activity: %#v", move)
	}

	req := httptest.NewRequest(http.MethodGet, "/ap/users/member", nil)
	w := httptest.NewRecorder()
	a.actor(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, w.Code)
	}
	var actor map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &actor); err != nil {
		t.Fatal(err)
	}
	if actor["movedTo"] != target || actor["discoverable"] != false {
		t.Fatalf("expected migrated actor, got %#v", actor)
	}
}

func TestStartAccountMigrationRequiresAlias(t *testing.T) {
	a := testApp(t)
	user, err := a.db.CreateUser("member", "member@example.com")
	if err != nil {
		t.Fatal(err)
	}
	target := "https://remote.example/users/member"
	if err := a.db.UpdateSettings(user.ID, "smart", false, "", target); err != nil {
		t.Fatal(err)
	}
	user, err = a.db.UserByID(user.ID)
	if err != nil {
		t.Fatal(err)
	}

	oldClient := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		body := `{"id":"` + target + `","type":"Person"}`
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: req}, nil
	})}
	t.Cleanup(func() { http.DefaultClient = oldClient })

	err = a.startAccountMigration(t.Context(), user)
	if err == nil || !strings.Contains(err.Error(), "must first list this igrec account as an alias") {
		t.Fatalf("expected alias verification error, got %v", err)
	}
	if _, err := a.db.AccountMigrationByUser(user.ID); err == nil {
		t.Fatal("expected migration not to start")
	}
}
