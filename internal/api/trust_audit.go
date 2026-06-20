package api

import (
	"context"
	"crypto/tls"
	"io"
	"net"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/coralsearesorts/hims/internal/storage/postgres/db"
	"github.com/google/uuid"
)

// Device-class-agnostic discovery trust audit. For every device in a weak / non-managed state it
// asks the standing questions: what evidence exists, which collectors are APPLICABLE from that
// evidence, which were actually attempted, did a weaker protocol win while a stronger one was
// skipped, and is the final state honest. Evidence is reconstructed from real attempt history
// (credential_test_results — the scanner only tests a protocol whose port was open), durable
// collection tables, device identity, AND a bounded ACTIVE re-probe of weak hosts (the only way to
// catch "strong collector never attempted" — e.g. an ESXi host that only ever got an HTTP probe).
// No fake devices, no fabricated success: probing is read-only fingerprinting.

type auditRow struct {
	IP          string   `json:"ip"`
	DeviceID    string   `json:"device_id"`
	Category    string   `json:"category"`
	State       string   `json:"state"`
	Evidence    []string `json:"evidence"`
	Expected    []string `json:"expected_collectors"`
	Attempted   []string `json:"attempted_collectors"`
	Succeeded   []string `json:"succeeded_collectors"`
	Missing     []string `json:"missing_attempt"`
	WeakerWon   bool     `json:"weaker_won"`
	Corrected   string   `json:"corrected_action"`
	HonestState string   `json:"final_honest_state"`
	PatternKey  string   `json:"pattern,omitempty"`
	Action      string   `json:"action,omitempty"`       // guided action key for this pattern ("" = none)
	LastAt      string   `json:"last_attempt,omitempty"` // last guided-action time (RFC3339)
	LastResult  string   `json:"last_result,omitempty"`  // last guided-action result (status: detail)
}

// patternAction maps an actionable trust pattern to its guided action (a stronger collector the
// system can attempt safely). Honest-terminal patterns map to nothing.
var patternAction = map[string]string{
	"esxi_evidence_vsphere_skipped":         "onboard_esxi",
	"redfish_bmc_reachable_not_collected":   "collect_bmc",
	"linux_evidence_ssh_skipped":            "retry_ssh",
	"windows_evidence_deep_collect_skipped": "rerun_windows",
}

type probeResult struct {
	esxi, redfish                      bool
	p443, p902, p5985, p22, p135, p445 bool
	serverHdr                          string
}

// winEvidence / linuxEvidence decide deep collectors from REAL evidence, never from the bare
// category label. A device categorised "server" with no Windows signal must NOT be told it is
// "missing WinRM/WMI".
func winEvidence(d db.Device, pr probeResult, att map[string]bool) bool {
	// 135 (MSRPC/DCOM) and 5985 (WinRM) are Windows-specific. 445 (SMB) is NOT used as evidence on
	// its own — Linux Samba / NAS appliances expose it and are not WMI-manageable.
	return d.OsFamily == "windows" || pr.p5985 || pr.p135 || att["winrm"] || att["wmi"]
}
func linuxEvidence(d db.Device, pr probeResult, att map[string]bool) bool {
	return d.OsFamily == "linux" || pr.p22 || att["ssh"]
}

