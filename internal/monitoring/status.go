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

// RollupDeviceWithSupplemental folds a device's checks into one status while
// honouring the reachability/supplemental split:
//
//   - REACHABILITY checks decide whether the device is up/down/warning/unknown.
//     They alone can mark a device "down" (offline) — that drives the inventory
//     online/offline counts.
//   - SUPPLEMENTAL checks (extra ports/metrics an operator added) can only
//     DEGRADE a reachable device to "warning" (needs attention). A failing extra
//     check is informational: it lowers the health score and is surfaced to the
//     operator, but it must never flip the device to "down"/offline or inflate
//     the offline count. (cf. requirement: "an extra check offline must not make
//     the whole device offline, but I want to know it's degraded".)
//
// If the device has no reachability checks at all, supplemental checks are used
// as the fallback signal (so a device isn't blind), but still capped at warning
// so an extra check can never report a hard offline on its own.
func RollupDeviceWithSupplemental(reachability, supplemental []Status) Status {
	if len(reachability) == 0 && len(supplemental) == 0 {
		return StatusUnknown
	}
	var dev Status
	if len(reachability) > 0 {
		dev = RollupDevice(reachability)
	} else {
		// No reachability check: fall back to the supplemental signal but cap at
		// warning — extra checks never assert a hard offline by themselves.
		dev = capAtWarning(RollupDevice(supplemental))
		return dev
	}
	// Reachability says the device is reachable (or merely unknown). If any
	// supplemental check is unhealthy, degrade to "warning" — never "down".
	if dev == StatusUp || dev == StatusUnknown {
		if w := RollupDevice(supplemental); w == StatusDown || w == StatusWarning {
			dev = StatusWarning
		}
	}
	return dev
}

// capAtWarning turns a "down" into "warning"; everything else is unchanged.
func capAtWarning(s Status) Status {
	if s == StatusDown {
		return StatusWarning
	}
	return s
}
