package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/coralsearesorts/hims/internal/apply"
	"github.com/coralsearesorts/hims/internal/classify"
	"github.com/coralsearesorts/hims/internal/credresolver"
	"github.com/coralsearesorts/hims/internal/credtest"
	"github.com/coralsearesorts/hims/internal/discovery"
	"github.com/coralsearesorts/hims/internal/domain"
	"github.com/coralsearesorts/hims/internal/driver"
	"github.com/coralsearesorts/hims/internal/nas"
	"github.com/coralsearesorts/hims/internal/scan"
	"github.com/coralsearesorts/hims/internal/storage/postgres/db"
)

const scanMaxHosts = 4096

// scanReq drives every network-scan input mode. The operator supplies ONE of:
//   - Targets: free-text single IP / IP-range / CIDR / mixed list (mode "targets")
//   - LocationID with Mode "site_subnets": scan every subnet bound to that site
//   - CIDR: legacy single-CIDR field (back-compat; equivalent to Targets)
//
// CredentialIDs optionally pins which stored credentials this scan may use
// (highest-priority tier; the resolver still orders + binds per-probe). When
// empty, the scan auto-tries ALL stored credentials. CredentialGroupIDs is the
// older group-based selector, still honored if supplied.
type scanReq struct {
	Targets            string   `json:"targets"`
	CIDR               string   `json:"cidr"` // legacy / single-CIDR
	Mode               string   `json:"mode"` // "targets" | "site_subnets"
	LocationID         *string  `json:"location_id"`
	CredentialIDs      []string `json:"credential_ids"`
	CredentialGroupIDs []string `json:"credential_group_ids"`
	Concurrency        int      `json:"concurrency"`
	// Exclude carves IPs out of the resolved scope (single IP / range / CIDR /
	// comma-or-space-separated mix) — e.g. scan a /24 but skip a few hosts.
	Exclude string `json:"exclude"`
}

// osCollectionCandidate decides whether the discovery pipeline should attempt a
// deep OS collection for an enrolled device. It deliberately takes NO open-port
// list: a Windows host is ALWAYS worth an attempt — even if it bound no
// WinRM/SSH credential this run and even if no Windows management port
// (445/135/5985/5986) was observed in this run's port scan. runOSCollection
// tries WinRM, then FALLS BACK to the site Relay Agent (WMI/DCOM), which works
// where WinRM is off and regardless of which TCP ports the scan happened to
// catch, and returns an HONEST reason (no_credential / agent_missing /
// winrm_disabled / wmi_firewall_blocked) when nothing works — never a false auth
// failure and never a silent skip.
//
// Gating on an OBSERVED management port was the bug that silently left
// late/slow-probed Windows endpoints (port not seen this run) enrolled with ZERO
// collection attempts, stuck at "needs_credential". Keeping ports OUT of this
// signature makes that regression impossible to reintroduce. Specialized
// appliances (wireless / VMware / voice / CCTV) have their own collection branch
// and are excluded here.
func osCollectionCandidate(d db.Device, boundOS, legacyWSMan, specialized, winMgmtPort bool) bool {
	if specialized {
		return false
	}
	// A NAS appliance is never a WinRM/OS-collection target — it has its own
	// dedicated SNMP collector (the storage branch below). Without this, a QNAP
	// that serves SMB (445) trips winMgmtPort, is treated as a Windows host, dead-
	// ends at "OS collection incomplete: unsupported_os", and — because the NAS
	// collection is an else-if — never runs. Exclude storage so it reaches its branch.
	if d.Category == string(domain.CatStorage) {
		return false
	}
	winHost := d.OsFamily == domain.OSFamilyWindows || d.Category == string(domain.CatEndpoint)
	// winMgmtPort: the host answered on a Windows management port (WinRM 5985/5986, RPC 135,
	// or SMB 445) THIS run. A host speaking WinRM/RPC is a Windows host worth a deep OS
	// collection even when it enrolled as category=server with a blank os_family (the gap
	// that left .67/.68/.116 — legacy-auth-OK Windows servers — never attempted and stuck at
	// needs_agent: runOSCollection tries WinRM, gets auth_ok_operation_fault, and routes to
	// the site Relay Agent for WMI/DCOM). This is an ADDITIONAL positive trigger, never a
	// gate — a host WITHOUT the port still qualifies via winHost/boundOS/legacyWSMan, so the
	// "gated on an observed management port" regression cannot return.
	return boundOS || legacyWSMan || winHost || winMgmtPort
}

// startScan launches a background subnet scan and returns the job immediately
// (202). The scan runs in its own goroutine writing progress to the
// discovery_jobs / discovery_results tables; the UI polls the job.
func (s *Server) startScan(w http.ResponseWriter, r *http.Request) {
	if s.reg == nil || s.fetcher == nil {
		http.Error(w, "discovery not configured on this server", http.StatusServiceUnavailable)
		return
	}
	var req scanReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}
	locID := parseUUIDPtr(req.LocationID)

	// Resolve the input mode into a host list (+ a scope label for the job).
	hosts, scopeLabel, err := s.resolveScanHosts(r.Context(), req, locID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if len(hosts) == 0 {
		http.Error(w, "no hosts in scan scope", http.StatusBadRequest)
		return
	}

	// Build the explicit credential tier: the operator-selected credentials, or
	// (when none are selected) ALL stored credentials — the "auto-detect from
	// the whole credential list" default. Group selection, if supplied, is
	// merged in too.
	extra, err := s.scanCredentialTier(r.Context(), req.CredentialIDs, req.CredentialGroupIDs)
	if err != nil {
		if _, ok := err.(*badRequest); ok {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeErr(w, err)
		return
	}

	// Timeouts + default concurrency come from operator Settings.
	snmpTO, portTO, defConcurrency := s.scanSettings(r.Context())
	concurrency := req.Concurrency
	if concurrency < 1 || concurrency > 64 {
		concurrency = defConcurrency
	}

	var scopePrefix *netip.Prefix
	if p, perr := netip.ParsePrefix(scopeLabel); perr == nil {
		scopePrefix = &p
	}
	job, err := s.queries.CreateDiscoveryJob(r.Context(), db.CreateDiscoveryJobParams{
		LocationID: locID, ScopeCidr: scopePrefix,
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	_ = s.queries.UpdateDiscoveryJobStatus(r.Context(), db.UpdateDiscoveryJobStatusParams{
		ID: job.ID, Status: "running", HostCount: int32(len(hosts)), FoundCount: 0,
	})
	// Persist the spec so the job can be re-run verbatim (any mode, not just CIDR).
	if spec, err := json.Marshal(rerunSpec{
		Mode: req.Mode, Targets: req.Targets, CIDR: req.CIDR,
		CredentialIDs: req.CredentialIDs, CredentialGroupIDs: req.CredentialGroupIDs,
		Exclude: req.Exclude,
	}); err == nil {
		_ = s.queries.SetDiscoveryJobMetadata(r.Context(), db.SetDiscoveryJobMetadataParams{ID: job.ID, Metadata: spec})
	}

	// An explicit per-scan credential selection (specific creds or groups chosen
	// in the dialog) overrides the standing subnet-scoped set — only those are
	// tried. An empty selection ("all stored, auto") leaves subnet scope in force.
	explicitCreds := len(req.CredentialIDs) > 0 || len(req.CredentialGroupIDs) > 0
	go s.runScanJob(job.ID, hosts, locID, concurrency, extra, explicitCreds, snmpTO, portTO)
	s.audit(r, "discovery", "discovery.scan", "discovery_job", job.ID.String(), "Launched discovery scan ("+scopeLabel+")", map[string]any{"hosts": len(hosts), "mode": req.Mode})
	writeJSON(w, http.StatusAccepted, job)
}

// scanPreflight handles GET /discovery/scan-preflight. Before a scan starts it
// reports what protocols the operator is actually equipped to authenticate with
// — credential counts by kind + VMware/CCTV profile counts for the selected site
// — plus warnings naming the gaps ("No WinRM credential …"). This sets honest
// expectations: a subnet of Windows PCs with no WinRM credential will not be
// onboarded, and the operator learns that up front instead of from a wall of
// auth_failed results. No secrets returned.
func (s *Server) scanPreflight(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	locID := parseUUIDPtr(strPtr(r.URL.Query().Get("location_id")))
	selected := splitCSV(r.URL.Query().Get("credential_ids"))

	creds, err := s.queries.ListCredentials(ctx)
	if err != nil {
		writeErr(w, err)
		return
	}
	selSet := map[string]bool{}
	for _, id := range selected {
		selSet[id] = true
	}
	counts := map[string]int{"snmp": 0, "ssh": 0, "windows": 0, "onvif": 0, "http_basic": 0, "vendor_api": 0}
	for _, c := range creds {
		if len(selSet) > 0 && !selSet[c.ID.String()] {
			continue
		}
		switch c.Kind {
		case string(domain.CredSNMPv2c), string(domain.CredSNMPv3):
			counts["snmp"]++
		case string(domain.CredSSH):
			counts["ssh"]++
		case string(domain.CredWindows), string(domain.CredWinRM), string(domain.CredWMI):
			counts["windows"]++
		case string(domain.CredONVIF):
			counts["onvif"]++
		case string(domain.CredHTTPBasic):
			counts["http_basic"]++
		case string(domain.CredVendorAPI):
			counts["vendor_api"]++
		}
	}

	// VMware / CCTV profiles applicable to the selected site (site-bound or global).
	vmware, cctv := 0, 0
	if profs, perr := s.queries.ListVendorProfiles(ctx); perr == nil {
		for _, p := range profs {
			if !p.Enabled {
				continue
			}
			applies := p.LocationID == nil || (locID != nil && *p.LocationID == *locID)
			if !applies {
				continue
			}
			switch {
			case p.VendorType == "vmware":
				vmware++
			case p.VendorType == "cctv":
				cctv++
			}
		}
	}

	var warnings []string
	if counts["windows"] == 0 {
		warnings = append(warnings, "No Windows credential available — Windows hosts cannot be onboarded (deep OS inventory via WinRM/WMI/agent).")
	}
	if counts["ssh"] == 0 {
		warnings = append(warnings, "No SSH credential available — Linux hosts and CLI-managed network gear cannot be onboarded.")
	}
	if counts["snmp"] == 0 {
		warnings = append(warnings, "No SNMP credential available — switches/routers/printers/UPS rely on default communities only.")
	}
	if counts["onvif"] == 0 {
		warnings = append(warnings, "No ONVIF credential available — cameras/NVRs cannot be authenticated (configure a CCTV Vendor Profile or ONVIF credential).")
	}
	if vmware == 0 {
		warnings = append(warnings, "No VMware profile assigned to this site — ESXi/vCenter hosts will be detected but not collected.")
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"credential_counts": counts,
		"vmware_profiles":   vmware,
		"cctv_profiles":     cctv,
		"warnings":          warnings,
	})
}

func splitCSV(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if t := strings.TrimSpace(p); t != "" {
			out = append(out, t)
		}
	}
	return out
}

// nativeCollectorStatus handles GET /discovery/native-collector-status. Reports
// ONLY whether the Windows Native Collector env vars are configured (booleans) —
// never the URL or token values. Powers the legacy-Windows Onboarding card.
func (s *Server) nativeCollectorStatus(w http.ResponseWriter, r *http.Request) {
	url, token := nativeCollectorConfig()
	writeJSON(w, http.StatusOK, map[string]any{
		"url_configured":   url != "",
		"token_configured": token != "",
	})
}

// nativeCollectorTest handles POST /discovery/native-collector-test. Confirms the
// configured Windows Native Collector URL is reachable (any HTTP response counts
// as reachable). No credential is sent; no secret is returned.
func (s *Server) nativeCollectorTest(w http.ResponseWriter, r *http.Request) {
	url, _ := nativeCollectorConfig()
	if url == "" {
		writeJSON(w, http.StatusOK, map[string]any{"configured": false, "reachable": false, "detail": "Windows Native Collector not configured (set HIMS_WINDOWS_NATIVE_COLLECTOR_URL)."})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 6*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"configured": true, "reachable": false, "detail": "invalid collector URL"})
		return
	}
	cl := &http.Client{Timeout: 6 * time.Second, Transport: insecureDoer(6 * time.Second).Transport}
	resp, derr := cl.Do(req)
	if derr != nil {
		writeJSON(w, http.StatusOK, map[string]any{"configured": true, "reachable": false, "detail": shortErr(derr)})
		return
	}
	_ = resp.Body.Close()
	writeJSON(w, http.StatusOK, map[string]any{"configured": true, "reachable": true, "detail": "collector responded (HTTP " + itoaN(resp.StatusCode) + ")"})
}