func (s *Server) trustAudit(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	doProbe := r.URL.Query().Get("probe") != "false" // default ON
	devs, _ := s.queries.ListAllDevices(ctx)
	devs = s.scopeDevices(ctx, devs)
	maps, _ := s.buildStatusMaps(ctx)

	// Attempted + succeeded protocols per device from real attempt history.
	attempted := map[uuid.UUID]map[string]bool{}
	succeeded := map[uuid.UUID]map[string]bool{}
	if rows, e := s.queries.ListCredTestProtocols(ctx); e == nil {
		for _, x := range rows {
			if attempted[x.DeviceID] == nil {
				attempted[x.DeviceID] = map[string]bool{}
				succeeded[x.DeviceID] = map[string]bool{}
			}
			attempted[x.DeviceID][x.Protocol] = true
			if x.AnySuccess {
				succeeded[x.DeviceID][x.Protocol] = true
			}
		}
	}
	// Durable collection-evidence flags (a successful deep collection is also an "attempt+success").
	osInv := idSet(s.collectedDeviceIDs(ctx, "os_inventory"))
	vmHost := idSet(s.collectedDeviceIDs(ctx, "virtual_machines"))
	camInv := idSet(s.collectedDeviceIDs(ctx, "camera_info"))
	hasIface := idSet(s.collectedDeviceIDs(ctx, "interfaces"))
	hasBMC := idSet(s.collectedDeviceIDs(ctx, "bmc_info"))

	// Weak devices = anything not cleanly managed, PLUS compute devices that are "managed" only
	// shallowly (a server/endpoint/virtual_host with NO deep collection evidence — e.g. managed by
	// SNMP alone while OS/vSphere/Redfish was never collected). This catches the "weaker protocol
	// won, stronger skipped" pattern even when the label says managed.
	deepCompute := map[string]bool{"server": true, "endpoint": true, "virtual_host": true}
	type cand struct {
		d     db.Device
		state string
	}
	var weak []cand
	for _, d := range devs {
		st := maps.statusFor(d).Management
		deep := osInv[d.ID] || vmHost[d.ID] || hasBMC[d.ID]
		shallowManaged := st == MgmtManaged && deepCompute[d.Category] && !deep
		if st != MgmtManaged || shallowManaged {
			weak = append(weak, cand{d, st})
		}
	}

	// Bounded active re-probe of the weak hosts (read-only fingerprint).
	probes := map[uuid.UUID]probeResult{}
	if doProbe {
		var mu sync.Mutex
		sem := make(chan struct{}, 24)
		var wg sync.WaitGroup
		for _, c := range weak {
			if c.d.PrimaryIp == nil || !c.d.PrimaryIp.IsValid() {
				continue
			}
			wg.Add(1)
			go func(id uuid.UUID, ip string) {
				defer wg.Done()
				sem <- struct{}{}
				defer func() { <-sem }()
				pr := probeHost(ip)
				mu.Lock()
				probes[id] = pr
				mu.Unlock()
			}(c.d.ID, c.d.PrimaryIp.String())
		}
		wg.Wait()
	}

	patterns := map[string]*patternAgg{}
	rows := make([]auditRow, 0, len(weak))
	for _, c := range weak {
		d := c.d
		att := keys(attempted[d.ID])
		suc := keys(succeeded[d.ID])
		// Fold collection evidence into attempted+succeeded.
		if osInv[d.ID] {
			att, suc = addUniq(att, "wmi"), addUniq(suc, "wmi")
		}
		if vmHost[d.ID] {
			att, suc = addUniq(att, "vmware"), addUniq(suc, "vmware")
		}
		if camInv[d.ID] {
			att, suc = addUniq(att, "onvif"), addUniq(suc, "onvif")
		}
		if hasBMC[d.ID] {
			att, suc = addUniq(att, "redfish"), addUniq(suc, "redfish")
		}
		pr := probes[d.ID]
		ev := buildEvidence(d, suc, pr, hasIface[d.ID])
		attSet := setOf(att)
		expected := expectedCollectors(d, pr, attSet)
		var missing []string
		for _, e := range expected {
			if !attSet[e] {
				missing = append(missing, e)
			}
		}
		// Collapse alternative collectors for the SAME capability: winrm↔wmi are two paths to
		// Windows deep collection, onvif↔isapi to camera inventory. If EITHER was attempted, the
		// capability was attempted — don't report the sibling as a skipped collector.
		if attSet["winrm"] || attSet["wmi"] {
			missing = removeAll(missing, "winrm", "wmi")
		}
		if attSet["onvif"] || attSet["isapi"] {
			missing = removeAll(missing, "onvif", "isapi")
		}
		row := auditRow{
			IP: addrStr(d.PrimaryIp), DeviceID: d.ID.String(), Category: d.Category, State: c.state,
			Evidence: ev, Expected: expected, Attempted: sortUniq(att), Succeeded: sortUniq(suc), Missing: missing,
		}
		// weaker-won = a shallow protocol succeeded but a stronger expected one was never attempted.
		shallow := suc != nil && (contains(suc, "http") || contains(suc, "onvif") || contains(suc, "isapi") || contains(suc, "snmp"))
		strongMissing := contains(missing, "vmware") || contains(missing, "winrm") || contains(missing, "wmi") || contains(missing, "redfish")
		row.WeakerWon = shallow && strongMissing
		row.PatternKey, row.Corrected, row.HonestState = assessAudit(d, c.state, suc, missing, pr)
		row.Action = patternAction[row.PatternKey]
		if la, ok := lastTrustAction(row.DeviceID); ok {
			row.LastAt = la.At
			row.LastResult = la.Status + ": " + la.Detail
		}
		if row.PatternKey != "" {
			pa := patterns[row.PatternKey]
			if pa == nil {
				pa = &patternAgg{cats: map[string]bool{}}
				patterns[row.PatternKey] = pa
			}
			pa.count++
			if len(pa.examples) < 6 {
				pa.examples = append(pa.examples, row.IP)
			}
			pa.cats[d.Category] = true
		}
		rows = append(rows, row)
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].WeakerWon != rows[j].WeakerWon {
			return rows[i].WeakerWon
		}
		return rows[i].IP < rows[j].IP
	})

	pats := make([]map[string]any, 0, len(patterns))
	for k, p := range patterns {
		cats := keys(p.cats)
		sort.Strings(cats)
		pats = append(pats, map[string]any{"pattern": k, "count": p.count, "device_types": cats, "examples": p.examples})
	}
	sort.Slice(pats, func(i, j int) bool { return pats[i]["count"].(int) > pats[j]["count"].(int) })

	writeJSON(w, http.StatusOK, map[string]any{
		"generated_at":  time.Now().UTC().Format(time.RFC3339),
		"probed":        doProbe,
		"total_devices": len(devs),
		"weak_devices":  len(weak),
		"devices":       rows,
		"patterns":      pats,
	})
}

