package main

import (
	"crypto/ecdh"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	webpush "github.com/SherClockHolmes/webpush-go"

	"igrec.net/igrec/internal/app"
	"igrec.net/igrec/internal/store"
)

func testDailyDB(t *testing.T) *store.DB {
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
	return db
}

func TestSendDailyPushesMarksSentAndPrunesStaleSubscriptions(t *testing.T) {
	db := testDailyDB(t)
	author, err := db.CreateUser("author", "author@example.com")
	if err != nil {
		t.Fatal(err)
	}
	reader, err := db.CreateUser("reader", "reader@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.CreateUserFollow(reader.ID, author.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.CreatePost(author.ID, "ember", nil); err != nil {
		t.Fatal(err)
	}

	keys := mustSubscriptionKeys(t)
	if err := db.UpsertPushSubscription(reader.ID, "https://push.example/ok", keys.p256dh, keys.auth); err != nil {
		t.Fatal(err)
	}
	if err := db.UpsertPushSubscription(reader.ID, "https://push.example/stale", keys.p256dh, keys.auth); err != nil {
		t.Fatal(err)
	}

	vapidPrivate, vapidPublic, err := webpush.GenerateVAPIDKeys()
	if err != nil {
		t.Fatal(err)
	}
	cfg := app.Config{
		BaseURL:        "https://igrec.net",
		DailyEmailFrom: "Y <_@igrec.net>",
		VAPIDPublic:    vapidPublic,
		VAPIDPrivate:   vapidPrivate,
	}

	originalSend := sendWebPushNotification
	t.Cleanup(func() { sendWebPushNotification = originalSend })
	staleEndpoint := ""
	goodRequests := 0
	sendWebPushNotification = func(message []byte, subscription *webpush.Subscription, options *webpush.Options) (*http.Response, error) {
		var payload map[string]any
		if err := json.Unmarshal(message, &payload); err != nil {
			t.Fatalf("expected JSON payload, got %v", err)
		}
		if got := payload["url"]; got != app.NotificationURL {
			t.Fatalf("expected write target, got %#v", payload)
		}
		if options.Topic != "daily-prompt" {
			t.Fatalf("expected Topic header, got %q", options.Topic)
		}
		if options.TTL != 3600 {
			t.Fatalf("expected TTL 3600, got %d", options.TTL)
		}
		if options.Subscriber != "_@igrec.net" {
			t.Fatalf("expected mail subscriber, got %q", options.Subscriber)
		}
		if subscription.Endpoint == "https://push.example/stale" {
			staleEndpoint = subscription.Endpoint
			return &http.Response{
				StatusCode: http.StatusGone,
				Status:     "410 Gone",
				Body:       io.NopCloser(strings.NewReader("gone")),
			}, nil
		}
		goodRequests++
		return &http.Response{
			StatusCode: http.StatusCreated,
			Status:     "201 Created",
			Body:       io.NopCloser(strings.NewReader("ok")),
		}, nil
	}

	sentUsers, sentSubscriptions, err := sendDailyPushes(cfg, db, nil)
	if err != nil {
		t.Fatal(err)
	}
	if sentUsers != 1 || sentSubscriptions != 1 {
		t.Fatalf("expected 1 pushed user and 1 live delivery, got users=%d deliveries=%d", sentUsers, sentSubscriptions)
	}
	if goodRequests != 1 {
		t.Fatalf("expected one successful push request, got %d", goodRequests)
	}
	subscriptions, err := db.PushSubscriptionsByUser(reader.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(subscriptions) != 1 || subscriptions[0].Endpoint != "https://push.example/ok" {
		t.Fatalf("expected stale subscription pruned, got %#v", subscriptions)
	}
	candidates, err := db.DailyPushCandidates("9999-12-31", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 1 || candidates[0].SentCount != 1 {
		t.Fatalf("expected total send count recorded, got %#v", candidates)
	}
	if staleEndpoint != "https://push.example/stale" {
		t.Fatalf("expected stale endpoint hit, got %q", staleEndpoint)
	}
}

func TestDailyPushPayloadIncludesWriteTarget(t *testing.T) {
	payload, err := dailyPushPayload(store.DailyPushCandidate{
		Post: sql.Null[store.Post]{
			V:     store.Post{Username: "author", Word: "ember"},
			Valid: true,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(payload, &got); err != nil {
		t.Fatal(err)
	}
	if got["title"] != "igrec" || got["url"] != app.NotificationURL {
		t.Fatalf("unexpected payload %#v", got)
	}
	if got["body"] != "@author said: ember\nreply with one word." {
		t.Fatalf("unexpected body %#v", got["body"])
	}
}

type subscriptionKeys struct {
	p256dh string
	auth   string
}

func mustSubscriptionKeys(t *testing.T) subscriptionKeys {
	t.Helper()
	privateKey, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	auth := make([]byte, 16)
	if _, err := rand.Read(auth); err != nil {
		t.Fatal(err)
	}
	return subscriptionKeys{
		p256dh: base64.RawURLEncoding.EncodeToString(privateKey.PublicKey().Bytes()),
		auth:   base64.RawURLEncoding.EncodeToString(auth),
	}
}
