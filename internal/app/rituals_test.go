package app

import (
	"testing"
	"time"
)

func TestStreakCountsConsecutiveDays(t *testing.T) {
	now := time.Date(2026, 6, 10, 15, 0, 0, 0, time.UTC)
	cases := []struct {
		name string
		days []string
		want int
	}{
		{"empty", nil, 0},
		{"today only", []string{"2026-06-10"}, 1},
		{"yesterday keeps streak alive", []string{"2026-06-09", "2026-06-08"}, 2},
		{"run ending today", []string{"2026-06-10", "2026-06-09", "2026-06-08"}, 3},
		{"gap inside run stops count", []string{"2026-06-10", "2026-06-08"}, 1},
		{"stale run is no streak", []string{"2026-06-07", "2026-06-06"}, 0},
	}
	for _, tc := range cases {
		if got := streak(tc.days, now); got != tc.want {
			t.Errorf("%s: expected streak %d, got %d", tc.name, tc.want, got)
		}
	}
}