type patternAgg struct {
	count    int
	examples []string
	cats     map[string]bool
}

// expectedCollectors maps a device's EVIDENCE (probe fingerprint, OS family, prior attempts) to the
// collectors that should be attempted — NOT the bare category label. A "server" with no Windows
// signal is never told it is missing WinRM/WMI; a Linux/appliance/BMC/ESXi host gets its real
// applicable collector. Infrastructure classes (switch/camera/etc.) keep their protocol since the
// category there IS the evidence (it was classified from SNMP/ONVIF/etc.).
func expectedCollectors(d db.Device, pr probeResult, att map[string]bool) []string {
	set := map[string]bool{}
	add := func(xs ...string) {
		for _, x := range xs {
			set[x] = true
		}
	}
	switch d.Category {
	case "switch", "router", "firewall", "printer", "ups", "storage", "pdu":
		add("snmp")
	case "camera", "nvr", "dvr":
		add("onvif", "isapi")
	case "access_point", "wireless_controller":
		add("vendor_api", "snmp")
	case "pbx", "ip_phone", "voice_gateway":
		add("vendor_api")
	case "virtual_host":
		if d.DeviceClass != nil && *d.DeviceClass == "hyperv" {
			add("wmi")
		} else {
			add("vmware")
		}
		// server / endpoint / unknown: purely evidence-driven (handled below); no blanket WinRM/WMI.
	}
	// Evidence-driven deep collectors (apply to every class; evidence beats the label).
	if pr.esxi {
		add("vmware") // vSphere SDK present
	}
	if pr.redfish {
		add("redfish") // iLO/iDRAC/Redfish BMC present
	}
	if winEvidence(d, pr, att) {
		add("winrm", "wmi") // 5985/135/445 open, Windows OS, or a prior WinRM/WMI attempt
	}
	if linuxEvidence(d, pr, att) {
		add("ssh") // 22 open, Linux OS, or a prior SSH attempt
	}
	return sortUniq(keys(set))
}

