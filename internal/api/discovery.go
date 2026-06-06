package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/netip"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/coralsearesorts/hims/internal/apply"
	"github.com/coralsearesorts/hims/internal/credresolver"
	"github.com/coralsearesorts/hims/internal/credtest"
	"github.com/coralsearesorts/hims/internal/discovery"
	"github.com/coralsearesorts/hims/internal/domain"
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
	}); err == nil {
		_ = s.queries.SetDiscoveryJobMetadata(r.Context(), db.SetDiscoveryJobMetadataParams{ID: job.ID, Metadata: spec})
	}

	go s.runScanJob(job.ID, hosts, locID, concurrency, extra, snmpTO, portTO)
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
	counts := map[string]int{"snmp": 0, "ssh": 0, "winrm": 0, "wmi": 0, "onvif": 0, "http_basic": 0, "vendor_api": 0}
	for _, c := range creds {
		if len(selSet) > 0 && !selSet[c.ID.String()] {
			continue
		}
		switch c.Kind {
		case string(domain.CredSNMPv2c), string(domain.CredSNMPv3):
			counts["snmp"]++
		case string(domain.CredSSH):
			counts["ssh"]++
		case string(domain.CredWinRM):
			counts["winrm"]++
		case string(domain.CredWMI):
			counts["wmi"]++
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
	if counts["winrm"] == 0 {
		warnings = append(warnings, "No WinRM credential available — Windows hosts cannot be onboarded (deep OS inventory).")
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
}

// resolveScanHosts expands the request's input mode into a host list. It
// returns a scope label (a CIDR string when the scope is a single prefix, for
// the job record; otherwise a free-text summary).
func (s *Server) resolveScanHosts(ctx context.Context, req scanReq, locID *uuid.UUID) ([]netip.Addr, string, error) {
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
		var all []netip.Addr
		for _, sn := range subnets {
			hosts, err := discovery.ExpandCIDR(sn.Cidr, scanMaxHosts)
			if err != nil {
				return nil, "", err
			}
			all = append(all, hosts...)
			if len(all) > scanMaxHosts {
				return nil, "", errBadRequest("site subnets expand beyond the scan cap; scan a subset")
			}
		}
		return all, "site_subnets", nil
	}

	spec := req.Targets
	if spec == "" {
		spec = req.CIDR // legacy single-CIDR field
	}
	if spec == "" {
		return nil, "", errBadRequest("provide targets (IP / range / CIDR) or location_id with mode=site_subnets")
	}
	hosts, err := discovery.ParseTargets(spec, scanMaxHosts)
	if err != nil {
		return nil, "", err
	}
	return hosts, spec, nil
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

// runScanJob is the background scan worker. It owns its own context (the HTTP
// request's is long gone) and records per-host outcomes + a final job status.
func (s *Server) runScanJob(jobID uuid.UUID, hosts []netip.Addr, locID *uuid.UUID, concurrency int, extraGroups []credresolver.ScopedGroup, snmpTO, portTO time.Duration) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	cfg := discovery.PipelineConfig{
		Registry: s.reg, Fetcher: s.fetcher, Decrypt: s.scanDecrypt,
		ExtraGroups: extraGroups,
		SNMPTimeout: snmpTO, PortTimeout: portTO,
	}
	applier := apply.New(s.queries)

	res := scan.Scope(ctx, hosts, concurrency, func(ctx context.Context, ip netip.Addr) (uuid.UUID, error) {
		hctx, hcancel := context.WithTimeout(ctx, 45*time.Second)
		defer hcancel()
		r := discovery.Run(hctx, ip, locID, cfg)
		id, err := applier.Apply(hctx, r, locID)
		// Post-onboarding follow-ups for an enrolled host (best-effort).
		enrichment := ""
		var profRes *scanProfileResult
		collectedVia, agentName := "", "" // how OS inventory was/will be collected
		if r.Facts != nil {
			enrichment = "SNMP facts collected"
		}
		if err == nil && id != uuid.Nil {
			if dev, derr := s.queries.GetDevice(ctx, id); derr == nil {
				// Persist every credential auth attempt (success + failure + reason)
				// to credential-test history → feeds Coverage / Data Quality.
				s.persistScanCredAttempts(ctx, dev, r.CredAttempts)
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
				if s.cipher() != nil && (boundOS || legacyWSMan) {
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
				} else if string(r.Match.Category) == string(domain.CatVirtualHost) && s.cipher() != nil {
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
				} else if cat := string(r.Match.Category); (cat == string(domain.CatWirelessController) || cat == string(domain.CatAccessPoint)) && s.cipher() != nil {
					// Wireless controller candidate — use a matching Vendor Connection
					// Profile (controller URL + site) if the operator configured one.
					if prof, found := s.resolveScanProfile(ctx, cat, dev.ID, dev.LocationID); found {
						cctx, ccancel := context.WithTimeout(ctx, 90*time.Second)
						ok, detail := s.collectWirelessProfile(cctx, prof, dev)
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
						enrichment = "Wireless controller — add a Vendor Connection Profile (Discovery → Vendor Profiles) to onboard"
					}
				} else if cat := string(r.Match.Category); (cat == string(domain.CatPBX) || cat == string(domain.CatVoiceGateway)) && s.cipher() != nil {
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
				} else if cat := string(r.Match.Category); (cat == string(domain.CatCamera) || cat == string(domain.CatNVR)) && s.cipher() != nil {
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
						cctx, ccancel := context.WithTimeout(ctx, 90*time.Second)
						cv := s.runCCTVCollection(cctx, dev)
						ccancel()
						if cv.ok() {
							enrichment = "ONVIF facts collected (" + cv.Category + ")"
						} else if cv.Reason == "no_credential" {
							enrichment = "CCTV candidate — no profile configured; add a CCTV / ONVIF Vendor Connection Profile"
						} else {
							enrichment = "ONVIF collection incomplete: " + cv.Reason
						}
					}
				}
			}
		}
		if r.Alive {
			s.recordResult(hctx, jobID, ip, r, id, err, enrichment, profRes, collectedVia, agentName)
		}
		return id, err
	})

	status, errMsg := "completed", (*string)(nil)
	if ctx.Err() != nil {
		status = "failed"
		m := ctx.Err().Error()
		errMsg = &m
	}
	_ = s.queries.UpdateDiscoveryJobStatus(context.Background(), db.UpdateDiscoveryJobStatusParams{
		ID: jobID, Status: status, HostCount: int32(len(hosts)), FoundCount: int32(res.Persisted), Error: errMsg,
	})
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
}

