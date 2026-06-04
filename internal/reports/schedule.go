package reports

import "time"

// IsDue reports whether a schedule should run now. A schedule fires once per
// period (day/week/month) at or after its hour_utc, and only if it hasn't
// already run in the current period. This is pure so the dispatcher's timing is
// unit-testable.
//
//   - frequency: "daily" | "weekly" | "monthly"
//   - hourUTC:   the hour-of-day (UTC) the report should go out
//   - lastRun:   when it last ran (nil = never)
//   - now:       current time (UTC)
func IsDue(frequency string, hourUTC int, lastRun *time.Time, now time.Time) bool {
	if now.Hour() < hourUTC {
		return false // too early in the day
	}
	if lastRun == nil {
		return true // never run and we're past the hour today
	}
	last := lastRun.UTC()
	switch frequency {
	case "daily":
		return !sameDay(last, now)
	case "weekly":
		y1, w1 := last.ISOWeek()
		y2, w2 := now.ISOWeek()
		return y1 != y2 || w1 != w2
	case "monthly":
		return last.Year() != now.Year() || last.Month() != now.Month()
	default:
		return !sameDay(last, now)
	}
}

func sameDay(a, b time.Time) bool {
	return a.Year() == b.Year() && a.YearDay() == b.YearDay()
}
