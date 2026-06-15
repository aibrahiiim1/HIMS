package api

import (
	"testing"
	"time"
)

// TestShouldSkipCCTV pins the fleet skip-guard: a device that auth-failed over
// ONVIF/ISAPI within the window is skipped (so repeated fleet runs don't keep
// hammering a wrong credential toward a Hikvision lockout), but an old failure, a
// success, or a non-auth failure is still attempted.
func TestShouldSkipCCTV(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	const window = 6 * time.Hour
	cases := []struct {
		name     string
		category string
		success  bool
		age      time.Duration
		wantSkip bool
	}{
		{"recent auth failure", "auth_failed", false, 1 * time.Hour, true},
		{"auth failure at edge (still inside)", "auth_failed", false, 5*time.Hour + 59*time.Minute, true},
		{"stale auth failure", "auth_failed", false, 8 * time.Hour, false},
		{"recent success", "success", true, 1 * time.Hour, false},
		{"recent unreachable (not auth)", "isapi_timeout", false, 1 * time.Hour, false},
		{"recent connection refused (not auth)", "connection_refused", false, 30 * time.Minute, false},
	}
	for _, c := range cases {
		got := shouldSkipCCTV(c.category, c.success, now.Add(-c.age), now, window)
		if got != c.wantSkip {
			t.Errorf("%s: shouldSkipCCTV=%v, want %v", c.name, got, c.wantSkip)
		}
	}
}

// TestISAPIAuthRejectedIsAuthFailed pins the CCTV Phase 2 fix: the ISAPI error
// string "authentication rejected" must categorise as auth_failed (not the
// generic collection_error bucket), so the fleet report classes it as an auth
// failure rather than an opaque error.
func TestISAPIAuthRejectedIsAuthFailed(t *testing.T) {
	reason, _ := categorizeCollectErr("isapi", "isapi: authentication rejected")
	if reason != "auth_failed" {
		t.Errorf("ISAPI 'authentication rejected' → %q, want auth_failed", reason)
	}
}

// TestFleetOutcomeBuckets locks the failure-class mapping the operator report
// relies on — in particular that the lockout-avoidance guidance attached to
// every auth failure does NOT get mislabelled as a real lockout, and that a
// genuine device lock phrase does.
func TestFleetOutcomeBuckets(t *testing.T) {
	cases := []struct {
		name, reason, detail, want string
	}{
		{"auth with lockout-hint", "auth_failed",
			"authentication rejected — use the device WEB login; repeated failures can trigger a Hikvision IP lockout", "auth"},
		{"no credential mentions lockout", "no_credential",
			"no ONVIF/HTTP web credential bound — Collection never sprays, to avoid a Hikvision IP lockout.", "no_credential"},
		{"genuine device lock", "auth_failed", "device returned: illegal login, ip is locked", "lockout"},
		{"isapi timeout", "isapi_timeout", "ISAPI timed out (host slow, firewalled, or port filtered)", "unreachable"},
		{"connection refused", "connection_refused", "ISAPI connection refused", "unreachable"},
		{"not exposed", "", "/ISAPI/System/Video/inputs/channels not exposed by device", "unsupported"},
		{"collected has no failure mapping", "", "collected via ISAPI", "error"},
	}
	for _, c := range cases {
		if got := fleetOutcome(c.reason, c.detail); got != c.want {
			t.Errorf("%s: fleetOutcome(%q,…) = %q, want %q", c.name, c.reason, got, c.want)
		}
	}
}