// wmiCollectorStatus handles GET /discovery/wmi-collector-status — booleans only.
func (s *Server) wmiCollectorStatus(w http.ResponseWriter, r *http.Request) {
	url, token := wmiCollectorConfig()
	writeJSON(w, http.StatusOK, map[string]any{"url_configured": url != "", "token_configured": token != ""})
}

// wmiCollectorTest handles POST /discovery/wmi-collector-test — reachability of
// the configured WMI collector helper URL.
func (s *Server) wmiCollectorTest(w http.ResponseWriter, r *http.Request) {
	url, _ := wmiCollectorConfig()
	if url == "" {
		writeJSON(w, http.StatusOK, map[string]any{"configured": false, "reachable": false, "detail": "WMI/DCOM collector not configured (set HIMS_WMI_COLLECTOR_URL)."})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 6*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"configured": true, "reachable": false, "detail": "invalid collector URL"})
		return
	}
	cl := &http.Client{Timeout: 6 * time.Second, Transport: insecureDoer(6 * time.Second).Transport}
	resp, derr := cl.Do(req)
	if derr != nil {
		writeJSON(w, http.StatusOK, map[string]any{"configured": true, "reachable": false, "detail": shortErr(derr)})
		return
	}
	_ = resp.Body.Close()
	writeJSON(w, http.StatusOK, map[string]any{"configured": true, "reachable": true, "detail": "collector responded (HTTP " + itoaN(resp.StatusCode) + ")"})
}

// rerunSpec is the scan request persisted in a job's metadata for re-runs.
type rerunSpec struct {
	Mode               string   `json:"mode"`
	Targets            string   `json:"targets"`
	CIDR               string   `json:"cidr"`
	CredentialIDs      []string `json:"credential_ids"`
	CredentialGroupIDs []string `json:"credential_group_ids"`
	Exclude            string   `json:"exclude,omitempty"`
}

// resolveScanHosts expands the request's input mode into a host list. It
// returns a scope label (a CIDR string when the scope is a single prefix, for
// the job record; otherwise a free-text summary).
func (s *Server) resolveScanHosts(ctx context.Context, req scanReq, locID *uuid.UUID) ([]netip.Addr, string, error) {
	var hosts []netip.Addr
	var label string

	if req.Mode == "site_subnets" {
		if locID == nil {
			return nil, "", errBadRequest("site_subnets mode requires location_id")
		}
		subnets, err := s.queries.ListSubnetsByLocation(ctx, *locID)
		if err != nil {
			return nil, "", err
		}
		if len(subnets) == 0 {
			return nil, "", errBadRequest("no subnets configured for this site")
		}
		for _, sn := range subnets {
			h, err := discovery.ExpandCIDR(sn.Cidr, scanMaxHosts)
			if err != nil {
				return nil, "", err
			}
			hosts = append(hosts, h...)
			if len(hosts) > scanMaxHosts {
				return nil, "", errBadRequest("site subnets expand beyond the scan cap; scan a subset")
			}
		}
		label = "site_subnets"
	} else {
		spec := req.Targets
		if spec == "" {
			spec = req.CIDR // legacy single-CIDR field
		}
		if spec == "" {
			return nil, "", errBadRequest("provide targets (IP / range / CIDR) or location_id with mode=site_subnets")
		}
		h, err := discovery.ParseTargets(spec, scanMaxHosts)
		if err != nil {
			return nil, "", err
		}
		hosts, label = h, spec
	}

	// Carve out operator-excluded IPs/ranges/CIDRs from the resolved scope — so a
	// /24 or site-subnet scan can skip a few specific hosts the operator names.
	if strings.TrimSpace(req.Exclude) != "" {
		filtered, removed, err := discovery.FilterExcluded(hosts, req.Exclude, scanMaxHosts)
		if err != nil {
			return nil, "", errBadRequest(err.Error())
		}
		hosts = filtered
		if removed > 0 {
			label = fmt.Sprintf("%s (excluded %d)", label, removed)
		}
		if len(hosts) == 0 {
			return nil, "", errBadRequest("every target was excluded — nothing left to scan")
		}
	}

	return hosts, label, nil
}

// explicitGroups loads the operator-selected credential groups' members into
// the resolver-input shape, as a single highest-specificity ScopedGroup. The
// secrets are NOT decrypted here — only the candidate refs are loaded; the
// pipeline decrypts a credential only when it is about to try it.
// scanCredentialTier builds the explicit credential candidate tier for a scan:
// the operator-selected credentials, or — when none are selected — ALL stored
// credentials (the "auto-detect from the whole credential list" default). Any
// selected credential groups are merged in as an additional tier. All tiers
// sit above scope-resolved candidates; the resolver still orders by
// fingerprint/weakness/priority and binds on first success.
func (s *Server) scanCredentialTier(ctx context.Context, credIDStrs, groupIDStrs []string) ([]credresolver.ScopedGroup, error) {
	var out []credresolver.ScopedGroup

	// Credential tier: selected ids, else all.
	var members []credresolver.CredRef
	if len(credIDStrs) > 0 {
		ids := make([]uuid.UUID, 0, len(credIDStrs))
		for _, str := range credIDStrs {
			id, err := uuid.Parse(str)
			if err != nil {
				return nil, errBadRequest("invalid credential_id: " + str)
			}
			ids = append(ids, id)
		}
		rows, err := s.queries.ListCredentialCandidatesByIDs(ctx, ids)
		if err != nil {
			return nil, err
		}
		for _, m := range rows {
			members = append(members, credresolver.CredRef{ID: m.ID, Kind: domain.CredentialKind(m.Kind), Weak: m.Weak})
		}
	} else {
		rows, err := s.queries.ListCredentialCandidates(ctx)
		if err != nil {
			return nil, err
		}
		for _, m := range rows {
			members = append(members, credresolver.CredRef{ID: m.ID, Kind: domain.CredentialKind(m.Kind), Weak: m.Weak})
		}
	}
	if len(members) > 0 {
		out = append(out, credresolver.ScopedGroup{Specificity: 100, Members: members})
	}

	// Optional group tier (older selector; still honored if supplied).
	if len(groupIDStrs) > 0 {
		ids := make([]uuid.UUID, 0, len(groupIDStrs))
		for _, str := range groupIDStrs {
			id, err := uuid.Parse(str)
			if err != nil {
				return nil, errBadRequest("invalid credential_group_id: " + str)
			}
			ids = append(ids, id)
		}
		rows, err := s.queries.ListCredentialGroupMembers(ctx, ids)
		if err != nil {
			return nil, err
		}
		gm := make([]credresolver.CredRef, 0, len(rows))
		for _, m := range rows {
			gm = append(gm, credresolver.CredRef{ID: m.ID, Kind: domain.CredentialKind(m.Kind), Priority: int(m.Priority), Weak: m.Weak})
		}
		if len(gm) > 0 {
			out = append(out, credresolver.ScopedGroup{Specificity: 100, Members: gm})
		}
	}
	return out, nil
}

// cctvWebCredsForScan returns the web (ONVIF/HTTP-Basic) credentials to try for a
// camera during a scan, honouring subnet scope. If the IP's site subnet has
// assigned credentials, ONLY its web creds are returned (and a human label of the
// subnet) — even an empty set, so the global scan web creds are NOT sprayed at a
// scoped CCTV subnet. With no subnet scope, the scan's selected web creds (the
// fallback) are returned and the label is empty.
func (s *Server) cctvWebCredsForScan(ctx context.Context, ip netip.Addr, locID *uuid.UUID, fallback []uuid.UUID) ([]uuid.UUID, string) {
	scoped, err := s.queries.SubnetScopedCredentialsForIP(ctx, db.SubnetScopedCredentialsForIPParams{LocationID: locID, Ip: ip})
	if err != nil || len(scoped) == 0 {
		return fallback, ""
	}
	var web []uuid.UUID
	for _, c := range scoped {
		if c.Kind == string(domain.CredONVIF) || c.Kind == string(domain.CredHTTPBasic) {
			web = append(web, c.ID)
		}
	}
	label := scoped[0].Cidr
	if scoped[0].SubnetName != nil && *scoped[0].SubnetName != "" {
		label = *scoped[0].SubnetName + " " + scoped[0].Cidr
	}
	return web, label
}

// runScanJob is the background scan worker. It owns its own context (the HTTP
// request's is long gone) and records per-host outcomes + a final job status.
// recordSweepMetadata merges the liveness-sweep outcome into the job's metadata
// so the scan page can report "N alive of M" and name any port it stopped
// trusting. It MERGES: metadata already carries the scan spec used to re-run the
// job, which must not be clobbered.
func (s *Server) recordSweepMetadata(ctx context.Context, jobID uuid.UUID, sw discovery.SweepResult) {
	meta := map[string]any{}
	if job, err := s.queries.GetDiscoveryJob(ctx, jobID); err == nil && len(job.Metadata) > 0 {
		_ = json.Unmarshal(job.Metadata, &meta)
	}
	untrusted := make([]map[string]any, 0, len(sw.Promiscuous))
	for _, p := range sw.Promiscuous {
		untrusted = append(untrusted, map[string]any{
			"port": p.Port, "reason": p.Reason, "open_count": p.OpenCount,
			"total": p.Total, "proved_by_control": p.ViaControl,
		})
	}
	meta["liveness"] = map[string]any{
		"total":                   sw.Total,
		"alive":                   len(sw.Alive),
		"no_response":             len(sw.NoResponse),
		"suppressed_by_middlebox": len(sw.SuppressedByMiddlebox),
		"untrusted_ports":         untrusted,
		"summary":                 sw.Summary(),
	}
	if blob, err := json.Marshal(meta); err == nil {
		_ = s.queries.SetDiscoveryJobMetadata(ctx, db.SetDiscoveryJobMetadataParams{ID: jobID, Metadata: blob})
	}
}

