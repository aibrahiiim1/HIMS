package api

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/coralsearesorts/hims/internal/domain"
	"github.com/coralsearesorts/hims/internal/fingerprint"
	"github.com/coralsearesorts/hims/internal/osinv"
	"github.com/coralsearesorts/hims/internal/storage/postgres/db"
	"github.com/google/uuid"
)

// Data Quality Center — surfaces inventory hygiene issues an operator should fix
// (duplicates, missing classification, stale records). Everything is derived
// from the real device inventory; nothing is fabricated.

type dqDevice struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	PrimaryIP string `json:"primary_ip,omitempty"`
	Category  string `json:"category"`
	Vendor    string `json:"vendor,omitempty"`
	Note      string `json:"note,omitempty"`
}

type dqIssue struct {
	Key         string     `json:"key"`
	Label       string     `json:"label"`
	Description string     `json:"description"`
	Severity    string     `json:"severity"` // info | warning | critical
	Count       int        `json:"count"`
	Devices     []dqDevice `json:"devices"` // sample (capped)
}

const dqSampleCap = 100

// lowConfidenceThreshold: auto-classifications scoring below this (out of 100)
// are surfaced for operator confirmation in the Data Quality center.
const lowConfidenceThreshold = 50

// credentialedCategories are device classes that normally need a bound
// credential; missing creds elsewhere (printers, cameras, unknown) is expected
// and would only add noise.
var credentialedCategories = map[string]bool{
	"switch": true, "router": true, "firewall": true, "server": true,
	"wireless_controller": true, "virtual_host": true, "nvr": true, "pbx": true,
	"database": true,
}

