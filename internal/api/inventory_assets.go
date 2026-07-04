package api

import (
	"context"
	"net/http"
	"sort"

	"github.com/coralsearesorts/hims/internal/domain"
	"github.com/coralsearesorts/hims/internal/storage/postgres/db"
)

// Canonical inventory model (derived, no destructive migration). HIMS stores one
// `devices` row per discovered IP/endpoint; this layer folds endpoints that are
// really PART of another asset — using EVIDENCE only — so counts reflect real
// assets, not raw IPs:
//
//   - an SVI / VLAN-gateway IP (device_facts svi.gateway_of) is an interface of the
//     owning switch, not a standalone device (evidence: the switch's ipAddrTable).
//   - a BMC (iLO/iDRAC) that matches a server by chassis serial / system UUID is the
//     out-of-band management endpoint of that ONE physical server (evidence: serial/UUID).
//
// Nothing is merged by IP proximity, name guess, VLAN or topology. A BMC linked only
// by a hostname stem is NOT auto-folded — it stays its own asset and is surfaced for
// operator review. An unlinked BMC represents its own physical-server asset (BMC
// endpoint present, host OS not yet linked).

// assetRole is a device's place in the canonical model.
type assetRole string

const (
	roleAsset       assetRole = "asset"        // a standalone real asset (counts as 1)
	roleSVIGateway  assetRole = "svi_gateway"  // an SVI/VLAN-gateway IP — folds into a switch
	roleBMCEndpoint assetRole = "bmc_endpoint" // an iLO/iDRAC linked to a server — folds into it
)

// assetClassification is one device's derived role + (when folded) its parent asset.
type assetClassification struct {
	DeviceID     string    `json:"device_id"`
	Name         string    `json:"name"`
	IP           string    `json:"ip"`
	Category     string    `json:"category"`
	Role         assetRole `json:"role"`
	ParentID     string    `json:"parent_asset_id,omitempty"`
	ParentName   string    `json:"parent_asset_name,omitempty"`
	Evidence     string    `json:"evidence,omitempty"`
	EvidenceKind string    `json:"evidence_kind,omitempty"` // svi.gateway_of | chassis_serial | system_uuid
	Confidence   int       `json:"confidence,omitempty"`
}

// assetReviewItem is an ambiguous link left for a human (never auto-merged).
type assetReviewItem struct {
	DeviceID   string `json:"device_id"`
	Name       string `json:"name"`
	IP         string `json:"ip"`
	Kind       string `json:"kind"` // bmc_hostname_candidate
	Reason     string `json:"reason"`
	Suggested  string `json:"suggested_parent,omitempty"`
	Confidence int    `json:"confidence"`
}

// assetSummary is the counting model that replaces the single ambiguous "Total Devices".
type assetSummary struct {
	Assets              int `json:"assets"`               // real inventory objects (folded)
	MonitoredEndpoints  int `json:"monitored_endpoints"`  // every device row = a reachability target
	ManagementEndpoints int `json:"management_endpoints"` // endpoints HIMS proved it can collect from
	InterfaceAddresses  int `json:"interface_addresses"`  // L3 interface IPs on assets (ipAddrTable)
	LogicalGateways     int `json:"logical_gateways"`     // SVI / VLAN-gateway IPs
	VirtualAssets       int `json:"virtual_assets"`       // VMs (logical assets)

	// Folded — endpoints that did NOT inflate the asset count, with why.
	FoldedSVIGateways  int `json:"folded_svi_gateways"`
	FoldedBMCEndpoints int `json:"folded_bmc_endpoints"`

	NeedsReview []assetReviewItem `json:"needs_review"`
}