func (s *Server) runScanJob(jobID uuid.UUID, hosts []netip.Addr, locID *uuid.UUID, concurrency int, extraGroups []credresolver.ScopedGroup, explicitCreds bool, snmpTO, portTO time.Duration) {
	// Overall job budget scales with the host count: a flat 30m can't cover a large
	// multi-subnet scan whose deep collection is slow (camera ONVIF/ISAPI walks),
	// which truncated the tail of a 764-host two-subnet scan ("context deadline
	// exceeded" with the last /24 left unprocessed). Budget ~10s/host on a 45m floor,
	// capped at 4h so a hung run can't linger indefinitely.
	deadline := 45 * time.Minute
	if perHost := time.Duration(len(hosts)) * 10 * time.Second; perHost > deadline {
		deadline = perHost
	}
	if deadline > 4*time.Hour {
		deadline = 4 * time.Hour
	}
	ctx, cancel := context.WithTimeout(context.Background(), deadline)
	defer cancel()

	cfg := discovery.PipelineConfig{
		Registry: s.reg, Fetcher: s.fetcher, Decrypt: s.scanDecrypt,
		ExtraGroups: extraGroups, ExplicitCreds: explicitCreds,
		SNMPTimeout: snmpTO, PortTimeout: portTO,
		// Vendor-fingerprint library (operator ∪ built-in) — overrides generic
		// driver categories from product evidence (e.g. ExtremeCloud IQ Controller
		// → wireless_controller, not "Extreme switch"). Loaded once per job.
		Fingerprints: s.scanFingerprintLibrary(ctx),
		// Operator-configured HTTP/Web candidate ports (Settings → Web Ports) are
		// added to the scan's TCP port set so a device's custom web port (8008/8012/
		// 8081…) is discovered open and stored, then preferred by the collectors.
		ExtraPorts: s.enabledWebPorts(ctx),
	}
	applier := apply.New(s.queries)

	// Per-device site resolution. When the scan itself carries no site (a targets /
	// CIDR scan, locID == nil), resolve each device's location from the configured
	// subnet→site mappings so a device whose IP falls inside a site's subnet (e.g.
	// 172.21.60.0/24 → CHR) is auto-assigned that site — and an EXISTING device left
	// with a null location gets it filled on re-scan (reconcile COALESCEs the
	// non-nil FillLocation). A site-scoped scan's explicit location always wins.
	resolveLoc := s.subnetLocationResolver(ctx, locID)

	// Web credentials selected for this scan (ONVIF / HTTP-Basic). CCTV collection
	// tries EACH of these in turn on a camera/NVR/DVR — first success binds — so
	// selecting several http_basic credentials actually tries all of them, not just
	// one. (Mirrors how SNMP tries every selected community.) Empty ⇒ CCTV falls
	// back to the device's bound/CCTV credential.
	var scanWebCreds []uuid.UUID
	seenWebCred := map[uuid.UUID]bool{}
	for _, g := range extraGroups {
		for _, m := range g.Members {
			if (m.Kind == domain.CredONVIF || m.Kind == domain.CredHTTPBasic) && !seenWebCred[m.ID] {
				seenWebCred[m.ID] = true
				scanWebCreds = append(scanWebCreds, m.ID)
			}
		}
	}

	// --- Known-Device Retry: load the devices already in inventory for the IPs in
	// this scan's scope. A known device that the main sweep misses (transient
	// timeout under load) is retried separately and, if still gone, recorded as
	// "missed" — never silently dropped from the job. ---
	knownByIP := map[netip.Addr]db.Device{}
	if devs, derr := s.queries.ListAllDevices(ctx); derr == nil {
		scopeSet := make(map[netip.Addr]bool, len(hosts))
		for _, ip := range hosts {
			scopeSet[ip] = true
		}
		for _, d := range devs {
			if d.PrimaryIp != nil && scopeSet[*d.PrimaryIp] {
				knownByIP[*d.PrimaryIp] = d
			}
		}
	}
	var seenMu sync.Mutex
	seenAlive := make(map[netip.Addr]bool)
	var newCount, knownSeenCount, recoveredCount, missedCount int

	// --- Stage 0: liveness sweep. Establish which addresses are REAL before any
	// deep probe, credential attempt or enrolment. Without this, "any TCP port
	// answered" is enough to enrol, and a middlebox that answers one port for a
	// whole range (a SIP ALG on TCP/5060) makes every address look alive — a /24
	// with ~61 real devices enrolled all 254. The sweep probes the network and
	// broadcast addresses as negative controls: nothing can live there, so a port
	// answering on them proves the answer is not coming from the target. ---
	sweepPorts := append(append([]int(nil), discovery.StandardScanPorts...), cfg.ExtraPorts...)
	sweep := discovery.LivenessSweep(ctx, hosts, discovery.SweepConfig{
		Ports:       sweepPorts,
		Timeout:     portTO,
		Concurrency: concurrency,
		Controls:    discovery.ControlsForHosts(hosts, 16),
	}, nil)
	s.publishScanEvent(jobID, netip.Addr{}, uuid.Nil, "liveness_sweep", "", "info", sweep.Summary())
	for _, p := range sweep.Promiscuous {
		s.publishScanEvent(jobID, netip.Addr{}, uuid.Nil, "port_untrusted", "", "warning",
			fmt.Sprintf("TCP/%d: %s", p.Port, p.Reason))
	}
	// Addresses the sweep ruled out are still SCANNED — advance the progress
	// counter for them so the job reports against the full scope, not just the
	// survivors.
	for range sweep.SuppressedByMiddlebox {
		s.bumpScanned(jobID)
	}
	for range sweep.NoResponse {
		s.bumpScanned(jobID)
	}
	s.recordSweepMetadata(ctx, jobID, sweep)
	// Deep pipeline runs ONLY against addresses with trustworthy evidence.
	hosts = sweep.Alive

	res := scan.Scope(ctx, hosts, concurrency, func(ctx context.Context, ip netip.Addr) (uuid.UUID, error) {
		defer s.bumpScanned(jobID) // advance the 0→100% progress counter (once per host)
		// Per-host budget: the whole pipeline (TCP port scan → credential resolution
		// → SNMP classify → deep collect) must finish within this, else the host is
		// recorded "context deadline exceeded" and left in discovery (not enrolled).
		// 60s (raised from 45s) gives slow/large SNMP walks and multi-credential
		// hosts room to enroll; the outer job budget still caps the whole run.
		hctx, hcancel := context.WithTimeout(ctx, 60*time.Second)
		defer hcancel()
		hcfg := cfg
		hcfg.OnEvent = s.pipelineEventEmitter(jobID, ip) // live per-stage events for this host
		r := discovery.Run(hctx, ip, locID, hcfg)
		// Enrollment must NOT run under the per-host PROBE budget (hctx). A host that
		// spends its whole budget probing (e.g. an SNMP-silent, web-only host the
		// credential sweep can't authenticate) is still classified from the cheap
		// banners + open ports, and Apply enrolls every alive host (category "unknown"
		// at worst) so it surfaces as an UNMANAGED device. Writing that under the now-
		// expired hctx fails with "context deadline exceeded" — the host then vanishes
		// into a device-less "discovery" result instead of appearing in inventory.
		// Persist on a fresh budget from the job context so a discovered host is never
		// lost just because its probe ran long.
		actx, acancel := context.WithTimeout(ctx, 30*time.Second)
		s.refineClassByOUI(actx, ip, &r)
		id, err := applier.Apply(actx, r, resolveLoc(ip))
		acancel()
		// Post-onboarding follow-ups for an enrolled host (best-effort).
		enrichment := ""
		var profRes *scanProfileResult
		var sshSum *scanSSHSummary
		effCat, classNote := "", ""       // reconciled category + honest preservation note
		collectedVia, agentName := "", "" // how OS inventory was/will be collected
		if r.Facts != nil {
			enrichment = "SNMP facts collected"
		}
		if err == nil && id != uuid.Nil {
			if dev, derr := s.queries.GetDevice(ctx, id); derr == nil {
				// The reconciled category is authoritative for this scan record + for
				// gating collection below. When it differs from this run's fresh probe
				// guess AND the device is known managed infrastructure, the reconcile
				// preserved the established identity (transient SNMP failure, or an
				// operator lock) — surface that honestly in Job Results.
				effCat = dev.Category
				if fresh := string(r.Match.Category); fresh != "" && fresh != dev.Category && domain.IsStickyInfraCategory(dev.Category) {
					if dev.ClassificationLocked {
						classNote = "Operator-locked classification \"" + dev.Category + "\" preserved (this run probed as \"" + fresh + "\")."
					} else {
						classNote = "SNMP identity probe did not confirm this device this run (transient failure) — preserved known classification \"" + dev.Category + "\" instead of the weaker guess \"" + fresh + "\"; collection attempted via the device's known identity."
					}
				}
				// Discovery-refined subtype (e.g. alcatel_omnipcx) — enrichment within the
				// category, leaves classification/lock/other fields untouched. Never fabricated.
				if r.Subtype != "" && dev.Subtype != r.Subtype {
					_ = s.queries.SetDeviceSubtype(ctx, db.SetDeviceSubtypeParams{ID: dev.ID, Subtype: r.Subtype})
					dev.Subtype = r.Subtype
				}
				// Persist every credential auth attempt (success + failure + reason)
				// to credential-test history → feeds Coverage / Data Quality.
				// Pipeline attempts already carry their own Source ("subnet"/"default").
				s.persistScanCredAttempts(ctx, dev, r.CredAttempts, "")
				// Point this host's reachability check at a port it actually answered
				// on (or SNMP), so a freshly-discovered/up host is never marked
				// "offline" for a category-default port it doesn't serve.
				s.seedReachabilityCheck(ctx, dev, r.OpenPorts, r.Probe.SNMPSysDescr != "")
				// A WinRM/SSH bind means we onboarded a Windows/Linux host. Run a
				// deep OS collection to refine classification (workstation vs
				// server) and enrich vendor/model/OS — reusing the bound credential.
				// ALSO run it for a legacy WSMan-2.0 Windows host that authenticated
				// but could not be driven over Go WinRM (auth_ok_operation_fault):
				// runOSCollection will route it to the site Relay Agent (WMI/DCOM).
				// Bounded to that signal so a broad scan doesn't retry every host.
				legacyWSMan := false
				for _, a := range r.CredAttempts {
					if a.Category == credtest.CatOperationFault {
						legacyWSMan = true
						break
					}
				}
				boundOS := r.BoundCred != nil && (r.BoundCred.Kind == domain.CredWinRM || r.BoundCred.Kind == domain.CredSSH)
				// Specialized appliances (wireless / VMware / voice / CCTV) have their
				// own dedicated collection branch below and must take it even when an
				// SSH/WinRM credential happens to bind during the probe — generic "deep
				// OS inventory" is for plain servers/endpoints. Gate on the RECONCILED
				// dev.Category (not the volatile r.Match) so a known wireless controller
				// whose SNMP identity probe transiently failed this run is still routed
				// to its wireless branch instead of being treated as an SSH server.
				specialized := func(cat string) bool {
					switch domain.DeviceCategory(cat) {
					case domain.CatVirtualHost, domain.CatWirelessController, domain.CatAccessPoint,
						domain.CatPBX, domain.CatVoiceGateway, domain.CatCamera, domain.CatNVR, domain.CatDVR:
						return true
					}
					return false
				}(dev.Category)
				// A Windows host is ALWAYS worth a deep OS collection attempt regardless
				// of which ports the scan observed — see osCollectionCandidate, which
				// owns this decision (and deliberately excludes ports so the
				// "gated on an observed management port" regression can't return).
				winMgmtPort, winRMPort := false, false
				for _, p := range r.OpenPorts {
					if p == 5985 || p == 5986 {
						winRMPort = true
					}
					if p == 5985 || p == 5986 || p == 135 || p == 445 {
						winMgmtPort = true
					}
				}
				// A host that authenticated WSMan (legacy auth-ok) or answers on WinRM
				// (5985/5986) is DEFINITIVELY Windows even when it enrolled as a blank-os
				// "server". Without an os_family, runOSCollection cannot pick the winrm
				// method and dead-ends at "unsupported_os" — never trying WinRM and never
				// routing to the site agent (the .67/.68/.116 gap: legacy-auth-OK Windows
				// servers stuck at needs_agent with zero collect_os jobs). Set it in-memory
				// for this collection so the host routes to the agent; a successful agent
				// collection then persists the authoritative os_family from the OS caption.
				if dev.OsFamily == "" && (legacyWSMan || winRMPort) {
					dev.OsFamily = domain.OSFamilyWindows
				}
				if s.cipher() != nil && osCollectionCandidate(dev, boundOS, legacyWSMan, specialized, winMgmtPort) {
					s.publishScanEvent(jobID, ip, id, "collection_started", "", "started", "deep OS inventory")
					cctx, ccancel := context.WithTimeout(ctx, 2*time.Minute)
					oc := s.runOSCollection(cctx, dev)
					ccancel()
					switch {
					case oc.Status == "collected":
						enrichment, collectedVia = "Deep OS inventory collected", "direct"
					case oc.Status == "queued":
						// Dispatched to the site Relay Agent — completes out of band.
						enrichment, collectedVia, agentName = oc.Detail, "relay_agent", oc.AgentName
					case strings.Contains(oc.Reason, "agent_offline"):
						enrichment, collectedVia = "OS collection needs the site Relay Agent, which is offline", "agent_offline"
					case strings.Contains(oc.Reason, "no_agent") || strings.Contains(oc.Reason, "agent_missing"):
						enrichment, collectedVia = "OS collection needs a Relay Agent for this site (none assigned)", "agent_missing"
					default:
						enrichment = "OS collection incomplete: " + oc.Reason
					}
				} else if dev.Category == string(domain.CatVirtualHost) && s.cipher() != nil {
					// ESXi/vCenter candidate. PREFER a matching Vendor Connection
					// Profile (device > site > global) so we authenticate to the
					// configured vCenter/ESXi URL with the linked credential; fall
					// back to the device-IP collector when none is configured.
					if prof, found := s.resolveScanProfile(ctx, string(domain.CatVirtualHost), dev.ID, dev.LocationID); found {
						cctx, ccancel := context.WithTimeout(ctx, 2*time.Minute)
						pc := s.collectVSphereProfile(cctx, prof, dev)
						ccancel()
						profRes = profResultFrom(prof, pc)
						_ = s.queries.SetVendorProfileCollection(ctx, db.SetVendorProfileCollectionParams{ID: prof.ID, LastCollectionDetail: pc.Detail})
						_ = s.queries.SetVendorProfileTest(ctx, db.SetVendorProfileTestParams{ID: prof.ID, LastTestOk: &pc.AuthOK, LastTestDetail: pc.Detail})
						if pc.CollectionOK {
							enrichment = "VMware collected via profile " + prof.Name + ": " + pc.Detail
						} else {
							enrichment = "VMware profile " + prof.Name + " failed: " + pc.Detail
						}
					} else {
						profRes = &scanProfileResult{Resolved: false}
						cctx, ccancel := context.WithTimeout(ctx, 2*time.Minute)
						vc := s.runVSphereCollection(cctx, dev)
						ccancel()
						if vc.ok() {
							enrichment = "VMware host + VM facts collected"
						} else if vc.Reason == "no_credential" {
							enrichment = "VMware candidate — no profile configured; add a VMware Vendor Connection Profile"
						} else {
							enrichment = "VMware collection incomplete: " + vc.Reason
						}
					}
				} else if cat := dev.Category; (cat == string(domain.CatWirelessController) || cat == string(domain.CatAccessPoint)) && s.cipher() != nil {
					// Wireless controller candidate — use a matching Vendor Connection
					// Profile (controller URL + site) if the operator configured one.
					if prof, found := s.resolveScanProfile(ctx, cat, dev.ID, dev.LocationID); found {
						cctx, ccancel := context.WithTimeout(ctx, 90*time.Second)
						var ok bool
						var detail string
						if prof.VendorType == "extreme_xcc" {
							ok, detail = s.collectXCCProfile(cctx, prof, dev)
						} else {
							ok, detail = s.collectWirelessProfile(cctx, prof, dev)
						}
						ccancel()
						_ = s.queries.SetVendorProfileCollection(ctx, db.SetVendorProfileCollectionParams{ID: prof.ID, LastCollectionDetail: detail})
						_ = s.queries.SetVendorProfileTest(ctx, db.SetVendorProfileTestParams{ID: prof.ID, LastTestOk: &ok, LastTestDetail: detail})
						profRes = profResultFrom(prof, profileCollect{AuthOK: ok, CollectionOK: ok, Detail: detail})
						enrichment = detail
						if !ok {
							enrichment = "Wireless profile collection incomplete: " + detail
						}
					} else {
						profRes = &scanProfileResult{Resolved: false}
						// Extreme on-prem controllers (XCC) get a specific next action.
						if isExtremeXCC(r) {
							enrichment = "Configure Extreme XCC profile to collect AP/SSID/client data (SNMP identity already captured)"
						} else {
							enrichment = "Wireless controller — add a Vendor Connection Profile (Discovery → Vendor Profiles) to onboard"
						}
					}
					// SNMP wireless MIB collection: independent of the REST API path.
					// If an enabled MIB pack applies to this controller, walk its mapped
					// tables over the bound SNMP credential and persist what's exposed
					// (honest partial). Runs whenever SNMP is proven for the device.
					if dev.CredentialID != nil {
						mctx, mcancel := context.WithTimeout(ctx, 60*time.Second)
						if _, mdetail, mok := s.collectWirelessMib(mctx, dev, ""); mdetail != "" {
							if enrichment == "" || mok {
								enrichment = strings.TrimSpace(enrichment + " | SNMP MIB: " + mdetail)
							}
						}
						mcancel()
					}
					// SSH CLI roster collection when a working SSH credential resolves
					// (bound or auto-tried). Extreme XCC uses the exec-per-command CLI
					// collector; a Ruckus ZoneDirector uses the interactive-shell ZD
					// collector (read-only connectivity/diagnostic — the Web-XML primary
					// owns its rosters). Binds the SSH cred only if none is bound yet.
					if s.cipher() != nil {
						sctx, scancel := context.WithTimeout(ctx, 150*time.Second)
						emit := func(stage, status, command, message string, parsed, skipped, warns int) {
							s.publishSSHCmdEvent(jobID, ip, id, stage, status, command, message, parsed, skipped, warns)
						}
						var sres sshCLISummary
						if s.sshCLIApplicable(ctx, dev) {
							sres = s.collectSSHCLI(sctx, dev, "", "", true, emit)
						} else {
							sres = s.collectRuckusSSHCLI(sctx, dev, "", "", emit)
						}
						scancel()
						if sres.Reachable {
							enrichment = strings.TrimSpace(enrichment + " | SSH CLI: " + sres.Detail)
							warnN := 0
							for _, rr := range sres.Results {
								if rr.Warnings != "" {
									warnN++
								}
							}
							sshSum = &scanSSHSummary{
								Status: sres.Status, Supported: sres.Supported, Unsupported: sres.Unsupported,
								APRows: sres.APs, ClientRows: sres.Clients, APTotal: sres.APTotal, ClientTotal: sres.ClientsTotal,
								Warnings: warnN, Detail: sres.Detail,
							}
						}
					}
				} else if cat := dev.Category; (cat == string(domain.CatPBX) || cat == string(domain.CatVoiceGateway)) && s.cipher() != nil {
					// Voice/PBX candidate — use a matching CUCM Vendor Connection Profile.
					if prof, found := s.resolveScanProfile(ctx, cat, dev.ID, dev.LocationID); found {
						cctx, ccancel := context.WithTimeout(ctx, 90*time.Second)
						ok, detail := false, ""
						if prof.VendorType == "cucm" {
							ok, detail = s.collectCUCMProfile(cctx, prof, dev)
						} else {
							detail = prof.VendorType + " deep collection not implemented yet — profile recorded; detection + classification active"
						}
						ccancel()
						_ = s.queries.SetVendorProfileCollection(ctx, db.SetVendorProfileCollectionParams{ID: prof.ID, LastCollectionDetail: detail})
						if prof.VendorType == "cucm" {
							_ = s.queries.SetVendorProfileTest(ctx, db.SetVendorProfileTestParams{ID: prof.ID, LastTestOk: &ok, LastTestDetail: detail})
						}
						profRes = profResultFrom(prof, profileCollect{AuthOK: ok, CollectionOK: ok, Detail: detail})
						enrichment = detail
						if !ok {
							enrichment = "Voice profile: " + detail
						}
					} else {
						profRes = &scanProfileResult{Resolved: false}
						enrichment = "Voice/PBX — add a Vendor Connection Profile (Discovery → Vendor Profiles) to onboard"
					}
				} else if cat := dev.Category; (cat == string(domain.CatCamera) || cat == string(domain.CatNVR) || cat == string(domain.CatDVR)) && s.cipher() != nil {
					// Camera/NVR/DVR candidate. PREFER a matching CCTV Vendor Connection
					// Profile (device > site > global) so we authenticate to the
					// configured target with the linked ONVIF/HTTP credential; fall back
					// to the device-IP collector when none is configured.
					if prof, found := s.resolveScanProfile(ctx, cat, dev.ID, dev.LocationID); found {
						cctx, ccancel := context.WithTimeout(ctx, 90*time.Second)
						pc := s.collectCCTVProfile(cctx, prof, dev)
						ccancel()
						profRes = profResultFrom(prof, pc)
						_ = s.queries.SetVendorProfileCollection(ctx, db.SetVendorProfileCollectionParams{ID: prof.ID, LastCollectionDetail: pc.Detail})
						_ = s.queries.SetVendorProfileTest(ctx, db.SetVendorProfileTestParams{ID: prof.ID, LastTestOk: &pc.AuthOK, LastTestDetail: pc.Detail})
						if pc.CollectionOK {
							enrichment = "CCTV collected via profile " + prof.Name + ": " + pc.Detail
						} else {
							enrichment = "CCTV profile " + prof.Name + " failed: " + pc.Detail
						}
					} else {
						profRes = &scanProfileResult{Resolved: false}
						cctx, ccancel := context.WithTimeout(ctx, 120*time.Second) // room for ONVIF phase + reserved ISAPI fallback
						// Subnet-scoped credentials: if this camera's site subnet has
						// assigned credentials, use ONLY its web (ONVIF/HTTP-Basic) creds
						// — never the global scan web creds. This is the CCTV anti-spray
						// / lockout-safety path (mirrors the resolver exclusivity used for
						// SNMP/SSH/WinRM). Empty fall-through keeps current behaviour.
						var webCreds []uuid.UUID
						var scopedLabel string
						scopeNote, cctvSource := "", "default"
						if explicitCreds {
							// Explicit operator selection wins over the subnet's assigned
							// web creds (mirrors the resolver path) — try ONLY the selected
							// ONVIF/HTTP-Basic credentials. Anti-spray still holds: just the
							// chosen set, never the global list.
							webCreds = scanWebCreds
							scopeNote, cctvSource = " [operator-selected]", "selected"
						} else {
							webCreds, scopedLabel = s.cctvWebCredsForScan(ctx, ip, locID, scanWebCreds)
							if scopedLabel != "" {
								scopeNote = " [subnet-scoped: " + scopedLabel + "]"
								cctvSource = "subnet"
							}
						}
						// Already-onboarded CCTV device: it has a durable bound web credential
						// that works. Re-use ONLY that (bound-credential-only — pass no
						// selection) instead of re-spraying the subnet's full web-credential set
						// at it every scan; each wrong digest on a Hikvision recorder costs a
						// ~15s throttle and risks an IP lockout. New/unbound devices still try
						// the subnet set for first-time onboarding.
						if dev.CctvCredentialID != nil {
							webCreds = nil
							scopeNote, cctvSource = " [bound credential]", "bound"
						}
						// Try each selected web credential (first success binds). Pass the
						// LIVE discovered open ports so the device's actual web port is tried
						// first (probe_data isn't persisted until after this runs).
						cv := s.runCCTVCollection(cctx, dev, webCreds, cctvSource, r.OpenPorts...)
						ccancel()
						if cv.ok() {
							enrichment = "CCTV collected (" + cv.Category + ") via " + cv.CredentialUsed + scopeNote
						} else if cv.Reason == "no_credential" {
							if scopedLabel != "" {
								enrichment = "CCTV candidate — subnet " + scopedLabel + " has no ONVIF/HTTP-Basic credential assigned (assign one under Locations → Subnet)"
							} else {
								enrichment = "CCTV candidate — select an ONVIF/HTTP-Basic credential in the scan (or add a CCTV Vendor Connection Profile)"
							}
						} else {
							enrichment = "CCTV collection incomplete: " + cv.Reason + scopeNote
						}
					}
				} else if dev.Category == string(domain.CatStorage) && s.cipher() != nil && dev.CredentialID != nil {
					// NAS candidate with a bound SNMP community. QNAP QTS/QuTS exposes
					// disks, volumes, network interfaces, and live system health over
					// SNMP — collect it now using ONLY the bound community (never sprays).
					// Other NAS platforms (Synology/TrueNAS) stay honest candidates until
					// their collector exists.
					oid := r.Probe.SNMPSysObjectID
					vendorLC := ""
					if dev.Vendor != nil {
						vendorLC = strings.ToLower(*dev.Vendor)
					}
					isQNAP := strings.Contains(oid, "55062") || strings.Contains(oid, "24681") ||
						strings.Contains(vendorLC, "qnap") ||
						strings.Contains(strings.ToLower(r.Probe.SNMPSysDescr), "qnap")
					if isQNAP {
						cctx, ccancel := context.WithTimeout(ctx, 60*time.Second)
						if c, cerr := s.snmpClientForDevice(cctx, dev, "", 8*time.Second); cerr == nil {
							rep := nas.CollectQNAP(cctx, c)
							c.Close()
							poll := time.Now()
							s.persistNAS(cctx, dev.ID, rep, poll)
							_, _ = s.collectSNMPInterfaces(cctx, dev, "", 8*time.Second)
							enrichment = fmt.Sprintf("NAS inventory collected: %d disk(s), %d volume(s)", len(rep.Disks), len(rep.Volumes))
						} else {
							enrichment = "NAS candidate — SNMP collect failed: " + cerr.Error()
						}
						ccancel()
					} else {
						enrichment = "NAS candidate — QNAP SNMP MIB not detected; add a QTS/DSM credential to onboard deep inventory"
					}
				}
			}
		}
		if r.Alive {
			disp := "newly_discovered"
			seenMu.Lock()
			if _, known := knownByIP[ip]; known {
				disp = "known_seen"
				knownSeenCount++
			} else {
				newCount++
			}
			seenAlive[ip] = true
			seenMu.Unlock()
			switch collectedVia {
			case "direct":
				s.publishScanEvent(jobID, ip, id, "collection_success", "", "success", enrichment)
			case "relay_agent":
				s.publishScanEvent(jobID, ip, id, "relay_agent_job_queued", "", "queued", agentName)
			case "agent_offline":
				s.publishScanEvent(jobID, ip, id, "relay_agent_failed", "", "failed", "site Relay Agent offline")
			case "agent_missing":
				s.publishScanEvent(jobID, ip, id, "relay_agent_failed", "", "failed", "no Relay Agent for this site")
			default:
				if strings.HasPrefix(enrichment, "OS collection incomplete") {
					s.publishScanEvent(jobID, ip, id, "collection_failed", "", "failed", enrichment)
				}
			}
			s.recordResult(hctx, jobID, ip, r, id, err, enrichment, profRes, collectedVia, agentName, disp, 0, sshSum, effCat, classNote)
		}
		return id, err
	})

	// --- Known-Device Retry pass: any known device NOT seen alive in the main
	// sweep is retried separately with slower, contention-free targeted probes
	// (longer timeouts + its last-known open ports), up to 3 attempts. Recovered →
	// "known_recovered"; still gone → a "missed" row so it never disappears. ---
	s.retryMissedKnown(ctx, jobID, resolveLoc, cfg, applier, knownByIP, seenAlive, &recoveredCount, &missedCount)

	status, errMsg := "completed", (*string)(nil)
	if ctx.Err() != nil {
		status = "failed"
		m := ctx.Err().Error()
		errMsg = &m
	}
	// found_count = devices present this run (new + known-seen + recovered). It is
	// NOT the stable inventory count — the Job Results API splits the dispositions.
	// found_count = devices actually RECORDED present this run, counted from the
	// persisted rows — never the in-flight counters, which can diverge from the
	// rows if a slow host's write missed its deadline. This guarantees the jobs
	// list / KPI always matches the result rows + the API counts.
	found := newCount + knownSeenCount + recoveredCount // fallback if the row query fails
	if rows, rerr := s.queries.ListDiscoveryResults(context.Background(), jobID); rerr == nil {
		found = 0
		for _, rr := range rows {
			if rr.Outcome != "missed" {
				found++
			}
		}
	}
	// Link any NVR/DVR channels to the camera devices at their IPs. The per-channel
	// link is computed once at NVR-collect time, so a camera discovered in THIS scan
	// that an NVR referenced earlier (or one whose apply raced an NVR collect) would
	// otherwise stay unlinked. Fleet-wide, idempotent, best-effort.
	_, _ = s.queries.ReconcileNVRChannelLinks(context.Background())
	_ = s.queries.UpdateDiscoveryJobStatus(context.Background(), db.UpdateDiscoveryJobStatusParams{
		// scanned_count = host_count so a finished job reads exactly 100% even if a
		// per-host increment was missed.
		ID: jobID, Status: status, HostCount: int32(len(hosts)), FoundCount: int32(found), ScannedCount: int32(len(hosts)), Error: errMsg,
	})
	s.publishScanEvent(jobID, netip.Addr{}, uuid.Nil, "job_completed", "", status,
		fmt.Sprintf("%d found · %d new · %d known · %d recovered · %d missed", found, newCount, knownSeenCount, recoveredCount, missedCount))
	_ = res // per-IP aggregate retained for potential future telemetry
}