func (s *Server) dataQuality(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	devs, err := s.queries.ListAllDevices(ctx)
	if err != nil {
		writeErr(w, err)
		return
	}
	now := time.Now().UTC()
	const staleDays = 30
	staleBefore := now.AddDate(0, 0, -staleDays)

	ipGroups := map[string][]db.Device{}
	nameGroups := map[string][]db.Device{}
	var missingLoc, missingVendor, missingCreds, unknownCat, stale, lowConf []db.Device

	for _, d := range devs {
		if d.PrimaryIp != nil && d.PrimaryIp.IsValid() {
			ip := d.PrimaryIp.String()
			ipGroups[ip] = append(ipGroups[ip], d)
		}
		nameGroups[strings.ToLower(strings.TrimSpace(d.Name))] = append(nameGroups[strings.ToLower(strings.TrimSpace(d.Name))], d)

		if d.LocationID == nil {
			missingLoc = append(missingLoc, d)
		}
		if d.Vendor == nil || strings.TrimSpace(*d.Vendor) == "" {
			missingVendor = append(missingVendor, d)
		}
		if credentialedCategories[d.Category] && d.CredentialID == nil {
			missingCreds = append(missingCreds, d)
		}
		if d.Category == "unknown" {
			unknownCat = append(unknownCat, d)
		}
		if d.LastDiscoveryAt == nil || d.LastDiscoveryAt.Before(staleBefore) {
			stale = append(stale, d)
		}
		// Weakly auto-classified devices (scored but below the confidence bar) —
		// an operator should confirm and Lock them. Unscored/unknown devices are
		// already covered by "Unclassified", so exclude them here.
		if d.Category != "unknown" && !d.ClassificationLocked && d.ConfidenceScore != nil && *d.ConfidenceScore < lowConfidenceThreshold {
			lowConf = append(lowConf, d)
		}
	}

	issues := []dqIssue{}

	// Conflicting IPs — the same primary IP on more than one device record.
	var ipConflicts []dqDevice
	for ip, g := range ipGroups {
		if len(g) > 1 {
			for _, d := range g {
				dd := toDQ(d)
				dd.Note = "shares IP " + ip
				ipConflicts = append(ipConflicts, dd)
			}
		}
	}
	if len(ipConflicts) > 0 {
		if len(ipConflicts) > dqSampleCap {
			ipConflicts = ipConflicts[:dqSampleCap]
		}
		issues = append(issues, dqIssue{
			Key: "conflicting_ip", Label: "Conflicting IP addresses", Severity: "critical",
			Description: "The same primary IP is recorded on more than one device. This usually means a duplicate or a stale record that should be merged or removed.",
			Count:       len(ipConflicts), Devices: ipConflicts,
		})
	}

	// Duplicate names.
	var nameDupes []db.Device
	for _, g := range nameGroups {
		if len(g) > 1 {
			nameDupes = append(nameDupes, g...)
		}
	}
	if len(nameDupes) > 0 {
		issues = append(issues, dqIssue{
			Key: "duplicate_name", Label: "Duplicate device names", Severity: "warning",
			Description: "Multiple devices share the same name. Confirm these are distinct devices and rename, or merge duplicates.",
			Count:       len(nameDupes), Devices: sampleDevices(nameDupes),
		})
	}

	addIssue := func(key, label, desc, sev string, list []db.Device) {
		if len(list) == 0 {
			return
		}
		issues = append(issues, dqIssue{Key: key, Label: label, Description: desc, Severity: sev, Count: len(list), Devices: sampleDevices(list)})
	}
	// addDQ appends an issue whose "devices" are pre-built rows (used for
	// agent-centric issues where the subject is a Relay Agent or a relay job, not
	// a device). count is the caller-supplied total (list may already be capped).
	addDQ := func(key, label, desc, sev string, list []dqDevice, count int) {
		if count == 0 {
			return
		}
		issues = append(issues, dqIssue{Key: key, Label: label, Description: desc, Severity: sev, Count: count, Devices: list})
	}
	addIssue("stale_devices", "Not seen recently", "These devices have not been re-discovered in over 30 days (or never). They may be decommissioned, moved, or unreachable.", "warning", stale)
	addIssue("missing_location", "Missing location", "Devices not assigned to a site/location. Assign them so they roll up correctly in Sites Health and reports.", "warning", missingLoc)
	addIssue("missing_credentials", "Missing credentials", "Credentialed device classes (switches, firewalls, servers…) with no bound credential cannot be deeply collected. Bind a working credential.", "warning", missingCreds)
	addIssue("unknown_category", "Unclassified devices", "Devices still classified as 'unknown'. Enrich with SNMP/CLI or set the type manually so they appear in the right inventory views.", "info", unknownCat)
	addIssue("low_confidence", "Low-confidence classification", "Devices auto-classified below "+strconv.Itoa(lowConfidenceThreshold)+"% confidence. Open the device, Re-classify (or bind a credential for deeper signals), then Lock once correct.", "info", lowConf)
	addIssue("missing_vendor", "Missing vendor", "Devices with no vendor recorded. Vendor enriches fingerprinting, reports and lifecycle.", "info", missingVendor)

	// --- Fingerprint-driven classification quality (FP-ext) -------------------
	// Re-evaluate the vendor-fingerprint library against each device's STORED raw
	// SNMP identity facts (no re-probe). Surfaces devices that a fingerprint could
	// classify better than the value currently on the row — most importantly, a
	// device that an operator's own (user) fingerprint would re-classify but which
	// hasn't been re-scanned since the rule was added.
	s.addFingerprintDQ(ctx, devs, &issues)

	// --- Wireless controller collection quality (WC-P5) ----------------------
	s.addWirelessDQ(ctx, devs, &issues, staleBefore)

	// --- MIB pack quality (MWC) ----------------------------------------------
	s.addMibDQ(ctx, devs, &issues)

	// --- Extreme XCC SSH CLI quality (SSH) ------------------------------------
	s.addSSHWirelessDQ(ctx, devs, &issues)
	s.addWirelessSummaryDQ(ctx, devs, &issues)

	// Servers/endpoints that have never had a deep OS inventory collected.
	if rows, err := s.queries.ListDevicesWithoutOSInventory(ctx); err == nil && len(rows) > 0 {
		devs := make([]dqDevice, 0, len(rows))
		for i, d := range rows {
			if i >= dqSampleCap {
				break
			}
			dd := dqDevice{ID: d.ID.String(), Name: d.Name, Category: d.Category}
			if d.PrimaryIp != nil && d.PrimaryIp.IsValid() {
				dd.PrimaryIP = d.PrimaryIp.String()
			}
			devs = append(devs, dd)
		}
		issues = append(issues, dqIssue{
			Key: "os_not_inventoried", Label: "OS not inventoried", Severity: "info",
			Description: "Servers/endpoints with no deep OS inventory yet. Open the device, bind a working credential (WinRM for Windows, SSH for Linux) and use Collect OS to gather OS/hardware/services/software.",
			Count:       len(rows), Devices: devs,
		})
	}

	// Credential-health issues — derived from real bindings, collection evidence
	// and persisted credential-test history (access map + test map). All counts
	// are real; classes that legitimately have no signal yet simply don't appear.
	if am, aerr := s.deviceAccessMap(ctx); aerr == nil {
		tm, _ := s.deviceTestMap(ctx)
		isWin := func(d db.Device) bool { return d.OsFamily == "windows" }
		isLin := func(d db.Device) bool { return d.OsFamily == "linux" }
		inCat := func(d db.Device, cats ...string) bool {
			for _, c := range cats {
				if d.Category == c {
					return true
				}
			}
			return false
		}
		// winLike = a Windows host whether or not the OS was collected yet (a legacy
		// host that never collected has category=endpoint but os_family unset).
		winLike := func(d db.Device) bool { return isWin(d) || d.Category == "endpoint" }
		var credFailed, neverTested, linNoSSH, camNoONVIF, swNoSSH []db.Device
		var legacyWin, winAuthFailed, winDisabled, winReady []db.Device
		var wmiUnreachable, wmiAccessDenied, wmiCredFailed []db.Device
		for _, d := range devs {
			da := am[d.ID]
			ts := tm[d.ID]
			winrmCat := ""
			if ts != nil {
				winrmCat = ts.kindCategory["winrm"]
			}
			// auth_ok_operation_fault must NOT count as a failed credential. A bound
			// credential that is not PROVEN to work still counts as failed (proven-only
			// management rule) — a bare binding never masks a real auth failure.
			if ts != nil && ts.authFailed && !da.hasProven() {
				credFailed = append(credFailed, d)
			}
			testable := credentialedCategories[d.Category] || inCat(d, "camera", "nvr") || isWin(d) || isLin(d)
			if testable && (ts == nil || !ts.tested) {
				neverTested = append(neverTested, d)
			}
			if isLin(d) && !accessSatisfies(da, "ssh") {
				linNoSSH = append(linNoSSH, d)
			}
			if inCat(d, "camera", "nvr") && !accessSatisfies(da, "onvif") {
				camNoONVIF = append(camNoONVIF, d)
			}
			if inCat(d, "switch", "router", "firewall") && !accessSatisfies(da, "ssh") {
				swNoSSH = append(swNoSSH, d)
			}

			// --- Windows onboarding buckets (mutually exclusive, most-specific first) ---
			if !winLike(d) {
				continue
			}
			switch {
			case ts.winrmLegacy():
				legacyWin = append(legacyWin, d) // auth OK, WSMan operation fault → needs fallback
			case accessSatisfies(da, "winrm") || accessSatisfies(da, "wmi"):
				winReady = append(winReady, d) // WinRM or WMI works — collection-ready
			case winrmCat == "auth_failed":
				winAuthFailed = append(winAuthFailed, d)
			case winrmCat == "unreachable" || winrmCat == "":
				winDisabled = append(winDisabled, d) // 5985 closed / WinRM disabled / not attempted
			}
			// WMI/DCOM-specific buckets (latest wmi test outcome), independent of WinRM.
			wmiCat := ""
			if ts != nil {
				wmiCat = ts.kindCategory["wmi"]
			}
			switch wmiCat {
			case osinv.WMIDcomUnreachable, osinv.WMIRpcUnreachable, osinv.WMIFirewallBlocked:
				if !accessSatisfies(da, "wmi") {
					wmiUnreachable = append(wmiUnreachable, d)
				}
			case osinv.WMIAccessDenied:
				wmiAccessDenied = append(wmiAccessDenied, d)
			case osinv.WMIAuthFailed:
				wmiCredFailed = append(wmiCredFailed, d)
			}
		}
		addIssue("credential_failed", "Failed credentials", "The latest credential test for these devices was rejected (authentication failed) and no other working method is bound. Fix the credential or bind a working one.", "warning", credFailed)
		addIssue("never_tested", "Never credential-tested", "These devices (servers, network gear, cameras, Windows/Linux hosts) have no saved credential-test result yet. Run a credential test so HIMS knows what works.", "info", neverTested)
		addIssue("legacy_windows_wsman2", "Legacy Windows — needs fallback collector", "Windows hosts where WinRM AUTHENTICATION SUCCEEDED but the WSMan operation faulted (legacy WSMan 2.0 — Windows 7 / Server 2008 R2). The credential is valid; native PowerShell works but the Go WinRM library cannot run commands. Configure the Windows Native Collector or WMI/DCOM fallback.", "warning", legacyWin)
		// Surface the collector-config gap tied to the affected legacy hosts.
		if url, _ := nativeCollectorConfig(); url == "" && len(legacyWin) > 0 {
			addIssue("windows_native_collector_not_configured", "Windows Native Collector not configured", "Legacy Windows hosts were detected but the Windows Native Collector is not configured. Set HIMS_WINDOWS_NATIVE_COLLECTOR_URL and HIMS_WINDOWS_NATIVE_COLLECTOR_TOKEN and deploy deploy/windows-native-collector.ps1 to collect these hosts (or configure a WMI/DCOM fallback).", "warning", legacyWin)
		}
		addIssue("windows_winrm_auth_failed", "Windows with WinRM auth failed", "Windows hosts where WinRM is reachable but the credential was rejected. Fix the credential (DOMAIN\\user vs UPN, password).", "warning", winAuthFailed)
		addIssue("windows_winrm_disabled", "Windows with WinRM disabled / closed", "Windows hosts with no WinRM evidence (5985/5986 closed or not responding). Enable PowerShell Remoting (GPO) and open the firewall, then re-scan.", "warning", winDisabled)
		addIssue("windows_ready", "Windows ready for collection", "Windows hosts with working WinRM/WMI access — deep OS inventory can be collected.", "info", winReady)
		addIssue("windows_wmi_unreachable", "Windows WMI/DCOM unreachable", "Legacy Windows hosts where WMI/DCOM (RPC 135) is unreachable or firewall-blocked. Open RPC (135 + the DCOM dynamic range) or use a collector on the same broadcast domain.", "warning", wmiUnreachable)
		addIssue("windows_wmi_access_denied", "Windows WMI access denied", "WMI/DCOM authenticated but access was denied (DCOM launch/activation or namespace permissions). Grant the account WMI/DCOM remote access — NOT a wrong password.", "warning", wmiAccessDenied)
		addIssue("windows_wmi_credential_failed", "Windows WMI credential failed", "The WMI/DCOM credential was rejected. Fix the domain credential (DOMAIN\\user / password).", "warning", wmiCredFailed)
		// Legacy Windows that specifically needs the WMI/DCOM fallback (WinRM
		// disabled or legacy-incompatible) when no WMI collector is configured.
		if url, _ := wmiCollectorConfig(); url == "" && (len(winDisabled) > 0 || len(legacyWin) > 0) {
			needsWMI := append(append([]db.Device{}, legacyWin...), winDisabled...)
			addIssue("legacy_windows_needs_wmi", "Legacy Windows needs WMI/DCOM fallback", "Windows hosts that cannot be collected over WinRM (disabled or legacy WSMan 2.0) and have no WMI/DCOM collector configured. Set HIMS_WMI_COLLECTOR_URL and deploy the WMI collector helper, or enable WinRM.", "warning", needsWMI)
		}
		addIssue("linux_no_ssh", "Linux without working SSH", "Linux hosts with no successful SSH access. SSH is needed for deep OS inventory.", "warning", linNoSSH)
		addIssue("camera_no_onvif", "Cameras/NVRs without ONVIF", "Cameras/NVRs with no successful ONVIF access. ONVIF is needed for device-info and stream inventory.", "warning", camNoONVIF)
		addIssue("switch_no_ssh", "Switches/firewalls without SSH", "Network devices with no successful SSH access. SSH enables CLI collection and config backup beyond SNMP.", "info", swNoSSH)

		// Vendor Connection Profile issues — VMware / CCTV onboarding gaps. These
		// reuse the same scan resolution (device > site > global) in-memory so the
		// report matches what a scan would actually do. Counts are real.
		profs, perr := s.queries.ListVendorProfiles(ctx)
		if perr == nil {
			resolveProf := func(vendorTypes []string, devID uuid.UUID, locID *uuid.UUID) (db.VendorConnectionProfile, bool) {
				inTypes := func(t string) bool {
					for _, v := range vendorTypes {
						if v == t {
							return true
						}
					}
					return false
				}
				// device-bound first, then site, then global.
				for stage := 0; stage < 3; stage++ {
					for _, p := range profs {
						if !p.Enabled || !inTypes(p.VendorType) {
							continue
						}
						match := false
						switch stage {
						case 0:
							match = p.DeviceID != nil && *p.DeviceID == devID
						case 1:
							match = p.LocationID != nil && locID != nil && *p.LocationID == *locID
						case 2:
							match = p.DeviceID == nil && p.LocationID == nil
						}
						if match {
							return p, true
						}
					}
				}
				return db.VendorConnectionProfile{}, false
			}
			profFailed := func(p db.VendorConnectionProfile) bool {
				return p.LastTestOk != nil && !*p.LastTestOk
			}
			var vhNoProfile, vmwareProfileFailed, cctvNoProfile, nvrNotAuth, profFailedDevs []db.Device
			for _, d := range devs {
				da := am[d.ID]
				switch {
				case d.Category == "virtual_host":
					prof, found := resolveProf([]string{"vmware"}, d.ID, d.LocationID)
					if !found {
						if !accessSatisfies(da, "vmware") {
							vhNoProfile = append(vhNoProfile, d)
						}
					} else if profFailed(prof) && !accessSatisfies(da, "vmware") {
						vmwareProfileFailed = append(vmwareProfileFailed, d)
					}
				case d.Category == "camera" || d.Category == "nvr":
					prof, found := resolveProf([]string{"cctv"}, d.ID, d.LocationID)
					if !found && !accessSatisfies(da, "onvif") {
						cctvNoProfile = append(cctvNoProfile, d)
					}
					if found && profFailed(prof) && !accessSatisfies(da, "onvif") {
						profFailedDevs = append(profFailedDevs, d)
					}
					if d.Category == "nvr" && !accessSatisfies(da, "onvif") {
						nvrNotAuth = append(nvrNotAuth, d)
					}
				}
			}
			addIssue("virtual_host_no_vmware_profile", "Virtual hosts without a VMware profile", "ESXi/vCenter candidates with no VMware Vendor Connection Profile and no successful vSphere collection. Create a VMware profile (Discovery → Vendor Profiles) so the scan can collect host + VM facts.", "warning", vhNoProfile)
			addIssue("vmware_profile_failed", "VMware profile failed", "ESXi/vCenter candidates whose VMware Vendor Connection Profile test/collection failed. Fix the credential or vCenter/ESXi URL in Vendor Profiles.", "warning", vmwareProfileFailed)
			addIssue("cctv_no_profile", "Cameras/NVRs without a CCTV profile", "Camera/NVR/DVR candidates with no CCTV / ONVIF Vendor Connection Profile and no successful ONVIF access. Create a CCTV profile to authenticate and confirm camera vs NVR/DVR.", "warning", cctvNoProfile)
			addIssue("nvr_not_authenticated", "NVR/DVR not authenticated", "NVR/DVR candidates with no successful ONVIF authentication yet. Bind a working ONVIF/HTTP credential or create a CCTV Vendor Connection Profile.", "warning", nvrNotAuth)
			addIssue("vendor_profile_failed", "Vendor profile connection failed", "Devices whose matching Vendor Connection Profile failed its last connection test. Fix the credential/URL in Vendor Profiles.", "warning", profFailedDevs)
		}

		// --- Relay Agent / Site Collector issues ---------------------------------
		// Windows hosts that cannot be collected directly (legacy WSMan 2.0 or WinRM
		// disabled/unreachable) need the site Relay Agent. Surface where an agent is
		// missing, offline, stale, or failing jobs so the operator can fix it.
		agents, aerr := s.queries.ListRelayAgents(ctx)
		if aerr == nil {
			agentSites := map[uuid.UUID]bool{}  // location has >=1 agent
			onlineSites := map[uuid.UUID]bool{} // location has >=1 online agent
			for _, a := range agents {
				if a.LocationID != nil {
					agentSites[*a.LocationID] = true
					if relayAgentOnline(a) {
						onlineSites[*a.LocationID] = true
					}
				}
			}
			// Devices needing local/agent collection = legacy + WinRM-disabled Windows.
			needSet := append(append([]db.Device{}, legacyWin...), winDisabled...)
			var needsAgent, siteNoAgent []db.Device
			for _, d := range needSet {
				needsAgent = append(needsAgent, d)
				onlineHere := d.LocationID != nil && onlineSites[*d.LocationID]
				if !onlineHere && (d.LocationID == nil || !agentSites[*d.LocationID]) {
					siteNoAgent = append(siteNoAgent, d)
				}
			}
			addIssue("device_requires_agent", "Devices that need a Relay Agent", "Windows hosts that cannot be collected directly (legacy WSMan 2.0, or WinRM disabled/unreachable). Install or assign a Relay Agent to their site and HIMS will collect them via WMI/DCOM.", "warning", needsAgent)
			addIssue("site_legacy_windows_no_agent", "Sites with legacy Windows but no Relay Agent", "These hosts need local collection but no Relay Agent is assigned to their site (or they have no site). Install a Relay Agent on a trusted machine in the site and assign it (Discovery → Relay Agents).", "warning", siteNoAgent)

			// Agent-centric issues (subject is the agent, not a device).
			var offline, staleHb, failedAgents []dqDevice
			for _, a := range agents {
				if !a.Enabled {
					continue
				}
				if !relayAgentOnline(a) {
					note, dest := "never connected", &offline
					if a.LastHeartbeat != nil {
						note = "last heartbeat " + a.LastHeartbeat.Format("2006-01-02 15:04 MST")
						if timeSince(*a.LastHeartbeat) < 15*time.Minute {
							dest = &staleHb // recently online, just went quiet
						}
					}
					if a.LastError != "" {
						note += "; last error: " + truncate(a.LastError, 100)
					}
					*dest = append(*dest, agentDQ(a, note))
				}
				if n, _ := s.queries.CountFailedAgentJobs(ctx, a.ID); n > 0 {
					failedAgents = append(failedAgents, agentDQ(a, strconv.FormatInt(n, 10)+" failed collection job(s)"))
				}
			}
			addDQ("relay_agent_offline", "Relay Agent offline", "Enabled Relay Agents that are not reporting in. Start or repair the agent service on the site machine; devices that depend on it cannot be collected until it is back.", "critical", offline, len(offline))
			addDQ("relay_agent_stale_heartbeat", "Relay Agent heartbeat stale", "Relay Agents that were online recently but have stopped heartbeating. Check the agent process and network path before it is marked offline.", "warning", staleHb, len(staleHb))
			addDQ("relay_agent_failed_jobs", "Relay Agent has failed jobs", "Relay Agents with one or more failed collection jobs. Open the agent to see the failures (bad credential, unreachable target, or collection error).", "warning", failedAgents, len(failedAgents))

			// Recent failed relay jobs (subject is the job).
			if jobs, jerr := s.queries.ListRecentAgentJobsAll(ctx, 200); jerr == nil {
				name := map[uuid.UUID]string{}
				for _, a := range agents {
					name[a.ID] = a.Name
				}
				var jobFails []dqDevice
				total := 0
				for _, j := range jobs {
					if j.Status != "failed" {
						continue
					}
					total++
					if len(jobFails) >= dqSampleCap {
						continue
					}
					jobFails = append(jobFails, dqDevice{
						Name: name[j.AgentID] + " → " + j.Target, Category: j.Protocol,
						Note: strings.TrimSpace(j.Category + " " + truncate(j.Error, 120)),
					})
				}
				addDQ("relay_job_failed", "Relay collection job failed", "Recent Relay Agent collection jobs that failed. Review the cause (credential rejected, target unreachable, or collection error) and retry.", "warning", jobFails, total)
			}
		}
	}

	// --- Scan stability: Known-Device Retry signals from scan-result history -----
	// Derived purely from real discovery_results dispositions (newest-first). A
	// "known device missed last scan" is one whose most recent scan disposition was
	// unreachable-after-retry; "flapping" recovered by retry in the latest scan;
	// "frequently missed" was missed by the first sweep in >=3 of the last 5 scans.
	if disp, derr := s.queries.ListKnownDeviceScanDispositions(ctx); derr == nil && len(disp) > 0 {
		byID := make(map[uuid.UUID]db.Device, len(devs))
		for _, d := range devs {
			byID[d.ID] = d
		}
		perDev := map[uuid.UUID][]string{} // dispositions newest-first, one per (device,job)
		order := []uuid.UUID{}
		for _, r := range disp {
			if r.DeviceID == nil {
				continue
			}
			id := *r.DeviceID
			if _, ok := perDev[id]; !ok {
				order = append(order, id)
			}
			perDev[id] = append(perDev[id], r.Disposition)
		}
		stillMissed := map[string]bool{"known_missed": true, "known_unreachable": true}
		firstSweepMissed := map[string]bool{"known_missed": true, "known_unreachable": true, "known_recovered": true}
		const freqWindow, freqThreshold = 5, 3
		var missedLast, flapping, frequent []dqDevice
		for _, id := range order {
			evs := perDev[id]
			d, ok := byID[id]
			if !ok || len(evs) == 0 { // device deleted since the scan — skip
				continue
			}
			switch {
			case stillMissed[evs[0]]:
				dd := toDQ(d)
				dd.Note = "missed in the latest scan (unreachable after retry)"
				missedLast = append(missedLast, dd)
			case evs[0] == "known_recovered":
				dd := toDQ(d)
				dd.Note = "missed the first sweep but recovered by retry in the latest scan"
				flapping = append(flapping, dd)
			}
			n, seen := 0, 0
			for _, e := range evs {
				if seen >= freqWindow {
					break
				}
				seen++
				if firstSweepMissed[e] {
					n++
				}
			}
			if n >= freqThreshold {
				dd := toDQ(d)
				dd.Note = "missed the first sweep in " + strconv.Itoa(n) + " of the last " + strconv.Itoa(seen) + " scans"
				frequent = append(frequent, dd)
			}
		}
		capDQ := func(x []dqDevice) []dqDevice {
			if len(x) > dqSampleCap {
				return x[:dqSampleCap]
			}
			return x
		}
		addDQ("known_device_missed_last_scan", "Known device missed last scan", "Managed devices NOT found in the most recent scan of their subnet, even after targeted retries. They were not removed from inventory — verify power/reachability and re-scan. Open the scan job to see the miss.", "warning", capDQ(missedLast), len(missedLast))
		addDQ("known_device_flapping_in_scan", "Known device flapping in scan", "Managed devices the main sweep missed but a targeted retry recovered in the latest scan. Intermittent reachability under scan load — consider lowering scan concurrency or raising timeouts for this site.", "info", capDQ(flapping), len(flapping))
		addDQ("frequently_missed_known_device", "Frequently missed known device", "Managed devices missed by the first sweep in at least "+strconv.Itoa(freqThreshold)+" of the last "+strconv.Itoa(freqWindow)+" scans. Chronically flaky discovery — investigate the host, its switch port, or the scan profile.", "warning", capDQ(frequent), len(frequent))
	}

	// Reachability-vs-Management hygiene issues — the cases that prove Online and
	// Managed are distinct (online-but-unmanaged, offline-but-previously-managed,
	// credential-bound-but-not-working, …). Derived from the shared status model.
	if sm, serr := s.buildStatusMaps(ctx); serr == nil {
		issues = append(issues, sm.statusDataQualityIssues(devs, now)...)
	}

	// Stable order: critical first, then warning, then info; ties by count desc.
	rank := map[string]int{"critical": 0, "warning": 1, "info": 2}
	sort.SliceStable(issues, func(i, j int) bool {
		if rank[issues[i].Severity] != rank[issues[j].Severity] {
			return rank[issues[i].Severity] < rank[issues[j].Severity]
		}
		return issues[i].Count > issues[j].Count
	})

	clean := len(issues) == 0
	writeJSON(w, http.StatusOK, map[string]any{
		"generated_at":  now.Format(time.RFC3339),
		"total_devices": len(devs),
		"issue_count":   len(issues),
		"clean":         clean,
		"issues":        issues,
	})
}

