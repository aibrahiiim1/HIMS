// Package operations holds the pure business logic for the operations layer
// (work orders, systems/licenses) — the parts worth unit-testing without a
// DB. Storage + HTTP wiring live in their own packages.
package operations

import "time"

// LicenseStatus is the computed health of a license/support expiry, relative
// to "now". It is never stored — always derived so it can't go stale.
type LicenseStatus string

const (
	LicenseActive   LicenseStatus = "active"   // > 90 days out (or no expiry set)
	LicenseExpiring LicenseStatus = "expiring" // within 90 days
	LicenseDueSoon  LicenseStatus = "due_soon" // within 30 days
	LicenseCritical LicenseStatus = "critical" // within 7 days
	LicenseExpired  LicenseStatus = "expired"  // past
	LicenseUnknown  LicenseStatus = "unknown"  // no expiry date
)

// Expiry thresholds in days (the operator-facing alert windows).
const (
	thresholdExpiring = 90
	thresholdDueSoon  = 30
	thresholdCritical = 7
)

// ComputeLicenseStatus classifies an expiry date relative to now. A nil
// expiry is "unknown". now is passed in (not time.Now) so the logic is
// deterministic and testable.
func ComputeLicenseStatus(expiry *time.Time, now time.Time) LicenseStatus {
	if expiry == nil {
		return LicenseUnknown
	}
	// Compare by calendar day to avoid intra-day flapping.
	days := int(expiry.Sub(now).Hours() / 24)
	switch {
	case days < 0:
		return LicenseExpired
	case days <= thresholdCritical:
		return LicenseCritical
	case days <= thresholdDueSoon:
		return LicenseDueSoon
	case days <= thresholdExpiring:
		return LicenseExpiring
	default:
		return LicenseActive
	}
}

// WorstStatus returns the more urgent of two statuses — used to roll a
// system's license + support expiry into one headline status.
func WorstStatus(a, b LicenseStatus) LicenseStatus {
	rank := map[LicenseStatus]int{
		LicenseExpired: 5, LicenseCritical: 4, LicenseDueSoon: 3,
		LicenseExpiring: 2, LicenseActive: 1, LicenseUnknown: 0,
	}
	if rank[a] >= rank[b] {
		return a
	}
	return b
}
