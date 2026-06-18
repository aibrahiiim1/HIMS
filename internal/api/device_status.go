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
	// MgmtPendingCollection: a collect_os agent job is queued or dispatched right
	// now — collection is actively in progress, not settled. Transient/healthy, NOT
	// a failure: it outranks the stale direct-probe failure so an in-flight host is
	// never misreported as credential_failed/collection_failed/needs_agent while its
	// agent job is still mid-flight.
	MgmtPendingCollection = "pending_collection"
	// MgmtNotAttempted: a reachable Windows-like host that was enrolled but for which
	// NO collection was attempted (no in-flight job, no evidence, no auth attempt, no
	// binding). This is "not attempted yet", NOT "needs a credential" — surfacing
	// needs_credential here would wrongly blame the operator's credentials when
	// collection simply never ran (an enqueue gap). After the hardening this should
	// be 0 for a settled from-zero scan.
	MgmtNotAttempted = "not_attempted"
	MgmtVirtual      = "virtual" // operator-entered placeholder; not probed/monitored
	// MgmtWebAuthenticated: a WEB/identity credential (http_basic/http) authenticated, but
	// no DEEP OS/endpoint management exists (winrm/wmi/ssh/snmp did not collect). The
	// credential WORKS — so this is NEVER credential_failed; the operator may add a deep
	// management credential if full inventory is required.
	MgmtWebAuthenticated = "web_authenticated"
	// MgmtNotAuthorized: a credential AUTHENTICATED but the host denied access (UAC
	// LocalAccountTokenFilterPolicy, remote-logon rights, group membership, WinRM/DCOM
	// policy). The credential is valid — NOT a wrong password, so NOT credential_failed;
	// the fix is host policy or a host-authorized credential.
	MgmtNotAuthorized = "not_authorized"
)

// credSignal is the per-device aggregate of ALL credential-test outcomes (every
// credential, every kind) — the masking-proof read model behind the management
// classification rule (a host is credential_failed ONLY if some credential was cleanly
// rejected AND nothing authenticated by any supported method).
type credSignal struct {
	anySuccess    bool // any credential succeeded (deep OR web)
	webSuccess    bool // an http_basic/http login authenticated (not deep management)
	legacyAuthOK  bool // a credential authenticated but the WSMan op faulted (legacy WSMan)
	notAuthorized bool // a credential authenticated but the host denied access (UAC/policy)
	authRejected  bool // a credential was cleanly rejected (wrong username/password)
}

