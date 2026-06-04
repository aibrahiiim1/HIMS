package operations

import "time"

// SLA (#19 Work Orders): the response/resolution target for a work order is a
// policy of its priority. The deadline is derived (created_at + policy) rather
// than stored, so changing a ticket's priority re-targets it from creation —
// the conventional SLA semantic — with no extra column to keep in sync.

// SLAStatus is a work order's standing against its SLA deadline.
type SLAStatus string

const (
	SLANone     SLAStatus = "none"      // no deadline applies
	SLAOnTrack  SLAStatus = "on_track"  // open, comfortably before the deadline
	SLADueSoon  SLAStatus = "due_soon"  // open, within the final quarter of the window
	SLABreached SLAStatus = "breached"  // open past deadline, or resolved late
	SLAMet      SLAStatus = "met"       // resolved on or before the deadline
)

// SLAMinutes is the resolution target per priority.
func SLAMinutes(priority string) int {
	switch priority {
	case "critical":
		return 4 * 60       // 4 hours
	case "high":
		return 24 * 60      // 1 day
	case "medium":
		return 3 * 24 * 60  // 3 days
	case "low":
		return 7 * 24 * 60  // 7 days
	default:
		return 3 * 24 * 60
	}
}

// SLADeadline returns the SLA deadline for a ticket created at createdAt with
// the given priority.
func SLADeadline(createdAt time.Time, priority string) time.Time {
	return createdAt.Add(time.Duration(SLAMinutes(priority)) * time.Minute)
}

// terminalStatus reports whether a work-order status means "done".
func terminalStatus(status string) bool {
	return status == "solved" || status == "closed"
}

// ComputeSLAStatus evaluates a work order against its SLA. For terminal tickets
// it compares the resolution time (resolvedAt, or now if missing) to the
// deadline; for active tickets it reports breached / due_soon / on_track based
// on time remaining (due_soon = within the last 25% of the window).
func ComputeSLAStatus(createdAt time.Time, priority, status string, resolvedAt *time.Time, now time.Time) SLAStatus {
	deadline := SLADeadline(createdAt, priority)
	if terminalStatus(status) {
		ref := now
		if resolvedAt != nil {
			ref = *resolvedAt
		}
		if ref.After(deadline) {
			return SLABreached
		}
		return SLAMet
	}
	if !now.Before(deadline) {
		return SLABreached
	}
	window := time.Duration(SLAMinutes(priority)) * time.Minute
	if deadline.Sub(now) <= window/4 {
		return SLADueSoon
	}
	return SLAOnTrack
}