func toDQ(d db.Device) dqDevice {
	dd := dqDevice{ID: d.ID.String(), Name: d.Name, Category: d.Category}
	if d.PrimaryIp != nil && d.PrimaryIp.IsValid() {
		dd.PrimaryIP = d.PrimaryIp.String()
	}
	if d.Vendor != nil {
		dd.Vendor = *d.Vendor
	}
	return dd
}

// agentDQ renders a Relay Agent as a Data-Quality "device" row so agent-centric
// issues display in the same table. Category "relay_agent" lets the UI link to
// the agent detail page; Note carries the human cause.
func agentDQ(a db.RelayAgent, note string) dqDevice {
	return dqDevice{ID: a.ID.String(), Name: a.Name, PrimaryIP: a.Ip, Category: "relay_agent", Note: note}
}

// addWirelessDQ appends the five wireless-controller collection-quality issues.
func (s *Server) addWirelessDQ(ctx context.Context, devs []db.Device, issues *[]dqIssue, staleBefore time.Time) {
	var controllers []db.Device
	for _, d := range devs {
		if d.Category == string(domain.CatWirelessController) {
			controllers = append(controllers, d)
		}
	}
	if len(controllers) == 0 {
		return
	}
	// Wireless API profiles by bound device + location (any wireless vendor type).
	wirelessVT := map[string]bool{"extreme_xcc": true, "wireless_unifi": true, "wireless_omada": true, "wireless_ruckus": true, "wireless_extreme": true, "wireless_aruba": true}
	profByDev := map[uuid.UUID]db.VendorConnectionProfile{}
	profByLoc := map[uuid.UUID]db.VendorConnectionProfile{}
	if profs, err := s.queries.ListVendorProfiles(ctx); err == nil {
		for _, p := range profs {
			if !wirelessVT[p.VendorType] {
				continue
			}
			if p.DeviceID != nil {
				profByDev[*p.DeviceID] = p
			} else if p.LocationID != nil {
				profByLoc[*p.LocationID] = p
			}
		}
	}

	var missingProfile, snmpOnly, apMissing, collFailed, stale []db.Device
	for _, d := range controllers {
		prof, hasProf := profByDev[d.ID]
		if !hasProf && d.LocationID != nil {
			prof, hasProf = profByLoc[*d.LocationID]
		}
		info, infoErr := s.queries.GetWLANControllerInfo(ctx, d.ID)
		aps, _ := s.queries.ListAccessPoints(ctx, d.ID)

		if !hasProf {
			missingProfile = append(missingProfile, d)
		} else if prof.LastTestOk != nil && !*prof.LastTestOk {
			collFailed = append(collFailed, d)
		}
		// SNMP-only: no API-sourced controller info row (never collected via API).
		apiCollected := infoErr == nil && info.Source == "extreme_xcc_api"
		if !apiCollected {
			snmpOnly = append(snmpOnly, d)
		}
		if len(aps) == 0 {
			apMissing = append(apMissing, d)
		} else if infoErr == nil && info.CollectedAt.Before(staleBefore) {
			stale = append(stale, d)
		}
	}

	add := func(key, label, desc, sev string, list []db.Device) {
		if len(list) == 0 {
			return
		}
		*issues = append(*issues, dqIssue{Key: key, Label: label, Description: desc, Severity: sev, Count: len(list), Devices: sampleDevices(list)})
	}
	add("wireless_controller_missing_api_profile", "Wireless controller: no API profile",
		"Wireless controllers with no Vendor Connection Profile. SNMP identity is captured, but AP/SSID/client data needs a controller API profile (e.g. Extreme XCC).", "warning", missingProfile)
	add("wireless_controller_snmp_only", "Wireless controller: SNMP-only",
		"Wireless controllers managed via SNMP only — no API collection has run, so the AP/SSID/client roster is not available. Configure + run an API profile.", "info", snmpOnly)
	add("wireless_controller_ap_data_missing", "Wireless controller: no AP inventory",
		"Wireless controllers with zero access points collected. Run an API collection; if the API exposes no APs on this firmware, that is surfaced honestly.", "warning", apMissing)
	add("wireless_controller_collection_failed", "Wireless controller: collection failed",
		"Wireless controllers whose last API profile test/collection failed. Open the controller and use Test Connection to see the exact reason.", "critical", collFailed)
	add("wireless_ap_inventory_stale", "Wireless AP inventory stale",
		"Wireless controllers whose AP inventory has not been refreshed recently. Re-run collection to update AP/client status.", "info", stale)
}

