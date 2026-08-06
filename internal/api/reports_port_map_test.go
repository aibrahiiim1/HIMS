package api

import (
	"net/netip"
	"strings"
	"testing"

	"github.com/coralsearesorts/hims/internal/storage/postgres/db"
)

// The VLAN column must never show a value that is not a real 802.1Q VLAN.
// Some switches report the bridge/FdbId index (commonly 0) when their forwarding
// table is not VLAN-aware; printing "0" reads as a genuine VLAN 0 and is worse
// than an honest blank with a reason.
func TestPortMapVLANColumn(t *testing.T) {
	i32 := func(v int32) *int32 { return &v }
	str := func(v string) *string { return &v }

	cases := []struct {
		name       string
		untagged   *int32
		untagName  *string
		fdbVLAN    int32
		suspect    bool
		wantVLAN   string
		wantNameIn string // substring expected in the VLAN name cell ("" = don't care)
	}{
		{
			name:     "authoritative untagged VLAN wins",
			untagged: i32(96), untagName: str("MGT"), fdbVLAN: 1,
			wantVLAN: "96", wantNameIn: "MGT",
		},
		{
			name:    "FDB VLAN used when no per-port VLAN is known",
			fdbVLAN: 60, wantVLAN: "60",
		},
		{
			name:    "bridge index 0 is not a VLAN",
			fdbVLAN: 0, wantVLAN: "", wantNameIn: "no usable VLAN",
		},
		{
			name:    "out-of-range VLAN is not asserted",
			fdbVLAN: 5000, wantVLAN: "", wantNameIn: "no usable VLAN",
		},
		{
			name:    "suspect VLAN is not asserted and says so",
			fdbVLAN: 777, suspect: true, wantVLAN: "", wantNameIn: "not a configured VLAN",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotVLAN, gotName := portMapVLAN(tc.untagged, tc.untagName, tc.fdbVLAN, tc.suspect, nil)
			if gotVLAN != tc.wantVLAN {
				t.Errorf("VLAN = %q, want %q", gotVLAN, tc.wantVLAN)
			}
			if tc.wantNameIn != "" && !strings.Contains(gotName, tc.wantNameIn) {
				t.Errorf("VLAN name = %q, want it to contain %q", gotName, tc.wantNameIn)
			}
		})
	}
}

// Every device gets a row; one with no attachment must carry a REASON, never a
// silent blank — a device missing from a port map reads as "not on the network".
func TestUnresolvedReasonIsAlwaysExplained(t *testing.T) {
	if r := unresolvedReason(db.Device{Category: "endpoint"}); r == "" {
		t.Error("a device with no IP must still get a reason")
	} else if !strings.Contains(r, "no IP") {
		t.Errorf("reason should name the missing IP, got: %s", r)
	}
	ip := netip.MustParseAddr("10.0.0.5")
	d := db.Device{Category: "endpoint", PrimaryIp: &ip}
	r := unresolvedReason(d)
	if r == "" {
		t.Fatal("an unresolved device must always carry a reason")
	}
	// It must point at the actionable causes, not just say "unknown".
	for _, want := range []string{"SNMP", "FDB"} {
		if !strings.Contains(r, want) {
			t.Errorf("reason should mention %q so the operator knows what to fix; got: %s", want, r)
		}
	}
}
