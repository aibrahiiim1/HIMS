package api

import (
	"net/http"
	"net/netip"
	"testing"

	"github.com/google/uuid"
)

func TestMatchSubnet(t *testing.T) {
	group := uuid.New()
	chr := uuid.New()
	cac := uuid.New()

	mustP := func(s string) netip.Prefix {
		p, err := netip.ParsePrefix(s)
		if err != nil {
			t.Fatalf("bad prefix %q: %v", s, err)
		}
		return p
	}
	mustA := func(s string) netip.Addr {
		a, err := netip.ParseAddr(s)
		if err != nil {
			t.Fatalf("bad addr %q: %v", s, err)
		}
		return a
	}

	cands := []subnetCandidate{
		{Prefix: mustP("172.21.96.0/24"), LocID: group, Rank: 0}, // umbrella group
		{Prefix: mustP("172.21.96.0/24"), LocID: chr, Rank: 1},   // hotel — more specific
		{Prefix: mustP("172.21.60.0/24"), LocID: cac, Rank: 1},
	}

	// IP inside the doubly-declared /24 → the hotel (higher kind rank) wins,
	// not the umbrella group.
	if got, ok := matchSubnet(mustA("172.21.96.39"), cands); !ok || got != chr {
		t.Errorf("96.39 → %v %v, want CHR(%v) true", got, ok, chr)
	}
	// IP in CAC's subnet.
	if got, ok := matchSubnet(mustA("172.21.60.10"), cands); !ok || got != cac {
		t.Errorf("60.10 → %v %v, want CAC(%v) true", got, ok, cac)
	}
	// IP matching no declared subnet → no guess.
	if _, ok := matchSubnet(mustA("172.21.211.5"), cands); ok {
		t.Error("211.5 matches no subnet; must not be assigned")
	}

	// Longest-prefix wins over kind rank: a /28 group beats an overlapping /24 hotel.
	specific := uuid.New()
	cands2 := []subnetCandidate{
		{Prefix: mustP("172.21.96.0/24"), LocID: chr, Rank: 1},
		{Prefix: mustP("172.21.96.32/28"), LocID: specific, Rank: 0},
	}
	if got, ok := matchSubnet(mustA("172.21.96.39"), cands2); !ok || got != specific {
		t.Errorf("longest-prefix should win: got %v %v, want %v", got, ok, specific)
	}
}

func TestRequiredPermissionDataQuality(t *testing.T) {
	if got := requiredPermission(http.MethodGet, "/api/v1/data-quality"); got != "" {
		t.Errorf("GET data-quality = %q, want authenticated-only", got)
	}
	if got := requiredPermission(http.MethodPost, "/api/v1/data-quality/reconcile-sites"); got != "devices.write" {
		t.Errorf("POST reconcile-sites = %q, want devices.write", got)
	}
}