// addMibDQ appends MIB-pack + SNMP-MIB-collection data-quality issues.
func (s *Server) addMibDQ(ctx context.Context, devs []db.Device, issues *[]dqIssue) {
	packs, _ := s.queries.ListMibPacks(ctx)
	var uploadedUnused, mappingMissing, parseErrors, noTablesSupported []dqDevice
	for _, p := range packs {
		row := dqDevice{ID: p.ID.String(), Name: p.Name, Category: "mib_pack"}
		if p.Source == "user" && p.LastTestedAt == nil && p.LastCollectedAt == nil {
			r := row
			r.Note = "uploaded but never tested or used"
			uploadedUnused = append(uploadedUnused, r)
		}
		if ts, _ := s.queries.ListMibPackTables(ctx, p.ID); len(ts) == 0 {
			r := row
			r.Note = "no table → OID mappings defined"
			mappingMissing = append(mappingMissing, r)
		}
		var meta struct {
			Warnings []string `json:"warnings"`
		}
		_ = json.Unmarshal(p.ParseMeta, &meta)
		if len(meta.Warnings) > 0 {
			r := row
			r.Note = strconv.Itoa(len(meta.Warnings)) + " parse warning(s)"
			parseErrors = append(parseErrors, r)
		}
		if strings.HasPrefix(p.LastTestDetail, "0/") {
			r := row
			r.Note = "matched a device but no tables responded"
			noTablesSupported = append(noTablesSupported, r)
		}
	}

	var missingPack, userNotApplied, apEmpty, partial []db.Device
	userPackExists := false
	for _, p := range packs {
		if p.Source == "user" && p.Enabled {
			userPackExists = true
			break
		}
	}
	for _, d := range devs {
		if d.Category != string(domain.CatWirelessController) {
			continue
		}
		_, hasPack := s.matchMibPack(ctx, d)
		// Did an SNMP MIB walk actually run on this device? (raw rows are captured
		// even when nothing maps into the wireless tables — the honest evidence.)
		walk, _ := s.queries.ListMibWalkRows(ctx, db.ListMibWalkRowsParams{DeviceID: d.ID, Limit: 1})
		mibRan := len(walk) > 0
		// AP / client rosters that came specifically from the MIB source.
		apFromMib, cliFromMib := 0, 0
		if aps, e := s.queries.ListAccessPoints(ctx, d.ID); e == nil {
			for _, a := range aps {
				if a.Source == mibSource {
					apFromMib++
				}
			}
		}
		if cls, e := s.queries.ListWirelessClients(ctx, d.ID); e == nil {
			for _, c := range cls {
				if c.Source == mibSource {
					cliFromMib++
				}
			}
		}
		if !hasPack {
			missingPack = append(missingPack, d)
		} else if userPackExists && !mibRan {
			// A user pack is configured but no MIB walk has run on this controller.
			userNotApplied = append(userNotApplied, d)
		}
		if mibRan {
			if apFromMib == 0 {
				apEmpty = append(apEmpty, d)
			}
			if apFromMib == 0 || cliFromMib == 0 {
				partial = append(partial, d)
			}
		}
	}

	addDev := func(key, label, desc, sev string, list []db.Device) {
		if len(list) == 0 {
			return
		}
		*issues = append(*issues, dqIssue{Key: key, Label: label, Description: desc, Severity: sev, Count: len(list), Devices: sampleDevices(list)})
	}
	addRows := func(key, label, desc, sev string, list []dqDevice) {
		if len(list) == 0 {
			return
		}
		*issues = append(*issues, dqIssue{Key: key, Label: label, Description: desc, Severity: sev, Count: len(list), Devices: list})
	}
	addRows("mib_pack_uploaded_not_used", "MIB pack uploaded, not used", "Uploaded MIB packs that have never been tested or run against a device. Test the pack against a controller to start using it.", "info", uploadedUnused)
	addRows("mib_mapping_missing", "MIB pack mapping missing", "MIB packs with no table → OID → purpose mappings. Add mappings (or they cannot drive collection).", "warning", mappingMissing)
	addRows("mib_parse_errors", "MIB parse warnings", "Uploaded MIB files that produced parse warnings. Review the files; collection still works via mappings.", "info", parseErrors)
	addRows("mib_pack_matched_but_no_tables_supported", "MIB pack matched, no tables supported", "MIB packs that matched a device but none of the mapped tables responded on that firmware. Re-map to tables the device exposes.", "warning", noTablesSupported)
	addDev("wireless_controller_missing_mib_pack", "Wireless controller: no MIB pack", "Wireless controllers with no applicable MIB pack. Upload/enable a pack whose applies-to matches this vendor/sysObjectID.", "warning", missingPack)
	addDev("user_defined_mib_not_applied", "User MIB pack not applied", "A user-defined MIB pack is configured but these controllers have no MIB-sourced collection yet. Run SNMP Wireless Collection.", "info", userNotApplied)
	addDev("wireless_snmp_ap_table_empty", "SNMP MIB: AP table empty", "Controllers collected via SNMP MIB whose AP table returned no rows (firmware does not expose apTable). Use the API profile or another mapped table for AP data.", "info", apEmpty)
	addDev("wireless_snmp_mib_partial", "SNMP MIB: partial collection", "Controllers where SNMP MIB collection ran but some rosters (APs/clients) were empty — partial, honest coverage.", "info", partial)
}

