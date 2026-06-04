package api

import (
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/coralsearesorts/hims/internal/storage/postgres/db"
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
	addIssue("stale_devices", "Not seen recently", "These devices have not been re-discovered in over 30 days (or never). They may be decommissioned, moved, or unreachable.", "warning", stale)
	addIssue("missing_location", "Missing location", "Devices not assigned to a site/location. Assign them so they roll up correctly in Sites Health and reports.", "warning", missingLoc)
	addIssue("missing_credentials", "Missing credentials", "Credentialed device classes (switches, firewalls, servers…) with no bound credential cannot be deeply collected. Bind a working credential.", "warning", missingCreds)
	addIssue("unknown_category", "Unclassified devices", "Devices still classified as 'unknown'. Enrich with SNMP/CLI or set the type manually so they appear in the right inventory views.", "info", unknownCat)
	addIssue("low_confidence", "Low-confidence classification", "Devices auto-classified below "+strconv.Itoa(lowConfidenceThreshold)+"% confidence. Open the device, Re-classify (or bind a credential for deeper signals), then Lock once correct.", "info", lowConf)
	addIssue("missing_vendor", "Missing vendor", "Devices with no vendor recorded. Vendor enriches fingerprinting, reports and lifecycle.", "info", missingVendor)

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
