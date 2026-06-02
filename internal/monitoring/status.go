// Package monitoring is HIMS's monitoring engine: it polls registered
// devices on a schedule and records a time-series of reachability samples.
//
// Monitoring is deliberately separate from discovery (PLAN §6). Discovery
// answers "what is on the network and what is it"; monitoring answers "is it
// still up, right now". This file holds the PURE state logic — no DB, no
// sockets — so the up→warning→down transition rules are unit-tested in
// isolation and reused identically by the scheduler.
package monitoring

// Status is a check's (and, rolled up, a device's) health.
type Status string

const (
	StatusUp      Status = "up"
	StatusDown    Status = "down"
	StatusWarning Status = "warning"
	StatusUnknown Status = "unknown"
)

// Evaluate computes the new status and consecutive-failure counter from a
// single poll outcome. This is the heart of the engine's hysteresis:
//
//   - A success always resets to up with zero failures.
//   - A failure increments the counter. Until it reaches downThreshold the
//     device is only "warning" (a transient blip shouldn't page anyone);
//     once it reaches the threshold it is firmly "down".
//
// downThreshold is clamped to >= 1, so a threshold of 1 means the first
// failure flips straight to down (no warning band).
//
// Evaluating the transition here — at the poll event, from the prior counter
// — rather than via a background sweep keeps the rule in one tested place
// (cf. "evaluate state transitions at transition time").
func Evaluate(ok bool, prevFailures, downThreshold int) (Status, int) {
	if downThreshold < 1 {
		downThreshold = 1
	}
	if ok {
		return StatusUp, 0
	}
	failures := prevFailures + 1
	if failures >= downThreshold {
		return StatusDown, failures
	}
	return StatusWarning, failures
}

// Worst returns the more severe of two statuses, for rolling several checks
// on one device into a single device badge. Order (worst→best):
// down > warning > unknown > up. Choosing unknown-over-up means a device with
// one never-run check doesn't falsely advertise "up".
func Worst(a, b Status) Status {
	if rank(a) >= rank(b) {
		return a
	}
	return b
}

func rank(s Status) int {
	switch s {
	case StatusDown:
		return 3
	case StatusWarning:
		return 2
	case StatusUnknown:
		return 1
	case StatusUp:
		return 0
	default:
		return 1
	}
}

// RollupDevice folds a device's check statuses into one. With no checks the
// device status is unknown.
func RollupDevice(statuses []Status) Status {
	if len(statuses) == 0 {
		return StatusUnknown
	}
	worst := StatusUp
	for _, s := range statuses {
		worst = Worst(worst, s)
	}
	return worst
}