// authenticatedAny reports whether ANY credential authenticated by ANY supported method
// (deep or web success, legacy auth-ok, or authenticated-but-access-denied). When true,
// the host must NEVER be reported credential_failed.
func (c credSignal) authenticatedAny() bool {
	return c.anySuccess || c.legacyAuthOK || c.notAuthorized
}

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
	cred        map[uuid.UUID]credSignal // masking-proof per-device credential-outcome aggregate
	onlineSites map[uuid.UUID]bool       // location → has an online relay agent
	anySites    map[uuid.UUID]bool       // location → has any relay agent (online or not)
	// nvrChannelCams are camera device_ids that are a channel on an NVR/DVR — they
	// are managed VIA the recorder, so an RTSP-only feed (no web/ONVIF to
	// authenticate) must not be reported as credential_failed.
	nvrChannelCams map[uuid.UUID]bool
	// activeCollect are device_ids with an in-flight collect_os agent job (queued or
	// dispatched). Drives MgmtPendingCollection so an in-flight host is not
	// misreported with its stale direct-probe failure.
	activeCollect map[uuid.UUID]bool
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
	cm := map[uuid.UUID]credSignal{}
	if rows, cerr := s.queries.DeviceCredentialSignals(ctx); cerr == nil {
		for _, r := range rows {
			cm[r.DeviceID] = credSignal{
				anySuccess: r.AnySuccess, webSuccess: r.WebSuccess, legacyAuthOK: r.LegacyAuthok,
				notAuthorized: r.NotAuthorized, authRejected: r.AuthRejected,
			}
		}
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
	nvrCams := map[uuid.UUID]bool{}
	if ids, cerr := s.queries.ListLinkedCameraDeviceIDs(ctx); cerr == nil {
		for _, id := range ids {
			if id != nil {
				nvrCams[*id] = true
			}
		}
	}
	activeCollect := map[uuid.UUID]bool{}
	if ids, perr := s.queries.ListDevicesWithActiveAgentJobs(ctx); perr == nil {
		for _, id := range ids {
			if id != nil {
				activeCollect[*id] = true
			}
		}
	}
	return &statusMaps{access: am, test: tm, cred: cm, onlineSites: onlineSites, anySites: anySites, nvrChannelCams: nvrCams, activeCollect: activeCollect}, nil
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

	// Managed requires a PROVEN working method (successful collection evidence or a
	// successful credential test) — never a bare bound credential, and never an
	// open port.
	if da.hasProven() {
		managedBy = da.provenProtocols()
		sort.Slice(managedBy, func(i, j int) bool { return protocolRank(managedBy[i]) < protocolRank(managedBy[j]) })
		return MgmtManaged, managedBy
	}

	// A camera that is a channel on an NVR/DVR is managed VIA the recorder — its
	// inventory + recording state come through the NVR. Many such cameras are
	// RTSP-only (port 554, no web/ONVIF to authenticate), so web-cred attempts
	// during a scan "fail" and would otherwise mis-flag them credential_failed.
	// The recorder is the management point, so report managed-via-NVR instead.
	if d.Category == "camera" && m.nvrChannelCams[d.ID] {
		return MgmtManaged, []string{"nvr"}
	}

	cs, hasCS := m.cred[d.ID]
	if !hasCS && ts != nil {
		// Fallback when the masking-proof aggregate is unavailable (e.g. unit tests, or a
		// device with test rows but no aggregate yet): synthesize the signals from the
		// latest-per-kind test status. ts.authFailed conflates wrong-credential and
		// not-authorized, so split by category where known; an authFailed with no category
		// is treated as a clean wrong-credential rejection.
		cs.anySuccess = ts.anySuccess()
		for k := range ts.successKinds {
			if k == "http_basic" || k == "http" {
				cs.webSuccess = true
			}
		}
		cs.legacyAuthOK = ts.winrmLegacy()
		for _, cat := range ts.kindCategory {
			switch cat {
			case "access_denied", "wmi_access_denied":
				cs.notAuthorized = true
			case "auth_failed":
				cs.authRejected = true
			}
		}
		if ts.authFailed && !cs.notAuthorized && !cs.authRejected {
			cs.authRejected = true
		}
	}

	// === Precedence below the proven-managed/NVR checks. The ROOT RULE: any credential
	// that AUTHENTICATED by any supported method outranks a sibling auth failure — so a
	// host is NEVER reported credential_failed when a credential actually worked. The
	// cred-signal aggregate is masking-proof (a sibling .\administrator auth_failed can't
	// hide a legacy auth-ok or an http_basic success). ===

	// (3) Authenticated but the WSMan operation faulted (legacy WSMan 2.0): the credential
	// is valid; Go WinRM can't drive it → needs the Relay Agent (WMI/DCOM). Above web so a
	// Windows host with a usable deep path (via agent) is steered there, not to web-only.
	// (legacyAuthOK is itself proof of a Windows WSMan host, so it is not windowsLike-gated
	// — a host enrolled as "server" with a blank os_family still routes to the agent.)
	if cs.legacyAuthOK || (windowsLike(d) && ts.winrmLegacy()) {
		if d.LocationID != nil && m.anySites[*d.LocationID] && !m.onlineSites[*d.LocationID] {
			return MgmtAgentOffline, nil
		}
		return MgmtNeedsAgent, nil
	}
	// (2) A WEB/identity credential authenticated (http_basic/http) but no DEEP management
	// exists (hasProven is deep-only, above). The credential WORKS → web_authenticated,
	// NEVER credential_failed. Operator adds a deep mgmt credential if inventory is needed.
	if cs.webSuccess {
		return MgmtWebAuthenticated, []string{"http"}
	}
	// (3b) A credential AUTHENTICATED but the host denied access (UAC / DCOM / WinRM policy
	// / group) and nothing else succeeded — valid credential, not a wrong password.
	if cs.notAuthorized && !cs.anySuccess {
		return MgmtNotAuthorized, nil
	}

	// (4) A collect_os job is in flight RIGHT NOW — deep collection actively in progress
	// (typically routed to the site Relay Agent during a scan). Outranks the not-yet-settled
	// failure signals below so an in-flight host is never misreported as a terminal failure.
	if m.activeCollect[d.ID] {
		if d.LocationID != nil && m.anySites[*d.LocationID] && !m.onlineSites[*d.LocationID] {
			return MgmtAgentOffline, nil
		}
		return MgmtPendingCollection, nil
	}

	// (6) TRUE credential_failed: a credential was cleanly rejected (wrong username/password)
	// AND nothing authenticated by ANY supported method. Everything that authenticated is
	// handled above, so this is reserved for "every applicable credential cleanly rejected,
	// no authenticated evidence" — never a false credential_failed.
	if cs.authRejected && !cs.authenticatedAny() {
		return MgmtCredentialFailed, nil
	}
	if d.CredentialID != nil {
		// A credential is bound but nothing successfully collected with it.
		return MgmtCollectionFailed, nil
	}
	// A credential was actually TRIED (not merely bound) yet nothing succeeded and
	// it was not a clean auth rejection (handled above): the attempt reached the
	// host and failed for a non-credential reason — a WinRM/WMI firewall block
	// (agent New-CimSession "firewall exception for the WinRM"), an RPC/DCOM
	// error, an unreachable port, or a protocol fault. That is a COLLECTION
	// failure, NOT "needs a credential": labeling it needs_credential points the
	// operator at the wrong fix (supply a credential) when the real fix is the
	// host firewall / GPO or access method. needs_credential is reserved below for
	// a credentialed-class host that was NEVER attempted and has no binding.
	if ts != nil && ts.tested {
		return MgmtCollectionFailed, nil
	}
	// Reachable, enrolled, but NOTHING was attempted (no in-flight job — handled
	// above; no evidence; no auth attempt; no binding) and nothing tested. For a
	// Windows-like host the pipeline ALWAYS routes a collection attempt
	// (osCollectionCandidate), so reaching here means collection never ran — an
	// enqueue gap, NOT a credential problem. Report not_attempted (with the real
	// reason surfaced elsewhere), never the misleading needs_credential. After the
	// dispatch/retry hardening this should be 0 for a settled from-zero scan.
	if windowsLike(d) {
		return MgmtNotAttempted, nil
	}
	// Other credentialed classes (switch/server/SNMP/SSH appliances) genuinely need
	// a credential of the right kind to be added before HIMS can even attempt them.
	if credentialedCategories[d.Category] || d.OsFamily == "linux" {
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
	// Virtual devices are operator-entered placeholders that are never probed, so
	// they must not appear offline/unmanaged or generate monitoring noise. Their
	// reachability honestly reflects the operator-set status; management is a
	// distinct "virtual" state (not a credential/collection gap). This also keeps
	// them out of every management-gap data-quality bucket below.
	if d.IsVirtual {
		return deviceStatus{Reachability: reachabilityFromStatus(d.Status), Management: MgmtVirtual}
	}
	reach := reachabilityFromStatus(d.Status)
	state, managedBy := m.deriveManagement(d)
	return deviceStatus{
		Reachability:      reach,
		Management:        state,
		ManagedBy:         managedBy,
		PreviouslyManaged: reach == ReachOffline && len(managedBy) > 0,
	}
}

// statusDataQualityIssues derives the reachability-vs-management hygiene issues —
// the cases that prove Online and Managed are distinct axes. Every count is real
// (derived from monitoring status + proven access, never from open ports).
func (m *statusMaps) statusDataQualityIssues(devs []db.Device, now time.Time) []dqIssue {
	var onlineUnmanaged, reachableNoCred, credBoundNotWorking, needsAgentColl,
		agentOfflineManaged, offlinePrevManaged, managedStale, notAttempted,
		notAuthorized, webOnly []db.Device
	staleBefore := now.Add(-reachStale)
	for _, d := range devs {
		st := m.statusFor(d)
		// Offline now but has a working method on record — was managed, can't be reached.
		if st.PreviouslyManaged {
			offlinePrevManaged = append(offlinePrevManaged, d)
		}
		// Managed but the last proven check is stale — collection may be silently rotting.
		if st.Management == MgmtManaged {
			if ts := m.test[d.ID]; ts != nil && !ts.lastTestedAt.IsZero() && ts.lastTestedAt.Before(staleBefore) {
				managedStale = append(managedStale, d)
			}
		}
		// Management-gap buckets (mutually exclusive by state).
		switch st.Management {
		case MgmtUnmanaged:
			if st.Reachability == ReachOnline {
				onlineUnmanaged = append(onlineUnmanaged, d)
			}
		case MgmtNeedsCredential:
			if st.Reachability == ReachOnline {
				reachableNoCred = append(reachableNoCred, d)
			}
		case MgmtNotAttempted:
			// Reachable + enrolled but collection never ran — an enqueue gap to fix
			// (should be 0 once a from-zero scan settles). Distinct from "needs cred".
			if st.Reachability == ReachOnline {
				notAttempted = append(notAttempted, d)
			}
		case MgmtCollectionFailed, MgmtCredentialFailed:
			credBoundNotWorking = append(credBoundNotWorking, d)
		case MgmtNotAuthorized:
			notAuthorized = append(notAuthorized, d)
		case MgmtWebAuthenticated:
			webOnly = append(webOnly, d)
		case MgmtNeedsAgent:
			needsAgentColl = append(needsAgentColl, d)
		case MgmtAgentOffline:
			agentOfflineManaged = append(agentOfflineManaged, d)
			// MgmtPendingCollection is intentionally omitted: collection is actively in
			// progress, not a data-quality issue.
		}
	}
	out := []dqIssue{}
	add := func(key, label, desc, sev string, list []db.Device) {
		if len(list) == 0 {
			return
		}
		out = append(out, dqIssue{Key: key, Label: label, Description: desc, Severity: sev, Count: len(list), Devices: sampleDevices(list)})
	}
	add("online_but_unmanaged", "Online but Unmanaged", "These devices respond on the network but HIMS has no working management method for them. Being online (or having open ports) is NOT management — bind and prove a credential, or assign an agent, to manage them.", "warning", onlineUnmanaged)
	add("reachable_but_no_credential", "Reachable but no credential", "Online devices in a credentialed class (switch, server, firewall, Windows/Linux host…) with no credential bound yet. Bind a credential so HIMS can authenticate and collect.", "warning", reachableNoCred)
	add("credential_bound_but_not_working", "Credential bound but not working", "A credential is bound (or was tested) but no authenticated collection has succeeded — the device is NOT managed. Fix the credential or the access method.", "warning", credBoundNotWorking)
	add("needs_agent_collection", "Needs agent collection", "Windows hosts that cannot be collected directly (legacy WSMan 2.0 or WinRM disabled). Install/assign a Relay Agent to their site to collect them via WMI/DCOM.", "warning", needsAgentColl)
	add("agent_offline_for_managed_site", "Agent offline for managed site", "Hosts that depend on a site Relay Agent for collection, but that site's agent is currently offline. Bring the agent back online to resume management.", "critical", agentOfflineManaged)
	add("offline_but_previously_managed", "Offline but previously Managed", "These devices have a proven working management method on record but are currently offline (unreachable). Check power/network — management resumes when they are reachable again.", "warning", offlinePrevManaged)
	add("managed_device_collection_stale", "Managed device collection stale", "Devices that are Managed but whose last successful authenticated check is over 30 days old. Re-test the credential / re-collect to confirm management is still working.", "info", managedStale)
	add("collection_not_attempted", "Collection not attempted", "Reachable Windows hosts that were enrolled but never had a collection attempt (no in-flight job, no recorded attempt). This should be 0 once a from-zero scan settles — a non-zero count is an enqueue gap, not a credential problem. Re-run a targeted collection.", "warning", notAttempted)
	add("credential_not_authorized", "Credential not authorized on host", "A credential AUTHENTICATED but the host denied access (UAC LocalAccountTokenFilterPolicy, remote-logon rights, group membership, WinRM/DCOM policy). This is NOT a wrong password — fix host policy or use a credential authorized on this host.", "warning", notAuthorized)
	add("web_authenticated_no_deep", "Web-authenticated, no deep management", "A web/identity credential (HTTP) authenticates, but no deep OS/endpoint management exists yet. The credential works — add a Windows/Linux/SNMP management credential if deep inventory is required.", "info", webOnly)
	return out
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
