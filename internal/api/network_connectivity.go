package api

import (
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/coralsearesorts/hims/internal/topology"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

// unknownMACs (GET /topology/unknown-macs) lists MACs learned on switch ports that map to NO
// inventory device — the unmapped L2 endpoints. Each row shows the switch, edge port, VLAN,
// last-seen, the port's MAC count, a possible IP from ARP, and a suggested next action. No fake
// devices are ever created; this is read-only visibility.
func (s *Server) unknownMACs(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	total := 0
	if n, e := s.queries.CountUnknownMACs(ctx); e == nil {
		total = int(n)
	}
	rows, err := s.queries.ListUnknownMACs(ctx)
	if err != nil {
		writeErr(w, err)
		return
	}
	out := make([]map[string]any, 0, len(rows))
	const maxRows = 2000
	skipped := 0
	for _, m := range rows {
		if isBogusMAC(m.Mac) { // broadcast / multicast / all-zero are not real endpoints
			skipped++
			continue
		}
		ip := ""
		if m.PossibleIp.IsValid() {
			ip = m.PossibleIp.String()
		}
		action := "Unmapped L2 endpoint (no IP seen) — check the switch port; likely an unmanaged device."
		if ip != "" {
			action = "Discover " + ip + " — an IP was learned via ARP but it is not in inventory."
		}
		out = append(out, map[string]any{
			"mac":          m.Mac,
			"switch_id":    m.SwitchID.String(),
			"switch_name":  m.SwitchName,
			"if_index":     m.IfIndex,
			"if_name":      m.IfName,
			"if_alias":     m.IfAlias,
			"vlan_id":      m.VlanID,
			"last_seen_at": m.LastSeenAt,
			"port_macs":    m.PortMacCount,
			"possible_ip":  ip,
			"suggested":    action,
		})
		if len(out) >= maxRows {
			break
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"total": total, "shown": len(out), "filtered_bogus": skipped, "macs": out})
}

// isBogusMAC reports MACs that aren't real unicast endpoints: all-zero, broadcast,
// or multicast (LSB of the first octet set) — these are FDB artifacts, not devices.
func isBogusMAC(mac string) bool {
	m := strings.ToLower(strings.ReplaceAll(strings.ReplaceAll(mac, ":", ""), "-", ""))
	if len(m) < 2 {
		return true
	}
	if m == "000000000000" || m == "ffffffffffff" {
		return true
	}
	b, err := strconv.ParseUint(m[:2], 16, 8)
	if err != nil {
		return true
	}
	return b&0x01 == 1 // multicast bit
}

// deviceConnectivity (GET /devices/{id}/connectivity) answers "which switch + port + VLAN is this
// device attached to, and how sure are we?" — reusing the topology engine. It tries the device's
// management IP (ARP→MAC→FDB) first, then each of its own NIC MACs (interfaces + os_nics) so a
// device with no ARP entry still resolves by MAC. Attachments are sorted edge-first (the port with
// the FEWEST learned MACs is the true point of attachment; a trunk/uplink that also saw the MAC
// carries hundreds). Honest gaps: if no evidence exists, switch_port is empty and `gap` says why.
func (s *Server) deviceConnectivity(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}
	dev, err := s.queries.GetDevice(ctx, id)
	if err != nil {
		http.Error(w, "device not found", http.StatusNotFound)
		return
	}

	var res topology.SearchResult
	found := false
	if dev.PrimaryIp != nil {
		if sr, e := s.topo.SearchIP(ctx, *dev.PrimaryIp); e == nil && len(sr.SwitchPort) > 0 {
			res, found = sr, true
		}
	}
	if !found {
		macs := map[string]bool{}
		if ifs, e := s.queries.ListInterfaces(ctx, id); e == nil {
			for _, i := range ifs {
				if i.Mac != nil && *i.Mac != "" {
					macs[normMAC(*i.Mac)] = true
				}
			}
		}
		if nics, e := s.queries.ListOSNics(ctx, id); e == nil {
			for _, n := range nics {
				if n.Mac != nil && *n.Mac != "" {
					macs[normMAC(*n.Mac)] = true
				}
			}
		}
		for m := range macs {
			if sr, e := s.topo.SearchMAC(ctx, m); e == nil && len(sr.SwitchPort) > 0 {
				res, found = sr, true
				break
			}
		}
	}

	// Sort attachments edge-first (fewest learned MACs = the real point of attachment),
	// then dedup to one row per (switch, port) — the engine returns one FDB match per
	// VLAN/FdbId, which would otherwise repeat the same physical port many times.
	sorted := append([]topology.SwitchPortEntry(nil), res.SwitchPort...)
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
	seen := map[string]bool{}
	ports := make([]topology.SwitchPortEntry, 0, len(sorted))
	for _, p := range sorted {
		ifIdx := int32(-1)
		if p.IfIndex != nil {
			ifIdx = *p.IfIndex
		}
		k := p.SwitchID.String() + "/" + strconv.Itoa(int(ifIdx))
		if seen[k] {
			continue
		}
		seen[k] = true
		ports = append(ports, p)
	}

	gap := ""
	if !found || len(ports) == 0 {
		gap = "No switch evidence: this device's IP/MAC has not been seen in any collected FDB/ARP table. It may be on an un-collected switch, behind a router, or wireless."
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"device_id":          id.String(),
		"device_name":        dev.Name,
		"primary_ip":         addrStr(dev.PrimaryIp),
		"matched_mac":        res.MAC,
		"switch_port":        ports,
		"path":               res.Path,
		"confidence":         res.Confidence,
		"confidence_reasons": res.ConfidenceReasons,
		"arp_device_name":    res.ARPDeviceName,
		"arp_source":         res.ARPSource,
		"gap":                gap,
	})
}
