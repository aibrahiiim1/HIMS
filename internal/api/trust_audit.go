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
}

type probeResult struct {
	esxi, redfish                bool
	p443, p902, p5985, p22, p161 bool
	serverHdr                    string
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
		expected := expectedCollectors(d, pr)
		attSet := setOf(att)
		var missing []string
		for _, e := range expected {
			if !attSet[e] {
				missing = append(missing, e)
			}
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

// expectedCollectors maps a device's evidence (category, OS, vendor, and live probe) to the
// collectors that SHOULD be attempted.
func expectedCollectors(d db.Device, pr probeResult) []string {
	set := map[string]bool{}
	add := func(xs ...string) {
		for _, x := range xs {
			set[x] = true
		}
	}
	switch d.Category {
	case "switch", "router":
		add("snmp")
	case "firewall":
		add("snmp")
	case "camera", "nvr", "dvr":
		add("onvif", "isapi")
	case "access_point", "wireless_controller":
		add("vendor_api", "snmp")
	case "pbx", "ip_phone", "voice_gateway":
		add("vendor_api")
	case "printer", "ups", "storage", "pdu":
		add("snmp")
	case "virtual_host":
		if d.DeviceClass != nil && *d.DeviceClass == "hyperv" {
			add("wmi")
		} else {
			add("vmware")
		}
	case "server", "endpoint":
		if d.OsFamily == "linux" {
			add("ssh")
		} else {
			add("winrm", "wmi")
		}
	}
	// Live probe overrides the label: evidence beats the stored category.
	if pr.esxi {
		add("vmware")
	}
	if pr.redfish {
		add("redfish")
	}
	if pr.p5985 {
		add("winrm", "wmi")
	}
	if d.OsFamily == "windows" {
		add("winrm", "wmi")
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

// assessAudit returns (patternKey, correctedAction, honestState).
func assessAudit(d db.Device, state string, suc, missing []string, pr probeResult) (string, string, string) {
	switch {
	case pr.esxi && contains(missing, "vmware"):
		return "esxi_evidence_vsphere_skipped",
			"Classify as ESXi virtual_host and run vSphere collection (collect-vsphere).",
			"INCOMPLETE: vSphere SDK present but vmware collector never attempted."
	case pr.redfish && contains(missing, "redfish"):
		return "redfish_bmc_reachable_not_collected",
			"Add an iLO/iDRAC (http_basic) credential, then collect BMC via the Redfish driver.",
			"INCOMPLETE: Redfish/BMC reachable but no BMC collected (no valid iLO credential)."
	case (pr.p5985 || d.OsFamily == "windows") && (contains(missing, "winrm") || contains(missing, "wmi")):
		return "windows_evidence_deep_collect_skipped",
			"Route to the relay agent for WMI/DCOM (Win2008 WinRM negotiation fails); verify the credential.",
			"INCOMPLETE: Windows host with no deep (WMI/WinRM) collection attempted."
	case state == MgmtCredentialFailed:
		return "credential_failed_real",
			"Supply/correct the credential for the applicable protocol, then re-collect.",
			"Honest: every applicable credential was attempted and rejected."
	case state == MgmtNotAuthorized:
		return "authenticated_but_access_denied",
			"Grant the account WMI/DCOM/API rights on the host (host-side), then re-scan.",
			"Honest: credential authenticates but host denies access."
	case len(suc) == 0:
		return "unidentified_no_working_collector",
			"No collector authenticated. Add correct credentials or classify manually; confirm the device is in scope.",
			"Honest: reachable but no applicable credential succeeded — unidentified."
	case state == MgmtWebAuthenticated && len(missing) > 0:
		return "web_authenticated_deep_skipped",
			"A deep collector (" + strings.Join(missing, ",") + ") is applicable — attempt it.",
			"INCOMPLETE: only a web login succeeded; a deeper collector applies."
	case state == MgmtManaged && len(missing) > 0:
		return "managed_shallow_stronger_skipped",
			"Managed only by a shallow protocol; attempt the deeper collector (" + strings.Join(missing, ",") + ").",
			"INCOMPLETE: managed by a shallow protocol (e.g. SNMP) while a deeper collector applies."
	}
	return "", "Review.", "Weak state with no stronger collector evidenced."
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
func sortUniq(xs []string) []string {
	m := setOf(xs)
	out := keys(m)
	sort.Strings(out)
	return out
}
