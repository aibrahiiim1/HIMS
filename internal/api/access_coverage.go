package api

import (
	"context"
	"net/http"
	"sort"
	"time"

	"github.com/coralsearesorts/hims/internal/credtest"
	"github.com/coralsearesorts/hims/internal/storage/postgres/db"
	"github.com/google/uuid"
)

// accessStaleAfter is how old the latest successful credential test may be before
// a device's access is considered "stale" (worth re-verifying).
const accessStaleAfter = 30 * 24 * time.Hour

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

// deviceTestStatus summarises the LATEST persisted credential-test outcome per
// (device, kind). Read model behind the unmanaged reasons, per-protocol failure
// counts, and the Inventory access-issue filters. Pure data from real rows.
type deviceTestStatus struct {
	tested       bool
	authFailed   bool // some kind's latest result was an auth rejection
	lastTestedAt time.Time
	successKinds map[string]bool
	failedKinds  map[string]bool
	kindCategory map[string]string // latest category per kind (e.g. winrm → auth_ok_operation_fault)
}

// winrmLegacy reports that the latest WinRM test authenticated but the WSMan
// operation faulted — a legacy WSMan 2.0 host (auth is fine; needs a fallback
// collector), NOT a credential failure.
func (t *deviceTestStatus) winrmLegacy() bool {
	return t != nil && t.kindCategory["winrm"] == credtest.CatOperationFault
}

func (t *deviceTestStatus) anySuccess() bool { return t != nil && len(t.successKinds) > 0 }