func buildEvidence(d db.Device, suc []string, pr probeResult, iface bool) []string {
	var ev []string
	if d.OsFamily != "" {
		ev = append(ev, "os:"+d.OsFamily)
	}
	if d.Vendor != nil && *d.Vendor != "" {
		ev = append(ev, "vendor:"+*d.Vendor)
	}
	if pr.esxi {
		ev = append(ev, "vSphere SDK (vim25)")
	}
	if pr.redfish {
		ev = append(ev, "Redfish/BMC")
	}
	if pr.p902 {
		ev = append(ev, "tcp/902")
	}
	if pr.p5985 {
		ev = append(ev, "tcp/5985(WinRM)")
	}
	if pr.p135 || pr.p445 {
		ev = append(ev, "tcp/135-445(Windows RPC/SMB)")
	}
	if pr.p22 {
		ev = append(ev, "tcp/22(SSH)")
	}
	if pr.p443 {
		ev = append(ev, "tcp/443")
	}
	if pr.serverHdr != "" {
		ev = append(ev, "http:"+pr.serverHdr)
	}
	if iface {
		ev = append(ev, "snmp-interfaces")
	}
	for _, p := range suc {
		ev = append(ev, "auth-ok:"+p)
	}
	return ev
}

// assessAudit returns (patternKey, correctedAction, honestState). The `missing` set is already
// evidence-gated (a collector only appears there if its evidence exists), so a missing deep
// collector is always a REAL skip, never a category-default guess.
func assessAudit(d db.Device, state string, suc, missing []string, pr probeResult) (string, string, string) {
	switch {
	// --- real skipped-collector findings (evidence present, collector not attempted) ---
	case contains(missing, "vmware"):
		return "esxi_evidence_vsphere_skipped",
			"Classify as ESXi virtual_host and run vSphere collection (collect-vsphere).",
			"INCOMPLETE: vSphere SDK present but vmware collector never attempted."
	case contains(missing, "redfish"):
		return "redfish_bmc_reachable_not_collected",
			"Add an iLO/iDRAC (http_basic) credential, then collect BMC via the Redfish driver.",
			"INCOMPLETE: Redfish/BMC reachable but no BMC collected (no valid iLO credential)."
	case contains(missing, "winrm") || contains(missing, "wmi"):
		return "windows_evidence_deep_collect_skipped",
			"Windows evidence present — route to the relay agent for WMI/DCOM (Win2008 WinRM negotiation fails); verify the credential.",
			"INCOMPLETE: Windows host with no deep (WMI/WinRM) collection attempted."
	case contains(missing, "ssh"):
		return "linux_evidence_ssh_skipped",
			"SSH/Linux evidence present — attempt the SSH collector / verify the SSH credential.",
			"INCOMPLETE: SSH evidence present but the SSH collector was not attempted."
	// --- honest terminal states (no applicable collector was skipped) ---
	case state == MgmtCredentialFailed:
		return "credential_failed_real",
			"Supply/correct the credential for the applicable protocol, then re-collect.",
			"Honest: every applicable credential was attempted and rejected."
	case state == MgmtNotAuthorized:
		return "authenticated_but_access_denied",
			"Grant the account WMI/DCOM/API rights on the host (host-side), then re-scan.",
			"Honest: credential authenticates but host denies access."
	case len(missing) > 0:
		// A non-deep collector (e.g. snmp) is applicable but unattempted.
		return "applicable_collector_skipped",
			"Attempt the applicable collector (" + strings.Join(missing, ",") + ").",
			"INCOMPLETE: an applicable collector (" + strings.Join(missing, ",") + ") was not attempted."
	case len(suc) == 0:
		return "unidentified_no_working_collector",
			"No collector authenticated. Add correct credentials or classify manually; confirm the device is in scope.",
			"Honest: reachable but no applicable credential succeeded — unidentified."
	case state == MgmtWebAuthenticated || (contains(suc, "http") && !hasDeep(suc)):
		return "web_only_no_deeper_evidence",
			"Web login works and no stronger protocol is evidenced — treat as web-only/unsupported, or add deep evidence (enable SNMP/WinRM/SSH/API on the host).",
			"Honest: web-only — only an HTTP login authenticated and no deeper collector is applicable from current evidence."
	case state == MgmtManaged:
		return "managed_shallow_no_deeper_evidence",
			"Managed via a shallow protocol (e.g. SNMP) and no deeper collector is evidenced — acceptable unless this host should expose WinRM/SSH/Redfish/vSphere.",
			"Honest: managed as fully as current evidence allows (no deeper collector applicable)."
	}
	return "", "Review.", "Honest: weak state with no stronger collector evidenced."
}