// addSSHWirelessDQ appends Extreme XCC SSH-CLI collection data-quality issues.
func (s *Server) addSSHWirelessDQ(ctx context.Context, devs []db.Device, issues *[]dqIssue) {
	sshCredExists := false
	if creds, e := s.queries.ListCredentials(ctx); e == nil {
		for _, c := range creds {
			if c.Kind == string(domain.CredSSH) {
				sshCredExists = true
				break
			}
		}
	}
	var availNotUsed, failed, partial, noSupp, noRoster []db.Device
	for _, d := range devs {
		if d.Category != string(domain.CatWirelessController) {
			continue
		}
		results, _ := s.queries.ListSSHCliResults(ctx, d.ID)
		ran := false
		supp, unsupp, anyFail := 0, 0, false
		for _, r := range results {
			if r.Source != sshCLISource {
				continue
			}
			ran = true
			switch r.Status {
			case "parsed", "not_parsed":
				supp++
			case "unsupported":
				unsupp++
			case "failed", "timeout":
				anyFail = true
			}
		}
		// Roster presence by source.
		sshAP, sshCli, totalAP, totalCli := 0, 0, 0, 0
		if aps, e := s.queries.ListAccessPoints(ctx, d.ID); e == nil {
			totalAP = len(aps)
			for _, a := range aps {
				if a.Source == sshCLISource {
					sshAP++
				}
			}
		}
		if cls, e := s.queries.ListWirelessClients(ctx, d.ID); e == nil {
			totalCli = len(cls)
			for _, c := range cls {
				if c.Source == sshCLISource {
					sshCli++
				}
			}
		}
		switch {
		case sshCredExists && !ran:
			availNotUsed = append(availNotUsed, d)
		case ran && supp == 0 && unsupp > 0:
			noSupp = append(noSupp, d)
		case ran && anyFail && supp == 0 && unsupp == 0:
			failed = append(failed, d)
		case ran && supp > 0 && sshAP == 0 && sshCli == 0:
			partial = append(partial, d)
		}
		if totalAP == 0 && totalCli == 0 {
			noRoster = append(noRoster, d)
		}
	}
	add := func(key, label, desc, sev string, list []db.Device) {
		if len(list) == 0 {
			return
		}
		*issues = append(*issues, dqIssue{Key: key, Label: label, Description: desc, Severity: sev, Count: len(list), Devices: sampleDevices(list)})
	}
	add("wireless_ssh_available_not_used", "Wireless: SSH available, not used", "Wireless controllers with SSH credentials on file but no SSH CLI collection has run. Run SSH CLI collection — it may expose AP/client rosters SNMP does not.", "info", availNotUsed)
	add("wireless_ssh_no_supported_commands", "Wireless: SSH CLI — no supported commands", "SSH connected but the controller's restricted CLI rejected every probed read-only command. Identity stays via SNMP.", "info", noSupp)
	add("wireless_ssh_collection_failed", "Wireless: SSH CLI collection failed", "SSH CLI commands failed/timed out on these controllers. Check the SSH credential and reachability.", "warning", failed)
	add("wireless_ssh_collection_partial", "Wireless: SSH CLI partial", "SSH CLI ran with supported commands but exposed no AP/client roster via the supported commands.", "info", partial)
	add("wireless_no_roster_from_snmp_or_ssh", "Wireless: no roster from SNMP or SSH", "Wireless controllers with no AP/SSID/client roster from any source (SNMP MIB, SSH CLI, or API). Configure the Extreme XCC API profile or map a CLI/MIB roster.", "warning", noRoster)
}

