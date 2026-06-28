package api

import (
	"context"
	"net/http"
	"strings"

	"github.com/coralsearesorts/hims/internal/domain"
	"github.com/coralsearesorts/hims/internal/storage/postgres/db"
	"github.com/google/uuid"
)

// bmcInventoryRow is one out-of-band controller for the iLO/BMC inventory page. It joins
// the device record (identity + management state) with its collected bmc_info (firmware /
// health / power), and the result of an evidence-based BMC→server linking pass (see
// linkBMCToServer): a real serial/UUID match links the controller to its physical server;
// weaker hostname evidence yields a candidate_link with confidence; no evidence yields an
// honest unlinked_bmc WITH the reason. Links are never faked or guessed by IP pattern.
type bmcInventoryRow struct {
	ID              string   `json:"id"`
	IP              string   `json:"ip"`
	Hostname        string   `json:"hostname"`
	Vendor          string   `json:"vendor"`
	Model           string   `json:"model"`
	Serial          string   `json:"serial"`
	Firmware        string   `json:"firmware"`
	ControllerKind  string   `json:"controller_kind"` // iLO / iDRAC / XClarity / IPMI / Redfish
	RedfishStatus   string   `json:"redfish_status"`  // collected | not_collected
	IPMIStatus      string   `json:"ipmi_status"`     // not_collected (no IPMI collector yet)
	PowerState      string   `json:"power_state"`
	HealthSummary   string   `json:"health_summary"`
	LinkState       string   `json:"link_state"` // linked | candidate_link | unlinked_bmc
	LinkedServer    string   `json:"linked_server"`
	LinkedServerID  string   `json:"linked_server_id,omitempty"`
	LinkConfidence  int      `json:"link_confidence"`
	LinkEvidence    string   `json:"link_evidence"`
	ManagementState string   `json:"management"`
	ManagedBy       []string `json:"managed_by"`
	Site            string   `json:"site"`
	LastSeen        string   `json:"last_seen,omitempty"`
	LastCollected   string   `json:"last_collected,omitempty"`
	Confidence      int      `json:"confidence"`
	Evidence        string   `json:"evidence"`
}

// bmcLink is the result of the evidence-based BMC→server linking attempt.
type bmcLink struct {
	state      string // linked | candidate_link | unlinked_bmc
	serverID   string
	serverName string
	confidence int
	evidence   string
}

// normSerial upper-cases and strips spaces/separators so serials compare reliably.
func normSerial(s string) string {
	s = strings.ToUpper(strings.TrimSpace(s))
	return strings.NewReplacer(" ", "", "-", "", "_", "").Replace(s)
}

// linkBMCToServer attempts, with EVIDENCE only, to link a BMC to its physical server among
// the candidate servers. Precedence (strongest first):
//  1. system UUID match (Redfish Systems UUID == server's collected UUID) → linked.
//  2. chassis serial match (BMC's collected serial == a server's serial) → linked.
//  3. hostname stem similarity (iLO "<name>-ilo"/"ilo<name>" vs server hostname) → candidate_link.
//  4. nothing → unlinked_bmc, with the reason (what evidence was missing).
//
// It NEVER links by IP proximity or guesses. bmcSerial/bmcUUID come from collected facts
// (empty when no Redfish/BMC collection has run — then the result is honestly unlinked).
func linkBMCToServer(bmcSerial, bmcUUID, bmcHostname string, serialIdx, uuidIdx map[string]db.Device, servers []db.Device) bmcLink {
	if u := normSerial(bmcUUID); u != "" {
		if sv, ok := uuidIdx[u]; ok {
			return bmcLink{"linked", sv.ID.String(), deviceName(sv), 98, "system UUID match"}
		}
	}
	if sn := normSerial(bmcSerial); sn != "" {
		if sv, ok := serialIdx[sn]; ok {
			return bmcLink{"linked", sv.ID.String(), deviceName(sv), 95, "chassis serial match (" + bmcSerial + ")"}
		}
	}
	// Hostname stem: strip an "ilo"/"idrac"/"bmc"/"mgmt" affix and look for a server whose
	// hostname shares the stem. Weak (names can collide) → candidate_link, not a hard link.
	if stem := bmcHostStem(bmcHostname); len(stem) >= 4 {
		for _, sv := range servers {
			h := strings.ToLower(strings.TrimSpace(derefStr(sv.Hostname)))
			if h == "" {
				continue
			}
			if h == stem || strings.HasPrefix(h, stem+".") {
				return bmcLink{"candidate_link", sv.ID.String(), deviceName(sv), 55, "hostname stem \"" + stem + "\" matches server hostname"}
			}
		}
	}
	reason := "unlinked_bmc: no controller inventory collected (Redfish/BMC) — no system UUID, chassis serial, or hostname to match a discovered server"
	if bmcSerial != "" || bmcUUID != "" || bmcHostname != "" {
		reason = "unlinked_bmc: no discovered server matches this controller's serial/UUID/hostname"
	}
	return bmcLink{"unlinked_bmc", "", "", 0, reason}
}

// bmcHostStem lowercases a BMC hostname and strips a common controller affix so it can be
// compared to a server hostname ("server01-ilo" → "server01", "iloABC123" → "abc123").
func bmcHostStem(h string) string {
	h = strings.ToLower(strings.TrimSpace(h))
	if h == "" {
		return ""
	}
	for _, suf := range []string{"-ilo", "-idrac", "-bmc", "-mgmt", "-rmc"} {
		h = strings.TrimSuffix(h, suf)
	}
	for _, pre := range []string{"ilo", "idrac", "bmc"} {
		if strings.HasPrefix(h, pre) && len(h) > len(pre)+2 {
			h = h[len(pre):]
		}
	}
	return strings.TrimLeft(h, "-")
}