// classifyAssets folds SVI + linked-BMC endpoints into their parent assets using
// existing evidence and returns per-device classifications + the ambiguous review list.
// Pure over its inputs (maps + device lists) so the roles are testable.
func classifyAssets(devs []db.Device, maps *statusMaps, serverIdx serverSerialIndex, bmcUUID func(db.Device) string) ([]assetClassification, []assetReviewItem) {
	out := make([]assetClassification, 0, len(devs))
	var review []assetReviewItem
	for _, d := range devs {
		c := assetClassification{DeviceID: d.ID.String(), Name: deviceName(d), IP: addrStr(d.PrimaryIp), Category: d.Category, Role: roleAsset}
		// SVI / VLAN-gateway IP → interface of the owning switch (fact-based).
		if ref, ok := maps.sviParent[d.ID]; ok {
			c.Role = roleSVIGateway
			c.ParentID = ref.Switch.ID.String()
			c.ParentName = ref.Switch.Name
			c.EvidenceKind = "svi.gateway_of"
			c.Evidence = "ipAddrTable SVI on " + ref.Switch.Name + (func() string {
				if ref.VLAN != "" {
					return " · VLAN " + ref.VLAN
				}
				return ""
			})()
			c.Confidence = 95
			out = append(out, c)
			continue
		}
		// BMC → out-of-band endpoint of its physical server, but ONLY on strong evidence.
		if d.Category == string(domain.CatBMC) {
			link := linkBMCToServer(derefStr(d.Serial), bmcUUID(d), derefStr(d.Hostname), serverIdx.bySerial, serverIdx.byUUID, serverIdx.servers)
			switch link.state {
			case "linked": // system UUID or chassis serial match — fold into the server asset.
				c.Role = roleBMCEndpoint
				c.ParentID = link.serverID
				c.ParentName = link.serverName
				c.EvidenceKind = evidenceKindOf(link.evidence)
				c.Evidence = link.evidence
				c.Confidence = link.confidence
				out = append(out, c)
				continue
			case "candidate_link": // weak (hostname) — DO NOT auto-fold; surface for review.
				review = append(review, assetReviewItem{
					DeviceID: d.ID.String(), Name: deviceName(d), IP: addrStr(d.PrimaryIp),
					Kind: "bmc_hostname_candidate", Reason: link.evidence, Suggested: link.serverName, Confidence: link.confidence,
				})
			}
			// unlinked_bmc → its own physical-server asset (BMC endpoint, host OS not yet linked).
		}
		out = append(out, c)
	}
	return out, review
}

func evidenceKindOf(evidence string) string {
	switch {
	case len(evidence) >= 11 && evidence[:11] == "system UUID":
		return "system_uuid"
	case len(evidence) >= 13 && evidence[:13] == "chassis seria":
		return "chassis_serial"
	default:
		return "evidence"
	}
}

// serverSerialIndex holds the server serial/UUID lookup for BMC linking.
type serverSerialIndex struct {
	servers  []db.Device
	bySerial map[string]db.Device
	byUUID   map[string]db.Device
}

func (s *Server) buildServerSerialIndex(ctx context.Context) serverSerialIndex {
	servers, _ := s.queries.ListDevicesByCategory(ctx, string(domain.CatServer))
	if vh, e := s.queries.ListDevicesByCategory(ctx, string(domain.CatVirtualHost)); e == nil {
		servers = append(servers, vh...)
	}
	idx := serverSerialIndex{servers: servers, bySerial: map[string]db.Device{}, byUUID: map[string]db.Device{}}
	for _, sv := range servers {
		if k := normSerial(derefStr(sv.Serial)); k != "" {
			idx.bySerial[k] = sv
		}
		if u := normSerial(s.deviceUUID(ctx, sv.ID)); u != "" {
			idx.byUUID[u] = sv
		}
	}
	return idx
}

