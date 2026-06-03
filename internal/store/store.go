package store

import (
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/go-webauthn/webauthn/webauthn"
	_ "github.com/mattn/go-sqlite3"
)

type DB struct {
	*sql.DB
}

type User struct {
	ID                  int64
	Username            string
	Domain              string
	Email               string
	FediverseAcct       string
	EmailOptIn          bool
	TimestampPreference string
	CreatedAt           time.Time
	MigrationTarget     string
}

type Post struct {
	ID        int64
	UserID    int64
	Username  string
	Word      string
	ImageURL  sql.NullString
	CreatedAt time.Time
}

type Invite struct {
	Code      string
	InviterID sql.NullInt64
	UsedBy    sql.NullInt64
	CreatedAt time.Time
	UsedAt    sql.NullTime
}

type DailyEmailCandidate struct {
	User      User
	Post      sql.Null[Post]
	SentCount int
}

type WebAuthnSession struct {
	ID        string
	UserID    sql.NullInt64
	Kind      string
	Data      []byte
	ExpiresAt time.Time
}

type ActivityPubKey struct {
	UserID        int64
	PrivateKeyPEM string
	PublicKeyPEM  string
}

type ActivityPubFollower struct {
	Actor string
	Inbox string
}

type APIToken struct {
	ID         int64
	UserID     int64
	Name       string
	Prefix     string
	CreatedAt  time.Time
	LastUsedAt sql.NullTime
}

type ActivityPubDelivery struct {
	ID          int64
	UserID      int64
	Inbox       string
	Activity    []byte
	Attempts    int
	NextAt      time.Time
	LastError   string
	CreatedAt   time.Time
	DeliveredAt sql.NullTime
}