func deviceName(d db.Device) string {
	if n := strings.TrimSpace(d.Name); n != "" {
		return n
	}
	if d.Hostname != nil && *d.Hostname != "" {
		return *d.Hostname
	}
	if d.PrimaryIp != nil {
		return d.PrimaryIp.String()
	}
	return d.ID.String()
}

// listBMCInventory is GET /inventory/bmc — every out-of-band management controller
// (category 'bmc': HPE iLO / Dell iDRAC / Lenovo XClarity-IMM / Supermicro IPMI / Redfish)
// with its bmc_info health/firmware. BMCs are NOT mixed with servers (separate category);
// a controller with no collected bmc_info still appears (identity-classified) with an
// honest not_collected status.
func (s *Server) listBMCInventory(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	devs, err := s.queries.ListDevicesByCategory(ctx, string(domain.CatBMC))
	if err != nil {
		writeErr(w, err)
		return
	}
	maps, err := s.buildStatusMaps(ctx)
	if err != nil {
		writeErr(w, err)
		return
	}
	// Candidate physical servers + indexes for the linking pass (serial/UUID). The UUID
	// index uses the collected "redfish.system_uuid"/"os.uuid" fact when present.
	servers, _ := s.queries.ListDevicesByCategory(ctx, string(domain.CatServer))
	if vh, e := s.queries.ListDevicesByCategory(ctx, string(domain.CatVirtualHost)); e == nil {
		servers = append(servers, vh...) // an ESXi/Hyper-V host is also a physical chassis a BMC can manage
	}
	serialIdx := map[string]db.Device{}
	uuidIdx := map[string]db.Device{}
	for _, sv := range servers {
		if sv.Serial != nil {
			if k := normSerial(*sv.Serial); k != "" {
				serialIdx[k] = sv
			}
		}
		if u := normSerial(s.deviceUUID(ctx, sv.ID)); u != "" {
			uuidIdx[u] = sv
		}
	}
	out := make([]bmcInventoryRow, 0, len(devs))
	for _, d := range devs {
		st := maps.statusFor(d)
		row := bmcInventoryRow{
			ID: d.ID.String(), Hostname: derefStr(d.Hostname), Vendor: derefStr(d.Vendor),
			Model: derefStr(d.Model), Serial: derefStr(d.Serial),
			ManagementState: st.Management, ManagedBy: st.ManagedBy, Site: derefStr(d.Location),
			IPMIStatus: "not_collected", RedfishStatus: "not_collected",
		}
		if d.PrimaryIp != nil {
			row.IP = d.PrimaryIp.String()
		}
		if d.ConfidenceScore != nil {
			row.Confidence = int(*d.ConfidenceScore)
		}
		if d.LastMonitoringAt != nil {
			row.LastSeen = d.LastMonitoringAt.Format("2006-01-02 15:04")
		}
		if d.LastDiscoveryAt != nil {
			row.LastCollected = d.LastDiscoveryAt.Format("2006-01-02 15:04")
		}
		row.Evidence = bmcEvidence(d)
		// Join collected controller facts where present (Redfish/IPMI driver ran).
		if b, berr := s.queries.GetBMCInfo(ctx, d.ID); berr == nil && b.DeviceID == d.ID {
			row.RedfishStatus = "collected"
			row.Firmware = derefStr(b.FirmwareVersion)
			row.ControllerKind = derefStr(b.ControllerKind)
			row.PowerState = derefStr(b.PowerState)
			row.HealthSummary = derefStr(b.Health)
			if row.Vendor == "" {
				row.Vendor = derefStr(b.Vendor)
			}
			if row.Model == "" {
				row.Model = derefStr(b.Model)
			}
			if !b.LastSeenAt.IsZero() {
				row.LastCollected = b.LastSeenAt.Format("2006-01-02 15:04")
			}
		}
		if row.ControllerKind == "" {
			row.ControllerKind = d.Subtype
		}
		// Evidence-based BMC→server link (serial/UUID/hostname; never IP-guessed).
		link := linkBMCToServer(row.Serial, s.deviceUUID(ctx, d.ID), row.Hostname, serialIdx, uuidIdx, servers)
		row.LinkState = link.state
		row.LinkConfidence = link.confidence
		row.LinkEvidence = link.evidence
		if link.serverID != "" {
			row.LinkedServer = link.serverName
			row.LinkedServerID = link.serverID
		}
		out = append(out, row)
	}
	writeJSON(w, http.StatusOK, out)
}

// deviceUUID returns a device's collected system UUID (Redfish/OS inventory) from facts,
// or "" when none has been collected. Used as link evidence; never fabricated.
func (s *Server) deviceUUID(ctx context.Context, id uuid.UUID) string {
	facts, err := s.queries.ListDeviceFacts(ctx, id)
	if err != nil {
		return ""
	}
	for _, f := range facts {
		if f.Value == nil {
			continue
		}
		switch f.Key {
		case "redfish.system_uuid", "os.uuid", "system.uuid", "dmi.uuid":
			if v := strings.TrimSpace(*f.Value); v != "" {
				return v
			}
		}
	}
	return ""
}

// bmcEvidence summarises why a device is classified as a BMC (honest source string).
func bmcEvidence(d db.Device) string {
	if d.Model != nil && *d.Model != "" {
		return "identity: " + *d.Model
	}
	if d.Subtype != "" {
		return "identity: " + d.Subtype
	}
	return "classified bmc"
}
