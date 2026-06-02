package topology

import (
	"sort"
	"strings"

	"github.com/coralsearesorts/hims/internal/driver"
)

// NeighborMerge deduplicates and merges LLDP + CDP neighbor snapshots for a
// single device before they become topology links. A mixed-vendor segment
// often reports the same physical neighbor via both protocols (Cisco emits
// CDP and LLDP; an Aruba peer emits LLDP). Without merging, the topology
// would show the link twice.
//
// Merge key: (local interface, remote identity). Remote identity prefers
// rem_sys_name, falling back to chassis-id / mgmt-ip. When the same
// (local, remote) pair appears via both protocols, LLDP wins for the
// standard fields but CDP's management IP / platform is folded in if LLDP
// lacked them — so the merged record is the richest of the two.
func NeighborMerge(neighbors []driver.NeighborSnap) []driver.NeighborSnap {
	type key struct {
		localIf int
		remote  string
	}
	merged := map[key]*driver.NeighborSnap{}
	order := []key{}

	remoteID := func(n driver.NeighborSnap) string {
		switch {
		case n.RemSysName != "":
			return strings.ToLower(n.RemSysName)
		case n.RemChassisID != "":
			return strings.ToLower(n.RemChassisID)
		case n.RemMgmtIP != nil:
			return n.RemMgmtIP.String()
		}
		return ""
	}

	for _, n := range neighbors {
		rid := remoteID(n)
		if rid == "" {
			continue // can't identify the remote; skip
		}
		k := key{localIf: n.LocalIfIndex, remote: rid}
		existing := merged[k]
		if existing == nil {
			cp := n
			merged[k] = &cp
			order = append(order, k)
			continue
		}
		// Merge: prefer LLDP for identity, fold in whichever fields are richer.
		if existing.Protocol == "cdp" && n.Protocol == "lldp" {
			// LLDP supersedes CDP for the standard fields; keep CDP mgmt IP.
			cdpMgmt := existing.RemMgmtIP
			cp := n
			if cp.RemMgmtIP == nil {
				cp.RemMgmtIP = cdpMgmt
			}
			cp.Protocol = "lldp"
			merged[k] = &cp
		} else {
			// Fold missing fields into the existing record.
			if existing.RemMgmtIP == nil && n.RemMgmtIP != nil {
				existing.RemMgmtIP = n.RemMgmtIP
			}
			if existing.RemPortID == "" {
				existing.RemPortID = n.RemPortID
			}
			if existing.RemSysName == "" {
				existing.RemSysName = n.RemSysName
			}
		}
	}

	out := make([]driver.NeighborSnap, 0, len(order))
	for _, k := range order {
		out = append(out, *merged[k])
	}
	// Stable order by local interface then remote for deterministic output.
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].LocalIfIndex != out[j].LocalIfIndex {
			return out[i].LocalIfIndex < out[j].LocalIfIndex
		}
		return out[i].RemSysName < out[j].RemSysName
	})
	return out
}