// assetSummaryHandler serves GET /inventory/asset-summary — the canonical counting model.
func (s *Server) assetSummaryHandler(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	devs, err := s.queries.ListAllDevices(ctx)
	if err != nil {
		writeErr(w, err)
		return
	}
	maps, err := s.buildStatusMaps(ctx)
	if err != nil {
		writeErr(w, err)
		return
	}
	serverIdx := s.buildServerSerialIndex(ctx)
	classes, review := classifyAssets(devs, maps, serverIdx, func(d db.Device) string { return s.deviceUUID(ctx, d.ID) })

	sum := assetSummary{MonitoredEndpoints: len(devs), NeedsReview: review}
	for i := range devs {
		d := devs[i]
		st := maps.statusFor(d)
		if st.Management == MgmtManaged {
			sum.ManagementEndpoints++
		}
		if st.ServerRole == "virtual_machine" || d.IsVirtual {
			sum.VirtualAssets++
		}
	}
	for _, c := range classes {
		switch c.Role {
		case roleSVIGateway:
			sum.FoldedSVIGateways++
		case roleBMCEndpoint:
			sum.FoldedBMCEndpoints++
		default:
			sum.Assets++
		}
	}
	if n, e := s.queries.CountInterfaceAddresses(ctx); e == nil {
		sum.InterfaceAddresses = int(n)
	}
	if n, e := s.queries.CountLogicalGateways(ctx); e == nil {
		sum.LogicalGateways = int(n)
	}
	writeJSON(w, http.StatusOK, sum)
}

// assetsHandler serves GET /inventory/assets — one row per REAL asset (folded), with its
// endpoint + interface counts, so the operator sees assets not raw IPs.
func (s *Server) assetsHandler(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	devs, err := s.queries.ListAllDevices(ctx)
	if err != nil {
		writeErr(w, err)
		return
	}
	maps, err := s.buildStatusMaps(ctx)
	if err != nil {
		writeErr(w, err)
		return
	}
	serverIdx := s.buildServerSerialIndex(ctx)
	classes, _ := classifyAssets(devs, maps, serverIdx, func(d db.Device) string { return s.deviceUUID(ctx, d.ID) })

	// endpoint fold counts per parent asset.
	extraEndpoints := map[string]int{}
	for _, c := range classes {
		if c.Role != roleAsset && c.ParentID != "" {
			extraEndpoints[c.ParentID]++
		}
	}
	type assetRow struct {
		ID            string   `json:"id"`
		Name          string   `json:"name"`
		Category      string   `json:"category"`
		Vendor        string   `json:"vendor,omitempty"`
		Model         string   `json:"model,omitempty"`
		Serial        string   `json:"serial,omitempty"`
		PrimaryIP     string   `json:"primary_ip,omitempty"`
		Management    string   `json:"management"`
		Reachability  string   `json:"reachability"`
		EndpointCount int      `json:"endpoint_count"` // this asset's own row + folded endpoints
		FoldedRoles   []string `json:"folded_roles,omitempty"`
	}
	rows := make([]assetRow, 0, len(devs))
	byID := map[string]db.Device{}
	for i := range devs {
		byID[devs[i].ID.String()] = devs[i]
	}
	foldedRoles := map[string][]string{}
	for _, c := range classes {
		if c.Role != roleAsset && c.ParentID != "" {
			foldedRoles[c.ParentID] = append(foldedRoles[c.ParentID], string(c.Role))
		}
	}
	for _, c := range classes {
		if c.Role != roleAsset {
			continue // folded endpoints don't get their own asset row
		}
		d := byID[c.DeviceID]
		st := maps.statusFor(d)
		rows = append(rows, assetRow{
			ID: c.DeviceID, Name: c.Name, Category: d.Category, Vendor: derefStr(d.Vendor), Model: derefStr(d.Model),
			Serial: derefStr(d.Serial), PrimaryIP: c.IP, Management: st.Management, Reachability: st.Reachability,
			EndpointCount: 1 + extraEndpoints[c.DeviceID], FoldedRoles: foldedRoles[c.DeviceID],
		})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Name < rows[j].Name })
	writeJSON(w, http.StatusOK, rows)
}
