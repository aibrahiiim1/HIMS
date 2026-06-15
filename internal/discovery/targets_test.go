package discovery

import (
	"net/netip"
	"testing"
)

func TestParseTargets(t *testing.T) {
	cases := []struct {
		name string
		spec string
		want []string
	}{
		{"single", "10.0.0.5", []string{"10.0.0.5"}},
		{"range full", "10.0.0.1-10.0.0.3", []string{"10.0.0.1", "10.0.0.2", "10.0.0.3"}},
		{"range shorthand", "172.21.96.1-3", []string{"172.21.96.1", "172.21.96.2", "172.21.96.3"}},
		{"cidr /30", "10.0.0.0/30", []string{"10.0.0.1", "10.0.0.2"}}, // skips net+broadcast
		{"mixed + dedup", "10.0.0.5, 10.0.0.5\n10.0.0.6", []string{"10.0.0.5", "10.0.0.6"}},
		{"mixed kinds", "10.0.0.1-2; 10.0.0.1/32", []string{"10.0.0.1", "10.0.0.2"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseTargets(tc.spec, 4096)
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("got %d hosts %v; want %d %v", len(got), got, len(tc.want), tc.want)
			}
			for i, w := range tc.want {
				if got[i] != netip.MustParseAddr(w) {
					t.Errorf("host[%d] = %s; want %s", i, got[i], w)
				}
			}
		})
	}
}

func TestParseTargets_Errors(t *testing.T) {
	for _, spec := range []string{
		"",                        // empty
		"not-an-ip",               // garbage
		"10.0.0.10-10.0.0.1",      // reversed
		"10.0.0.0/8",              // too large
		"10.0.0.1-300",            // bad octet
		"2001:db8::1-2001:db8::5", // ipv6 range unsupported
	} {
		if _, err := ParseTargets(spec, 4096); err == nil {
			t.Errorf("spec %q should error", spec)
		}
	}
}

func TestParseTargets_CapEnforced(t *testing.T) {
	if _, err := ParseTargets("10.0.0.1-10.0.0.100", 10); err == nil {
		t.Fatal("range exceeding maxHosts should error")
	}
}

func TestFilterExcluded(t *testing.T) {
	hosts, err := ParseTargets("172.21.96.1-172.21.96.10", 4096)
	if err != nil {
		t.Fatal(err)
	}
	// Empty exclude is a no-op.
	if got, n, err := FilterExcluded(hosts, "", 4096); err != nil || n != 0 || len(got) != 10 {
		t.Fatalf("empty exclude: got %d removed=%d err=%v; want 10/0/nil", len(got), n, err)
	}
	// Exclude a single IP, a list, and a sub-range — order preserved, only the
	// named hosts removed. (.3 appears in both the list and the range — dedup.)
	got, removed, err := FilterExcluded(hosts, "172.21.96.2, 172.21.96.3\n172.21.96.3-5", 4096)
	if err != nil {
		t.Fatal(err)
	}
	if removed != 4 { // .2, .3, .4, .5
		t.Fatalf("removed = %d; want 4", removed)
	}
	want := []string{"172.21.96.1", "172.21.96.6", "172.21.96.7", "172.21.96.8", "172.21.96.9", "172.21.96.10"}
	if len(got) != len(want) {
		t.Fatalf("got %d hosts %v; want %d", len(got), got, len(want))
	}
	for i, w := range want {
		if got[i] != netip.MustParseAddr(w) {
			t.Errorf("host[%d] = %s; want %s", i, got[i], w)
		}
	}
	// Excluding an IP outside the scope removes nothing.
	if _, n, _ := FilterExcluded(hosts, "10.9.9.9", 4096); n != 0 {
		t.Fatalf("out-of-scope exclude removed %d; want 0", n)
	}
	// A CIDR exclude carves out the whole block.
	if _, n, _ := FilterExcluded(hosts, "172.21.96.0/29", 4096); n != 6 { // .1..6 are in-scope hosts of /29
		t.Fatalf("CIDR exclude removed %d; want 6", n)
	}
}
