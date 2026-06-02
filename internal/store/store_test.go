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
