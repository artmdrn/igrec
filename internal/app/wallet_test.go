package app

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAppleWalletPassReportsUnconfigured(t *testing.T) {
	a := testApp(t)
	user, err := a.db.CreateUser("wallet", "wallet@example.com")
	if err != nil {
		t.Fatal(err)
	}
	sessionToken, sessionHash, err := newToken()
	if err != nil {
		t.Fatal(err)
	}
	if err := a.db.CreateSession(sessionHash, user.ID, farFuture()); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/wallet/apple.pkpass", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: sessionToken})
	w := httptest.NewRecorder()
	a.appleWalletPass(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected status %d, got %d", http.StatusServiceUnavailable, w.Code)
	}
}

func TestAppleWalletPassHeadRequiresLogin(t *testing.T) {
	a := testApp(t)
	req := httptest.NewRequest(http.MethodHead, "/wallet/apple.pkpass", nil)
	w := httptest.NewRecorder()
	a.appleWalletPass(w, req)

	if w.Code != http.StatusSeeOther {
		t.Fatalf("expected status %d, got %d", http.StatusSeeOther, w.Code)
	}
	if location := w.Header().Get("Location"); !strings.HasPrefix(location, "/login?next=") {
		t.Fatalf("expected login redirect, got %q", location)
	}
}

func TestSettingsShowsWalletSetupWhenApplePassMissing(t *testing.T) {
	a := testApp(t)
	user, err := a.db.CreateUser("wallet", "wallet@example.com")
	if err != nil {
		t.Fatal(err)
	}
	data := a.settingsData(user, nil)
	if ready, _ := data["AppleWalletReady"].(bool); ready {
		t.Fatal("expected apple wallet to be unavailable without signing config")
	}
	if data["AppleWalletURL"] != "/wallet/apple.pkpass" {
		t.Fatalf("unexpected wallet URL %#v", data["AppleWalletURL"])
	}
}

func TestApplePassJSONUsesLatestWordsAndWriteURL(t *testing.T) {
	a := testApp(t)
	a.cfg.ApplePass = ApplePassConfig{PassTypeID: "pass.net.igrec", TeamID: "TEAMID"}
	user, err := a.db.CreateUser("wallet", "wallet@example.com")
	if err != nil {
		t.Fatal(err)
	}
	friend, err := a.db.CreateUser("friend", "friend@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if err := a.db.CreateUserFollow(user.ID, friend.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := a.db.CreatePost(user.ID, "ember", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := a.db.CreatePost(friend.ID, "signal", nil); err != nil {
		t.Fatal(err)
	}
	data := walletPassData{User: user, WriteURL: "http://localhost:8080/write?source=wallet"}
	if post, err := a.db.LatestPostByUser(user.Username); err == nil {
		data.SelfPost.Valid = true
		data.SelfPost.V = post
	}
	data.FriendPosts, _ = a.db.FriendPosts(user.ID, 4)

	raw := a.applePassJSON(data)
	body := strings.TrimSpace(raw["description"].(string))
	if body != "igrec pocket word" {
		t.Fatalf("unexpected description %q", body)
	}
	generic := raw["generic"].(map[string]any)
	primary := generic["primaryFields"].([]any)[0].(map[string]string)
	if primary["value"] != "ember" {
		t.Fatalf("expected latest self word, got %#v", primary)
	}
	aux := generic["auxiliaryFields"].([]any)[0].(map[string]string)
	if !strings.Contains(aux["value"], "@friend: signal") {
		t.Fatalf("expected friend word in pass, got %#v", aux)
	}
}
