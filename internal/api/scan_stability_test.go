package api

import (
	"testing"
	"time"

	"github.com/coralsearesorts/hims/internal/storage/postgres/db"
)

func i16p(n int16) *int16 { return &n }

// TestDeviceNeedsClassification pins the server-side Missing-Classification
// predicate used by the badge count + scan-stability — it must match the
// frontend lib/classify.needsClassification (category unknown OR vendor missing
// OR low-confidence-and-unlocked).
func TestDeviceNeedsClassification(t *testing.T) {
	cases := []struct {
		name string
		dev  db.Device
		want bool
	}{
		{"unknown category", db.Device{Category: "unknown", Vendor: strp("Cisco")}, true},
		{"empty category", db.Device{Category: "", Vendor: strp("Cisco")}, true},
		{"missing vendor", db.Device{Category: "server", Vendor: nil}, true},
		{"blank vendor", db.Device{Category: "server", Vendor: strp("  ")}, true},
		{"low confidence unlocked", db.Device{Category: "switch", Vendor: strp("Cisco"), ConfidenceScore: i16p(30)}, true},
		{"low confidence but LOCKED", db.Device{Category: "switch", Vendor: strp("Cisco"), ConfidenceScore: i16p(30), ClassificationLocked: true}, false},
		{"fully classified", db.Device{Category: "switch", Vendor: strp("Cisco"), ConfidenceScore: i16p(95)}, false},
		{"classified no score", db.Device{Category: "switch", Vendor: strp("Cisco")}, false},
		{"zero score (uncomputed) not low-conf", db.Device{Category: "switch", Vendor: strp("Cisco"), ConfidenceScore: i16p(0)}, false},
	}
	for _, c := range cases {
		if got := deviceNeedsClassification(c.dev); got != c.want {
			t.Errorf("%s: deviceNeedsClassification = %v, want %v", c.name, got, c.want)
		}
	}
}

// TestMaxDur pins the retry-timeout floor logic: the Known-Device-Retry pass uses
// the LARGER of 2x the balanced timeout and a fixed floor, so a fast balanced
// profile still gets a forgiving retry window.
func TestMaxDur(t *testing.T) {
	// balanced tcp 800ms -> retry max(1600ms, 1500ms) = 1600ms
	if got := maxDur(800*time.Millisecond*2, 1500*time.Millisecond); got != 1600*time.Millisecond {
		t.Errorf("tcp retry = %v, want 1600ms", got)
	}
	// a very fast balanced tcp 500ms -> retry max(1000ms, 1500ms) = 1500ms floor
	if got := maxDur(500*time.Millisecond*2, 1500*time.Millisecond); got != 1500*time.Millisecond {
		t.Errorf("tcp retry floor = %v, want 1500ms", got)
	}
	// balanced snmp 2000ms -> retry max(4000ms, 4000ms) = 4000ms
	if got := maxDur(2000*time.Millisecond*2, 4000*time.Millisecond); got != 4000*time.Millisecond {
		t.Errorf("snmp retry = %v, want 4000ms", got)
	}
}
