package api

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"strconv"
	"time"

	"github.com/coralsearesorts/hims/internal/monitoring"
	"github.com/coralsearesorts/hims/internal/storage/postgres/db"
	"github.com/google/uuid"
)

// Reachability vs. Management — two distinct, never-conflated axes.
//
//   - Reachability (Online status): can monitoring reach the device at all
//     (TCP/ping/SNMP/agent). Driven by the device's monitoring status.
//   - Management status: can HIMS actually authenticate + collect from it
//     (a working credential / successful collection / agent collection). Open
//     ports are NEVER management — only real authenticated evidence counts
//     (deviceAccess, which is built from bound creds + successful-collection
//     evidence + successful credential tests, never from open ports).
//
// A device can be Online + Unmanaged, or Offline + (previously) Managed.

// Reachability states.
const (
	ReachOnline  = "online"
	ReachOffline = "offline"
	ReachWarning = "warning"
	ReachUnknown = "unknown"
)

// Management states.
const (
	MgmtManaged          = "managed"
	MgmtPartiallyManaged = "partially_managed"
	MgmtUnmanaged        = "unmanaged"
	MgmtNeedsCredential  = "needs_credential"
	MgmtCredentialFailed = "credential_failed"
	MgmtNeedsAgent       = "needs_agent"
	MgmtAgentOffline     = "agent_offline"
	MgmtCollectionFailed = "collection_failed"
)

// reachabilityFromStatus maps the honest backend device.status to a reachability
// value. (The 4-state device-status vocabulary stays intact underneath.)
func reachabilityFromStatus(status string) string {
	switch status {
	case "up":
		return ReachOnline
	case "down":
		return ReachOffline
	case "warning", "needs_attention":
		return ReachWarning
	default:
		return ReachUnknown
	}
}

// statusMaps holds the fleet-wide inputs the status derivation needs, fetched
// once per request rather than per device.
type statusMaps struct {
	access      map[uuid.UUID]*deviceAccess
	test        map[uuid.UUID]*deviceTestStatus
	onlineSites map[uuid.UUID]bool // location → has an online relay agent
	anySites    map[uuid.UUID]bool // location → has any relay agent (online or not)
}

func (s *Server) buildStatusMaps(ctx context.Context) (*statusMaps, error) {
	am, err := s.deviceAccessMap(ctx)
	if err != nil {
		return nil, err
	}
	tm, err := s.deviceTestMap(ctx)
	if err != nil {
		return nil, err
	}
	onlineSites, anySites := map[uuid.UUID]bool{}, map[uuid.UUID]bool{}
	if agents, aerr := s.queries.ListRelayAgents(ctx); aerr == nil {
		for _, a := range agents {
			if a.LocationID == nil {
				continue
			}
			anySites[*a.LocationID] = true
			if relayAgentOnline(a) {
				onlineSites[*a.LocationID] = true
			}
		}
	}
	return &statusMaps{access: am, test: tm, onlineSites: onlineSites, anySites: anySites}, nil
}

// windowsLike reports whether a device is (or is most likely) a Windows host even
// before OS collection — a workstation enrolled by WinRM evidence has
// category=endpoint but os_family unset until collected.
func windowsLike(d db.Device) bool { return d.OsFamily == "windows" || d.Category == "endpoint" }

// deriveManagement computes the management state + the protocols a device is
// actually managed by. Open ports never appear here — only deviceAccess (real
// working methods) does.
func (m *statusMaps) deriveManagement(d db.Device) (state string, managedBy []string) {
	da := m.access[d.ID]
	ts := m.test[d.ID]

	if da.managed() {
		for p := range da.protocols {
			managedBy = append(managedBy, p)
		}
		sort.Slice(managedBy, func(i, j int) bool { return protocolRank(managedBy[i]) < protocolRank(managedBy[j]) })
		return MgmtManaged, managedBy
	}

	// Not managed — classify the gap so the operator knows the next action.
	legacy := ts.winrmLegacy()
	if windowsLike(d) && legacy {
		// Legacy WSMan-2.0: auth works but Go WinRM can't drive it → needs the
		// Relay Agent (WMI/DCOM). Distinguish a present-but-offline site agent.
		if d.LocationID != nil && m.anySites[*d.LocationID] && !m.onlineSites[*d.LocationID] {
			return MgmtAgentOffline, nil
		}
		return MgmtNeedsAgent, nil
	}
	if ts != nil && ts.authFailed {
		return MgmtCredentialFailed, nil
	}
	if d.CredentialID != nil {
		// A credential is bound but nothing successfully collected with it.
		return MgmtCollectionFailed, nil
	}
	if credentialedCategories[d.Category] || windowsLike(d) || d.OsFamily == "linux" {
		return MgmtNeedsCredential, nil
	}
	return MgmtUnmanaged, nil
}