// bumpScanned advances the job's per-host scan-progress counter (drives the
// 0→100% progress bar in the Live + Table views). Best-effort with a fresh, short
// context so it isn't tied to the per-host probe deadline. Called once per host.
func (s *Server) bumpScanned(jobID uuid.UUID) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = s.queries.IncrDiscoveryJobScanned(ctx, jobID)
}

// retryMissedKnown runs the targeted Known-Device Retry pass. For each known
// device missed by the main sweep it re-probes the IP alone — generous timeouts,
// no concurrency contention, its last-known open ports added — up to 3 attempts.
// Recovered devices are applied + recorded "known_recovered"; the rest get a
// "missed" row (recordMissed) so a known managed device is never silently absent.
// subnetLocationResolver returns a per-IP site resolver for a scan. If the scan
// carried an explicit site (jobLoc != nil) that always wins. Otherwise it matches
// each IP against the configured subnet→site mappings (narrowest containing CIDR
// wins) so an unscoped targets/CIDR scan still resolves a device to its site —
// and reconcile fills the site on devices previously left with a null location
// (e.g. 172.21.60.181 in 172.21.60.0/24 → CHR). Subnets are loaded ONCE per scan.
func (s *Server) subnetLocationResolver(ctx context.Context, jobLoc *uuid.UUID) func(netip.Addr) *uuid.UUID {
	if jobLoc != nil {
		return func(netip.Addr) *uuid.UUID { return jobLoc }
	}
	subs, err := s.queries.ListSubnets(ctx)
	if err != nil || len(subs) == 0 {
		return func(netip.Addr) *uuid.UUID { return nil }
	}
	return func(ip netip.Addr) *uuid.UUID { return subnetLocationFor(subs, ip) }
}