// addWirelessSummaryDQ appends controller-summary vs parsed-roster mismatch +
// roster-source + staleness data-quality issues.
func (s *Server) addWirelessSummaryDQ(ctx context.Context, devs []db.Device, issues *[]dqIssue) {
	stale := time.Now().Add(-7 * 24 * time.Hour)
	var mismatch, apLess, clientLess, partial, hasRoster, snmpNoSshYes, sshStale, apStale, cliStale, apNameMissing []db.Device
	for _, d := range devs {
		if d.Category != string(domain.CatWirelessController) {
			continue
		}
		sshAP, mibAP, nameMissing := 0, 0, 0
		var apNewest time.Time
		if aps, e := s.queries.ListAccessPoints(ctx, d.ID); e == nil {
			for _, a := range aps {
				switch a.Source {
				case sshCLISource:
					sshAP++
					if a.Name == a.Serial && a.Serial != "" {
						nameMissing++
					}
				case mibSource:
					mibAP++
				}
				if a.Source == sshCLISource && a.CollectedAt.After(apNewest) {
					apNewest = a.CollectedAt
				}
			}
		}
		sshCli, mibCli := 0, 0
		var cliNewest time.Time
		if cls, e := s.queries.ListWirelessClients(ctx, d.ID); e == nil {
			for _, c := range cls {
				switch c.Source {
				case sshCLISource:
					sshCli++
				case mibSource:
					mibCli++
				}
				if c.Source == sshCLISource && c.CollectedAt.After(cliNewest) {
					cliNewest = c.CollectedAt
				}
			}
		}
		if sshAP > 0 || sshCli > 0 {
			hasRoster = append(hasRoster, d)
		}
		if (mibAP == 0 && mibCli == 0) && (sshAP > 0 || sshCli > 0) {
			snmpNoSshYes = append(snmpNoSshYes, d)
		}
		if nameMissing > 0 {
			apNameMissing = append(apNameMissing, d)
		}
		if !apNewest.IsZero() && apNewest.Before(stale) {
			apStale = append(apStale, d)
		}
		if !cliNewest.IsZero() && cliNewest.Before(stale) {
			cliStale = append(cliStale, d)
		}
		if cs, err := s.queries.GetWirelessControllerSummary(ctx, d.ID); err == nil {
			if cs.CollectionStatus == "partial" || cs.CollectionStatus == "summary_only" {
				partial = append(partial, d)
			}
			if cs.ParsedApRows < cs.ApTotal {
				apLess = append(apLess, d)
			}
			if cs.ParsedClientRows < cs.ClientsTotal {
				clientLess = append(clientLess, d)
			}
			if cs.ParsedApRows < cs.ApTotal || cs.ParsedClientRows < cs.ClientsTotal {
				mismatch = append(mismatch, d)
			}
			if cs.CollectedAt.Before(stale) {
				sshStale = append(sshStale, d)
			}
		}
	}
	add := func(key, label, desc, sev string, list []db.Device) {
		if len(list) == 0 {
			return
		}
		*issues = append(*issues, dqIssue{Key: key, Label: label, Description: desc, Severity: sev, Count: len(list), Devices: sampleDevices(list)})
	}
	add("wireless_controller_has_roster_via_ssh", "Wireless: roster collected via SSH CLI", "Controllers whose AP/client roster was collected over the SSH CLI (the path that works when SNMP does not expose it).", "info", hasRoster)
	add("wireless_snmp_no_roster_but_ssh_roster_available", "Wireless: SNMP empty, SSH has roster", "Controllers where the SNMP MIB exposes no AP/client roster but the SSH CLI does — prefer SSH for these.", "info", snmpNoSshYes)
	add("wireless_controller_summary_mismatch", "Wireless: summary vs parsed mismatch", "Controllers where the controller-reported AP/client totals exceed the parsed roster rows — collection is partial.", "warning", mismatch)
	add("wireless_ap_rows_less_than_controller_summary", "Wireless: fewer AP rows than reported", "Parsed AP rows are fewer than the controller-reported AP total. Review command coverage / parser.", "warning", apLess)
	add("wireless_client_rows_less_than_controller_summary", "Wireless: fewer client rows than reported", "Parsed client rows are fewer than the controller-reported client total.", "warning", clientLess)
	add("wireless_collection_partial", "Wireless: collection partial", "Controllers whose latest collection is partial or summary-only (not a complete roster).", "info", partial)
	add("wireless_ap_name_missing", "Wireless: AP display name derived from serial", "APs where no friendly name was exposed by the CLI; display name falls back to the serial.", "info", apNameMissing)
	add("wireless_ssh_collection_stale", "Wireless: SSH collection stale", "SSH CLI collection older than 7 days. Re-run collection.", "info", sshStale)
	add("wireless_ap_inventory_stale", "Wireless: AP inventory stale", "SSH-sourced AP rows older than 7 days.", "info", apStale)
	add("wireless_clients_stale", "Wireless: client roster stale", "SSH-sourced client rows older than 7 days (clients are volatile; expect frequent change).", "info", cliStale)
}