// deviceTestMap indexes the latest per-(device,kind) credential-test outcome.
func (s *Server) deviceTestMap(ctx context.Context) (map[uuid.UUID]*deviceTestStatus, error) {
	rows, err := s.queries.LatestDeviceKindResults(ctx)
	if err != nil {
		return nil, err
	}
	m := make(map[uuid.UUID]*deviceTestStatus)
	for _, r := range rows {
		t := m[r.DeviceID]
		if t == nil {
			t = &deviceTestStatus{successKinds: map[string]bool{}, failedKinds: map[string]bool{}, kindCategory: map[string]string{}}
			m[r.DeviceID] = t
		}
		t.tested = true
		if r.TestedAt.After(t.lastTestedAt) {
			t.lastTestedAt = r.TestedAt
		}
		// LatestDeviceKindResults is ordered newest-first per (device,kind); record
		// the first (latest) category we see for each kind.
		if _, seen := t.kindCategory[r.Kind]; !seen {
			t.kindCategory[r.Kind] = r.Category
		}
		if r.Success {
			t.successKinds[r.Kind] = true
		} else {
			t.failedKinds[r.Kind] = true
			// auth_ok_operation_fault is NOT an auth failure — the credential is valid.
			if r.Category == "auth_failed" {
				t.authFailed = true
			}
		}
	}
	return m, nil
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
	tm, err := s.deviceTestMap(ctx)
	if err != nil {
		writeErr(w, err)
		return
	}

	type agg struct {
		sources map[string]bool // bound_credential | evidence | test_result
		count   int
		failed  int // devices whose LATEST test for this protocol failed
	}
	byProto := map[string]*agg{}
	managed := 0
	var noCredBound, credFailed, notTested int

	for _, d := range devices {
		da := am[d.ID]
		if da.managed() {
			managed++
			for p, src := range da.protocols {
				a := byProto[p]
				if a == nil {
					a = &agg{sources: map[string]bool{}}
					byProto[p] = a
				}
				a.count++
				a.sources[src] = true
			}
			continue
		}
		// Unmanaged → classify the reason from REAL signals (mutually exclusive,
		// most-actionable first). Never fabricated: every bucket maps to data.
		ts := tm[d.ID]
		switch {
		case ts != nil && ts.authFailed:
			credFailed++ // a credential was tested and rejected
		case ts == nil || !ts.tested:
			notTested++ // no credential test has ever run for this device
		default:
			noCredBound++ // tested (only unreachable/error) or simply no credential assigned
		}
	}

	// Per-protocol failure counts from the latest test per (device, kind).
	for _, ts := range tm {
		for k := range ts.failedKinds {
			if ts.successKinds[k] {
				continue // a later/other success for this kind — not currently failing
			}
			a := byProto[k]
			if a == nil {
				a = &agg{sources: map[string]bool{}}
				byProto[k] = a
			}
			a.failed++
		}
	}

	total := len(devices)
	unmanaged := total - managed

	protoDTOs := make([]accessProtocolDTO, 0, len(byProto))
	for p, a := range byProto {
		src := "evidence"
		switch {
		case len(a.sources) > 1:
			src = "mixed"
		case a.sources["bound_credential"]:
			src = "bound_credential"
		case a.sources["test_result"]:
			src = "test_result"
		}
		protoDTOs = append(protoDTOs, accessProtocolDTO{
			Protocol: p, Label: protocolLabel(p), DeviceCount: a.count,
			SuccessCount: a.count, FailedCount: a.failed, Source: src,
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
	addReason := func(name string, n int) {
		if n > 0 {
			reasons = append(reasons, accessReasonDTO{Reason: name, Count: n})
		}
	}
	addReason("credential_failed", credFailed)
	addReason("no_credential_bound", noCredBound)
	addReason("not_tested", notTested)

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

// expectedProtocols lists the management protocol(s) a device of this class is
// expected to be reachable by (used by the missing_expected_protocol filter and
// the Data Quality credential checks). An snmp_v2c expectation is satisfied by
// either SNMP version. Returns nil when there's no firm expectation.
func expectedProtocols(category, osFamily string) []string {
	switch osFamily {
	case "windows":
		return []string{"winrm"}
	case "linux":
		return []string{"ssh"}
	}
	switch category {
	case "switch", "router", "firewall":
		return []string{"snmp_v2c", "ssh"}
	case "camera", "nvr":
		return []string{"onvif"}
	case "ups", "printer":
		return []string{"snmp_v2c"}
	}
	return nil
}

// accessSatisfies reports whether the device's working access methods include the
// expected protocol (treating snmp_v2c/snmp_v3 as interchangeable).
func accessSatisfies(da *deviceAccess, expected string) bool {
	if da.has(expected) {
		return true
	}
	if expected == "snmp_v2c" || expected == "snmp_v3" {
		return da.has("snmp_v2c") || da.has("snmp_v3")
	}
	return false
}

// missingExpectedProtocol reports whether the device has a firm expected protocol
// for its class that none of its working access methods satisfy.
func missingExpectedProtocol(d db.Device, da *deviceAccess) bool {
	exp := expectedProtocols(d.Category, d.OsFamily)
	if len(exp) == 0 {
		return false
	}
	for _, p := range exp {
		if accessSatisfies(da, p) {
			return false
		}
	}
	return true
}

// filterDevicesByAccess narrows a device list by the access drill-down params
// used by the dashboard card links. Empty params → unchanged. All predicates are
// computed from real bindings, collection evidence, and persisted test history.
func filterDevicesByAccess(rows []db.Device, am map[uuid.UUID]*deviceAccess, tm map[uuid.UUID]*deviceTestStatus, now time.Time, access, proto, issue string) []db.Device {
	if access == "" && proto == "" && issue == "" {
		return rows
	}
	out := make([]db.Device, 0, len(rows))
	for _, d := range rows {
		da := am[d.ID]
		ts := tm[d.ID]
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
				keep = d.CredentialID == nil && !da.managed()
			case "credential_failed":
				keep = ts != nil && ts.authFailed && !da.managed()
			case "not_tested":
				keep = ts == nil || !ts.tested
			case "stale":
				// Managed by a credential test whose latest success is old.
				keep = ts != nil && ts.anySuccess() && !ts.lastTestedAt.IsZero() && now.Sub(ts.lastTestedAt) > accessStaleAfter
			case "missing_expected_protocol":
				keep = missingExpectedProtocol(d, da)
			default:
				keep = false
			}
		}
		if keep {
			out = append(out, d)
		}
	}
	return out
}
