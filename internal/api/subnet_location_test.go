package api

import (
	"net/netip"
	"testing"

	"github.com/coralsearesorts/hims/internal/storage/postgres/db"
	"github.com/google/uuid"
)

// A device whose IP falls inside a configured site subnet must resolve to that
// site even on an unscoped scan (locID == nil) — this is the .181 reconcile bug:
// 172.21.60.181 is inside 172.21.60.0/24 (→ CHR) and must no longer be left with
// a null location. Narrowest-prefix wins; an IP in no subnet stays unassigned;
// and a device from another subnet is NOT misassigned to CHR.
func TestSubnetLocationFor(t *testing.T) {
	chr := uuid.New()
	other := uuid.New()
	wide := uuid.New()
	mustPfx := func(s string) netip.Prefix {
		p, err := netip.ParsePrefix(s)
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	subs := []db.Subnet{
		{LocationID: chr, Cidr: mustPfx("172.21.60.0/24")},
		{LocationID: other, Cidr: mustPfx("172.21.96.0/24")},
		{LocationID: wide, Cidr: mustPfx("172.21.0.0/16")}, // overlaps .60 and .96 — must lose to /24
	}
	mustIP := func(s string) netip.Addr {
		a, err := netip.ParseAddr(s)
		if err != nil {
			t.Fatal(err)
		}
		return a
	}

	if got := subnetLocationFor(subs, mustIP("172.21.60.181")); got == nil || *got != chr {
		t.Errorf(".181 → %v, want CHR (%v)", got, chr)
	}
	if got := subnetLocationFor(subs, mustIP("172.21.96.50")); got == nil || *got != other {
		t.Errorf(".96.50 must resolve to its own site, not CHR: got %v", got)
	}
	// Inside the /16 only (not the /24s) → the wide site, not CHR.
	if got := subnetLocationFor(subs, mustIP("172.21.5.5")); got == nil || *got != wide {
		t.Errorf("172.21.5.5 → %v, want wide /16 site", got)
	}
	// No configured subnet contains it → unassigned (honest "needs site").
	if got := subnetLocationFor(subs, mustIP("10.0.0.1")); got != nil {
		t.Errorf("unmatched IP must stay unassigned, got %v", got)
	}
}