func sampleDevices(list []db.Device) []dqDevice {
	out := make([]dqDevice, 0, len(list))
	for i, d := range list {
		if i >= dqSampleCap {
			break
		}
		out = append(out, toDQ(d))
	}
	return out
}

// genericVendorLabels are the bare single-word vendor names the low-confidence
// fingerprint fallbacks produce (e.g. "Extreme", not "Extreme Networks"). A device
// whose vendor is exactly one of these was matched only generically — a specific
// product fingerprint (or re-scan) would give a fuller vendor + correct category.
var genericVendorLabels = map[string]bool{
	"extreme": true, "cisco": true, "aruba": true, "aruba/hpe": true, "huawei": true,
	"hp": true, "hpe": true, "dell": true, "vmware": true, "axis": true, "apc": true,
	"juniper": true, "arista": true, "netgear": true, "brocade": true, "ruckus": true,
	"qnap": true, "zebra": true, "xerox": true, "epson": true, "eaton": true,
	"dahua": true, "hikvision": true, "ubiquiti": true, "mikrotik": true,
	"generic": true, "embedded": true, "tp-link": true,
}

// addFingerprintDQ appends the four FP-ext classification-quality issues, derived
// from each device's STORED raw SNMP identity facts re-matched against the library.
func (s *Server) addFingerprintDQ(ctx context.Context, devs []db.Device, issues *[]dqIssue) {
	// Bulk-load the raw SNMP identity facts (one query, all devices).
	type snmpID struct{ oid, descr, name string }
	byDev := map[uuid.UUID]*snmpID{}
	if rows, err := s.queries.ListSNMPIdentityFacts(ctx); err == nil {
		for _, f := range rows {
			e := byDev[f.DeviceID]
			if e == nil {
				e = &snmpID{}
				byDev[f.DeviceID] = e
			}
			v := ""
			if f.Value != nil {
				v = *f.Value
			}
			switch f.Key {
			case "snmp.sysobjectid":
				e.oid = v
			case "snmp.sysdescr":
				e.descr = v
			case "snmp.sysname":
				e.name = v
			}
		}
	}

	// Operator (source='user') ENABLED fingerprints only — the "custom_fingerprint_
	// not_applied" check is specifically about rules an operator added themselves.
	var userLib []fingerprint.Print
	if rows, err := s.queries.ListVendorFingerprints(ctx); err == nil {
		for _, r := range rows {
			if r.Enabled && r.Source == "user" {
				userLib = append(userLib, fingerprint.Print{
					Kind: r.Kind, Pattern: r.Pattern, Vendor: r.Vendor,
					DeviceType: r.DeviceType, Confidence: int(r.Confidence),
				})
			}
		}
	}

	var genericVendor, weakSNMP, vendorNoCat, fpNotApplied []db.Device
	for _, d := range devs {
		vendor := ""
		if d.Vendor != nil {
			vendor = strings.TrimSpace(*d.Vendor)
		}
		id := byDev[d.ID]
		answeredSNMP := id != nil && (id.oid != "" || id.descr != "")

		if vendor != "" && genericVendorLabels[strings.ToLower(vendor)] {
			genericVendor = append(genericVendor, d)
		}
		if answeredSNMP && (d.Category == "unknown" ||
			(d.ConfidenceScore != nil && *d.ConfidenceScore > 0 && *d.ConfidenceScore < lowConfidenceThreshold)) {
			weakSNMP = append(weakSNMP, d)
		}
		if vendor != "" && d.Category == "unknown" {
			vendorNoCat = append(vendorNoCat, d)
		}
		// A user fingerprint that WOULD match this device's stored evidence but
		// whose verdict the device doesn't reflect → operator added a rule but
		// hasn't re-scanned/reclassified. The actionable, operator-owned case.
		if id != nil && len(userLib) > 0 {
			ev := fingerprint.Evidence{SysObjectID: id.oid, SysDescr: id.descr, SysName: id.name}
			if res := fingerprint.Match(ev, userLib); len(res) > 0 {
				top := res[0]
				cat := fingerprint.CanonicalCategory(top.DeviceType)
				catDiff := cat != "" && cat != d.Category
				venDiff := top.Vendor != "" && !strings.EqualFold(top.Vendor, vendor)
				if catDiff || venDiff {
					fpNotApplied = append(fpNotApplied, d)
				}
			}
		}
	}

	add := func(key, label, desc, sev string, list []db.Device) {
		if len(list) == 0 {
			return
		}
		*issues = append(*issues, dqIssue{Key: key, Label: label, Description: desc, Severity: sev, Count: len(list), Devices: sampleDevices(list)})
	}
	add("generic_vendor_classification", "Generic vendor classification",
		"Devices matched only to a bare vendor name (e.g. \"Extreme\" rather than \"Extreme Networks\"). Add a product fingerprint or re-scan so the specific model and category are captured.",
		"info", genericVendor)
	add("weak_snmp_classification", "Weak SNMP classification",
		"Devices that answered SNMP (sysObjectID/sysDescr are stored) but are still 'unknown' or classified below "+strconv.Itoa(lowConfidenceThreshold)+"% confidence. A vendor fingerprint for their sysObjectID would classify them.",
		"warning", weakSNMP)
	add("vendor_known_but_category_generic", "Vendor known, category unknown",
		"Devices where the vendor is known but the category is still 'unknown'. Add a fingerprint mapping this vendor's product OID to the right device type.",
		"warning", vendorNoCat)
	add("custom_fingerprint_not_applied", "Custom fingerprint not applied",
		"One of your own vendor fingerprints matches these devices' stored SNMP evidence, but their current classification doesn't reflect it. Re-scan or Re-classify them so the rule takes effect.",
		"warning", fpNotApplied)
}