// subnetLocationFor returns the site/location of the configured subnet that
// contains ip, preferring the narrowest (longest-prefix) match so a /24 site
// subnet outranks an overlapping /16. Returns nil when no configured subnet
// contains the IP. Pure (no DB) so the reconcile rule is unit-testable.
func subnetLocationFor(subs []db.Subnet, ip netip.Addr) *uuid.UUID {
	var best *uuid.UUID
	bestBits := -1
	for i := range subs {
		p := subs[i].Cidr
		if p.IsValid() && p.Contains(ip) && p.Bits() > bestBits {
			bestBits = p.Bits()
			loc := subs[i].LocationID
			best = &loc
		}
	}
	return best
}

func (s *Server) retryMissedKnown(ctx context.Context, jobID uuid.UUID, resolveLoc func(netip.Addr) *uuid.UUID, base discovery.PipelineConfig, applier *apply.Applier, knownByIP map[netip.Addr]db.Device, seenAlive map[netip.Addr]bool, recoveredCount, missedCount *int) {
	var missed []netip.Addr
	for ip := range knownByIP {
		if !seenAlive[ip] {
			missed = append(missed, ip)
		}
	}
	if len(missed) == 0 {
		return
	}
	// Slower, more forgiving than the balanced sweep — completeness over speed for
	// the FEW missed hosts (not the whole subnet).
	retryCfg := base
	retryCfg.PortTimeout = maxDur(base.PortTimeout*2, 1500*time.Millisecond)
	retryCfg.SNMPTimeout = maxDur(base.SNMPTimeout*2, 4000*time.Millisecond)
	const maxAttempts = 3

	for _, ip := range missed {
		if ctx.Err() != nil {
			return
		}
		dev := knownByIP[ip]
		rcfg := retryCfg
		rcfg.ExtraPorts = s.lastKnownOpenPorts(ctx, dev.ID) // last successful open ports
		rcfg.OnEvent = s.pipelineEventEmitter(jobID, ip)
		s.publishScanEvent(jobID, ip, dev.ID, "known_retry_started", "", "started", "missed in main sweep — targeted retry")
		recovered := false
		attempt := 0
		for attempt = 1; attempt <= maxAttempts; attempt++ {
			if ctx.Err() != nil {
				return
			}
			actx, acancel := context.WithTimeout(ctx, 40*time.Second)
			rr := discovery.Run(actx, ip, resolveLoc(ip), rcfg)
			if rr.Alive {
				s.refineClassByOUI(actx, ip, &rr)
				id, aerr := applier.Apply(actx, rr, resolveLoc(ip))
				if aerr == nil && id != uuid.Nil {
					if d2, e := s.queries.GetDevice(actx, id); e == nil {
						s.persistScanCredAttempts(actx, d2, rr.CredAttempts, "")
						s.seedReachabilityCheck(actx, d2, rr.OpenPorts, rr.Probe.SNMPSysDescr != "")
					}
				}
				acancel()
				s.recordResult(ctx, jobID, ip, rr, id, aerr,
					"Recovered by Known-Device Retry (attempt "+strconv.Itoa(attempt)+" of "+strconv.Itoa(maxAttempts)+", slower targeted probe)",
					nil, "", "", "known_recovered", attempt, nil, "", "")
				s.publishScanEvent(jobID, ip, dev.ID, "known_recovered", "", "success", "recovered on retry "+strconv.Itoa(attempt))
				*recoveredCount++
				recovered = true
				break
			}
			acancel()
		}
		if !recovered {
			s.recordMissed(ctx, jobID, ip, dev, discovery.HostResult{}, maxAttempts)
			s.publishScanEvent(jobID, ip, dev.ID, "known_missed", "", "failed", "unreachable after "+strconv.Itoa(maxAttempts)+" retries")
			*missedCount++
		}
	}
}

// maxDur returns the larger of two durations.
func maxDur(a, b time.Duration) time.Duration {
	if a > b {
		return a
	}
	return b
}

// scanCredAttemptDTO / scanDetail are the actionable per-device scan record
// stored in discovery_results.probe_data — what was detected, how it was
// classified, which credentials were tried (success/failure + reason), what was
// bound, what enrichment ran, and the next action. No secrets.
type scanCredAttemptDTO struct {
	Kind     string `json:"kind"`
	Protocol string `json:"protocol"`
	Category string `json:"category"`
	Detail   string `json:"detail"`
	Success  bool   `json:"success"`
	Relevant bool   `json:"relevant"`
	// Source: why this credential was tried — "subnet" (subnet-scoped) | "default".
	Source string `json:"source,omitempty"`
}

