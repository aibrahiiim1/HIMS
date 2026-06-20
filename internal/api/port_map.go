package api

import (
	"net/http"
	"sort"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

// devicePortMap (GET /devices/{id}/port-map) resolves, for each connected port of a switch, what is
// attached: learned MACs → resolved inventory device / VM / IP, the device category + server_role,
// any LLDP/CDP neighbor, and a per-port confidence. Honest rules: unknown MACs are counted, never
// turned into devices; trunk/uplink ports (many MACs) are flagged, not guessed; access ports (few
// MACs) are the reliable endpoint-attachment points. Resolution priority per MAC:
// OS-NIC (real host) > switch-interface (another fabric device) > ARP-IP correlation; VMs by vNIC MAC.

type pmMac struct {
	MAC        string `json:"mac"`
	IP         string `json:"ip,omitempty"`
	DeviceID   string `json:"device_id,omitempty"`
	DeviceName string `json:"device_name,omitempty"`
	Category   string `json:"category,omitempty"`
	ServerRole string `json:"server_role,omitempty"`
	VMID       string `json:"vm_id,omitempty"`
	VMName     string `json:"vm_name,omitempty"`
	VMHostID   string `json:"vm_host_device_id,omitempty"`
	VMDeviceID string `json:"vm_device_id,omitempty"`
	Method     string `json:"method"` // os_nic | switch_if | arp_mac | vm_nic | unknown
	LastSeen   string `json:"last_seen,omitempty"`
}

type pmPort struct {
	IfIndex      int32   `json:"if_index"`
	IfName       string  `json:"if_name,omitempty"`
	AdminStatus  *int16  `json:"admin_status,omitempty"`
	OperStatus   *int16  `json:"oper_status,omitempty"`
	SpeedMbps    *int32  `json:"speed_mbps,omitempty"`
	UntaggedVLAN *int32  `json:"untagged_vlan,omitempty"`
	TaggedVLANs  []int32 `json:"tagged_vlans,omitempty"`
	IsTrunk      bool    `json:"is_trunk"`
	MACCount     int     `json:"mac_count"`
	Resolved     int     `json:"resolved_count"`
	Unknown      int     `json:"unknown_count"`
	Confidence   string  `json:"confidence"`    // lldp_cdp | access_mac | arp_mac | trunk_uplink | ambiguous | none
	UnknownClass string  `json:"unknown_class"` // edge | transit | ambiguous (classifies this port's unknown MACs)
	Neighbor     string  `json:"neighbor,omitempty"`
	NeighborPort string  `json:"neighbor_port,omitempty"`
	MACs         []pmMac `json:"macs"`
}

const trunkMACThreshold = 16 // a port carrying more than this many MACs is a trunk/uplink, not an edge

func (s *Server) devicePortMap(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}
	rows, err := s.queries.ResolvePortMap(ctx, id)
	if err != nil {
		writeErr(w, err)
		return
	}
	// Port metadata.
	type ifMeta struct {
		name        string
		admin, oper *int16
		speed       *int32
		role        string
	}
	ifByIdx := map[int32]ifMeta{}
	if ifs, e := s.queries.ListInterfaces(ctx, id); e == nil {
		for _, i := range ifs {
			ifByIdx[i.IfIndex] = ifMeta{name: derefStr(i.IfName), admin: i.AdminStatus, oper: i.OperStatus, speed: i.SpeedMbps, role: i.PortRole}
		}
	}
	untagged := map[int32]int32{}
	tagged := map[int32][]int32{}
	if pvs, e := s.queries.ListPortVlans(ctx, id); e == nil {
		for _, p := range pvs {
			if p.Tagged {
				tagged[p.IfIndex] = append(tagged[p.IfIndex], p.VlanID)
			} else {
				v := p.VlanID
				untagged[p.IfIndex] = v
			}
		}
	}
	type neigh struct{ name, port string }
	neighByIdx := map[int32]neigh{}
	if ns, e := s.queries.ListNeighbors(ctx, id); e == nil {
		for _, n := range ns {
			if n.LocalIfIndex != nil {
				neighByIdx[*n.LocalIfIndex] = neigh{name: nz(derefStr(n.RemSysName), derefStr(n.RemChassisID)), port: nz(derefStr(n.RemPortDesc), derefStr(n.RemPortID))}
			}
		}
	}
	// server_role lookup for resolved devices.
	roleByID := map[uuid.UUID]string{}
	if devs, e := s.queries.ListAllDevices(ctx); e == nil {
		if maps, e2 := s.buildStatusMaps(ctx); e2 == nil {
			for _, d := range devs {
				if rl := maps.serverRole(d); rl != "" {
					roleByID[d.ID] = rl
				}
			}
		}
	}

	ports := map[int32]*pmPort{}
	getPort := func(idx int32) *pmPort {
		p := ports[idx]
		if p == nil {
			m := ifByIdx[idx]
			p = &pmPort{IfIndex: idx, IfName: m.name, AdminStatus: m.admin, OperStatus: m.oper, SpeedMbps: m.speed}
			if u, ok := untagged[idx]; ok {
				p.UntaggedVLAN = &u
			}
			p.TaggedVLANs = tagged[idx]
			p.IsTrunk = len(tagged[idx]) > 1 || m.role == "trunk" || m.role == "uplink"
			if n, ok := neighByIdx[idx]; ok {
				p.Neighbor, p.NeighborPort = n.name, n.port
			}
			ports[idx] = p
		}
		return p
	}

	for _, row := range rows {
		if row.IfIndex == nil || *row.IfIndex == 0 {
			continue
		}
		p := getPort(*row.IfIndex)
		p.MACCount++
		mr := pmMac{MAC: row.Mac, Method: "unknown"}
		if row.LastSeenAt.Year() > 2000 {
			mr.LastSeen = row.LastSeenAt.Format("2006-01-02T15:04:05Z07:00")
		}
		if row.ResolvedIp.IsValid() {
			mr.IP = row.ResolvedIp.String()
		}
		// VM by vNIC MAC (independent of host device resolution).
		if row.VmID != uuid.Nil {
			mr.VMID = row.VmID.String()
			mr.VMName = row.VmName
			if row.VmHostDeviceID != uuid.Nil {
				mr.VMHostID = row.VmHostDeviceID.String()
			}
			if row.VmDeviceID != nil {
				mr.VMDeviceID = row.VmDeviceID.String()
			}
		}
		// Device resolution priority: OS-NIC (real host) > switch interface > ARP-IP.
		switch {
		case row.DevNicID != uuid.Nil:
			mr.DeviceID, mr.DeviceName, mr.Category, mr.Method = row.DevNicID.String(), row.DevNicName, row.DevNicCat, "os_nic"
		case row.DevIfID != uuid.Nil:
			mr.DeviceID, mr.DeviceName, mr.Category, mr.Method = row.DevIfID.String(), row.DevIfName, row.DevIfCat, "switch_if"
		case row.DevArpID != uuid.Nil:
			mr.DeviceID, mr.DeviceName, mr.Category, mr.Method = row.DevArpID.String(), row.DevArpName, row.DevArpCat, "arp_mac"
		case mr.VMID != "":
			mr.Method = "vm_nic"
		}
		if mr.DeviceID != "" {
			if did, e := uuid.Parse(mr.DeviceID); e == nil {
				mr.ServerRole = roleByID[did]
			}
		}
		if mr.DeviceID != "" || mr.VMID != "" {
			p.Resolved++
		} else {
			p.Unknown++
		}
		p.MACs = append(p.MACs, mr)
	}

	// Per-port confidence + unknown-MAC classification + summary.
	out := make([]pmPort, 0, len(ports))
	var totalUnknown, trunkPorts, resolvedPorts, ambiguousPorts int
	var edgeUnknown, transitUnknown, ambiguousUnknown, resolvedEdgeDevices int
	for _, p := range ports {
		switch {
		case p.Neighbor != "":
			p.Confidence = "lldp_cdp"
		case p.IsTrunk || p.MACCount > trunkMACThreshold:
			p.Confidence = "trunk_uplink"
		case p.Resolved == 0:
			p.Confidence = "none"
		case p.MACCount <= 4 && p.Resolved >= 1:
			// edge/access port — reliable. Tag arp_mac if every resolution came via ARP.
			p.Confidence = "access_mac"
			allArp := true
			for _, m := range p.MACs {
				if m.Method != "arp_mac" && (m.DeviceID != "" || m.VMID != "") {
					allArp = false
				}
			}
			if allArp {
				p.Confidence = "arp_mac"
			}
		default:
			p.Confidence = "ambiguous"
		}
		// Unknown-MAC class is about the PORT's nature (independent of whether anything resolved):
		// transit = trunk/uplink/neighbor (MAC noise, not actionable); edge = real access port with
		// few MACs (actionable); ambiguous = mid-count port with no trunk/neighbor signal.
		switch {
		case p.IsTrunk || p.MACCount > trunkMACThreshold || p.Neighbor != "":
			p.UnknownClass = "transit"
		case p.MACCount <= 4:
			p.UnknownClass = "edge"
		default:
			p.UnknownClass = "ambiguous"
		}
		totalUnknown += p.Unknown
		switch p.UnknownClass {
		case "transit":
			transitUnknown += p.Unknown
		case "edge":
			edgeUnknown += p.Unknown
			resolvedEdgeDevices += p.Resolved
		case "ambiguous":
			ambiguousUnknown += p.Unknown
		}
		switch p.Confidence {
		case "trunk_uplink":
			trunkPorts++
		case "ambiguous":
			ambiguousPorts++
		case "access_mac", "arp_mac", "lldp_cdp":
			if p.Resolved > 0 {
				resolvedPorts++
			}
		}
		out = append(out, *p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].IfIndex < out[j].IfIndex })

	writeJSON(w, http.StatusOK, map[string]any{
		"device_id":       id.String(),
		"connected_ports": len(out),
		"total_ports":     len(ifByIdx),
		"summary": map[string]int{
			"resolved_device_ports":  resolvedPorts,
			"resolved_edge_devices":  resolvedEdgeDevices,
			"trunk_uplink_ports":     trunkPorts,
			"ambiguous_ports":        ambiguousPorts,
			"unknown_macs":           totalUnknown,
			"edge_unknown_macs":      edgeUnknown,
			"transit_unknown_macs":   transitUnknown,
			"ambiguous_unknown_macs": ambiguousUnknown,
		},
		"ports": out,
	})
}