// deviceStatus is the computed two-axis status for one device.
type deviceStatus struct {
	Reachability      string   `json:"reachability"`
	Management        string   `json:"management"`
	ManagedBy         []string `json:"managed_by,omitempty"`         // protocol tokens with a working method
	PreviouslyManaged bool     `json:"previously_managed,omitempty"` // offline but has a working method on record
}

func (m *statusMaps) statusFor(d db.Device) deviceStatus {
	reach := reachabilityFromStatus(d.Status)
	state, managedBy := m.deriveManagement(d)
	return deviceStatus{
		Reachability:      reach,
		Management:        state,
		ManagedBy:         managedBy,
		PreviouslyManaged: reach == ReachOffline && len(managedBy) > 0,
	}
}

// deviceWithStatus embeds the device row and adds the computed two-axis status,
// so existing Device consumers keep working while the UI gains reachability +
// management without conflating them.
type deviceWithStatus struct {
	db.Device
	deviceStatus
}

func (m *statusMaps) enrich(rows []db.Device) []deviceWithStatus {
	out := make([]deviceWithStatus, 0, len(rows))
	for _, d := range rows {
		out = append(out, deviceWithStatus{Device: d, deviceStatus: m.statusFor(d)})
	}
	return out
}

// --- reachability-check repair (operator action) ----------------------------

// deviceOpenPorts returns the ports the device most recently answered on during
// discovery (from probe_data.open_ports), for repairing its reachability check.
func (s *Server) deviceOpenPorts(ctx context.Context, deviceID uuid.UUID) []int {
	blob, err := s.queries.LatestDeviceProbeData(ctx, &deviceID)
	if err != nil || len(blob) == 0 {
		return nil
	}
	var pd struct {
		OpenPorts []int `json:"open_ports"`
	}
	if json.Unmarshal(blob, &pd) != nil {
		return nil
	}
	return pd.OpenPorts
}

// repairReachabilityCheck re-points a device's TCP reachability check at a port
// it actually answered on (or the OS-aware fallback), replacing a stale/wrong
// check. Returns the chosen port + its source. Idempotent.
func (s *Server) repairReachabilityCheck(ctx context.Context, d db.Device) (port int, source string, err error) {
	openPorts := s.deviceOpenPorts(ctx, d.ID)
	port = monitoring.ReachabilityPort(d.Category, d.OsFamily, openPorts)
	source = portSource(port, d, openPorts)
	if err = s.queries.DeleteDeviceReachabilityChecks(ctx, d.ID); err != nil {
		return port, source, err
	}
	p := int32(port)
	_, err = s.queries.UpsertMonitoringCheck(ctx, db.UpsertMonitoringCheckParams{
		DeviceID: d.ID, Kind: "tcp", TargetPort: &p, IntervalSeconds: 60, DownThreshold: 2, Enabled: true,
	})
	return port, source, err
}

// portSource explains why a reachability port was chosen, for operator display.
func portSource(port int, d db.Device, openPorts []int) string {
	for _, p := range openPorts {
		if p == port {
			return "discovered_open_port"
		}
	}
	if port == monitoring.DefaultPortForDevice(d.Category, d.OsFamily) {
		return "os_fallback"
	}
	return "manual"
}

// reachStale: a successful credential test older than this is "stale" for the
// managed_device_collection_stale data-quality signal.
const reachStale = 30 * 24 * time.Hour

