package api

import (
	"net/netip"
	"testing"

	"github.com/coralsearesorts/hims/internal/storage/postgres/db"
	"github.com/google/uuid"
)

func devAt(ip string, site *uuid.UUID) db.Device {
	a := netip.MustParseAddr(ip)
	return db.Device{PrimaryIp: &a, LocationID: site}
}

// The production failure this exists to surface: 78 devices on an unmapped /24
// had no site, so relay routing refused every one of them while a healthy agent
// sat on that very subnet. Naming the SUBNET makes it one fix, not 78.
func TestUnmappedSubnetGaps(t *testing.T) {
	site := uuid.New()
	mapped := []netip.Prefix{netip.MustParsePrefix("172.21.96.0/24")}

	devs := []db.Device{
		devAt("172.21.96.10", &site), // mapped subnet, has a site
		devAt("172.21.96.11", nil),   // mapped subnet, no site -> NOT a subnet gap
		devAt("172.21.60.20", nil),   // unmapped subnet, no site -> gap
		devAt("172.21.60.21", nil),   // same gap
		devAt("172.21.60.99", &site), // unmapped subnet but site set explicitly -> not a gap
		devAt("10.0.0.5", nil),       // a second, separate gap
	}

	rows, total := unmappedSubnetGaps(devs, mapped)

	if total != 3 {
		t.Errorf("want 3 site-less devices in unmapped subnets, got %d", total)
	}
	if len(rows) != 2 {
		t.Fatalf("want 2 distinct subnets reported, got %d: %+v", len(rows), rows)
	}
	// Sorted by subnet string: 10.0.0.0/24 before 172.21.60.0/24.
	if rows[0].Name != "10.0.0.0/24" {
		t.Errorf("rows[0] = %q, want 10.0.0.0/24 (sorted)", rows[0].Name)
	}
	if rows[1].Name != "172.21.60.0/24" {
		t.Errorf("rows[1] = %q, want 172.21.60.0/24", rows[1].Name)
	}
	if rows[1].Category != "subnet" {
		t.Errorf("the subject is a subnet, not a device; got category %q", rows[1].Category)
	}
	if rows[1].PrimaryIP == "" {
		t.Error("a sample address helps the operator recognise the subnet")
	}
}

// A fully-mapped estate must report nothing — no false positives, which is the
// state production is in after remediation.
func TestUnmappedSubnetGaps_NothingWhenAllMapped(t *testing.T) {
	site := uuid.New()
	mapped := []netip.Prefix{
		netip.MustParsePrefix("172.21.96.0/24"),
		netip.MustParsePrefix("172.21.60.0/24"),
	}
	devs := []db.Device{
		devAt("172.21.96.10", &site),
		devAt("172.21.60.20", &site),
		devAt("172.21.60.21", nil), // no site, but its subnet IS mapped
	}
	rows, total := unmappedSubnetGaps(devs, mapped)
	if total != 0 || len(rows) != 0 {
		t.Errorf("want no gaps when every subnet is mapped, got total=%d rows=%+v", total, rows)
	}
}

// Devices with no IP must not crash or be counted.
func TestUnmappedSubnetGaps_NoIP(t *testing.T) {
	rows, total := unmappedSubnetGaps([]db.Device{{}}, nil)
	if total != 0 || len(rows) != 0 {
		t.Errorf("a device with no IP is not a subnet gap, got total=%d rows=%+v", total, rows)
	}
}
