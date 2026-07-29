package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/go-webauthn/webauthn/webauthn"
	"igrec.net/igrec/internal/word"
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
	if err := db.SavePasskey(user.ID, "phone", webauthn.Credential{ID: []byte("delete-passkey"), PublicKey: []byte("public")}); err != nil {
		t.Fatal(err)
	}
	if err := db.CreateWebAuthnSession("webauthn-session", sql.NullInt64{Int64: user.ID, Valid: true}, "register", []byte(`{"challenge":"delete"}`), time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := db.LinkAuthIdentity(user.ID, "indieauth_domain", "delete.example"); err != nil {
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
	if count, err := db.PasskeyCount(user.ID); err != nil || count != 0 {
		t.Fatalf("expected passkeys to be deleted, got count=%d err=%v", count, err)
	}
	if _, err := db.UseWebAuthnSession("webauthn-session", "register"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("expected webauthn sessions to be deleted, got %v", err)
	}
	invite, err := db.InviteByCode("invite-b")
	if err != nil {
		t.Fatalf("expected redeemed invite record to remain, got %v", err)
	}
	if invite.UsedBy.Valid {
		t.Fatalf("expected redeemed invite to be detached from deleted user, got used_by=%d", invite.UsedBy.Int64)
	}
}

func TestPasskeyCredentialLifecycle(t *testing.T) {
	db := testDB(t)
	user, err := db.CreateUser("passkeyuser", "passkey@example.com")
	if err != nil {
		t.Fatal(err)
	}
	credential := webauthn.Credential{ID: []byte("credential-id"), PublicKey: []byte("public-key")}
	if err := db.SavePasskey(user.ID, "  laptop  ", credential); err != nil {
		t.Fatal(err)
	}

	credentials, err := db.PasskeyCredentialsByUser(user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(credentials) != 1 || string(credentials[0].ID) != "credential-id" || string(credentials[0].PublicKey) != "public-key" {
		t.Fatalf("unexpected credentials %#v", credentials)
	}
	if count, err := db.PasskeyCount(user.ID); err != nil || count != 1 {
		t.Fatalf("expected 1 passkey, got %d err=%v", count, err)
	}
	found, err := db.UserByPasskeyID([]byte("credential-id"))
	if err != nil {
		t.Fatal(err)
	}
	if found.ID != user.ID {
		t.Fatalf("expected user %d, got %d", user.ID, found.ID)
	}

	credential.PublicKey = []byte("rotated-public-key")
	if err := db.UpdatePasskeyCredential(credential); err != nil {
		t.Fatal(err)
	}
	credentials, err = db.PasskeyCredentialsByUser(user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if string(credentials[0].PublicKey) != "rotated-public-key" {
		t.Fatalf("expected updated credential, got %#v", credentials[0])
	}

	if err := db.SuspendUser(user.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.UserByPasskeyID([]byte("credential-id")); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("expected suspended passkey lookup to fail, got %v", err)
	}
}

func TestWebAuthnSessionLifecycle(t *testing.T) {
	db := testDB(t)
	user, err := db.CreateUser("webauthnuser", "webauthn@example.com")
	if err != nil {
		t.Fatal(err)
	}
	session := webauthn.SessionData{
		Challenge:      "challenge",
		RelyingPartyID: "igrec.example",
		UserID:         []byte("user-handle"),
		Expires:        time.Now().Add(5 * time.Minute).UTC(),
	}
	raw, err := json.Marshal(session)
	if err != nil {
		t.Fatal(err)
	}

	if err := db.CreateWebAuthnSession("session", sql.NullInt64{Int64: user.ID, Valid: true}, "register", raw, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.UseWebAuthnSession("session", "login"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("expected wrong-kind session lookup to fail, got %v", err)
	}
	record, err := db.UseWebAuthnSession("session", "register")
	if err != nil {
		t.Fatal(err)
	}
	if !record.UserID.Valid || record.UserID.Int64 != user.ID {
		t.Fatalf("unexpected session user %#v", record.UserID)
	}
	var restored webauthn.SessionData
	if err := json.Unmarshal(record.Data, &restored); err != nil {
		t.Fatal(err)
	}
	if restored.Challenge != session.Challenge || restored.RelyingPartyID != session.RelyingPartyID || string(restored.UserID) != string(session.UserID) {
		t.Fatalf("unexpected restored session %#v", restored)
	}
	if _, err := db.UseWebAuthnSession("session", "register"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("expected consumed session lookup to fail, got %v", err)
	}

	if err := db.CreateWebAuthnSession("expired", sql.NullInt64{}, "login", []byte(`{"challenge":"expired"}`), time.Now().Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.UseWebAuthnSession("expired", "login"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("expected expired session lookup to fail, got %v", err)
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

func TestDailyPushCandidatesFollowSubscriptionsAndDayLedger(t *testing.T) {
	db := testDB(t)
	author, err := db.CreateUser("author", "author@example.com")
	if err != nil {
		t.Fatal(err)
	}
	reader, err := db.CreateUser("reader", "reader@example.com")
	if err != nil {
		t.Fatal(err)
	}
	idle, err := db.CreateUser("idle", "idle@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.UpsertPushSubscription(reader.ID, "https://push.example/reader", "p256dh-reader", "auth-reader"); err != nil {
		t.Fatal(err)
	}
	if err := db.CreateUserFollow(reader.ID, author.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.CreatePost(author.ID, "ember", nil); err != nil {
		t.Fatal(err)
	}

	candidates, err := db.DailyPushCandidates("2026-06-17", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 1 {
		t.Fatalf("expected one push candidate, got %#v", candidates)
	}
	if candidates[0].User.ID != reader.ID {
		t.Fatalf("expected reader candidate, got %#v", candidates[0].User)
	}
	if !candidates[0].Post.Valid || candidates[0].Post.V.Word != "ember" || candidates[0].Post.V.Username != "author" {
		t.Fatalf("expected followed post in push candidate, got %#v", candidates[0].Post)
	}
	if err := db.MarkDailyPushSent(reader.ID, "2026-06-17"); err != nil {
		t.Fatal(err)
	}
	candidates, err = db.DailyPushCandidates("2026-06-17", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 0 {
		t.Fatalf("expected no same-day push candidates after send, got %#v", candidates)
	}
	if subs, err := db.PushSubscriptionsByUser(idle.ID); err != nil || len(subs) != 0 {
		t.Fatalf("expected idle user to remain unsubscribed, got %#v err=%v", subs, err)
	}
}

func TestInactiveUserPostsAreQuietAndSkippedByFriendFeeds(t *testing.T) {
	db := testDB(t)
	quiet, err := db.CreateUser("quiet", "quiet@example.com")
	if err != nil {
		t.Fatal(err)
	}
	active, err := db.CreateUser("active", "active@example.com")
	if err != nil {
		t.Fatal(err)
	}
	reader, err := db.CreateUser("reader", "reader@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.CreateUserFollow(reader.ID, quiet.ID); err != nil {
		t.Fatal(err)
	}
	if err := db.CreateUserFollow(reader.ID, active.ID); err != nil {
		t.Fatal(err)
	}
	staleAt := time.Now().UTC().AddDate(-1, 0, -2).Format("2006-01-02 15:04:05")
	recentAt := time.Now().UTC().AddDate(0, 0, -2).Format("2006-01-02 15:04:05")
	if _, err := db.Exec(`insert into posts (user_id, word, created_at) values (?, ?, ?)`, quiet.ID, "ember", staleAt); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`insert into posts (user_id, word, created_at) values (?, ?, ?)`, active.ID, "signal", recentAt); err != nil {
		t.Fatal(err)
	}

	posts, err := db.Firehose(10)
	if err != nil {
		t.Fatal(err)
	}
	quietByUsername := map[string]bool{}
	for _, post := range posts {
		quietByUsername[post.Username] = post.Quiet
	}
	if !quietByUsername["quiet"] {
		t.Fatalf("expected stale user's post to be quiet: %#v", quietByUsername)
	}
	if quietByUsername["active"] {
		t.Fatalf("expected recent user's post to stay active: %#v", quietByUsername)
	}

	friendPosts, err := db.FriendPosts(reader.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(friendPosts) != 1 || friendPosts[0].Username != "active" {
		t.Fatalf("expected only active followed posts, got %#v", friendPosts)
	}
}

func TestDailyEmailCandidatesSkipInactiveRecipients(t *testing.T) {
	db := testDB(t)
	quiet, err := db.CreateUser("quietmail", "quietmail@example.com")
	if err != nil {
		t.Fatal(err)
	}
	newUser, err := db.CreateUser("newmail", "newmail@example.com")
	if err != nil {
		t.Fatal(err)
	}
	for _, userID := range []int64{quiet.ID, newUser.ID} {
		if err := db.SetEmailOptIn(userID, true); err != nil {
			t.Fatal(err)
		}
	}
	staleAt := time.Now().UTC().AddDate(-1, 0, -2).Format("2006-01-02 15:04:05")
	if _, err := db.Exec(`insert into posts (user_id, word, created_at) values (?, ?, ?)`, quiet.ID, "ember", staleAt); err != nil {
		t.Fatal(err)
	}

	candidates, err := db.DailyEmailCandidates(dayKeyForTest(time.Now()), 10)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, candidate := range candidates {
		seen[candidate.User.Username] = true
	}
	if seen["quietmail"] {
		t.Fatalf("expected inactive recipient to be skipped: %#v", seen)
	}
	if !seen["newmail"] {
		t.Fatalf("expected unstarted recipient to remain eligible: %#v", seen)
	}
}

func dayKeyForTest(t time.Time) string {
	return t.Format("2006-01-02")
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

func TestUserByDomainFindsSingleLinkedAccount(t *testing.T) {
	db := testDB(t)
	user, err := db.CreateUser("domainuser", "domain@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`update users set domain = ? where id = ?`, "Example.COM", user.ID); err != nil {
		t.Fatal(err)
	}
	found, err := db.UserByDomain("example.com")
	if err != nil {
		t.Fatal(err)
	}
	if found.ID != user.ID {
		t.Fatalf("expected user %d, got %d", user.ID, found.ID)
	}
	if _, err := db.UserByDomain("missing.example"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("expected missing domain to return sql.ErrNoRows, got %v", err)
	}
}

func TestAuthIdentitiesLinkMultipleProviders(t *testing.T) {
	db := testDB(t)
	user, err := db.CreateUser("linked", "linked@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.LinkAuthIdentity(user.ID, "IndieAuth_Domain", "Example.COM"); err != nil {
		t.Fatal(err)
	}
	if err := db.LinkAuthIdentity(user.ID, "mastodon", "alice@mastodon.example"); err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		provider string
		subject  string
	}{
		{"email", "LINKED@EXAMPLE.COM"},
		{"indieauth_domain", "example.com"},
		{"mastodon", "ALICE@MASTODON.EXAMPLE"},
	} {
		found, err := db.UserByAuthIdentity(tt.provider, tt.subject)
		if err != nil {
			t.Fatalf("lookup %s/%s: %v", tt.provider, tt.subject, err)
		}
		if found.ID != user.ID {
			t.Fatalf("expected user %d, got %d", user.ID, found.ID)
		}
	}

	other, err := db.CreateUser("otherlinked", "otherlinked@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.LinkAuthIdentity(other.ID, "mastodon", "alice@mastodon.example"); err == nil {
		t.Fatal("expected linked identity conflict")
	}
	found, err := db.UserByAuthIdentity("mastodon", "alice@mastodon.example")
	if err != nil {
		t.Fatal(err)
	}
	if found.ID != user.ID {
		t.Fatalf("expected identity to remain linked to %d, got %d", user.ID, found.ID)
	}
}

func TestUserByFediverseAcctRequiresSingleLinkedAccount(t *testing.T) {
	db := testDB(t)
	user, err := db.CreateUser("fediverseuser", "fediverse@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.UpdateSettingsProfile(user.ID, "smart", true, "@alice@mastodon.example", "", nil); err != nil {
		t.Fatal(err)
	}
	found, err := db.UserByFediverseAcct("@ALICE@MASTODON.EXAMPLE")
	if err != nil {
		t.Fatal(err)
	}
	if found.ID != user.ID {
		t.Fatalf("expected user %d, got %d", user.ID, found.ID)
	}
	if _, err := db.UserByFediverseAcct("@missing@mastodon.example"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("expected missing fediverse account to return sql.ErrNoRows, got %v", err)
	}

	other, err := db.CreateUser("fediverseother", "fediverseother@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.UpdateSettingsProfile(other.ID, "smart", true, "@alice@mastodon.example", "", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := db.UserByFediverseAcct("@alice@mastodon.example"); err == nil {
		t.Fatal("expected duplicate fediverse handle error")
	}
}

func TestUseEmailChangeTokenReplacesEmailIdentity(t *testing.T) {
	db := testDB(t)
	user, err := db.CreateUser("emailchange", "old@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.CreateEmailChangeToken("email-change-hash", user.ID, "New@Example.COM", time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	updated, err := db.UseEmailChangeToken("email-change-hash")
	if err != nil {
		t.Fatal(err)
	}
	if updated.Email != "new@example.com" {
		t.Fatalf("expected normalized email, got %q", updated.Email)
	}
	if _, err := db.UserByAuthIdentity("email", "old@example.com"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("expected old email identity to be removed, got %v", err)
	}
	found, err := db.UserByAuthIdentity("email", "new@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if found.ID != user.ID {
		t.Fatalf("expected new email identity for user %d, got %d", user.ID, found.ID)
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

func TestCreatePostNormalizesAndRejectsInvalidWords(t *testing.T) {
	db := testDB(t)
	user, err := db.CreateUser("words", "words@example.com")
	if err != nil {
		t.Fatal(err)
	}
	post, err := db.CreatePost(user.ID, "  ember  ", nil)
	if err != nil {
		t.Fatal(err)
	}
	if post.Word != "ember" {
		t.Fatalf("expected normalized word, got %q", post.Word)
	}
	if _, err := db.CreatePost(user.ID, "two words", nil); !errors.Is(err, word.ErrWhitespace) {
		t.Fatalf("expected whitespace validation error, got %v", err)
	}
	posts, err := db.PostsByUser(user.Username, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(posts) != 1 {
		t.Fatalf("expected invalid post to be rejected, got %#v", posts)
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

func TestMoveActivityPubFollowerRenamesActorAndInbox(t *testing.T) {
	db := testDB(t)
	user, err := db.CreateUser("fed", "fed@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.UpsertActivityPubFollower(user.ID, "https://old.example/users/fed", "https://old.example/inbox"); err != nil {
		t.Fatal(err)
	}
	moved, err := db.MoveActivityPubFollower(user.ID, "https://old.example/users/fed", "https://new.example/users/fed", "https://new.example/inbox")
	if err != nil {
		t.Fatal(err)
	}
	if !moved {
		t.Fatal("expected follower to move")
	}
	followers, err := db.ActivityPubFollowers(user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(followers) != 1 || followers[0].Actor != "https://new.example/users/fed" || followers[0].Inbox != "https://new.example/inbox" {
		t.Fatalf("unexpected followers %#v", followers)
	}

	moved, err = db.MoveActivityPubFollower(user.ID, "https://missing.example/users/fed", "https://other.example/users/fed", "https://other.example/inbox")
	if err != nil {
		t.Fatal(err)
	}
	if moved {
		t.Fatal("expected missing follower to be ignored")
	}
	followers, err = db.ActivityPubFollowers(user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(followers) != 1 || followers[0].Actor != "https://new.example/users/fed" {
		t.Fatalf("unexpected followers after missing move %#v", followers)
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

func TestRevokeUnusedInviteKeepsUsedInvites(t *testing.T) {
	db := testDB(t)
	user, err := db.CreateUser("member", "member@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.CreateInvite("open-invite"); err != nil {
		t.Fatal(err)
	}
	if err := db.CreateInvite("used-invite"); err != nil {
		t.Fatal(err)
	}
	if err := db.UseInvite("used-invite", user.ID); err != nil {
		t.Fatal(err)
	}

	if err := db.RevokeUnusedInvite("open-invite"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.InviteByCode("open-invite"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("expected revoked invite to be gone, got %v", err)
	}
	if err := db.RevokeUnusedInvite("used-invite"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("expected used invite to remain, got %v", err)
	}
	if _, err := db.InviteByCode("used-invite"); err != nil {
		t.Fatalf("expected used invite to remain, got %v", err)
	}
}

func TestSuspendUserBlocksSessionsAndAuthLookups(t *testing.T) {
	db := testDB(t)
	user, err := db.CreateUser("member", "member@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.CreateSession("session-hash", user.ID, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := db.LinkAuthIdentity(user.ID, "mastodon", "@member@example.social"); err != nil {
		t.Fatal(err)
	}

	if err := db.SuspendUser(user.ID); err != nil {
		t.Fatal(err)
	}
	suspended, err := db.UserByID(user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !suspended.SuspendedAt.Valid {
		t.Fatal("expected user to be suspended")
	}
	if _, err := db.UserBySessionHash("session-hash"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("expected suspended session lookup to fail, got %v", err)
	}
	if _, err := db.UserByAuthIdentity("mastodon", "@member@example.social"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("expected suspended identity lookup to fail, got %v", err)
	}
	if _, err := db.UserByEmail("member@example.com"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("expected suspended email lookup to fail, got %v", err)
	}
	if _, err := db.UserByUsername("member"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("expected suspended username lookup to fail, got %v", err)
	}

	if err := db.UnsuspendUser(user.ID); err != nil {
		t.Fatal(err)
	}
	unsuspended, err := db.UserByEmail("member@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if unsuspended.SuspendedAt.Valid {
		t.Fatal("expected user to be unsuspended")
	}
}
