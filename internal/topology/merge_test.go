package topology

import (
	"net/netip"
	"testing"

	"github.com/coralsearesorts/hims/internal/driver"
)

func TestNeighborMerge_DedupesLLDPAndCDP(t *testing.T) {
	mgmt := netip.MustParseAddr("172.21.96.1")
	in := []driver.NeighborSnap{
		// Same neighbor seen via CDP (with mgmt IP + platform)…
		{LocalIfIndex: 48, RemSysName: "Core-Switch", RemPortID: "Gi1/0/1", RemMgmtIP: &mgmt, Protocol: "cdp"},
		// …and via LLDP (richer port id, no mgmt IP).
		{LocalIfIndex: 48, RemSysName: "Core-Switch", RemPortID: "GigabitEthernet1/0/1", Protocol: "lldp"},
	}
	out := NeighborMerge(in)
	if len(out) != 1 {
		t.Fatalf("LLDP+CDP for one neighbor should merge to 1, got %d", len(out))
	}
	n := out[0]
	if n.Protocol != "lldp" {
		t.Errorf("LLDP should win for identity, got %s", n.Protocol)
	}
	if n.RemMgmtIP == nil || n.RemMgmtIP.String() != "172.21.96.1" {
		t.Errorf("CDP management IP should be folded in, got %v", n.RemMgmtIP)
	}
}

func TestNeighborMerge_KeepsDistinctNeighbors(t *testing.T) {
	in := []driver.NeighborSnap{
		{LocalIfIndex: 1, RemSysName: "AP-1", Protocol: "lldp"},
		{LocalIfIndex: 2, RemSysName: "AP-2", Protocol: "lldp"},
		{LocalIfIndex: 48, RemSysName: "Core", Protocol: "cdp"},
	}
	out := NeighborMerge(in)
	if len(out) != 3 {
		t.Fatalf("distinct neighbors must be preserved, got %d", len(out))
	}
}

func TestNeighborMerge_DropsUnidentifiable(t *testing.T) {
	in := []driver.NeighborSnap{
		{LocalIfIndex: 5, Protocol: "lldp"}, // no sysname/chassis/ip
	}
	out := NeighborMerge(in)
	if len(out) != 0 {
		t.Fatalf("unidentifiable neighbor should be dropped, got %d", len(out))
	}
}

func TestNeighborMerge_SameRemoteTwoLocalPorts(t *testing.T) {
	// A neighbor reached via two local ports (LAG) → two distinct links.
	in := []driver.NeighborSnap{
		{LocalIfIndex: 49, RemSysName: "Core", Protocol: "lldp"},
		{LocalIfIndex: 50, RemSysName: "Core", Protocol: "lldp"},
	}
	out := NeighborMerge(in)
	if len(out) != 2 {
		t.Fatalf("two local ports to the same neighbor are two links, got %d", len(out))
	}
}
