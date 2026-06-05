package api

import (
	"context"
	"net/http"
	"sort"

	"github.com/coralsearesorts/hims/internal/storage/postgres/db"
	"github.com/google/uuid"
)

// Management Access Coverage — how many devices HIMS can actually manage, and by
// which protocol. "Managed" means a REAL authenticated/working method: a bound
// credential or proof of a successful authenticated collection (evidence). Open
// ports alone never count. The signals come from ListDeviceAccessSignals; this
// file normalises protocol tokens, aggregates the coverage report, and powers
// the /devices access filters used by the dashboard card's drill-downs.

// protocolLabels maps a raw protocol token to its display label.
var protocolLabels = map[string]string{
	"snmp_v2c":      "SNMP v2c",
	"snmp_v3":       "SNMP v3",
	"ssh":           "SSH",
	"winrm":         "WinRM",
	"wmi":           "WMI / CIM",
	"smb":           "SMB",
	"http_basic":    "HTTP Basic",
	"api_token":     "API Token",
	"onvif":         "ONVIF",
	"rtsp":          "RTSP",
	"vendor_api":    "Vendor API",
	"vmware":        "VMware",
	"fortigate_api": "FortiGate API",
	"cucm_axl":      "CUCM AXL",
	"ldap":          "LDAP",
}

// protocolOrder is the stable display order for the breakdown.
var protocolOrder = []string{
	"snmp_v2c", "snmp_v3", "ssh", "winrm", "wmi", "smb", "onvif", "rtsp",
	"http_basic", "api_token", "vendor_api", "vmware", "fortigate_api", "cucm_axl", "ldap",
}

func protocolLabel(p string) string {
	if l, ok := protocolLabels[p]; ok {
		return l
	}
	return p
}

func protocolRank(p string) int {
	for i, x := range protocolOrder {
		if x == p {
			return i
		}
	}
	return len(protocolOrder)
}

// deviceAccess is the set of protocols a device is manageable by, each tagged
// with its strongest source (bound_credential outranks evidence).
type deviceAccess struct {
	protocols map[string]string // protocol → source
}

func (a *deviceAccess) managed() bool { return a != nil && len(a.protocols) > 0 }
func (a *deviceAccess) has(p string) bool {
	if a == nil {
		return false
	}
	_, ok := a.protocols[p]
	return ok
}

// deviceAccessMap builds the per-device access map for ALL devices. Devices with
// no working/bound method are simply absent from the map (→ unmanaged).
func (s *Server) deviceAccessMap(ctx context.Context) (map[uuid.UUID]*deviceAccess, error) {
	rows, err := s.queries.ListDeviceAccessSignals(ctx)
	if err != nil {
		return nil, err
	}
	return buildAccessMap(rows), nil
}

// buildAccessMap merges raw (device, protocol, source) signals into a per-device
// access map. Pure — unit-tested without a DB. bound_credential outranks
// evidence for a given protocol.
func buildAccessMap(rows []db.ListDeviceAccessSignalsRow) map[uuid.UUID]*deviceAccess {
	m := make(map[uuid.UUID]*deviceAccess, len(rows))
	for _, r := range rows {
		da := m[r.DeviceID]
		if da == nil {
			da = &deviceAccess{protocols: map[string]string{}}
			m[r.DeviceID] = da
		}
		// bound_credential is the authoritative source; never downgrade it.
		if cur, ok := da.protocols[r.Protocol]; !ok || (cur != "bound_credential" && r.Source == "bound_credential") {
			da.protocols[r.Protocol] = r.Source
		}
	}
	return m
}

// --- response shapes ---------------------------------------------------------

type accessProtocolDTO struct {
	Protocol     string `json:"protocol"`
	Label        string `json:"label"`
	DeviceCount  int    `json:"device_count"`
	SuccessCount int    `json:"success_count"`
	FailedCount  int    `json:"failed_count"`
	Source       string `json:"source"` // bound_credential | evidence | mixed
}

type accessReasonDTO struct {
	Reason string `json:"reason"`
	Count  int    `json:"count"`
}

type accessUnmanagedDTO struct {
	DeviceCount int               `json:"device_count"`
	Reasons     []accessReasonDTO `json:"reasons"`
}

