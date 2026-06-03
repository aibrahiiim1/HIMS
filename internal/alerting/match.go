// Package alerting is HIMS's rule-based alerting engine. Rules match against
// the state the monitoring engine produces (a check's current status +
// consecutive-failure count); a match opens an alert and, optionally,
// auto-creates a linked work order. Alerts auto-resolve when the underlying
// check recovers.
//
// This file holds the PURE matching predicate — no DB — so the rule
// semantics are unit-tested in isolation and reused by the engine.
package alerting

// Rule is the matching-relevant subset of an alert rule.
type Rule struct {
	TriggerStatus  string  // "down" | "warning"
	MinFailures    int     // consecutive-failure floor
	DeviceCategory *string // nil = any category
}

// CheckState is the matching-relevant subset of a monitoring check + device.
type CheckState struct {
	Status         string
	Failures       int
	DeviceCategory string
}

// Matches reports whether a rule fires for a check's current state. All three
// conditions must hold: the status equals the trigger status, the failure
// count is at or above the floor, and the category filter (if set) matches.
func Matches(r Rule, c CheckState) bool {
	if c.Status != r.TriggerStatus {
		return false
	}
	if c.Failures < r.MinFailures {
		return false
	}
	if r.DeviceCategory != nil && *r.DeviceCategory != c.DeviceCategory {
		return false
	}
	return true
}