type scanDetail struct {
	OpenPorts         []int                `json:"open_ports,omitempty"`
	Classification    string               `json:"classification"`
	Confidence        int                  `json:"confidence"`
	Evidence          []string             `json:"evidence,omitempty"`
	Candidate         string               `json:"candidate,omitempty"`          // protocol-plan candidate type
	ExpectedProtocols []string             `json:"expected_protocols,omitempty"` // what the scan expected to manage by
	SkippedProtocols  []string             `json:"skipped_protocols,omitempty"`  // deliberately not tested (not applicable)
	CredAttempts      []scanCredAttemptDTO `json:"cred_attempts,omitempty"`
	BoundCred         string               `json:"bound_cred,omitempty"`
	Enrichment        string               `json:"enrichment,omitempty"`
	Profile           *scanProfileResult   `json:"profile,omitempty"`
	NextAction        string               `json:"next_action"`
	// How OS inventory was (or will be) collected for this host:
	//   "" (n/a) | "direct" | "relay_agent" (queued) | "agent_offline" | "agent_missing"
	CollectedVia string `json:"collected_via,omitempty"`
	AgentName    string `json:"agent_name,omitempty"` // relay agent the job was dispatched to
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
func skippedProtocols(plan discovery.ProtocolPlan) []string {
	universe := []struct {
		label string
		kind  domain.CredentialKind
	}{
		{"SNMP", domain.CredSNMPv2c}, {"SSH", domain.CredSSH},
		{"WinRM", domain.CredWinRM}, {"ONVIF", domain.CredONVIF},
	}
	var out []string
	for _, p := range universe {
		if !plan.Relevant(p.kind) {
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

// recordResult writes one actionable discovery_results row for an alive host.
func (s *Server) recordResult(ctx context.Context, jobID uuid.UUID, ip netip.Addr, r discovery.HostResult, id uuid.UUID, applyErr error, enrichment string, profRes *scanProfileResult, collectedVia, agentName string) {
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

	category := string(r.Match.Category)
	bound := r.BoundCred != nil
	boundKind := ""
	if bound {
		boundKind = string(r.BoundCred.Kind)
	}

	// Human-readable evidence trail (safe, non-secret).
	var evidence []string
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
			Kind: string(a.Kind), Protocol: a.Protocol, Category: a.Category, Detail: a.Detail, Success: a.Success, Relevant: a.Relevant,
		})
	}

	detail := scanDetail{
		OpenPorts: r.OpenPorts, Classification: category, Confidence: r.Match.Confidence,
		Evidence: evidence, Candidate: r.Plan.Candidate, ExpectedProtocols: r.Plan.Expected,
		SkippedProtocols: skippedProtocols(r.Plan),
		CredAttempts:     attempts, BoundCred: boundKind,
		Enrichment: enrichment, Profile: profRes,
		NextAction:   scanNextActionWithPlan(category, bound, boundKind, profRes, r.Plan, attempts),
		CollectedVia: collectedVia, AgentName: agentName,
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
	})
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

func (s *Server) listDiscoveryJobs(w http.ResponseWriter, r *http.Request) {
	rows, err := s.queries.ListDiscoveryJobs(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rows)
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
			req = scanReq{Mode: spec.Mode, Targets: spec.Targets, CIDR: spec.CIDR, CredentialIDs: spec.CredentialIDs, CredentialGroupIDs: spec.CredentialGroupIDs}
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
	go s.runScanJob(job.ID, hosts, prev.LocationID, defConc, extra, snmpTO, portTO)
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
	// per-device scan detail object.
	out := make([]map[string]any, 0, len(results))
	for _, x := range results {
		row := map[string]any{
			"id": x.ID, "job_id": x.JobID, "ip": x.Ip, "outcome": x.Outcome,
			"device_id": x.DeviceID, "driver": x.Driver, "category": x.Category,
			"error": x.Error, "probed_at": x.ProbedAt,
			"probe_data": json.RawMessage(x.ProbeData),
		}
		out = append(out, row)
	}
	writeJSON(w, http.StatusOK, map[string]any{"job": job, "results": out})
}
