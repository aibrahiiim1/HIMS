package operations

import (
	"testing"
	"time"
)

func TestComputeLicenseStatus(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	at := func(days int) *time.Time {
		d := now.Add(time.Duration(days) * 24 * time.Hour)
		return &d
	}
	cases := []struct {
		name   string
		expiry *time.Time
		want   LicenseStatus
	}{
		{"nil", nil, LicenseUnknown},
		{"past", at(-1), LicenseExpired},
		{"today-ish", at(0), LicenseCritical},
		{"5 days", at(5), LicenseCritical},
		{"20 days", at(20), LicenseDueSoon},
		{"60 days", at(60), LicenseExpiring},
		{"200 days", at(200), LicenseActive},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ComputeLicenseStatus(tc.expiry, now); got != tc.want {
				t.Errorf("ComputeLicenseStatus(%v) = %s, want %s", tc.expiry, got, tc.want)
			}
		})
	}
}

func TestWorstStatus(t *testing.T) {
	if WorstStatus(LicenseActive, LicenseExpired) != LicenseExpired {
		t.Error("expired should win over active")
	}
	if WorstStatus(LicenseDueSoon, LicenseExpiring) != LicenseDueSoon {
		t.Error("due_soon (30d) is more urgent than expiring (90d)")
	}
	if WorstStatus(LicenseUnknown, LicenseActive) != LicenseActive {
		t.Error("active should win over unknown")
	}
}