type accessCoverageDTO struct {
	TotalDevices     int                 `json:"total_devices"`
	ManagedDevices   int                 `json:"managed_devices"`
	UnmanagedDevices int                 `json:"unmanaged_devices"`
	CoveragePercent  int                 `json:"coverage_percent"`
	ByProtocol       []accessProtocolDTO `json:"by_protocol"`
	Unmanaged        accessUnmanagedDTO  `json:"unmanaged"`
}

// accessCoverage handles GET /dashboard/access-coverage. Counts are derived
// purely from real bindings + collection evidence (no fabricated numbers); a
// device with multiple working methods is counted under every applicable
// protocol, while managed_devices counts distinct devices.
func (s *Server) accessCoverage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	devices, err := s.queries.ListAllDevices(ctx)
	if err != nil {
		writeErr(w, err)
		return
	}
	devices = s.scopeDevices(ctx, devices)
	am, err := s.deviceAccessMap(ctx)
	if err != nil {
		writeErr(w, err)
		return
	}

	type agg struct{ count, bound, evidence int }
	byProto := map[string]*agg{}
	managed := 0
	noCredBound := 0

	for _, d := range devices {
		da := am[d.ID]
		if da.managed() {
			managed++
			for p, src := range da.protocols {
				a := byProto[p]
				if a == nil {
					a = &agg{}
					byProto[p] = a
				}
				a.count++
				if src == "bound_credential" {
					a.bound++
				} else {
					a.evidence++
				}
			}
			continue
		}
		// Unmanaged. With the current data model a bound credential always yields
		// a protocol, so every unmanaged device has no bound credential. We report
		// only reasons we can prove — no fabricated "failed"/"not tested" splits
		// (there is no persisted credential-test history yet).
		noCredBound++
	}

	total := len(devices)
	unmanaged := total - managed

	protoDTOs := make([]accessProtocolDTO, 0, len(byProto))
	for p, a := range byProto {
		src := "evidence"
		if a.bound > 0 && a.evidence > 0 {
			src = "mixed"
		} else if a.bound > 0 {
			src = "bound_credential"
		}
		protoDTOs = append(protoDTOs, accessProtocolDTO{
			Protocol: p, Label: protocolLabel(p), DeviceCount: a.count,
			// success == proven manageable; no failed-test persistence yet, so 0.
			SuccessCount: a.count, FailedCount: 0, Source: src,
		})
	}
	sort.Slice(protoDTOs, func(i, j int) bool {
		ri, rj := protocolRank(protoDTOs[i].Protocol), protocolRank(protoDTOs[j].Protocol)
		if ri != rj {
			return ri < rj
		}
		return protoDTOs[i].Protocol < protoDTOs[j].Protocol
	})

	reasons := []accessReasonDTO{}
	if noCredBound > 0 {
		reasons = append(reasons, accessReasonDTO{Reason: "no_credential_bound", Count: noCredBound})
	}

	pct := 0
	if total > 0 {
		pct = int((float64(managed) / float64(total) * 100) + 0.5)
	}

	writeJSON(w, http.StatusOK, accessCoverageDTO{
		TotalDevices:     total,
		ManagedDevices:   managed,
		UnmanagedDevices: unmanaged,
		CoveragePercent:  pct,
		ByProtocol:       protoDTOs,
		Unmanaged:        accessUnmanagedDTO{DeviceCount: unmanaged, Reasons: reasons},
	})
}

// filterDevicesByAccess narrows a device list by the access drill-down params
// used by the dashboard card links. Empty params → unchanged.
func filterDevicesByAccess(rows []db.Device, am map[uuid.UUID]*deviceAccess, access, proto, issue string) []db.Device {
	if access == "" && proto == "" && issue == "" {
		return rows
	}
	out := make([]db.Device, 0, len(rows))
	for _, d := range rows {
		da := am[d.ID]
		keep := true
		switch access {
		case "managed":
			keep = da.managed()
		case "unmanaged":
			keep = !da.managed()
		}
		if keep && proto != "" {
			keep = da.has(proto)
		}
		if keep && issue != "" {
			switch issue {
			case "no_credential_bound":
				keep = !da.managed() && d.CredentialID == nil
			default:
				// credential_failed / not_tested are not derivable without
				// persisted credential-test history — return nothing rather than
				// guess.
				keep = false
			}
		}
		if keep {
			out = append(out, d)
		}
	}
	return out
}
