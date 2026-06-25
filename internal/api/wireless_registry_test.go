package api

import (
	"errors"
	"testing"
)

// TestWirelessCatalogInvariants guards the driver catalog: keys + profile types
// are unique, every driver declares capabilities, and the public-key ⇆
// profile-vendor-type mapping round-trips. A broken catalog would silently
// desync the form, the test endpoint, and collection routing.
func TestWirelessCatalogInvariants(t *testing.T) {
	keys := map[string]bool{}
	ptypes := map[string]bool{}
	for _, v := range wirelessVendors {
		if v.Key == "" || v.profileVendorType == "" {
			t.Fatalf("driver %q missing key or profileVendorType", v.DisplayName)
		}
		if keys[v.Key] {
			t.Fatalf("duplicate driver key %q", v.Key)
		}
		keys[v.Key] = true
		if ptypes[v.profileVendorType] {
			t.Fatalf("duplicate profileVendorType %q", v.profileVendorType)
		}
		ptypes[v.profileVendorType] = true
		if v.deviceVendor == "" {
			t.Errorf("driver %q has no deviceVendor label", v.Key)
		}
		if len(v.Capabilities) == 0 {
			t.Errorf("driver %q declares no capabilities", v.Key)
		}
		// A working driver must declare at least one supported capability; a gated
		// driver must NOT (no fake "supported" on a collector that does not exist).
		hasSupported := false
		for _, c := range v.Capabilities {
			if c.Status == capSupported {
				hasSupported = true
			}
		}
		switch v.Status {
		case wlStatusWorking:
			if !hasSupported {
				t.Errorf("working driver %q declares no supported capability", v.Key)
			}
		case wlStatusCollectorPending:
			if hasSupported {
				t.Errorf("collector_pending driver %q must not declare a supported capability", v.Key)
			}
		}
		// Round-trip: profile type → public key.
		if got := wlVendorKeyForProfileType(v.profileVendorType); got != v.Key {
			t.Errorf("profileVendorType %q mapped to %q, want %q", v.profileVendorType, got, v.Key)
		}
		if got, ok := wlVendorByKey(v.Key); !ok || got.profileVendorType != v.profileVendorType {
			t.Errorf("wlVendorByKey(%q) round-trip failed", v.Key)
		}
	}
	// The vendors the operator named must all be present.
	for _, want := range []string{"ruckus_zd", "extreme_xcc", "ruckus_sz", "unifi", "omada", "aruba"} {
		if !keys[want] {
			t.Errorf("catalog is missing required driver %q", want)
		}
	}
}

func TestClassifyProbeErr(t *testing.T) {
	cases := []struct {
		err           error
		wantReachable bool
	}{
		{errors.New("dial tcp 10.0.0.1:443: connect: connection refused"), false},
		{errors.New("context deadline exceeded"), false},
		{errors.New("401 Unauthorized: invalid credentials"), true},
		{errors.New("login rejected: bad password"), true},
	}
	for _, c := range cases {
		gotReachable, reason := classifyProbeErr(c.err)
		if gotReachable != c.wantReachable {
			t.Errorf("classifyProbeErr(%q) reachable=%v, want %v", c.err, gotReachable, c.wantReachable)
		}
		if reason == "" {
			t.Errorf("classifyProbeErr(%q) returned empty reason", c.err)
		}
	}
}
