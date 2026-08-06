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

// Discovery names a device after its IP when nothing better is known, and a
// later collection stores the real hostname WITHOUT rewriting the name. A port
// map that prints "172.21.60.101" for the machine everyone calls "CHV-DOF" is
// useless for its one job: telling an engineer which box is on which port.
func TestDeviceDisplayName(t *testing.T) {
	ip := netip.MustParseAddr("172.21.60.101")
	str := func(s string) *string { return &s }

	cases := []struct {
		name string
		dev  db.Device
		want string
	}{
		{
			name: "IP-named device falls back to the learned hostname",
			dev:  db.Device{Name: "172.21.60.101", Hostname: str("CHV-DOF"), PrimaryIp: &ip},
			want: "CHV-DOF",
		},
		{
			name: "a real name wins over the hostname (an operator may have set it)",
			dev:  db.Device{Name: "Reception PC", Hostname: str("CHV-DOF"), PrimaryIp: &ip},
			want: "Reception PC",
		},
		{
			name: "no hostname at all keeps the IP so the row is still identifiable",
			dev:  db.Device{Name: "172.21.60.101", PrimaryIp: &ip},
			want: "172.21.60.101",
		},
		{
			name: "a blank hostname is not treated as a name",
			dev:  db.Device{Name: "172.21.60.101", Hostname: str("   "), PrimaryIp: &ip},
			want: "172.21.60.101",
		},
		{
			name: "device with no IP keeps its name",
			dev:  db.Device{Name: "orphan"},
			want: "orphan",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := deviceDisplayName(tc.dev); got != tc.want {
				t.Errorf("deviceDisplayName = %q, want %q", got, tc.want)
			}
		})
	}
}