// deviceStatusSummary (GET /devices/status-summary) — fleet rollup with
// Reachability and Management kept as SEPARATE axes. Powers the dashboard cards
// and the Management Access Coverage page so "online" and "managed" are never
// the same number.
func (s *Server) deviceStatusSummary(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	rows, err := s.queries.ListAllDevices(ctx)
	if err != nil {
		writeErr(w, err)
		return
	}
	rows = s.scopeDevices(ctx, rows)
	maps, err := s.buildStatusMaps(ctx)
	if err != nil {
		writeErr(w, err)
		return
	}
	reach := map[string]int{}
	mgmt := map[string]int{}
	byProto := map[string]int{} // managed-by protocol → device count
	onlineUnmanaged, offlinePrevManaged := 0, 0
	for _, d := range rows {
		st := maps.statusFor(d)
		reach[st.Reachability]++
		mgmt[st.Management]++
		if st.Management == MgmtManaged {
			for _, p := range st.ManagedBy {
				byProto[p]++
			}
		}
		if st.Reachability == ReachOnline && st.Management != MgmtManaged {
			onlineUnmanaged++
		}
		if st.PreviouslyManaged {
			offlinePrevManaged++
		}
	}
	protoList := make([]map[string]any, 0, len(byProto))
	for p, n := range byProto {
		protoList = append(protoList, map[string]any{"protocol": p, "label": protocolLabel(p), "count": n})
	}
	sort.Slice(protoList, func(i, j int) bool {
		return protocolRank(protoList[i]["protocol"].(string)) < protocolRank(protoList[j]["protocol"].(string))
	})

	writeJSON(w, http.StatusOK, map[string]any{
		"total":                len(rows),
		"reachability":         reach, // online/offline/warning/unknown
		"management":           mgmt,  // managed/unmanaged/needs_credential/...
		"managed_by_protocol":  protoList,
		"online_unmanaged":     onlineUnmanaged,
		"offline_prev_managed": offlinePrevManaged,
	})
}

// repairOneReachability (POST /devices/{id}/repair-reachability).
func (s *Server) repairOneReachability(w http.ResponseWriter, r *http.Request) {
	ctx, id, ok := pathDevice(w, r)
	if !ok {
		return
	}
	d, err := s.queries.GetDevice(ctx, id)
	if err != nil {
		writeErr(w, err)
		return
	}
	port, source, rerr := s.repairReachabilityCheck(ctx, d)
	if rerr != nil {
		writeErr(w, rerr)
		return
	}
	s.audit(r, "monitoring", "device.repair_reachability", "device", id.String(),
		"Repaired reachability check for "+d.Name+" → port "+itoa(port), map[string]any{"port": port, "source": source})
	writeJSON(w, http.StatusOK, map[string]any{"device_id": id.String(), "target_port": port, "source": source})
}

// repairManyReachability (POST /devices/repair-reachability) repairs a selected
// set ({device_ids:[...]}) or every device with a stale/wrong-port check
// ({all:true}). "Stale" = the current TCP check targets a port the device did
// not answer on in its latest scan.
func (s *Server) repairManyReachability(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var req struct {
		DeviceIDs []string `json:"device_ids"`
		All       bool     `json:"all"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)

	var targets []db.Device
	if req.All {
		all, err := s.queries.ListAllDevices(ctx)
		if err != nil {
			writeErr(w, err)
			return
		}
		for _, d := range s.scopeDevices(ctx, all) {
			if s.reachabilityCheckIsStale(ctx, d) {
				targets = append(targets, d)
			}
		}
	} else {
		for _, idStr := range req.DeviceIDs {
			id, perr := uuid.Parse(idStr)
			if perr != nil {
				continue
			}
			if d, derr := s.queries.GetDevice(ctx, id); derr == nil {
				targets = append(targets, d)
			}
		}
	}
	repaired := 0
	for _, d := range targets {
		if _, _, err := s.repairReachabilityCheck(ctx, d); err == nil {
			repaired++
		}
	}
	s.audit(r, "monitoring", "devices.repair_reachability", "device", "",
		itoa(repaired)+" reachability check(s) repaired", map[string]any{"all": req.All, "count": repaired})
	writeJSON(w, http.StatusOK, map[string]any{"repaired": repaired, "considered": len(targets)})
}

// reachabilityCheckIsStale reports whether the device's TCP reachability check
// targets a port it did NOT answer on in its latest scan (the false-offline
// condition). Devices with no open-port evidence are left alone.
func (s *Server) reachabilityCheckIsStale(ctx context.Context, d db.Device) bool {
	open := s.deviceOpenPorts(ctx, d.ID)
	if len(open) == 0 {
		return false
	}
	openSet := map[int32]bool{}
	for _, p := range open {
		openSet[int32(p)] = true
	}
	checks, err := s.queries.ListMonitoringChecksByDevice(ctx, d.ID)
	if err != nil {
		return false
	}
	hasTCP := false
	for _, c := range checks {
		if c.Kind != "tcp" {
			continue
		}
		hasTCP = true
		if c.TargetPort != nil && openSet[*c.TargetPort] {
			return false // already on a confirmed-open port — healthy
		}
	}
	return hasTCP // has a TCP check, none on an open port → stale
}

func itoa(n int) string { return strconv.Itoa(n) }
