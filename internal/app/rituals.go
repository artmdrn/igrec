package app

import (
	"net/http"
	"time"
)

func (a *App) about(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	a.render(w, r, "about.html", map[string]any{
		"PageTitle":     "about igrec",
		"OGDescription": "one word at a time. no likes, no follower counts, no algorithm.",
	})
}

// streak counts consecutive posting days ending today or yesterday, so
// it survives until a full day has actually been missed. days are UTC
// dates (YYYY-MM-DD) sorted newest first.
func streak(days []string, now time.Time) int {
	if len(days) == 0 {
		return 0
	}
	today := now.UTC().Truncate(24 * time.Hour)
	head, err := time.Parse("2006-01-02", days[0])
	if err != nil {
		return 0
	}
	gap := int(today.Sub(head).Hours() / 24)
	if gap > 1 {
		return 0
	}
	count := 1
	expect := head.AddDate(0, 0, -1)
	for _, raw := range days[1:] {
		day, err := time.Parse("2006-01-02", raw)
		if err != nil || !day.Equal(expect) {
			break
		}
		count++
		expect = expect.AddDate(0, 0, -1)
	}
	return count
}

// userStreak is the user's private streak for /write and /settings.
// It is never exposed on public pages or public APIs.
func (a *App) userStreak(userID int64) int {
	days, err := a.db.PostDays(userID, 400)
	if err != nil {
		return 0
	}
	return streak(days, time.Now())
}
