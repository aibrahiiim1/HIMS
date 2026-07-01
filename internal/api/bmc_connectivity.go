package api

import (
	"encoding/json"
	"net/http"
	"sort"

	"github.com/coralsearesorts/hims/internal/topology"
)

// bmcNicConnDTO is one physical/management NIC of a BMC-managed server, resolved to
// the switch + port it is attached to. The attachment is pure FDB/ARP evidence for the
// NIC's own MAC — never a fabricated or inferred link. When no switch has learned the
// MAC, switch fields are empty and Gap explains why.
type bmcNicConnDTO struct {
	Mac     string `json:"mac"`
	Role    string `json:"role"` // host | management
	Name    string `json:"name"`
	Adapter string `json:"adapter,omitempty"`
	IPv4    string `json:"ipv4,omitempty"`
	Port    string `json:"port,omitempty"` // physical port index on the adapter (as reported by Redfish)

	SwitchID   string `json:"switch_id,omitempty"`
	SwitchName string `json:"switch_name,omitempty"`
	SwitchIP   string `json:"switch_ip,omitempty"`
	SwitchPort string `json:"switch_port,omitempty"` // if_name on the switch
	PortAlias  string `json:"port_alias,omitempty"`  // port description (if_alias)
	VLAN       int32  `json:"vlan,omitempty"`
	VLANName   string `json:"vlan_name,omitempty"`
	MACCount   *int64 `json:"mac_count,omitempty"`
	Source     string `json:"source,omitempty"`
	LastSeen   string `json:"last_seen,omitempty"`
	Confidence string `json:"confidence"` // high | medium | none
	Gap        string `json:"gap,omitempty"`
}

// deviceBMCConnectivity (GET /devices/{id}/bmc-connectivity) maps every collected NIC of a
// BMC server — the management interface AND each host interface — to the switch + port it is
// physically attached to, by looking each NIC's MAC up in the collected FDB/ARP tables
// (topology engine). This is the per-interface "port map" the operator asked for: which
// switch and which port each interface lands on.
//
// The attachment is the port with the FEWEST learned MACs (the true edge; a trunk/uplink that
// also saw the MAC carries hundreds). No topology link is created and no port is inferred —
// only existing FDB evidence for the NIC's own MAC is displayed; NICs with no FDB match show
// an honest gap.
func (s *Server) deviceBMCConnectivity(w http.ResponseWriter, r *http.Request) {
	ctx, id, ok := pathDevice(w, r)
	if !ok {
		return
	}
	rows, err := s.queries.ListBMCComponents(ctx, id)
	if err != nil {
		writeErr(w, err)
		return
	}

	out := make([]bmcNicConnDTO, 0)
	for _, row := range rows {
		if row.Kind != "nic" {
			continue
		}
		var d map[string]string
		if len(row.Detail) > 0 {
			_ = json.Unmarshal(row.Detail, &d)
		}
		if d == nil {
			d = map[string]string{}
		}
		mac := d["mac"]
		if mac == "" {
			mac = derefStr(row.Serial)
		}
		nm := normMAC(mac)
		if nm == "" {
			continue
		}

		nic := bmcNicConnDTO{
			Mac:        mac,
			Role:       firstNonEmpty(d["role"], "host"),
			Name:       row.Name,
			Adapter:    d["adapter"],
			IPv4:       d["ipv4"],
			Port:       d["port"],
			Confidence: "none",
		}

		if sr, e := s.topo.SearchMAC(ctx, nm); e == nil && len(sr.SwitchPort) > 0 {
			if edge := edgeSwitchPort(sr.SwitchPort); edge != nil {
				nic.SwitchID = edge.SwitchID.String()
				nic.SwitchName = edge.SwitchName
				if edge.SwitchIP != nil {
					nic.SwitchIP = *edge.SwitchIP
				}
				if edge.IfName != nil {
					nic.SwitchPort = *edge.IfName
				}
				if edge.IfAlias != nil {
					nic.PortAlias = *edge.IfAlias
				}
				// Prefer the authoritative untagged/native VLAN when known; fall back to
				// the FDB-derived VLAN only when it is a real configured 802.1Q VLAN.
				switch {
				case edge.UntaggedVLAN != nil:
					nic.VLAN = *edge.UntaggedVLAN
					if edge.UntaggedVLANName != nil {
						nic.VLANName = *edge.UntaggedVLANName
					}
				case !edge.VLANSuspect:
					nic.VLAN = edge.VLANID
					if edge.VLANName != nil {
						nic.VLANName = *edge.VLANName
					}
				}
				nic.MACCount = edge.MACCount
				if edge.Source != nil {
					nic.Source = *edge.Source
				}
				if edge.LastSeenAt != nil {
					nic.LastSeen = *edge.LastSeenAt
				}
				nic.Confidence = sr.Confidence
			}
		}
		if nic.SwitchPort == "" && nic.SwitchName == "" {
			nic.Confidence = "none"
			nic.Gap = "No switch evidence: this NIC's MAC has not been seen in any collected FDB/ARP table. It may be on an un-collected switch, disconnected, or a disabled port."
		}
		out = append(out, nic)
	}

	// Management NIC(s) first, then host NICs — stable within each group.
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].Role == "management" && out[j].Role != "management"
	})

	writeJSON(w, http.StatusOK, out)
}

// edgeSwitchPort returns the true point of attachment from a set of FDB matches: the switch
// port with the FEWEST learned MACs (an edge port carrying just this device), after de-duping
// repeated (switch, port) rows the engine emits per VLAN/FdbId. Returns nil when empty.
func edgeSwitchPort(entries []topology.SwitchPortEntry) *topology.SwitchPortEntry {
	if len(entries) == 0 {
		return nil
	}
	sorted := append([]topology.SwitchPortEntry(nil), entries...)
	sort.SliceStable(sorted, func(i, j int) bool {
		ci, cj := int64(1<<62), int64(1<<62)
		if sorted[i].MACCount != nil {
			ci = *sorted[i].MACCount
		}
		if sorted[j].MACCount != nil {
			cj = *sorted[j].MACCount
		}
		return ci < cj
	})
	return &sorted[0]
}
