package api

import "testing"

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