type scanDetail struct {
	OpenPorts              []int                `json:"open_ports,omitempty"`
	Classification         string               `json:"classification"`
	Confidence             int                  `json:"confidence"`
	Evidence               []string             `json:"evidence,omitempty"`
	Candidate              string               `json:"candidate,omitempty"`               // protocol-plan candidate type
	ExpectedProtocols      []string             `json:"expected_protocols,omitempty"`      // what the scan expected to manage by
	OpportunisticProtocols []string             `json:"opportunistic_protocols,omitempty"` // not expected by the plan but probed anyway (e.g. SNMP — UDP/161, invisible to a port scan)
	SkippedProtocols       []string             `json:"skipped_protocols,omitempty"`       // truly NOT attempted (not expected and not probed)
	CredAttempts           []scanCredAttemptDTO `json:"cred_attempts,omitempty"`
	BoundCred              string               `json:"bound_cred,omitempty"`
	Enrichment             string               `json:"enrichment,omitempty"`
	Profile                *scanProfileResult   `json:"profile,omitempty"`
	NextAction             string               `json:"next_action"`
	// How OS inventory was (or will be) collected for this host:
	//   "" (n/a) | "direct" | "relay_agent" (queued) | "agent_offline" | "agent_missing"
	CollectedVia string          `json:"collected_via,omitempty"`
	AgentName    string          `json:"agent_name,omitempty"` // relay agent the job was dispatched to
	SSH          *scanSSHSummary `json:"ssh,omitempty"`        // Extreme XCC SSH CLI collection summary
	// ClassNote explains a scan-stability decision: a known managed-infrastructure
	// classification was preserved this run even though the fresh probe pointed
	// elsewhere (transient SNMP failure or an operator lock). Empty in the normal case.
	ClassNote string `json:"class_note,omitempty"`
	// CredScope, when set, names the site subnet whose assigned credentials were the
	// EXCLUSIVE set tried for this host (subnet-scoped credentials). Empty ⇒ normal
	// global/scope resolution was used.
	CredScope string `json:"cred_scope,omitempty"`
	// ClassDetail is the Phase-3 explainable-classification record: the evidence
	// channels, the fingerprint(s) that won, and the candidates that were considered
	// but rejected (with reasons). Powers the UI evidence panel and the "likely type"
	// hint for unknowns. Nil when no classification stage ran (e.g. dead host).
	ClassDetail *discovery.ClassificationDetail `json:"classification_detail,omitempty"`
}

// scanSSHSummary is the per-result SSH CLI collection rollup shown in Job Results.
type scanSSHSummary struct {
	Status      string `json:"status"` // complete|partial|summary_only|failed
	Supported   int    `json:"supported"`
	Unsupported int    `json:"unsupported"`
	APRows      int    `json:"ap_rows"`
	ClientRows  int    `json:"client_rows"`
	APTotal     int    `json:"ap_total"`
	ClientTotal int    `json:"client_total"`
	Warnings    int    `json:"warnings"`
	Detail      string `json:"detail"`
}

// scanProfileResult records how a Vendor Connection Profile was used during the
// scan for a VMware/CCTV/wireless/voice candidate, so Scan Results can show:
// resolved / no-profile / test ok|fail / collection ok|fail + the profile name.
type scanProfileResult struct {
	Resolved     bool   `json:"resolved"`
	ID           string `json:"id,omitempty"` // profile id → Open / Retry actions
	Name         string `json:"name,omitempty"`
	VendorType   string `json:"vendor_type,omitempty"`
	TestOK       *bool  `json:"test_ok,omitempty"`       // authentication/login succeeded
	CollectionOK *bool  `json:"collection_ok,omitempty"` // facts collected + persisted
	Detail       string `json:"detail,omitempty"`
}

// profResultFrom builds the Scan-Results profile summary from a deep-collection
// outcome (a resolved profile that was actually exercised during the scan).
func profResultFrom(p db.VendorConnectionProfile, pc profileCollect) *scanProfileResult {
	auth, coll := pc.AuthOK, pc.CollectionOK
	return &scanProfileResult{
		Resolved: true, ID: p.ID.String(), Name: p.Name, VendorType: p.VendorType,
		TestOK: &auth, CollectionOK: &coll, Detail: pc.Detail,
	}
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}

// scanNextAction explains, for a scanned host, what the operator should do next
// — the honest gate when a category needs a credential HIMS doesn't yet have.
func scanNextAction(category string, bound bool, boundKind string) string {
	if bound {
		return "Managed via " + boundKind
	}
	switch category {
	case "camera", "nvr":
		return "Needs ONVIF/HTTP credential to confirm camera vs NVR/DVR and enrich (model/firmware/channels)"
	case "virtual_host":
		return "Needs VMware/vSphere credential to collect host + VM facts"
	case "endpoint":
		return "Needs WinRM credential for deep OS inventory"
	case "server":
		return "Needs WinRM/SSH credential for deep OS inventory"
	case "pbx", "voice_gateway", "ip_phone":
		// CUCM AXL needs the AXL schema version + service-account URL (vendor
		// config a user/password credential can't carry) — onboard via the
		// Controllers import where the operator supplies it.
		return "Voice/PBX detected — onboard via Discovery → Controllers (CUCM AXL URL + version) or add vendor credential"
	case "wireless_controller", "access_point":
		// Wireless controllers need vendor connection params (UniFi site / Omada
		// controller-id / Ruckus API base) beyond user:pass — onboard via the
		// Controllers import.
		return "Wireless controller detected — onboard via Discovery → Controllers (controller URL + site)"
	case "switch", "router", "firewall", "printer", "ups":
		return "Needs SNMP/SSH credential to manage + enrich"
	case "unknown", "":
		return "Insufficient evidence — add a matching credential or re-scan"
	}
	return "Add a matching credential to onboard"
}

// unknownNextAction builds a precise, evidence-bearing next action for a host
// that stayed unclassified or unmanaged — never the vague "insufficient evidence
// — re-scan". It names the open ports and any unauthenticated banner so the
// operator can act (or classify by hand) instead of re-scanning blindly. This is
// the Discovery Reliability rule made concrete: if HIMS knows the ports and the
// banner, it must SAY what it knows and why management didn't complete.
func unknownNextAction(ports []int, httpServer, httpTitle, sshBanner string) string {
	parts := make([]string, 0, len(ports))
	for _, p := range ports {
		parts = append(parts, strconv.Itoa(p))
	}
	portList := strings.Join(parts, ", ")
	has := func(p int) bool {
		for _, x := range ports {
			if x == p {
				return true
			}
		}
		return false
	}
	webOpen := has(80) || has(443) || has(8080) || has(8443) || has(8000)
	web := strings.TrimSpace(httpServer)
	if httpTitle != "" {
		if web != "" {
			web += " — "
		}
		web += httpTitle
	}
	switch {
	case len(ports) == 1 && has(23):
		return "Only Telnet (23) is open — HIMS manages via SNMP/SSH/WinRM/HTTP, not Telnet. This device is Telnet-only (unsupported); enable SSH/SNMP on it, or classify it manually."
	case webOpen && web != "":
		return "HTTP-only device — banner \"" + truncate(web, 80) + "\" (open ports " + portList + "). No SNMP/SSH/WinRM authenticated; classify it from this banner, add a matching credential, or manage it via its web UI."
	case webOpen:
		return "HTTP/HTTPS open (ports " + portList + ") with no recognizable banner and no SNMP/SSH/WinRM. Open its web UI to identify it, then classify manually."
	case has(22) && sshBanner != "":
		return "SSH open (banner \"" + truncate(sshBanner, 60) + "\") but no stored credential authenticated — add or fix an SSH credential."
	case has(22):
		return "SSH (22) open but no credential authenticated — add an SSH credential to manage this host."
	default:
		return "No supported management protocol answered on open ports [" + portList + "]. Add a matching SNMP/SSH/WinRM credential, or classify the device manually."
	}
}

// scanNextActionWithProfile refines the next action for the profile-driven
// categories (VMware / CCTV) using how a Vendor Connection Profile resolved and
// performed during the scan. Other categories fall through to scanNextAction.
func scanNextActionWithProfile(category string, bound bool, boundKind string, pr *scanProfileResult) string {
	if bound {
		return "Managed via " + boundKind
	}
	if pr != nil {
		switch category {
		case "virtual_host":
			if !pr.Resolved {
				return "Create a VMware Vendor Connection Profile (Discovery → Vendor Profiles) to onboard this host"
			}
			if pr.CollectionOK == nil || !*pr.CollectionOK {
				return "VMware profile \"" + pr.Name + "\" failed: " + pr.Detail + " — fix the credential/URL in Vendor Profiles"
			}
			return "Managed via VMware profile \"" + pr.Name + "\""
		case "camera", "nvr":
			if !pr.Resolved {
				return "Create a CCTV / ONVIF Vendor Connection Profile (Discovery → Vendor Profiles) to authenticate and confirm camera vs NVR/DVR"
			}
			if pr.CollectionOK == nil || !*pr.CollectionOK {
				return "CCTV profile \"" + pr.Name + "\" failed: " + pr.Detail + " — fix the ONVIF/HTTP credential in Vendor Profiles"
			}
			return "Managed via CCTV profile \"" + pr.Name + "\""
		case "wireless_controller", "access_point", "pbx", "voice_gateway", "ip_phone":
			if !pr.Resolved {
				return scanNextAction(category, bound, boundKind)
			}
			if pr.CollectionOK == nil || !*pr.CollectionOK {
				return "Profile \"" + pr.Name + "\" failed: " + pr.Detail + " — fix it in Vendor Profiles"
			}
			return "Managed via profile \"" + pr.Name + "\""
		}
	}
	return scanNextAction(category, bound, boundKind)
}

// skippedProtocols lists the standard management protocols the scan deliberately
// did NOT test for this candidate (not applicable) — so the operator sees, e.g.
// on a Windows host, that SNMP/SSH/ONVIF were intentionally skipped, not failed.
// protocolUniverse is the set of management protocols the scan reasons about for
// the expected / opportunistic / skipped breakdown. token matches the cred-attempt
// Protocol value ("snmp"/"ssh"/"winrm"/"onvif").
var protocolUniverse = []struct {
	label, token string
	kind         domain.CredentialKind
}{
	{"SNMP", "snmp", domain.CredSNMPv2c}, {"SSH", "ssh", domain.CredSSH},
	{"WinRM", "winrm", domain.CredWinRM}, {"ONVIF", "onvif", domain.CredONVIF},
}

// attemptedProtocols is the set of protocol tokens the scan actually tried, taken
// from the recorded credential attempts (so "skipped" can never include something
// that was attempted).
func attemptedProtocols(attempts []scanCredAttemptDTO) map[string]bool {
	set := make(map[string]bool, len(attempts))
	for _, a := range attempts {
		if a.Protocol != "" {
			set[a.Protocol] = true
		}
	}
	return set
}

// skippedProtocols lists protocols the scan TRULY did not attempt — not expected
// by the plan AND no credential attempt recorded. A protocol probed
// opportunistically (e.g. SNMP on a Linux host) is NOT skipped.
func skippedProtocols(plan discovery.ProtocolPlan, attempts []scanCredAttemptDTO) []string {
	tried := attemptedProtocols(attempts)
	var out []string
	for _, p := range protocolUniverse {
		if !plan.Relevant(p.kind) && !tried[p.token] {
			out = append(out, p.label)
		}
	}
	return out
}

// opportunisticProtocols lists protocols that were NOT expected by the plan but
// were attempted anyway — SNMP is UDP/161 and invisible to a TCP port scan, so it
// is probed on every alive host. Shown as "attempted opportunistically", never as
// skipped.
func opportunisticProtocols(plan discovery.ProtocolPlan, attempts []scanCredAttemptDTO) []string {
	tried := attemptedProtocols(attempts)
	var out []string
	for _, p := range protocolUniverse {
		if !plan.Relevant(p.kind) && tried[p.token] {
			out = append(out, p.label)
		}
	}
	return out
}

// relevantAttemptCategory returns the outcome category of the first RELEVANT
// credential attempt (the expected-protocol test), so the next action can be
// specific ("WinRM auth_failed" vs "WinRM unreachable" vs "not tested").
func relevantAttemptCategory(attempts []scanCredAttemptDTO) (string, bool) {
	for _, a := range attempts {
		if a.Relevant {
			return a.Category, true
		}
	}
	return "", false
}

