package api

import (
	"testing"

	"github.com/coralsearesorts/hims/internal/topology"
	"github.com/google/uuid"
)

func i64(v int64) *int64 { return &v }
func s(v string) *string { return &v }

// The true point of attachment is the switch port with the FEWEST learned MACs — an edge
// port carrying just this device — not a trunk/uplink that also saw the MAC among hundreds.
func TestEdgeSwitchPort_PicksFewestMACs(t *testing.T) {
	trunk := topology.SwitchPortEntry{SwitchID: uuid.New(), SwitchName: "core-sw", IfName: s("Te1/0/1"), MACCount: i64(312)}
	edge := topology.SwitchPortEntry{SwitchID: uuid.New(), SwitchName: "access-sw", IfName: s("Gi1/0/7"), MACCount: i64(1)}
	mid := topology.SwitchPortEntry{SwitchID: uuid.New(), SwitchName: "dist-sw", IfName: s("Gi2/0/3"), MACCount: i64(42)}

	got := edgeSwitchPort([]topology.SwitchPortEntry{trunk, mid, edge})
	if got == nil {
		t.Fatal("expected an edge port, got nil")
	}
	if got.SwitchName != "access-sw" || got.IfName == nil || *got.IfName != "Gi1/0/7" {
		t.Fatalf("expected access-sw Gi1/0/7 (fewest MACs), got %s %v", got.SwitchName, got.IfName)
	}
}

// A nil MACCount sorts as effectively unbounded, so a port with a known small count wins.
func TestEdgeSwitchPort_KnownCountBeatsUnknown(t *testing.T) {
	unknown := topology.SwitchPortEntry{SwitchName: "sw-a", MACCount: nil}
	known := topology.SwitchPortEntry{SwitchName: "sw-b", MACCount: i64(2)}
	got := edgeSwitchPort([]topology.SwitchPortEntry{unknown, known})
	if got == nil || got.SwitchName != "sw-b" {
		t.Fatalf("expected sw-b (known count) to win over unknown, got %+v", got)
	}
}

func TestEdgeSwitchPort_Empty(t *testing.T) {
	if got := edgeSwitchPort(nil); got != nil {
		t.Fatalf("expected nil for empty input, got %+v", got)
	}
}
