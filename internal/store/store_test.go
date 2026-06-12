package store

import (
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func testDB(t *testing.T) *DB {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "igrec.db")
	db, err := Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestDeleteUserRemovesDependentRecords(t *testing.T) {
	db := testDB(t)

	inviter, err := db.CreateUser("inviter", "inviter@example.com")
	if err != nil {
		t.Fatal(err)
	}
	user, err := db.CreateUser("delete_me", "delete@example.com")
	if err != nil {
		t.Fatal(err)
	}
	friend, err := db.CreateUser("friend", "friend@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.CreateInviteForUser("invite-a", user.ID); err != nil {
		t.Fatal(err)
	}
	if err := db.CreateInviteForUser("invite-b", inviter.ID); err != nil {
		t.Fatal(err)
	}
	if err := db.UseInvite("invite-b", user.ID); err != nil {
		t.Fatal(err)
	}
	if err := db.CreateUserFollow(user.ID, friend.ID); err != nil {
		t.Fatal(err)
	}
	if err := db.CreateUserFollow(friend.ID, user.ID); err != nil {
		t.Fatal(err)
	}
	if err := db.CreateSession("session-hash", user.ID, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := db.CreateLoginToken("login-hash", user.ID, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := db.CreateEmailChangeToken("email-hash", user.ID, "next@example.com", time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := db.SetEmailOptIn(user.ID, true); err != nil {
		t.Fatal(err)
	}
	if _, err := db.UnsubscribeTokenForUser(user.ID, func() (string, error) { return "u-token", nil }); err != nil {
		t.Fatal(err)
	}
	if err := db.MarkDailyEmailSent(user.ID, "2026-06-02"); err != nil {
		t.Fatal(err)
	}
	if err := db.CreateActivityPubKey(user.ID, "private", "public"); err != nil {
		t.Fatal(err)
	}
	if err := db.CreateAPIToken(user.ID, "api-hash", "api-prefix", "cli"); err != nil {
		t.Fatal(err)
	}
	if err := db.UpsertPushSubscription(user.ID, "https://push.example/sub-a", "p256dh-a", "auth-a"); err != nil {
		t.Fatal(err)
	}
	if err := db.ReplaceRelMeLinks(user.ID, []string{"https://example.com/@delete_me"}); err != nil {
		t.Fatal(err)
	}
	if err := db.UpsertActivityPubFollower(user.ID, "https://remote.example/@follower", "https://remote.example/inbox"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.CreatePost(user.ID, "ember", nil); err != nil {
		t.Fatal(err)
	}

	if err := db.DeleteUser(user.ID); err != nil {
		t.Fatal(err)
	}

	if _, err := db.UserByID(user.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("expected deleted user lookup to fail with sql.ErrNoRows, got %v", err)
	}
	if follows, err := db.UserFriends(friend.ID); err != nil || len(follows) != 0 {
		t.Fatalf("expected no remaining friend edges, got %d err=%v", len(follows), err)
	}
	if subscriptions, err := db.PushSubscriptionsByUser(user.ID); err != nil || len(subscriptions) != 0 {
		t.Fatalf("expected no remaining push subscriptions, got %d err=%v", len(subscriptions), err)
	}
	if posts, err := db.Firehose(10); err != nil || len(posts) != 0 {
		t.Fatalf("expected no remaining posts, got %d err=%v", len(posts), err)
	}
	if _, err := db.InviteByCode("invite-a"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("expected inviter-owned invite to be deleted, got %v", err)
	}
	invite, err := db.InviteByCode("invite-b")
	if err != nil {
		t.Fatalf("expected redeemed invite record to remain, got %v", err)
	}
	if invite.UsedBy.Valid {
		t.Fatalf("expected redeemed invite to be detached from deleted user, got used_by=%d", invite.UsedBy.Int64)
	}
}

func TestAPITokenLifecycle(t *testing.T) {
	db := testDB(t)
	user, err := db.CreateUser("apiuser", "api@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.CreateAPIToken(user.ID, "hash", "prefix", "cli"); err != nil {
		t.Fatal(err)
	}
	tokens, err := db.APITokensByUser(user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(tokens) != 1 || tokens[0].Name != "cli" || tokens[0].Prefix != "prefix" {
		t.Fatalf("unexpected tokens %#v", tokens)
	}
	found, err := db.UserByAPITokenHash("hash")
	if err != nil {
		t.Fatal(err)
	}
	if found.ID != user.ID {
		t.Fatalf("expected user %d, got %d", user.ID, found.ID)
	}
	if err := db.DeleteAPIToken(user.ID, tokens[0].ID); err != nil {
		t.Fatal(err)
	}
	tokens, err = db.APITokensByUser(user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(tokens) != 0 {
		t.Fatalf("expected token deleted, got %#v", tokens)
	}
}

func TestPushSubscriptionLifecycle(t *testing.T) {
	db := testDB(t)
	first, err := db.CreateUser("pushone", "pushone@example.com")
	if err != nil {
		t.Fatal(err)
	}
	second, err := db.CreateUser("pushtwo", "pushtwo@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.UpsertPushSubscription(first.ID, "https://push.example/sub", "p256dh-a", "auth-a"); err != nil {
		t.Fatal(err)
	}
	if err := db.UpsertPushSubscription(first.ID, "https://push.example/sub-2", "p256dh-b", "auth-b"); err != nil {
		t.Fatal(err)
	}
	if count, err := db.PushSubscriptionCountByUser(first.ID); err != nil || count != 2 {
		t.Fatalf("expected 2 subscriptions, got %d err=%v", count, err)
	}
	if err := db.UpsertPushSubscription(second.ID, "https://push.example/sub", "p256dh-next", "auth-next"); err != nil {
		t.Fatal(err)
	}
	firstSubscriptions, err := db.PushSubscriptionsByUser(first.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(firstSubscriptions) != 1 || firstSubscriptions[0].Endpoint != "https://push.example/sub-2" {
		t.Fatalf("expected one remaining subscription for first user, got %#v", firstSubscriptions)
	}
	secondSubscriptions, err := db.PushSubscriptionsByUser(second.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(secondSubscriptions) != 1 || secondSubscriptions[0].P256DH != "p256dh-next" || secondSubscriptions[0].Auth != "auth-next" {
		t.Fatalf("expected moved subscription for second user, got %#v", secondSubscriptions)
	}
	if err := db.DeletePushSubscription(second.ID, "https://push.example/sub"); err != nil {
		t.Fatal(err)
	}
	if count, err := db.PushSubscriptionCountByUser(second.ID); err != nil || count != 0 {
		t.Fatalf("expected 0 subscriptions after delete, got %d err=%v", count, err)
	}
}

func TestRelMeLinksRoundTrip(t *testing.T) {
	db := testDB(t)
	user, err := db.CreateUser("links", "links@example.com")
	if err != nil {
		t.Fatal(err)
	}
	links := []string{
		"https://github.com/links",
		"https://mastodon.example/@links",
	}
	if err := db.ReplaceRelMeLinks(user.ID, links); err != nil {
		t.Fatal(err)
	}
	got, err := db.RelMeLinksByUser(user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(links) {
		t.Fatalf("expected %d links, got %#v", len(links), got)
	}
	for i := range links {
		if got[i] != links[i] {
			t.Fatalf("expected link %d to be %q, got %q", i, links[i], got[i])
		}
	}
	if err := db.ReplaceRelMeLinks(user.ID, []string{"https://example.com/~links"}); err != nil {
		t.Fatal(err)
	}
	got, err = db.RelMeLinksByUser(user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != "https://example.com/~links" {
		t.Fatalf("expected replacement links, got %#v", got)
	}
}

func TestCreatePostWithFocusStoresClampedFocus(t *testing.T) {
	db := testDB(t)
	user, err := db.CreateUser("photo", "photo@example.com")
	if err != nil {
		t.Fatal(err)
	}
	imageURL := "/uploads/photo.jpg"
	post, err := db.CreatePostWithFocus(user.ID, "frame", &imageURL, 1.4, -0.2)
	if err != nil {
		t.Fatal(err)
	}
	if post.FocusX != 1 || post.FocusY != 0 {
		t.Fatalf("expected clamped focus 1,0 got %.2f,%.2f", post.FocusX, post.FocusY)
	}
	found, err := db.PostByID(post.ID)
	if err != nil {
		t.Fatal(err)
	}
	if found.FocusX != 1 || found.FocusY != 0 {
		t.Fatalf("expected stored focus 1,0 got %.2f,%.2f", found.FocusX, found.FocusY)
	}
}

func TestInviterByUserIDFindsRedeemedInviteOwner(t *testing.T) {
	db := testDB(t)
	inviter, err := db.CreateUser("maker", "maker@example.com")
	if err != nil {
		t.Fatal(err)
	}
	user, err := db.CreateUser("made", "made@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.CreateInviteForUser("invite-maker", inviter.ID); err != nil {
		t.Fatal(err)
	}
	if err := db.UseInvite("invite-maker", user.ID); err != nil {
		t.Fatal(err)
	}
	found, err := db.InviterByUserID(user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if found.ID != inviter.ID {
		t.Fatalf("expected inviter %d, got %d", inviter.ID, found.ID)
	}
}

func TestPostsByWordExcludesCurrentPost(t *testing.T) {
	db := testDB(t)
	first, err := db.CreateUser("first", "first@example.com")
	if err != nil {
		t.Fatal(err)
	}
	second, err := db.CreateUser("second", "second@example.com")
	if err != nil {
		t.Fatal(err)
	}
	current, err := db.CreatePost(first.ID, "echo", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.CreatePost(second.ID, "echo", nil); err != nil {
		t.Fatal(err)
	}
	posts, err := db.PostsByWord("echo", current.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(posts) != 1 || posts[0].Username != "second" {
		t.Fatalf("unexpected echoes %#v", posts)
	}
}

func TestActivityPubDeliveryLifecycle(t *testing.T) {
	db := testDB(t)
	user, err := db.CreateUser("fed", "fed@example.com")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := db.EnqueueActivityPubDelivery(user.ID, "https://remote.example/inbox", []byte(`{"type":"Create"}`), now.Add(-time.Minute), "boom"); err != nil {
		t.Fatal(err)
	}
	deliveries, err := db.DueActivityPubDeliveries(now, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(deliveries) != 1 {
		t.Fatalf("expected one due delivery, got %#v", deliveries)
	}
	if string(deliveries[0].Activity) != `{"type":"Create"}` || deliveries[0].LastError != "boom" {
		t.Fatalf("unexpected delivery %#v", deliveries[0])
	}
	if err := db.MarkActivityPubDeliveryFailed(deliveries[0].ID, 1, now.Add(time.Hour), "still down"); err != nil {
		t.Fatal(err)
	}
	deliveries, err = db.DueActivityPubDeliveries(now, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(deliveries) != 0 {
		t.Fatalf("expected no due deliveries after backoff, got %#v", deliveries)
	}
	deliveries, err = db.DueActivityPubDeliveries(now.Add(2*time.Hour), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(deliveries) != 1 || deliveries[0].Attempts != 1 || deliveries[0].LastError != "still down" {
		t.Fatalf("unexpected failed delivery state %#v", deliveries)
	}
	if err := db.MarkActivityPubDeliveryDelivered(deliveries[0].ID); err != nil {
		t.Fatal(err)
	}
	deliveries, err = db.DueActivityPubDeliveries(now.Add(2*time.Hour), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(deliveries) != 0 {
		t.Fatalf("expected delivered row excluded, got %#v", deliveries)
	}
}