// scanNextActionWithPlan produces the operator next action using the protocol
// plan + the relevant attempt result. Profile-driven categories defer to the
// profile-aware action; everything else uses the expected protocol so a Windows
// host says "check WinRM", a Linux host "enable SSH", etc.
func scanNextActionWithPlan(category string, bound bool, boundKind string, pr *scanProfileResult, plan discovery.ProtocolPlan, attempts []scanCredAttemptDTO) string {
	if bound {
		return "Managed via " + boundKind
	}
	if pr != nil { // vmware / cctv / wireless / voice — profile path
		return scanNextActionWithProfile(category, bound, boundKind, pr)
	}
	cat, tested := relevantAttemptCategory(attempts)
	switch plan.Candidate {
	case "windows":
		if !tested {
			return "Windows host — add a WinRM credential and enable PowerShell Remoting (5985/5986) for deep OS inventory"
		}
		if cat == credtest.CatOperationFault {
			return "Authentication OK, but this Windows host uses an older WSMan stack (Windows 7 / Server 2008 R2). Native PowerShell works; the Go WinRM library cannot run commands here. Configure the Windows Native Collector / WMI fallback."
		}
		if cat == "auth_failed" {
			return "Expected WinRM; WinRM auth_failed — check the domain username (DOMAIN\\user or user@domain) and password"
		}
		return "Expected WinRM; WinRM unreachable — enable WinRM / open 5985-5986 on the host firewall"
	case "linux":
		if !tested {
			return "Linux host — add an SSH credential for deep OS inventory"
		}
		if cat == "auth_failed" {
			return "Expected SSH; SSH auth_failed — check the SSH username/password or key"
		}
		return "Expected SSH; SSH unreachable — enable sshd or open port 22"
	case "network":
		if !tested {
			return "Network device — add an SNMP (and SSH) credential to manage + enrich"
		}
		if cat == "auth_failed" {
			return "Expected SNMP/SSH; authentication failed — check the community / SSH credential"
		}
		return "Expected SNMP/SSH; unreachable — verify the management protocol is enabled"
	case "printer":
		return "Printer — add an SNMP credential (Printer-MIB) to collect supplies/status"
	}
	return scanNextActionWithProfile(category, bound, boundKind, pr)
}

// refineClassByOUI corrects a WEAK port-only classification using the device's MAC
// vendor. In a POS/hotel environment a Posiflex terminal or an Epson receipt printer
// can expose only SIP/5060 during a scan (no SNMP/HTTP), which the port classifier
// reads as an IP phone. The MAC OUI — learned from switch ARP tables (FindMACByIP) —
// is strong vendor evidence a bare port is not: a single-category vendor
// (Epson→printer, Posiflex→pos) overrides the weak guess. It ONLY overrides when the
// OUI category outranks the current confidence, so an authenticated SNMP/driver
// classification (≥78) always wins; a real IP phone (Alcatel/Cisco/… — multi-category
// vendors, intentionally unmapped) is untouched and stays ip_phone from its 5060 port.
// Manual classification locks are honoured downstream by apply.Apply.
func (s *Server) refineClassByOUI(ctx context.Context, ip netip.Addr, r *discovery.HostResult) {
	rows, err := s.queries.FindMACByIP(ctx, ip)
	if err != nil || len(rows) == 0 {
		return
	}
	vendor, cat, conf := classify.OUIClassify(rows[0].Mac)
	if cat == "" || conf <= r.Match.Confidence {
		return
	}
	r.Match = driver.Match{Category: cat, Confidence: conf}
	if r.Vendor == "" {
		r.Vendor = vendor
	}
	if r.Classification != nil {
		r.Classification.FinalSource = "mac_oui"
	}
}

// recordResult writes one actionable discovery_results row for an alive host.
// disposition tags the row for the Known-Device-Retry / scan-stability counts
// (newly_discovered | known_seen | known_recovered); retryCount is how many
// targeted retries were spent (0 for the main sweep).
func (s *Server) recordResult(ctx context.Context, jobID uuid.UUID, ip netip.Addr, r discovery.HostResult, id uuid.UUID, applyErr error, enrichment string, profRes *scanProfileResult, collectedVia, agentName, disposition string, retryCount int, sshSum *scanSSHSummary, effectiveCat, classNote string) {
	// Persist with a FRESH context, independent of the caller's per-host probe
	// deadline (hctx, 45s). A slow host whose probe+deep-collection exceeds that
	// deadline must still get its result row — otherwise it is counted-alive but
	// row-less (it silently vanishes from the job even though it was found).
	_ = ctx
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	outcome := "alive"
	switch {
	case applyErr != nil:
		outcome = "failed"
	case id != uuid.Nil:
		outcome = "enrolled"
	case r.MatchedDrv != nil:
		outcome = "classified"
	}
	row, err := s.queries.CreateDiscoveryResult(ctx, db.CreateDiscoveryResultParams{
		JobID: jobID, Ip: ip, Outcome: outcome, ProbeData: []byte("{}"),
	})
	if err != nil {
		return
	}

	// The DISPLAYED + persisted category is the reconciled device category when the
	// host was enrolled (effectiveCat) — honest even when this run's probe guessed
	// otherwise and the reconcile preserved a known classification. Falls back to
	// this run's fresh match for not-yet-enrolled / retry rows (effectiveCat == "").
	category := string(r.Match.Category)
	if effectiveCat != "" {
		category = effectiveCat
	}
	bound := r.BoundCred != nil
	boundKind := ""
	if bound {
		boundKind = string(r.BoundCred.Kind)
	}

	// Human-readable evidence trail (safe, non-secret).
	var evidence []string
	if classNote != "" {
		evidence = append(evidence, classNote)
	}
	if len(r.OpenPorts) > 0 {
		evidence = append(evidence, "open ports detected")
	}
	if r.Probe.SNMPSysDescr != "" {
		evidence = append(evidence, "SNMP sysDescr: "+truncate(r.Probe.SNMPSysDescr, 80))
	}
	if r.Probe.HTTPServer != "" {
		evidence = append(evidence, "HTTP Server: "+truncate(r.Probe.HTTPServer, 60))
	}
	if t := r.Probe.Hints["http_title"]; t != "" {
		evidence = append(evidence, "HTTP title: "+truncate(t, 60))
	}

	attempts := make([]scanCredAttemptDTO, 0, len(r.CredAttempts))
	for _, a := range r.CredAttempts {
		attempts = append(attempts, scanCredAttemptDTO{
			Kind: string(a.Kind), Protocol: a.Protocol, Category: a.Category, Detail: a.Detail, Success: a.Success, Relevant: a.Relevant, Source: a.Source,
		})
	}

	detail := scanDetail{
		OpenPorts: r.OpenPorts, Classification: category, Confidence: r.Match.Confidence,
		Evidence: evidence, Candidate: r.Plan.Candidate, ExpectedProtocols: r.Plan.Expected,
		OpportunisticProtocols: opportunisticProtocols(r.Plan, attempts),
		SkippedProtocols:       skippedProtocols(r.Plan, attempts),
		CredAttempts:           attempts, BoundCred: boundKind,
		Enrichment: enrichment, Profile: profRes,
		NextAction:   scanNextActionWithPlan(category, bound, boundKind, profRes, r.Plan, attempts),
		CollectedVia: collectedVia, AgentName: agentName, SSH: sshSum, ClassNote: classNote,
		CredScope:   r.CredScope,
		ClassDetail: r.Classification,
	}
	// Sharpen the next action for agent-routed Windows hosts.
	switch collectedVia {
	case "relay_agent":
		detail.NextAction = "Collection dispatched to site Relay Agent " + agentName + " — inventory appears when the agent reports back"
	case "agent_offline":
		detail.NextAction = "This host needs the site Relay Agent, which is offline — start/repair it (Discovery → Relay Agents)"
	case "agent_missing":
		detail.NextAction = "This host needs a Relay Agent — install or assign one to this site (Discovery → Relay Agents)"
	}
	// No vague dead-ends: when nothing managed the host and it stayed unclassified,
	// replace the generic "insufficient evidence — re-scan" line with the concrete
	// reason derived from the open ports + unauthenticated banners (Discovery
	// Reliability rule: never show a vague unmanaged state when the precise reason
	// is known). Classified-but-unmanaged hosts keep their category-specific gate.
	if !bound && collectedVia == "" &&
		(category == "" || category == string(domain.CatUnknown) || strings.HasPrefix(detail.NextAction, "Insufficient evidence")) {
		detail.NextAction = unknownNextAction(r.OpenPorts, r.Probe.HTTPServer, r.Probe.Hints["http_title"], r.Probe.Hints["ssh_banner"])
	}
	blob, merr := json.Marshal(detail)
	if merr != nil {
		blob = []byte("{}")
	}

	var drv, cat, errStr *string
	if r.MatchedDrv != nil {
		n := r.MatchedDrv.Name()
		drv = &n
	}
	if category != "" {
		c := category
		cat = &c
	}
	if applyErr != nil {
		m := applyErr.Error()
		errStr = &m
	}
	var devID *uuid.UUID
	if id != uuid.Nil {
		devID = &id
	}
	_ = s.queries.UpdateDiscoveryResult(ctx, db.UpdateDiscoveryResultParams{
		ID: row.ID, Outcome: outcome, DeviceID: devID, Driver: drv, Category: cat, Error: errStr, ProbeData: blob,
		Disposition: disposition, RetryCount: int32(retryCount),
	})
}

// recordMissed writes a discovery_results row for a KNOWN device that was not
// seen in the main sweep AND could not be recovered by targeted retries. The row
// keeps the device's identity (device_id + category) so it appears in the job —
// a known managed device must never silently vanish from a scan result. outcome
// is 'missed', disposition 'known_unreachable'.
func (s *Server) recordMissed(ctx context.Context, jobID uuid.UUID, ip netip.Addr, dev db.Device, last discovery.HostResult, retryCount int) {
	_ = ctx // persist with a fresh context (see recordResult) so a missed-known row always lands
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	row, err := s.queries.CreateDiscoveryResult(ctx, db.CreateDiscoveryResultParams{
		JobID: jobID, Ip: ip, Outcome: "missed", ProbeData: []byte("{}"),
	})
	if err != nil {
		return
	}
	lastPorts := s.lastKnownOpenPorts(ctx, dev.ID)
	detail := scanDetail{
		OpenPorts:      lastPorts,
		Classification: dev.Category,
		Evidence:       []string{"known device — not seen in main sweep; targeted retry also failed"},
		NextAction:     "Known device missed this run (unreachable after " + strconv.Itoa(retryCount) + " targeted retries). It was NOT removed from inventory — verify the host is powered on / reachable, then re-scan.",
	}
	if len(last.OpenPorts) > 0 {
		detail.OpenPorts = last.OpenPorts
	}
	blob, merr := json.Marshal(detail)
	if merr != nil {
		blob = []byte("{}")
	}
	devID := dev.ID
	cat := dev.Category
	_ = s.queries.UpdateDiscoveryResult(ctx, db.UpdateDiscoveryResultParams{
		ID: row.ID, Outcome: "missed", DeviceID: &devID, Category: &cat, ProbeData: blob,
		Disposition: "known_unreachable", RetryCount: int32(retryCount),
	})
}

// lastKnownOpenPorts returns the open ports from a device's most recent scan
// probe_data — the targeted-retry pass scans these (in addition to the standard
// set) so a host on a non-standard port is still re-detected.
func (s *Server) lastKnownOpenPorts(ctx context.Context, devID uuid.UUID) []int {
	blob, err := s.queries.LatestDeviceProbeData(ctx, &devID)
	if err != nil || len(blob) == 0 {
		return nil
	}
	var d struct {
		OpenPorts []int `json:"open_ports"`
	}
	if json.Unmarshal(blob, &d) != nil {
		return nil
	}
	return d.OpenPorts
}

// scanDecrypt opens a credential's secret in memory for the scan pipeline. It
// requires the server's cipher; the plaintext community is never logged.
func (s *Server) scanDecrypt(ctx context.Context, id uuid.UUID) (discovery.DecryptedCred, error) {
	c := s.cipher()
	if c == nil {
		return discovery.DecryptedCred{}, errBadRequest("no encryption key configured")
	}
	cred, err := s.queries.GetCredential(ctx, id)
	if err != nil {
		return discovery.DecryptedCred{}, err
	}
	plain, err := c.Open(cred.EncryptedBlob, cred.KeyID)
	if err != nil {
		return discovery.DecryptedCred{}, err
	}
	dc := discovery.DecryptedCred{ID: id, Kind: domain.CredentialKind(cred.Kind), Weak: cred.Weak}
	if cred.Kind == string(domain.CredSNMPv3) {
		if v3, err := discovery.ParseSNMPv3(plain); err == nil {
			dc.V3 = v3
		}
	} else {
		dc.Community = string(plain)
	}
	return dc, nil
}

