package api

import (
	"net/http"

	"github.com/coralsearesorts/hims/internal/domain"
	"github.com/coralsearesorts/hims/internal/storage/postgres/db"
)

// bmcInventoryRow is one out-of-band controller for the iLO/BMC inventory page. It joins
// the device record (identity + management state) with its collected bmc_info (firmware /
// health / power), and reports the linked physical server honestly: there is no BMC→server
// link mechanism yet, so an un-linked controller is reported as such — never a faked link.
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
	LinkedServer    string   `json:"linked_server"` // "" => unlinked_bmc
	LinkedServerID  string   `json:"linked_server_id,omitempty"`
	ManagementState string   `json:"management"`
	ManagedBy       []string `json:"managed_by"`
	Site            string   `json:"site"`
	LastSeen        string   `json:"last_seen,omitempty"`
	LastCollected   string   `json:"last_collected,omitempty"`
	Confidence      int      `json:"confidence"`
	Evidence        string   `json:"evidence"`
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
	out := make([]bmcInventoryRow, 0, len(devs))
	for _, d := range devs {
		st := maps.statusFor(d)
		row := bmcInventoryRow{
			ID: d.ID.String(), Hostname: derefStr(d.Hostname), Vendor: derefStr(d.Vendor),
			Model: derefStr(d.Model), Serial: derefStr(d.Serial),
			ManagementState: st.Management, ManagedBy: st.ManagedBy, Site: derefStr(d.Location),
			IPMIStatus: "not_collected", RedfishStatus: "not_collected",
			LinkedServer: "", // no BMC→server link mechanism yet → unlinked_bmc (never faked)
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
		out = append(out, row)
	}
	writeJSON(w, http.StatusOK, out)
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