func Open(databaseURL string) (*DB, error) {
	driver, dsn, err := parseDatabaseURL(databaseURL)
	if err != nil {
		return nil, err
	}
	db, err := sql.Open(driver, dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	return &DB{DB: db}, nil
}

func parseDatabaseURL(databaseURL string) (string, string, error) {
	if strings.HasPrefix(databaseURL, "sqlite://") {
		return "sqlite3", strings.TrimPrefix(databaseURL, "sqlite://"), nil
	}
	if databaseURL == "" {
		return "sqlite3", "igrec.db", nil
	}
	if strings.HasPrefix(databaseURL, "postgres://") || strings.HasPrefix(databaseURL, "postgresql://") {
		return "", "", errors.New("postgres is planned: add a pq/pgx driver and set DATABASE_URL")
	}
	return "sqlite3", databaseURL, nil
}

func (db *DB) Migrate() error {
	schema := `
create table if not exists users (
  id integer primary key autoincrement,
  username text not null unique,
  domain text not null default '',
  email text not null default '',
  fediverse_acct text not null default '',
  email_opt_in integer not null default 0,
  timestamp_preference text not null default 'smart',
  migration_target text not null default '',
  created_at datetime not null default current_timestamp
);
create table if not exists invites (
  code text primary key,
  inviter_id integer references users(id),
  used_by integer references users(id),
  created_at datetime not null default current_timestamp,
  used_at datetime
);
create table if not exists invite_grants (
  user_id integer primary key references users(id),
  extra_invites integer not null default 0,
  updated_at datetime not null default current_timestamp
);
create table if not exists sessions (
  token_hash text primary key,
  user_id integer not null references users(id),
  created_at datetime not null default current_timestamp,
  expires_at datetime not null
);
create table if not exists login_tokens (
  token_hash text primary key,
  user_id integer not null references users(id),
  created_at datetime not null default current_timestamp,
  expires_at datetime not null,
  used_at datetime
);
create table if not exists email_change_tokens (
  token_hash text primary key,
  user_id integer not null references users(id),
  email text not null,
  created_at datetime not null default current_timestamp,
  expires_at datetime not null,
  used_at datetime
);
create table if not exists passkeys (
  credential_id text primary key,
  user_id integer not null references users(id),
  name text not null default 'passkey',
  credential_json text not null,
  created_at datetime not null default current_timestamp,
  last_used_at datetime
);
create table if not exists webauthn_sessions (
  id text primary key,
  user_id integer references users(id),
  kind text not null,
  data text not null,
  created_at datetime not null default current_timestamp,
  expires_at datetime not null
);
create table if not exists posts (
  id integer primary key autoincrement,
  user_id integer not null references users(id),
  word text not null,
  image_url text,
  created_at datetime not null default current_timestamp
);
create index if not exists posts_created_at_idx on posts(created_at desc, id desc);
create index if not exists posts_user_word_idx on posts(user_id, word);
create unique index if not exists users_email_unique_idx on users(email) where email != '';
create index if not exists sessions_user_idx on sessions(user_id);
create index if not exists login_tokens_user_idx on login_tokens(user_id);
create index if not exists email_change_tokens_user_idx on email_change_tokens(user_id);
create index if not exists passkeys_user_idx on passkeys(user_id);
create index if not exists webauthn_sessions_expires_idx on webauthn_sessions(expires_at);
create table if not exists follows (
  id integer primary key autoincrement,
  follower_actor text not null,
  user_id integer not null references users(id),
  inbox_url text not null,
  created_at datetime not null default current_timestamp,
  unique(follower_actor, user_id)
);
create table if not exists activitypub_keys (
  user_id integer primary key references users(id),
  private_key_pem text not null,
  public_key_pem text not null,
  created_at datetime not null default current_timestamp
);
create table if not exists user_follows (
  follower_user_id integer not null references users(id),
  followed_user_id integer not null references users(id),
  created_at datetime not null default current_timestamp,
  primary key(follower_user_id, followed_user_id)
);
create table if not exists daily_email_sends (
  user_id integer not null references users(id),
  sent_on text not null,
  sent_at datetime not null default current_timestamp,
  primary key(user_id, sent_on)
);
create table if not exists email_unsubscribe_tokens (
  token text primary key,
  user_id integer not null unique references users(id),
  created_at datetime not null default current_timestamp
);
create table if not exists api_tokens (
  id integer primary key autoincrement,
  user_id integer not null references users(id),
  token_hash text not null unique,
  token_prefix text not null,
  name text not null default 'api token',
  created_at datetime not null default current_timestamp,
  last_used_at datetime
);
create index if not exists api_tokens_user_idx on api_tokens(user_id);
create table if not exists activitypub_deliveries (
  id integer primary key autoincrement,
  user_id integer not null references users(id),
  inbox_url text not null,
  activity_json text not null,
  attempts integer not null default 0,
  next_at datetime not null default current_timestamp,
  last_error text not null default '',
  created_at datetime not null default current_timestamp,
  delivered_at datetime
);
create index if not exists activitypub_deliveries_due_idx on activitypub_deliveries(delivered_at, next_at, id);
create index if not exists activitypub_deliveries_user_idx on activitypub_deliveries(user_id);
`
	if _, err := db.Exec(schema); err != nil {
		return err
	}
	if err := db.ensureColumn("users", "timestamp_preference", "text not null default 'smart'"); err != nil {
		return err
	}
	return db.ensureColumn("invites", "inviter_id", "integer references users(id)")
}

func (db *DB) ensureColumn(table, column, definition string) error {
	rows, err := db.Query(`pragma table_info(` + table + `)`)
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var cid int
		var name, typ string
		var notNull int
		var defaultValue any
		var pk int
		if err := rows.Scan(&cid, &name, &typ, &notNull, &defaultValue, &pk); err != nil {
			return err
		}
		if name == column {
			return nil
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	_, err = db.Exec(fmt.Sprintf(`alter table %s add column %s %s`, table, column, definition))
	return err
}

func (db *DB) EnsureLocalUser(username string) (User, error) {
	var user User
	err := db.QueryRow(`select id, username, domain, email, fediverse_acct, email_opt_in, timestamp_preference, migration_target, created_at from users where username = ?`, username).
		Scan(&user.ID, &user.Username, &user.Domain, &user.Email, &user.FediverseAcct, &user.EmailOptIn, &user.TimestampPreference, &user.MigrationTarget, &user.CreatedAt)
	if err == nil {
		user.TimestampPreference = normalizeTimestampPreference(user.TimestampPreference)
		return user, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return user, err
	}
	res, err := db.Exec(`insert into users(username) values(?)`, username)
	if err != nil {
		return user, err
	}
	user.ID, _ = res.LastInsertId()
	user.Username = username
	user.TimestampPreference = "smart"
	user.CreatedAt = time.Now().UTC()
	return user, nil
}

func (db *DB) UserByUsername(username string) (User, error) {
	var user User
	err := db.QueryRow(`select id, username, domain, email, fediverse_acct, email_opt_in, timestamp_preference, migration_target, created_at from users where username = ?`, username).
		Scan(&user.ID, &user.Username, &user.Domain, &user.Email, &user.FediverseAcct, &user.EmailOptIn, &user.TimestampPreference, &user.MigrationTarget, &user.CreatedAt)
	user.TimestampPreference = normalizeTimestampPreference(user.TimestampPreference)
	return user, err
}

func (db *DB) UserByID(id int64) (User, error) {
	var user User
	err := db.QueryRow(`select id, username, domain, email, fediverse_acct, email_opt_in, timestamp_preference, migration_target, created_at from users where id = ?`, id).
		Scan(&user.ID, &user.Username, &user.Domain, &user.Email, &user.FediverseAcct, &user.EmailOptIn, &user.TimestampPreference, &user.MigrationTarget, &user.CreatedAt)
	user.TimestampPreference = normalizeTimestampPreference(user.TimestampPreference)
	return user, err
}

func (db *DB) UserByEmail(email string) (User, error) {
	var user User
	err := db.QueryRow(`select id, username, domain, email, fediverse_acct, email_opt_in, timestamp_preference, migration_target, created_at from users where lower(email) = lower(?)`, strings.TrimSpace(email)).
		Scan(&user.ID, &user.Username, &user.Domain, &user.Email, &user.FediverseAcct, &user.EmailOptIn, &user.TimestampPreference, &user.MigrationTarget, &user.CreatedAt)
	user.TimestampPreference = normalizeTimestampPreference(user.TimestampPreference)
	return user, err
}

func (db *DB) UserBySessionHash(tokenHash string) (User, error) {
	var user User
	err := db.QueryRow(`
select users.id, users.username, users.domain, users.email, users.fediverse_acct, users.email_opt_in, users.timestamp_preference, users.migration_target, users.created_at
from sessions join users on users.id = sessions.user_id
where sessions.token_hash = ? and sessions.expires_at > current_timestamp`, tokenHash).
		Scan(&user.ID, &user.Username, &user.Domain, &user.Email, &user.FediverseAcct, &user.EmailOptIn, &user.TimestampPreference, &user.MigrationTarget, &user.CreatedAt)
	user.TimestampPreference = normalizeTimestampPreference(user.TimestampPreference)
	return user, err
}

func (db *DB) CreateUser(username, email string) (User, error) {
	res, err := db.Exec(`insert into users(username, email, timestamp_preference) values(?, ?, 'smart')`, username, strings.ToLower(strings.TrimSpace(email)))
	if err != nil {
		return User{}, err
	}
	id, _ := res.LastInsertId()
	var user User
	err = db.QueryRow(`select id, username, domain, email, fediverse_acct, email_opt_in, timestamp_preference, migration_target, created_at from users where id = ?`, id).
		Scan(&user.ID, &user.Username, &user.Domain, &user.Email, &user.FediverseAcct, &user.EmailOptIn, &user.TimestampPreference, &user.MigrationTarget, &user.CreatedAt)
	user.TimestampPreference = normalizeTimestampPreference(user.TimestampPreference)
	return user, err
}

func (db *DB) CreateInvite(code string) error {
	_, err := db.Exec(`insert into invites(code) values(?)`, code)
	return err
}

func (db *DB) CreateInviteForUser(code string, inviterID int64) error {
	_, err := db.Exec(`insert into invites(code, inviter_id) values(?, ?)`, code, inviterID)
	return err
}

func (db *DB) InviteByCode(code string) (Invite, error) {
	var invite Invite
	err := db.QueryRow(`select code, inviter_id, used_by, created_at, used_at from invites where code = ?`, code).
		Scan(&invite.Code, &invite.InviterID, &invite.UsedBy, &invite.CreatedAt, &invite.UsedAt)
	return invite, err
}

func (db *DB) InvitesByInviter(inviterID int64) ([]Invite, error) {
	rows, err := db.Query(`select code, inviter_id, used_by, created_at, used_at from invites where inviter_id = ? order by created_at asc`, inviterID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var invites []Invite
	for rows.Next() {
		var invite Invite
		if err := rows.Scan(&invite.Code, &invite.InviterID, &invite.UsedBy, &invite.CreatedAt, &invite.UsedAt); err != nil {
			return nil, err
		}
		invites = append(invites, invite)
	}
	return invites, rows.Err()
}

func (db *DB) InviteCountByInviter(inviterID int64) (int, error) {
	var count int
	err := db.QueryRow(`select count(*) from invites where inviter_id = ?`, inviterID).Scan(&count)
	return count, err
}

func (db *DB) InviteLimitByInviter(inviterID int64) (int, error) {
	var extra int
	err := db.QueryRow(`select coalesce(extra_invites, 0) from invite_grants where user_id = ?`, inviterID).Scan(&extra)
	if errors.Is(err, sql.ErrNoRows) {
		return 3, nil
	}
	if err != nil {
		return 0, err
	}
	return 3 + extra, nil
}

func (db *DB) RecentInvites(limit int) ([]Invite, error) {
	if limit <= 0 {
		limit = 25
	}
	rows, err := db.Query(`select code, inviter_id, used_by, created_at, used_at from invites order by created_at desc limit ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var invites []Invite
	for rows.Next() {
		var invite Invite
		if err := rows.Scan(&invite.Code, &invite.InviterID, &invite.UsedBy, &invite.CreatedAt, &invite.UsedAt); err != nil {
			return nil, err
		}
		invites = append(invites, invite)
	}
	return invites, rows.Err()
}

func (db *DB) UseInvite(code string, userID int64) error {
	res, err := db.Exec(`update invites set used_by = ?, used_at = current_timestamp where code = ? and used_at is null`, userID, code)
	if err != nil {
		return err
	}
	rows, _ := res.RowsAffected()
	if rows == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (db *DB) CreateUserFollow(followerID, followedID int64) error {
	if followerID == followedID {
		return nil
	}
	_, err := db.Exec(`insert or ignore into user_follows(follower_user_id, followed_user_id) values(?, ?)`, followerID, followedID)
	return err
}

func (db *DB) DeleteUserFollow(followerID, followedID int64) error {
	_, err := db.Exec(`delete from user_follows where follower_user_id = ? and followed_user_id = ?`, followerID, followedID)
	return err
}

func (db *DB) UserFollows(followerID, followedID int64) (bool, error) {
	var found int
	err := db.QueryRow(`select 1 from user_follows where follower_user_id = ? and followed_user_id = ?`, followerID, followedID).Scan(&found)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

func (db *DB) ActivityPubKey(userID int64) (ActivityPubKey, error) {
	var key ActivityPubKey
	err := db.QueryRow(`select user_id, private_key_pem, public_key_pem from activitypub_keys where user_id = ?`, userID).
		Scan(&key.UserID, &key.PrivateKeyPEM, &key.PublicKeyPEM)
	return key, err
}

func (db *DB) CreateActivityPubKey(userID int64, privateKeyPEM, publicKeyPEM string) error {
	_, err := db.Exec(`insert into activitypub_keys(user_id, private_key_pem, public_key_pem) values(?, ?, ?)
on conflict(user_id) do update set private_key_pem = excluded.private_key_pem, public_key_pem = excluded.public_key_pem`, userID, privateKeyPEM, publicKeyPEM)
	return err
}

func (db *DB) UpsertActivityPubFollower(userID int64, actor, inbox string) error {
	_, err := db.Exec(`insert into follows(follower_actor, user_id, inbox_url) values(?, ?, ?)
on conflict(follower_actor, user_id) do update set inbox_url = excluded.inbox_url`, actor, userID, inbox)
	return err
}

func (db *DB) DeleteActivityPubFollower(userID int64, actor string) error {
	_, err := db.Exec(`delete from follows where user_id = ? and follower_actor = ?`, userID, actor)
	return err
}

func (db *DB) ActivityPubFollowers(userID int64) ([]ActivityPubFollower, error) {
	rows, err := db.Query(`select follower_actor, inbox_url from follows where user_id = ? order by created_at asc`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var followers []ActivityPubFollower
	for rows.Next() {
		var follower ActivityPubFollower
		if err := rows.Scan(&follower.Actor, &follower.Inbox); err != nil {
			return nil, err
		}
		followers = append(followers, follower)
	}
	return followers, rows.Err()
}

func (db *DB) UserFriends(userID int64) ([]User, error) {
	rows, err := db.Query(`
select users.id, users.username, users.domain, users.email, users.fediverse_acct, users.email_opt_in, users.timestamp_preference, users.migration_target, users.created_at
from user_follows
join users on users.id = user_follows.followed_user_id
where user_follows.follower_user_id = ?
order by lower(users.username) asc`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var users []User
	for rows.Next() {
		var user User
		if err := rows.Scan(&user.ID, &user.Username, &user.Domain, &user.Email, &user.FediverseAcct, &user.EmailOptIn, &user.TimestampPreference, &user.MigrationTarget, &user.CreatedAt); err != nil {
			return nil, err
		}
		user.TimestampPreference = normalizeTimestampPreference(user.TimestampPreference)
		users = append(users, user)
	}
	return users, rows.Err()
}

func (db *DB) FriendPosts(userID int64, limit int) ([]Post, error) {
	return db.posts(`where posts.user_id in (select followed_user_id from user_follows where follower_user_id = ?)`, limit, userID)
}

func (db *DB) FriendPostsBefore(userID, beforeID int64, limit int) ([]Post, error) {
	if beforeID > 0 {
		return db.posts(`where posts.user_id in (select followed_user_id from user_follows where follower_user_id = ?) and posts.id < ?`, limit, userID, beforeID)
	}
	return db.FriendPosts(userID, limit)
}

func (db *DB) CreateSession(tokenHash string, userID int64, expiresAt time.Time) error {
	_, err := db.Exec(`insert into sessions(token_hash, user_id, expires_at) values(?, ?, ?)`, tokenHash, userID, expiresAt.UTC())
	return err
}

func (db *DB) DeleteSession(tokenHash string) error {
	_, err := db.Exec(`delete from sessions where token_hash = ?`, tokenHash)
	return err
}

func (db *DB) CreateLoginToken(tokenHash string, userID int64, expiresAt time.Time) error {
	_, err := db.Exec(`insert into login_tokens(token_hash, user_id, expires_at) values(?, ?, ?)`, tokenHash, userID, expiresAt.UTC())
	return err
}

func (db *DB) UseLoginToken(tokenHash string) (User, error) {
	tx, err := db.Begin()
	if err != nil {
		return User{}, err
	}
	defer tx.Rollback()

	var user User
	err = tx.QueryRow(`
select users.id, users.username, users.domain, users.email, users.fediverse_acct, users.email_opt_in, users.timestamp_preference, users.migration_target, users.created_at
from login_tokens join users on users.id = login_tokens.user_id
where login_tokens.token_hash = ? and login_tokens.used_at is null and login_tokens.expires_at > current_timestamp`, tokenHash).
		Scan(&user.ID, &user.Username, &user.Domain, &user.Email, &user.FediverseAcct, &user.EmailOptIn, &user.TimestampPreference, &user.MigrationTarget, &user.CreatedAt)
	if err != nil {
		return User{}, err
	}
	if _, err := tx.Exec(`update login_tokens set used_at = current_timestamp where token_hash = ?`, tokenHash); err != nil {
		return User{}, err
	}
	if err := tx.Commit(); err != nil {
		return User{}, err
	}
	user.TimestampPreference = normalizeTimestampPreference(user.TimestampPreference)
	return user, nil
}

func (db *DB) CreateEmailChangeToken(tokenHash string, userID int64, email string, expiresAt time.Time) error {
	_, err := db.Exec(`insert into email_change_tokens(token_hash, user_id, email, expires_at) values(?, ?, ?, ?)`, tokenHash, userID, strings.ToLower(strings.TrimSpace(email)), expiresAt.UTC())
	return err
}

func (db *DB) UseEmailChangeToken(tokenHash string) (User, error) {
	tx, err := db.Begin()
	if err != nil {
		return User{}, err
	}
	defer tx.Rollback()

	var userID int64
	var email string
	err = tx.QueryRow(`
select user_id, email
from email_change_tokens
where token_hash = ? and used_at is null and expires_at > current_timestamp`, tokenHash).
		Scan(&userID, &email)
	if err != nil {
		return User{}, err
	}
	if _, err := tx.Exec(`update users set email = ? where id = ?`, email, userID); err != nil {
		return User{}, err
	}
	if _, err := tx.Exec(`update email_change_tokens set used_at = current_timestamp where token_hash = ?`, tokenHash); err != nil {
		return User{}, err
	}
	if err := tx.Commit(); err != nil {
		return User{}, err
	}

	var user User
	err = db.QueryRow(`select id, username, domain, email, fediverse_acct, email_opt_in, timestamp_preference, migration_target, created_at from users where id = ?`, userID).
		Scan(&user.ID, &user.Username, &user.Domain, &user.Email, &user.FediverseAcct, &user.EmailOptIn, &user.TimestampPreference, &user.MigrationTarget, &user.CreatedAt)
	user.TimestampPreference = normalizeTimestampPreference(user.TimestampPreference)
	return user, err
}

func (db *DB) UpdateTimestampPreference(userID int64, preference string) error {
	_, err := db.Exec(`update users set timestamp_preference = ? where id = ?`, normalizeTimestampPreference(preference), userID)
	return err
}

func (db *DB) UpdateSettings(userID int64, preference string, emailOptIn bool, fediverseAcct string) error {
	optIn := 0
	if emailOptIn {
		optIn = 1
	}
	_, err := db.Exec(`update users set timestamp_preference = ?, email_opt_in = ?, fediverse_acct = ? where id = ?`,
		normalizeTimestampPreference(preference), optIn, strings.TrimSpace(fediverseAcct), userID)
	return err
}

func (db *DB) SetEmailOptIn(userID int64, emailOptIn bool) error {
	optIn := 0
	if emailOptIn {
		optIn = 1
	}
	_, err := db.Exec(`update users set email_opt_in = ? where id = ?`, optIn, userID)
	return err
}

func (db *DB) CreateAPIToken(userID int64, tokenHash, prefix, name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		name = "api token"
	}
	_, err := db.Exec(`insert into api_tokens(user_id, token_hash, token_prefix, name) values(?, ?, ?, ?)`, userID, tokenHash, prefix, name)
	return err
}

func (db *DB) DeleteAPIToken(userID, tokenID int64) error {
	_, err := db.Exec(`delete from api_tokens where user_id = ? and id = ?`, userID, tokenID)
	return err
}

func (db *DB) APITokensByUser(userID int64) ([]APIToken, error) {
	rows, err := db.Query(`select id, user_id, name, token_prefix, created_at, last_used_at from api_tokens where user_id = ? order by created_at desc`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var tokens []APIToken
	for rows.Next() {
		var token APIToken
		if err := rows.Scan(&token.ID, &token.UserID, &token.Name, &token.Prefix, &token.CreatedAt, &token.LastUsedAt); err != nil {
			return nil, err
		}
		tokens = append(tokens, token)
	}
	return tokens, rows.Err()
}

func (db *DB) UserByAPITokenHash(tokenHash string) (User, error) {
	var user User
	err := db.QueryRow(`
select users.id, users.username, users.domain, users.email, users.fediverse_acct, users.email_opt_in, users.timestamp_preference, users.migration_target, users.created_at
from api_tokens join users on users.id = api_tokens.user_id
where api_tokens.token_hash = ?`, tokenHash).
		Scan(&user.ID, &user.Username, &user.Domain, &user.Email, &user.FediverseAcct, &user.EmailOptIn, &user.TimestampPreference, &user.MigrationTarget, &user.CreatedAt)
	if err != nil {
		return User{}, err
	}
	_, _ = db.Exec(`update api_tokens set last_used_at = current_timestamp where token_hash = ?`, tokenHash)
	user.TimestampPreference = normalizeTimestampPreference(user.TimestampPreference)
	return user, nil
}

func (db *DB) EnqueueActivityPubDelivery(userID int64, inbox string, activity []byte, nextAt time.Time, lastError string) error {
	_, err := db.Exec(`insert into activitypub_deliveries(user_id, inbox_url, activity_json, next_at, last_error) values(?, ?, ?, ?, ?)`,
		userID, inbox, string(activity), nextAt.UTC(), lastError)
	return err
}

func (db *DB) DueActivityPubDeliveries(now time.Time, limit int) ([]ActivityPubDelivery, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := db.Query(`
select id, user_id, inbox_url, activity_json, attempts, next_at, last_error, created_at, delivered_at
from activitypub_deliveries
where delivered_at is null and next_at <= ?
order by next_at asc, id asc
limit ?`, now.UTC(), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var deliveries []ActivityPubDelivery
	for rows.Next() {
		var delivery ActivityPubDelivery
		var activity string
		if err := rows.Scan(&delivery.ID, &delivery.UserID, &delivery.Inbox, &activity, &delivery.Attempts, &delivery.NextAt, &delivery.LastError, &delivery.CreatedAt, &delivery.DeliveredAt); err != nil {
			return nil, err
		}
		delivery.Activity = []byte(activity)
		deliveries = append(deliveries, delivery)
	}
	return deliveries, rows.Err()
}

func (db *DB) MarkActivityPubDeliveryDelivered(id int64) error {
	_, err := db.Exec(`update activitypub_deliveries set delivered_at = current_timestamp where id = ?`, id)
	return err
}

func (db *DB) MarkActivityPubDeliveryFailed(id int64, attempts int, nextAt time.Time, lastError string) error {
	if len(lastError) > 1000 {
		lastError = lastError[:1000]
	}
	_, err := db.Exec(`update activitypub_deliveries set attempts = ?, next_at = ?, last_error = ? where id = ?`, attempts, nextAt.UTC(), lastError, id)
	return err
}

func (db *DB) PruneDeliveredActivityPubDeliveries(before time.Time) (int64, error) {
	res, err := db.Exec(`delete from activitypub_deliveries where delivered_at is not null and delivered_at < ?`, before.UTC())
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

func (db *DB) DeleteUser(userID int64) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	statements := []string{
		`delete from sessions where user_id = ?`,
		`delete from login_tokens where user_id = ?`,
		`delete from email_change_tokens where user_id = ?`,
		`delete from passkeys where user_id = ?`,
		`delete from webauthn_sessions where user_id = ?`,
		`delete from posts where user_id = ?`,
		`delete from follows where user_id = ?`,
		`delete from activitypub_keys where user_id = ?`,
		`delete from activitypub_deliveries where user_id = ?`,
		`delete from api_tokens where user_id = ?`,
		`delete from user_follows where follower_user_id = ? or followed_user_id = ?`,
		`delete from daily_email_sends where user_id = ?`,
		`delete from email_unsubscribe_tokens where user_id = ?`,
		`delete from invite_grants where user_id = ?`,
		`delete from invites where inviter_id = ?`,
	}
	for _, stmt := range statements {
		args := []any{userID}
		if strings.Count(stmt, "?") == 2 {
			args = append(args, userID)
		}
		if _, err := tx.Exec(stmt, args...); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(`update invites set used_by = null where used_by = ?`, userID); err != nil {
		return err
	}

	res, err := tx.Exec(`delete from users where id = ?`, userID)
	if err != nil {
		return err
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return sql.ErrNoRows
	}
	return tx.Commit()
}

func (db *DB) UnsubscribeTokenForUser(userID int64, tokenFactory func() (string, error)) (string, error) {
	var existing string
	err := db.QueryRow(`select token from email_unsubscribe_tokens where user_id = ?`, userID).Scan(&existing)
	if err == nil {
		return existing, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	for tries := 0; tries < 4; tries++ {
		token, err := tokenFactory()
		if err != nil {
			return "", err
		}
		_, err = db.Exec(`insert into email_unsubscribe_tokens(token, user_id) values(?, ?)`, token, userID)
		if err == nil {
			return token, nil
		}
	}
	return "", errors.New("could not create unsubscribe token")
}

func (db *DB) UserByUnsubscribeToken(token string) (User, error) {
	var user User
	err := db.QueryRow(`
select users.id, users.username, users.domain, users.email, users.fediverse_acct, users.email_opt_in, users.timestamp_preference, users.migration_target, users.created_at
from email_unsubscribe_tokens
join users on users.id = email_unsubscribe_tokens.user_id
where email_unsubscribe_tokens.token = ?`, token).
		Scan(&user.ID, &user.Username, &user.Domain, &user.Email, &user.FediverseAcct, &user.EmailOptIn, &user.TimestampPreference, &user.MigrationTarget, &user.CreatedAt)
	user.TimestampPreference = normalizeTimestampPreference(user.TimestampPreference)
	return user, err
}

func (db *DB) DailyEmailCandidates(sentOn string, limit int) ([]DailyEmailCandidate, error) {
	rows, err := db.Query(`
select users.id, users.username, users.domain, users.email, users.fediverse_acct, users.email_opt_in, users.timestamp_preference, users.migration_target, users.created_at,
       posts.id, posts.user_id, post_users.username, posts.word, posts.image_url, posts.created_at,
       (select count(*) from daily_email_sends all_sends where all_sends.user_id = users.id)
from users
left join daily_email_sends on daily_email_sends.user_id = users.id and daily_email_sends.sent_on = ?
left join posts on posts.id = (
  select posts.id
  from posts
  where (
    posts.user_id in (select followed_user_id from user_follows where follower_user_id = users.id)
    or (
      not exists (select 1 from user_follows where follower_user_id = users.id)
      and posts.user_id != users.id
    )
  )
  order by posts.created_at desc, posts.id desc
  limit 1
)
left join users post_users on post_users.id = posts.user_id
where users.email_opt_in = 1
  and users.email != ''
  and daily_email_sends.user_id is null
order by users.id asc
limit ?`, sentOn, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var candidates []DailyEmailCandidate
	for rows.Next() {
		var candidate DailyEmailCandidate
		var post Post
		var postID, postUserID sql.NullInt64
		var postUsername, postWord sql.NullString
		var imageURL sql.NullString
		var postCreatedAt sql.NullTime
		if err := rows.Scan(
			&candidate.User.ID,
			&candidate.User.Username,
			&candidate.User.Domain,
			&candidate.User.Email,
			&candidate.User.FediverseAcct,
			&candidate.User.EmailOptIn,
			&candidate.User.TimestampPreference,
			&candidate.User.MigrationTarget,
			&candidate.User.CreatedAt,
			&postID,
			&postUserID,
			&postUsername,
			&postWord,
			&imageURL,
			&postCreatedAt,
			&candidate.SentCount,
		); err != nil {
			return nil, err
		}
		candidate.User.TimestampPreference = normalizeTimestampPreference(candidate.User.TimestampPreference)
		if postID.Valid && postUserID.Valid && postUsername.Valid && postWord.Valid && postCreatedAt.Valid {
			post.ID = postID.Int64
			post.UserID = postUserID.Int64
			post.Username = postUsername.String
			post.Word = postWord.String
			post.ImageURL = imageURL
			post.CreatedAt = postCreatedAt.Time
			candidate.Post = sql.Null[Post]{V: post, Valid: true}
		}
		candidates = append(candidates, candidate)
	}
	return candidates, rows.Err()
}

func (db *DB) MarkDailyEmailSent(userID int64, sentOn string) error {
	_, err := db.Exec(`insert or ignore into daily_email_sends(user_id, sent_on) values(?, ?)`, userID, sentOn)
	return err
}

func (db *DB) PasskeyCredentialsByUser(userID int64) ([]webauthn.Credential, error) {
	rows, err := db.Query(`select credential_json from passkeys where user_id = ? order by created_at asc`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var credentials []webauthn.Credential
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		var credential webauthn.Credential
		if err := json.Unmarshal([]byte(raw), &credential); err != nil {
			return nil, err
		}
		credentials = append(credentials, credential)
	}
	return credentials, rows.Err()
}

func (db *DB) SavePasskey(userID int64, name string, credential webauthn.Credential) error {
	raw, err := json.Marshal(credential)
	if err != nil {
		return err
	}
	if strings.TrimSpace(name) == "" {
		name = "passkey"
	}
	_, err = db.Exec(`insert into passkeys(credential_id, user_id, name, credential_json) values(?, ?, ?, ?)`, passkeyID(credential.ID), userID, strings.TrimSpace(name), string(raw))
	return err
}

func (db *DB) UpdatePasskeyCredential(credential webauthn.Credential) error {
	raw, err := json.Marshal(credential)
	if err != nil {
		return err
	}
	_, err = db.Exec(`update passkeys set credential_json = ?, last_used_at = current_timestamp where credential_id = ?`, string(raw), passkeyID(credential.ID))
	return err
}

func (db *DB) UserByPasskeyID(rawID []byte) (User, error) {
	var user User
	err := db.QueryRow(`
select users.id, users.username, users.domain, users.email, users.fediverse_acct, users.email_opt_in, users.timestamp_preference, users.migration_target, users.created_at
from passkeys join users on users.id = passkeys.user_id
where passkeys.credential_id = ?`, passkeyID(rawID)).
		Scan(&user.ID, &user.Username, &user.Domain, &user.Email, &user.FediverseAcct, &user.EmailOptIn, &user.TimestampPreference, &user.MigrationTarget, &user.CreatedAt)
	user.TimestampPreference = normalizeTimestampPreference(user.TimestampPreference)
	return user, err
}

func (db *DB) PasskeyCount(userID int64) (int, error) {
	var count int
	err := db.QueryRow(`select count(*) from passkeys where user_id = ?`, userID).Scan(&count)
	return count, err
}

func (db *DB) CreateWebAuthnSession(id string, userID sql.NullInt64, kind string, data []byte, expiresAt time.Time) error {
	_, err := db.Exec(`insert into webauthn_sessions(id, user_id, kind, data, expires_at) values(?, ?, ?, ?, ?)`, id, userID, kind, string(data), expiresAt.UTC())
	return err
}

func (db *DB) UseWebAuthnSession(id, kind string) (WebAuthnSession, error) {
	tx, err := db.Begin()
	if err != nil {
		return WebAuthnSession{}, err
	}
	defer tx.Rollback()

	var session WebAuthnSession
	err = tx.QueryRow(`select id, user_id, kind, data, expires_at from webauthn_sessions where id = ? and kind = ? and expires_at > current_timestamp`, id, kind).
		Scan(&session.ID, &session.UserID, &session.Kind, &session.Data, &session.ExpiresAt)
	if err != nil {
		return WebAuthnSession{}, err
	}
	if _, err := tx.Exec(`delete from webauthn_sessions where id = ?`, id); err != nil {
		return WebAuthnSession{}, err
	}
	return session, tx.Commit()
}

func passkeyID(rawID []byte) string {
	return base64.RawURLEncoding.EncodeToString(rawID)
}

func (db *DB) CreatePost(userID int64, value string, imageURL *string) (Post, error) {
	var nullable sql.NullString
	if imageURL != nil && *imageURL != "" {
		nullable = sql.NullString{String: *imageURL, Valid: true}
	}
	res, err := db.Exec(`insert into posts(user_id, word, image_url) values(?, ?, ?)`, userID, value, nullable)
	if err != nil {
		return Post{}, err
	}
	id, _ := res.LastInsertId()
	return db.PostByID(id)
}

func normalizeTimestampPreference(preference string) string {
	switch preference {
	case "date", "datetime", "smart":
		return preference
	default:
		return "smart"
	}
}

func (db *DB) PostByID(id int64) (Post, error) {
	var post Post
	err := db.QueryRow(`
select posts.id, posts.user_id, users.username, posts.word, posts.image_url, posts.created_at
from posts join users on users.id = posts.user_id
where posts.id = ?`, id).
		Scan(&post.ID, &post.UserID, &post.Username, &post.Word, &post.ImageURL, &post.CreatedAt)
	return post, err
}

func (db *DB) PostByUserWord(username, value string) (Post, error) {
	var post Post
	err := db.QueryRow(`
select posts.id, posts.user_id, users.username, posts.word, posts.image_url, posts.created_at
from posts join users on users.id = posts.user_id
where users.username = ? and posts.word = ?
order by posts.created_at desc, posts.id desc
limit 1`, username, value).
		Scan(&post.ID, &post.UserID, &post.Username, &post.Word, &post.ImageURL, &post.CreatedAt)
	return post, err
}

func (db *DB) Firehose(limit int) ([]Post, error) {
	return db.posts(`where 1=1`, limit)
}

func (db *DB) FirehoseBefore(beforeID int64, limit int) ([]Post, error) {
	if beforeID > 0 {
		return db.posts(`where posts.id < ?`, limit, beforeID)
	}
	return db.Firehose(limit)
}

func (db *DB) PostsByUser(username string, limit int) ([]Post, error) {
	return db.posts(`where users.username = ?`, limit, username)
}

func (db *DB) AllPostsByUser(username string) ([]Post, error) {
	return db.postsWithoutLimit(`where users.username = ?`, username)
}

func (db *DB) posts(where string, limit int, args ...any) ([]Post, error) {
	args = append(args, limit)
	rows, err := db.Query(fmt.Sprintf(`
select posts.id, posts.user_id, users.username, posts.word, posts.image_url, posts.created_at
from posts join users on users.id = posts.user_id
%s
order by posts.created_at desc, posts.id desc
limit ?`, where), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var posts []Post
	for rows.Next() {
		var post Post
		if err := rows.Scan(&post.ID, &post.UserID, &post.Username, &post.Word, &post.ImageURL, &post.CreatedAt); err != nil {
			return nil, err
		}
		posts = append(posts, post)
	}
	return posts, rows.Err()
}

func (db *DB) postsWithoutLimit(where string, args ...any) ([]Post, error) {
	rows, err := db.Query(fmt.Sprintf(`
select posts.id, posts.user_id, users.username, posts.word, posts.image_url, posts.created_at
from posts join users on users.id = posts.user_id
%s
order by posts.created_at desc, posts.id desc`, where), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var posts []Post
	for rows.Next() {
		var post Post
		if err := rows.Scan(&post.ID, &post.UserID, &post.Username, &post.Word, &post.ImageURL, &post.CreatedAt); err != nil {
			return nil, err
		}
		posts = append(posts, post)
	}
	return posts, rows.Err()
}