// scanJobDTO is a discovery job with its scan scope decoded from metadata. The
// raw metadata JSONB marshals as base64, and scope_cidr is empty for target-mode
// scans, so the list view couldn't say WHAT was scanned — this exposes the
// targets/mode + a human "scope" label the UI shows in place of the blank cell.
type scanJobDTO struct {
	ID           uuid.UUID  `json:"id"`
	LocationID   *uuid.UUID `json:"location_id"`
	ScopeCidr    *string    `json:"scope_cidr"`
	Status       string     `json:"status"`
	StartedAt    *time.Time `json:"started_at"`
	FinishedAt   *time.Time `json:"finished_at"`
	CreatedAt    time.Time  `json:"created_at"`
	HostCount    int32      `json:"host_count"`
	FoundCount   int32      `json:"found_count"`
	ScannedCount int32      `json:"scanned_count"`
	Error        *string    `json:"error"`
	Mode         string     `json:"mode"`
	Targets      string     `json:"targets"`
	Scope        string     `json:"scope"`
	// Phase is the HONEST end-to-end state: a scan whose probe/enroll phase is
	// 'completed' is reported as "collecting" while deep OS collection drains, then
	// "self_healing" while terminal transient failures are still awaiting automatic
	// re-collection, and only "complete" once ALL automatic collection is actually done.
	Phase             string `json:"phase"`
	CollectingPending int64  `json:"collecting_pending"`
	SelfHealing       int64  `json:"self_healing"`
}

// scanPhase derives the honest end-to-end scan phase. The DB `status` only reflects the
// probe/enroll phase, which finishes BEFORE deep collection drains — and self-heal will
// keep re-collecting terminal transient failures AFTER that. So the UI must not show
// "complete" while any automatic collection remains:
//   - collectionPending > 0 (collect_os jobs queued/retry-waiting/dispatched) -> "collecting"
//   - else healing > 0 (terminal transient failures self-heal will still retry, including
//     the cooldown window where no job is in flight) -> "self_healing"
//   - else -> "complete"
//
// Single source of truth for the Scan Jobs list and the job detail header.
func scanPhase(status string, collectionPending, healing int64) string {
	switch status {
	case "pending":
		return "queued"
	case "running":
		return "discovering"
	case "failed":
		return "failed"
	case "cancelled":
		return "cancelled"
	case "completed", "":
		if collectionPending > 0 {
			return "collecting"
		}
		if healing > 0 {
			return "self_healing"
		}
		return "complete"
	}
	return status
}

// scanScopeLabel renders a human-readable "what was scanned" string from the
// job's saved scan spec + scope, used in the Scan Jobs list.
func scanScopeLabel(j db.DiscoveryJob, spec rerunSpec) string {
	switch {
	case spec.Targets != "":
		return spec.Targets
	case j.ScopeCidr != nil:
		return j.ScopeCidr.String()
	case spec.CIDR != "":
		return spec.CIDR
	case spec.Mode == "site_subnets":
		return "site subnets"
	default:
		return "import / manual"
	}
}

func (s *Server) listDiscoveryJobs(w http.ResponseWriter, r *http.Request) {
	rows, err := s.queries.ListDiscoveryJobs(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}
	// One small query maps job_id → (in-flight collection, self-heal-eligible) counts so
	// the list shows an honest "collecting" / "self-heal" phase (never a premature
	// "complete") while deep collection drains AND while self-heal will still re-collect.
	pending := map[uuid.UUID]int64{}
	healing := map[uuid.UUID]int64{}
	if rowsP, perr := s.queries.JobsCollectionState(r.Context()); perr == nil {
		for _, p := range rowsP {
			pending[p.JobID] = p.Pending
			healing[p.JobID] = p.Healing
		}
	}
	out := make([]scanJobDTO, 0, len(rows))
	for _, j := range rows {
		d := scanJobDTO{
			ID: j.ID, LocationID: j.LocationID, Status: j.Status,
			StartedAt: j.StartedAt, FinishedAt: j.FinishedAt, CreatedAt: j.CreatedAt,
			HostCount: j.HostCount, FoundCount: j.FoundCount, ScannedCount: j.ScannedCount, Error: j.Error,
		}
		if j.ScopeCidr != nil {
			sc := j.ScopeCidr.String()
			d.ScopeCidr = &sc
		}
		var spec rerunSpec
		if len(j.Metadata) > 0 {
			_ = json.Unmarshal(j.Metadata, &spec)
		}
		d.Mode = spec.Mode
		d.Targets = spec.Targets
		d.Scope = scanScopeLabel(j, spec)
		d.CollectingPending = pending[j.ID]
		d.SelfHealing = healing[j.ID]
		d.Phase = scanPhase(j.Status, d.CollectingPending, d.SelfHealing)
		out = append(out, d)
	}
	writeJSON(w, http.StatusOK, out)
}

// deleteDiscoveryJob removes a job + its results.
func (s *Server) deleteDiscoveryJob(w http.ResponseWriter, r *http.Request) {
	ctx, id, ok := pathUUID(w, r, "id")
	if !ok {
		return
	}
	if err := s.queries.DeleteDiscoveryJob(ctx, id); err != nil {
		writeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// rerunDiscoveryJob re-runs a prior scan job over its original scope (the saved
// scope_cidr, scoped to its location), as a fresh job.
func (s *Server) rerunDiscoveryJob(w http.ResponseWriter, r *http.Request) {
	if s.reg == nil || s.fetcher == nil {
		http.Error(w, "discovery not configured on this server", http.StatusServiceUnavailable)
		return
	}
	ctx, id, ok := pathUUID(w, r, "id")
	if !ok {
		return
	}
	prev, err := s.queries.GetDiscoveryJob(ctx, id)
	if err != nil {
		writeErr(w, err)
		return
	}
	// Rebuild the original request: prefer the persisted spec (covers single IP /
	// range / list / site modes); fall back to the saved CIDR scope.
	var req scanReq
	if len(prev.Metadata) > 0 {
		var spec rerunSpec
		if json.Unmarshal(prev.Metadata, &spec) == nil {
			req = scanReq{Mode: spec.Mode, Targets: spec.Targets, CIDR: spec.CIDR, CredentialIDs: spec.CredentialIDs, CredentialGroupIDs: spec.CredentialGroupIDs, Exclude: spec.Exclude}
		}
	}
	if req.Targets == "" && req.CIDR == "" && req.Mode != "site_subnets" {
		if prev.ScopeCidr == nil {
			http.Error(w, "this job has no re-runnable network scope (controller/AD imports aren't re-run here)", http.StatusBadRequest)
			return
		}
		req.CIDR = prev.ScopeCidr.String()
	}
	hosts, scopeLabel, err := s.resolveScanHosts(ctx, req, prev.LocationID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	extra, err := s.scanCredentialTier(ctx, req.CredentialIDs, req.CredentialGroupIDs)
	if err != nil {
		writeErr(w, err)
		return
	}
	snmpTO, portTO, defConc := s.scanSettings(ctx)
	var scopePrefix *netip.Prefix
	if p, perr := netip.ParsePrefix(scopeLabel); perr == nil {
		scopePrefix = &p
	}
	job, err := s.queries.CreateDiscoveryJob(ctx, db.CreateDiscoveryJobParams{LocationID: prev.LocationID, ScopeCidr: scopePrefix})
	if err != nil {
		writeErr(w, err)
		return
	}
	_ = s.queries.UpdateDiscoveryJobStatus(ctx, db.UpdateDiscoveryJobStatusParams{ID: job.ID, Status: "running", HostCount: int32(len(hosts)), FoundCount: 0})
	_ = s.queries.SetDiscoveryJobMetadata(ctx, db.SetDiscoveryJobMetadataParams{ID: job.ID, Metadata: prev.Metadata})
	explicitCreds := len(req.CredentialIDs) > 0 || len(req.CredentialGroupIDs) > 0
	go s.runScanJob(job.ID, hosts, prev.LocationID, defConc, extra, explicitCreds, snmpTO, portTO)
	writeJSON(w, http.StatusAccepted, job)
}

func (s *Server) getDiscoveryJob(w http.ResponseWriter, r *http.Request) {
	ctx, id, ok := pathUUID(w, r, "id")
	if !ok {
		return
	}
	job, err := s.queries.GetDiscoveryJob(ctx, id)
	if err != nil {
		writeErr(w, err)
		return
	}
	results, _ := s.queries.ListDiscoveryResults(ctx, id)
	// probe_data is a JSONB column stored as []byte; emit it as raw JSON (not the
	// base64 Go would produce for a byte slice) so the UI gets the actionable
	// per-device scan detail object. Each row carries its Known-Device-Retry
	// disposition + retry_count.
	out := make([]map[string]any, 0, len(results))
	// Separated, honest counts — found_count is NOT presented as a stable inventory
	// number. targets_probed = IPs in scope this run.
	var newlyDiscovered, knownSeen, knownRecovered, knownMissed, enrolledUpdated int
	for _, x := range results {
		out = append(out, map[string]any{
			"id": x.ID, "job_id": x.JobID, "ip": x.Ip, "outcome": x.Outcome,
			"device_id": x.DeviceID, "driver": x.Driver, "category": x.Category,
			"error": x.Error, "probed_at": x.ProbedAt,
			"disposition": x.Disposition, "retry_count": x.RetryCount,
			"probe_data": json.RawMessage(x.ProbeData),
		})
		switch x.Disposition {
		case "newly_discovered":
			newlyDiscovered++
		case "known_seen":
			knownSeen++
		case "known_recovered":
			knownRecovered++
		case "known_missed", "known_unreachable":
			knownMissed++
		}
		if x.Outcome == "enrolled" {
			enrolledUpdated++
		}
	}
	counts := map[string]int{
		"targets_probed":           int(job.HostCount),
		"newly_discovered":         newlyDiscovered,
		"known_seen_again":         knownSeen,
		"known_recovered_by_retry": knownRecovered,
		"known_missed_this_run":    knownMissed,
		"enrolled_updated":         enrolledUpdated,
	}
	// Deep OS collection runs ASYNC through the site relay agent AFTER the scan's
	// probe/enroll phase completes, so the job can be "completed" while devices are
	// still being collected (managed count climbing). Surface that explicitly so the
	// operator-facing result is never shown as fully settled while collection drains:
	// settled = queued + retry_waiting + running all 0.
	collection := map[string]any{"queued": 0, "retry_waiting": 0, "running": 0, "done": 0, "failed": 0, "pending": 0, "settled": true}
	var collectionPending int64
	if cp, cerr := s.queries.CollectionProgressForJob(ctx, id); cerr == nil {
		collectionPending = cp.Queued + cp.RetryWaiting + cp.Running
		collection = map[string]any{
			"queued": cp.Queued, "retry_waiting": cp.RetryWaiting, "running": cp.Running,
			"done": cp.Done, "failed": cp.Failed, "pending": collectionPending,
			"settled": collectionPending == 0,
		}
	}
	// Honest end-to-end phase: "collecting" while collection drains, "self_healing" while
	// terminal transient failures still await automatic re-collection (incl. the cooldown
	// window), never a premature "complete". Single source of truth with the jobs list.
	healing, _ := s.queries.CountSelfHealEligibleForJob(ctx, id)
	phase := scanPhase(job.Status, collectionPending, healing)
	collection["self_healing"] = healing
	writeJSON(w, http.StatusOK, map[string]any{"job": job, "results": out, "counts": counts, "collection": collection, "phase": phase})
}
