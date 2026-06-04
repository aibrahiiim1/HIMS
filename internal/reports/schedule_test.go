package reports

import (
	"testing"
	"time"
)

func TestIsDueTooEarly(t *testing.T) {
	now := time.Date(2026, 6, 4, 5, 0, 0, 0, time.UTC) // 05:00, before hour 6
	if IsDue("daily", 6, nil, now) {
		t.Error("should not be due before its hour")
	}
}

func TestIsDueNeverRun(t *testing.T) {
	now := time.Date(2026, 6, 4, 7, 0, 0, 0, time.UTC)
	if !IsDue("daily", 6, nil, now) {
		t.Error("never-run schedule past its hour should be due")
	}
}

func TestIsDueDaily(t *testing.T) {
	now := time.Date(2026, 6, 4, 7, 0, 0, 0, time.UTC)
	ranToday := time.Date(2026, 6, 4, 6, 30, 0, 0, time.UTC)
	if IsDue("daily", 6, &ranToday, now) {
		t.Error("already ran today → not due")
	}
	ranYesterday := time.Date(2026, 6, 3, 6, 30, 0, 0, time.UTC)
	if !IsDue("daily", 6, &ranYesterday, now) {
		t.Error("ran yesterday → due today")
	}
}

func TestIsDueWeeklyMonthly(t *testing.T) {
	now := time.Date(2026, 6, 4, 7, 0, 0, 0, time.UTC) // ISO week 23, June
	sameWeek := time.Date(2026, 6, 1, 6, 0, 0, 0, time.UTC)
	if IsDue("weekly", 6, &sameWeek, now) {
		t.Error("same ISO week → weekly not due")
	}
	lastWeek := time.Date(2026, 5, 25, 6, 0, 0, 0, time.UTC)
	if !IsDue("weekly", 6, &lastWeek, now) {
		t.Error("previous ISO week → weekly due")
	}
	sameMonth := time.Date(2026, 6, 1, 6, 0, 0, 0, time.UTC)
	if IsDue("monthly", 6, &sameMonth, now) {
		t.Error("same month → monthly not due")
	}
	lastMonth := time.Date(2026, 5, 31, 6, 0, 0, 0, time.UTC)
	if !IsDue("monthly", 6, &lastMonth, now) {
		t.Error("previous month → monthly due")
	}
}