// hasDeep reports whether a succeeded-protocol set already includes a deep collector.
func hasDeep(suc []string) bool {
	for _, p := range suc {
		switch p {
		case "winrm", "wmi", "ssh", "vmware", "redfish":
			return true
		}
	}
	return false
}

// probeHost does a bounded, read-only fingerprint of a host.
func probeHost(ip string) probeResult {
	var pr probeResult
	dial := func(port string) bool {
		c, err := net.DialTimeout("tcp", net.JoinHostPort(ip, port), 1500*time.Millisecond)
		if err != nil {
			return false
		}
		_ = c.Close()
		return true
	}
	pr.p443 = dial("443")
	pr.p902 = dial("902")
	pr.p5985 = dial("5985")
	pr.p22 = dial("22")
	pr.p135 = dial("135")
	pr.p445 = dial("445")
	client := &http.Client{
		Timeout:       4 * time.Second,
		Transport:     &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	if pr.p443 {
		if body, hdr, ok := httpGet(client, "https://"+ip+"/sdk/vimServiceVersions.xml"); ok {
			if strings.Contains(body, "vim25") {
				pr.esxi = true
			}
			_ = hdr
		}
		if body, _, ok := httpGet(client, "https://"+ip+"/redfish/v1/"); ok {
			if strings.Contains(body, "RedfishVersion") || strings.Contains(body, "ServiceRoot") || strings.Contains(body, "@odata.id") {
				pr.redfish = true
			}
		}
		if _, hdr, ok := httpGet(client, "https://"+ip+"/"); ok {
			pr.serverHdr = strings.TrimSpace(hdr.Get("Server"))
			if len(pr.serverHdr) > 24 {
				pr.serverHdr = pr.serverHdr[:24]
			}
		}
	}
	return pr
}

func httpGet(c *http.Client, url string) (string, http.Header, bool) {
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, url, nil)
	if err != nil {
		return "", nil, false
	}
	resp, err := c.Do(req)
	if err != nil {
		return "", nil, false
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
	return string(b), resp.Header, true
}

// collectedDeviceIDs returns device IDs (or host_device_id for virtual_machines) with >=1 row in a
// collection-evidence table — a proven successful collection.
func (s *Server) collectedDeviceIDs(ctx context.Context, table string) []uuid.UUID {
	if s.pool == nil {
		return nil
	}
	col := "device_id"
	if table == "virtual_machines" {
		col = "host_device_id"
	}
	rows, err := s.pool.Query(ctx, "SELECT DISTINCT "+col+" FROM "+table+" WHERE "+col+" IS NOT NULL")
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if rows.Scan(&id) == nil {
			out = append(out, id)
		}
	}
	return out
}

// --- small helpers ---
func idSet(ids []uuid.UUID) map[uuid.UUID]bool {
	m := make(map[uuid.UUID]bool, len(ids))
	for _, id := range ids {
		m[id] = true
	}
	return m
}
func keys[T comparable](m map[T]bool) []T {
	out := make([]T, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
func setOf(xs []string) map[string]bool {
	m := make(map[string]bool, len(xs))
	for _, x := range xs {
		m[x] = true
	}
	return m
}
func contains(xs []string, v string) bool {
	for _, x := range xs {
		if x == v {
			return true
		}
	}
	return false
}
func addUniq(xs []string, v string) []string {
	if contains(xs, v) {
		return xs
	}
	return append(xs, v)
}
func removeAll(xs []string, drop ...string) []string {
	ds := setOf(drop)
	out := xs[:0:0]
	for _, x := range xs {
		if !ds[x] {
			out = append(out, x)
		}
	}
	return out
}
func sortUniq(xs []string) []string {
	m := setOf(xs)
	out := keys(m)
	sort.Strings(out)
	return out
}
