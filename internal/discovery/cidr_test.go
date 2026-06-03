package discovery

import (
	"net/netip"
	"testing"
)

func TestExpandCIDR_SkipsNetworkAndBroadcast(t *testing.T) {
	ips, err := ExpandCIDR(netip.MustParsePrefix("192.168.1.0/29"), 1000)
	if err != nil {
		t.Fatal(err)
	}
	// /29 = 8 addresses; usable = 6 (skip .0 + .7).
	if len(ips) != 6 {
		t.Fatalf("got %d hosts; want 6", len(ips))
	}
	if ips[0].String() != "192.168.1.1" || ips[5].String() != "192.168.1.6" {
		t.Fatalf("range = %v..%v; want .1...6", ips[0], ips[5])
	}
}

func TestExpandCIDR_Slash32And31(t *testing.T) {
	one, _ := ExpandCIDR(netip.MustParsePrefix("10.0.0.5/32"), 10)
	if len(one) != 1 || one[0].String() != "10.0.0.5" {
		t.Fatalf("/32 = %v; want single .5", one)
	}
	two, _ := ExpandCIDR(netip.MustParsePrefix("10.0.0.4/31"), 10)
	if len(two) != 2 { // /31 has no network/broadcast (RFC 3021)
		t.Fatalf("/31 = %d hosts; want 2", len(two))
	}
}

func TestExpandCIDR_RefusesOversizedScope(t *testing.T) {
	if _, err := ExpandCIDR(netip.MustParsePrefix("10.0.0.0/16"), 1000); err == nil {
		t.Fatal("/16 (65k hosts) should exceed a 1000 cap")
	}
	if _, err := ExpandCIDR(netip.MustParsePrefix("10.0.0.0/8"), 1_000_000); err == nil {
		t.Fatal("/8 should be refused before allocating")
	}
}

func TestExpandCIDR_NormalizesUnmasked(t *testing.T) {
	// A host-bit-set prefix is masked first.
	ips, err := ExpandCIDR(netip.MustParsePrefix("192.168.1.5/29"), 100)
	if err != nil {
		t.Fatal(err)
	}
	if ips[0].String() != "192.168.1.1" {
		t.Fatalf("first = %v; want .1 (masked)", ips[0])
	}
}
