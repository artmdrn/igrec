package store

import (
	"testing"
	"time"
)

func TestPostsOnThisDayReturnsOnlyPriorYears(t *testing.T) {
	db := testDB(t)
	user, err := db.CreateUser("memory", "memory@example.com")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	insert := func(word string, at time.Time) {
		t.Helper()
		if _, err := db.Exec(
			`insert into posts (user_id, word, created_at) values (?, ?, ?)`,
			user.ID, word, at.Format("2006-01-02 15:04:05"),
		); err != nil {
			t.Fatal(err)
		}
	}
	insert("lastyear", now.AddDate(-1, 0, 0))
	insert("twoyears", now.AddDate(-2, 0, 0))
	insert("today", now)
	insert("otherday", now.AddDate(-1, 0, 0).Add(-72*time.Hour))

	posts, err := db.PostsOnThisDay(user.ID, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(posts) != 2 {
		t.Fatalf("expected 2 anniversary posts, got %d", len(posts))
	}
	if posts[0].Word != "lastyear" || posts[1].Word != "twoyears" {
		t.Fatalf("expected lastyear then twoyears, got %q then %q", posts[0].Word, posts[1].Word)
	}
}

func TestPostDaysReturnsDistinctDaysNewestFirst(t *testing.T) {
	db := testDB(t)
	user, err := db.CreateUser("daily", "daily@example.com")
	if err != nil {
		t.Fatal(err)
	}
	for _, stamp := range []string{
		"2026-06-10 08:00:00",
		"2026-06-10 21:00:00",
		"2026-06-09 12:00:00",
		"2026-06-07 12:00:00",
	} {
		if _, err := db.Exec(
			`insert into posts (user_id, word, created_at) values (?, ?, ?)`,
			user.ID, "w", stamp,
		); err != nil {
			t.Fatal(err)
		}
	}

	days, err := db.PostDays(user.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"2026-06-10", "2026-06-09", "2026-06-07"}
	if len(days) != len(want) {
		t.Fatalf("expected %d days, got %d (%v)", len(want), len(days), days)
	}
	for i := range want {
		if days[i] != want[i] {
			t.Fatalf("expected day %d to be %s, got %s", i, want[i], days[i])
		}
	}
}
