package store

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

type DB struct {
	*sql.DB
}

type User struct {
	ID             int64
	Username       string
	Domain         string
	Email          string
	FediverseAcct  string
	EmailOptIn     bool
	CreatedAt      time.Time
	MigrationTarget string
}

type Post struct {
	ID        int64
	UserID    int64
	Username  string
	Word      string
	ImageURL  sql.NullString
	CreatedAt time.Time
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
  migration_target text not null default '',
  created_at datetime not null default current_timestamp
);
create table if not exists invites (
  code text primary key,
  used_by integer references users(id),
  created_at datetime not null default current_timestamp,
  used_at datetime
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
create table if not exists follows (
  id integer primary key autoincrement,
  follower_actor text not null,
  user_id integer not null references users(id),
  inbox_url text not null,
  created_at datetime not null default current_timestamp,
  unique(follower_actor, user_id)
);
`
	_, err := db.Exec(schema)
	return err
}

func (db *DB) EnsureLocalUser(username string) (User, error) {
	var user User
	err := db.QueryRow(`select id, username, domain, email, fediverse_acct, email_opt_in, migration_target, created_at from users where username = ?`, username).
		Scan(&user.ID, &user.Username, &user.Domain, &user.Email, &user.FediverseAcct, &user.EmailOptIn, &user.MigrationTarget, &user.CreatedAt)
	if err == nil {
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
	user.CreatedAt = time.Now().UTC()
	return user, nil
}

func (db *DB) UserByUsername(username string) (User, error) {
	var user User
	err := db.QueryRow(`select id, username, domain, email, fediverse_acct, email_opt_in, migration_target, created_at from users where username = ?`, username).
		Scan(&user.ID, &user.Username, &user.Domain, &user.Email, &user.FediverseAcct, &user.EmailOptIn, &user.MigrationTarget, &user.CreatedAt)
	return user, err
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

func (db *DB) PostByID(id int64) (Post, error) {
	var post Post
	err := db.QueryRow(`
select posts.id, posts.user_id, users.username, posts.word, posts.image_url, posts.created_at
from posts join users on users.id = posts.user_id
where posts.id = ?`, id).
		Scan(&post.ID, &post.UserID, &post.Username, &post.Word, &post.ImageURL, &post.CreatedAt)
	return post, err
}

func (db *DB) Firehose(limit int) ([]Post, error) {
	return db.posts(`where 1=1`, limit)
}

func (db *DB) PostsByUser(username string, limit int) ([]Post, error) {
	return db.posts(`where users.username = ?`, limit, username)
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
