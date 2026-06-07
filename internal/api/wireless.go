package api

import (
	"net/http"
	"strings"
	"time"

	"github.com/coralsearesorts/hims/internal/storage/postgres/db"
)

// Wireless Controller detail — a single consolidated read that assembles the
// controller identity (from the device row + stored SNMP identity facts, always
// available once SNMP answered), the collection status (which source last ran,
// whether an Extreme XCC API profile is configured), and the AP / SSID / client
// / radio / event rosters (populated by a real collector). The page is honest:
// SNMP gives identity + "managed via SNMP"; the AP/SSID/client roster requires
// the controller API (extreme_xcc profile). Nothing here is fabricated.

type wirelessIdentity struct {
	Name        string   `json:"name"`
	IP          string   `json:"ip"`
	Vendor      string   `json:"vendor"`
	Product     string   `json:"product"` // e.g. "ExtremeCloud IQ Controller" (from sysDescr)
	Model       string   `json:"model"`
	Serial      string   `json:"serial"`
	Firmware    string   `json:"firmware"`
	Category    string   `json:"category"`
	Status      string   `json:"status"`
	SysObjectID string   `json:"sysobjectid"`
	SysDescr    string   `json:"sysdescr"`
	SysName     string   `json:"sysname"`
	ManagedVia  []string `json:"managed_via"`
}

type wirelessCollection struct {
	Source        string  `json:"source"`         // extreme_xcc_api | snmp_baseline | cloud_xiq | ""
	CollectedAt   *string `json:"collected_at"`   // RFC3339, nil when never collected
	HasAPIProfile bool    `json:"has_api_profile"`
	ProfileID     string  `json:"profile_id,omitempty"`
	ProfileStatus string  `json:"profile_status,omitempty"`
	LastDetail    string  `json:"last_detail,omitempty"`
	APDataKnown   bool    `json:"ap_data_known"` // true once a roster-capable source populated APs
	NextAction    string  `json:"next_action"`
}

type wirelessMibStatus struct {
	HasPack     bool             `json:"has_pack"`
	PackName    string           `json:"pack_name,omitempty"`
	PackSource  string           `json:"pack_source,omitempty"`
	PackID      string           `json:"pack_id,omitempty"`
	WalkedTables []wirelessMibTable `json:"walked_tables"`
}

type wirelessMibTable struct {
	Table string `json:"table"`
	Rows  int    `json:"rows"`
}

// wirelessSSHStatus summarizes the Extreme XCC SSH CLI collection source.
type wirelessSSHStatus struct {
	Status      string   `json:"status"` // not_run | collected | partial | unsupported | failed
	Supported   []string `json:"supported"`
	Unsupported []string `json:"unsupported"`
	ParsedRows  int      `json:"parsed_rows"`
	APs         int      `json:"aps"`
	Clients     int      `json:"clients"`
	LastRun     *string  `json:"last_run"`
}

// wirelessControllerSummary mirrors the controller-reported counts, kept SEPARATE
// from parsed roster rows so the UI never presents partial data as complete.
type wirelessControllerSummary struct {
	Has              bool    `json:"has"`
	Source           string  `json:"source"`
	CollectionStatus string  `json:"collection_status"` // complete|partial|summary_only|failed
	Networks         int     `json:"networks"`
	Switches         int     `json:"switches"`
	APTotal          int     `json:"ap_total"`
	AdoptionPrimary  int     `json:"adoption_primary"`
	AdoptionBackup   int     `json:"adoption_backup"`
	ActiveAPs        int     `json:"active_aps"`
	NonActiveAPs     int     `json:"non_active_aps"`
	ClientsTotal     int     `json:"clients_total"`
	ParsedAPRows     int     `json:"parsed_ap_rows"`
	ParsedClientRows int     `json:"parsed_client_rows"`
	ParsedSSIDRows   int     `json:"parsed_ssid_rows"`
	// Honest "what the CLI exposed" flags so the UI shows "—" not "0".
	APStatusExposed bool   `json:"ap_status_exposed"` // active/non-active split available
	Detail          string `json:"detail"`
	CollectedAt     *string `json:"collected_at"`
}

type wirelessDTO struct {
	Identity   wirelessIdentity       `json:"identity"`
	Collection wirelessCollection     `json:"collection"`
	Counts     map[string]int         `json:"counts"`
	MIB        wirelessMibStatus      `json:"mib"`
	SSH        wirelessSSHStatus      `json:"ssh"`
	Summary    wirelessControllerSummary `json:"summary"`
	APs        []db.AccessPoint       `json:"aps"`
	SSIDs      []db.WirelessSsid      `json:"ssids"`
	Clients    []db.WirelessClient    `json:"clients"`
	Radios     []db.WirelessRadioStatus `json:"radios"`
	Events     []db.WirelessEvent     `json:"events"`
}

// deviceWireless serves GET /devices/{id}/wireless.
func (s *Server) deviceWireless(w http.ResponseWriter, r *http.Request) {
	ctx, id, ok := pathDevice(w, r)
	if !ok {
		return
	}
	dev, err := s.queries.GetDevice(ctx, id)
	if err != nil {
		writeErr(w, err)
		return
	}

	facts, _ := s.queries.ListDeviceFacts(ctx, id)
	fact := func(key string) string {
		for _, f := range facts {
			if f.Key == key && f.Value != nil {
				return *f.Value
			}
		}
		return ""
	}
	sysDescr := fact("snmp.sysdescr")
	if sysDescr == "" && dev.OsVersion != nil {
		// os_version may carry the sysDescr for SNMP-only devices.
		sysDescr = ""
	}

	ip := ""
	if dev.PrimaryIp != nil && dev.PrimaryIp.IsValid() {
		ip = dev.PrimaryIp.String()
	}
	ident := wirelessIdentity{
		Name:        dev.Name,
		IP:          ip,
		Vendor:      derefStr(dev.Vendor),
		Product:     productFromSysDescr(fact("snmp.sysdescr"), derefStr(dev.Vendor)),
		Model:       derefStr(dev.Model),
		Serial:      derefStr(dev.Serial),
		Firmware:    derefStr(dev.OsVersion),
		Category:    dev.Category,
		Status:      dev.Status,
		SysObjectID: fact("snmp.sysobjectid"),
		SysDescr:    fact("snmp.sysdescr"),
		SysName:     fact("snmp.sysname"),
	}
	// Managed-via is honest: SNMP identity present ⇒ managed via SNMP. A bound
	// credential of a wireless-API kind would add "api" once collection runs.
	if ident.SysDescr != "" || ident.SysObjectID != "" {
		ident.ManagedVia = append(ident.ManagedVia, "snmp")
	}

	dto := wirelessDTO{Identity: ident, APs: []db.AccessPoint{}, SSIDs: []db.WirelessSsid{}, Clients: []db.WirelessClient{}, Radios: []db.WirelessRadioStatus{}, Events: []db.WirelessEvent{}}

	// Collection status from the controller-info row (if any source has run).
	if info, e := s.queries.GetWLANControllerInfo(ctx, id); e == nil {
		dto.Collection.Source = info.Source
		ts := info.CollectedAt.UTC().Format("2006-01-02T15:04:05Z07:00")
		dto.Collection.CollectedAt = &ts
		if info.ProfileID != nil {
			dto.Collection.ProfileID = info.ProfileID.String()
		}
	}
	if dto.Collection.Source == "" {
		// No collection row yet — identity comes from SNMP baseline.
		dto.Collection.Source = "snmp_baseline"
	}

	// Is an Extreme XCC API profile configured for this controller?
	if profs, e := s.queries.ListVendorProfiles(ctx); e == nil {
		for _, p := range profs {
			if p.VendorType == "extreme_xcc" && p.DeviceID != nil && *p.DeviceID == id {
				dto.Collection.HasAPIProfile = true
				dto.Collection.ProfileID = p.ID.String()
				dto.Collection.ProfileStatus = p.Status
				dto.Collection.LastDetail = p.LastCollectionDetail
				break
			}
		}
	}

	// Rosters.
	if rows, e := s.queries.ListAccessPoints(ctx, id); e == nil {
		dto.APs = rows
	}
	if rows, e := s.queries.ListWirelessSSIDs(ctx, id); e == nil {
		dto.SSIDs = rows
	}
	if rows, e := s.queries.ListWirelessClients(ctx, id); e == nil {
		dto.Clients = rows
	}
	if rows, e := s.queries.ListWirelessRadios(ctx, id); e == nil {
		dto.Radios = rows
	}
	if rows, e := s.queries.ListWirelessEvents(ctx, db.ListWirelessEventsParams{ControllerDeviceID: id, Limit: 50}); e == nil {
		dto.Events = rows
	}

	online := 0
	for _, a := range dto.APs {
		if a.Status == "online" {
			online++
		}
	}
	dto.Collection.APDataKnown = len(dto.APs) > 0
	dto.Counts = map[string]int{
		"aps":         len(dto.APs),
		"aps_online":  online,
		"aps_offline": len(dto.APs) - online,
		"ssids":       len(dto.SSIDs),
		"clients":     len(dto.Clients),
		"events":      len(dto.Events),
	}

	// MIB pack status: which pack applies (if any), and which tables actually
	// returned rows on the last SNMP walk (distinct table_name in mib_walk_rows).
	if pack, ok := s.matchMibPack(ctx, dev); ok {
		dto.MIB.HasPack = true
		dto.MIB.PackName = pack.Name
		dto.MIB.PackSource = pack.Source
		dto.MIB.PackID = pack.ID.String()
	}
	dto.MIB.WalkedTables = []wirelessMibTable{}
	if rows, e := s.queries.ListMibWalkRows(ctx, db.ListMibWalkRowsParams{DeviceID: id, Limit: 5000}); e == nil {
		byTable := map[string]int{}
		var order []string
		for _, rw := range rows {
			if _, seen := byTable[rw.TableName]; !seen {
				order = append(order, rw.TableName)
			}
			byTable[rw.TableName]++
		}
		for _, t := range order {
			dto.MIB.WalkedTables = append(dto.MIB.WalkedTables, wirelessMibTable{Table: t, Rows: byTable[t]})
		}
	}

	// SSH CLI status: per-command support + what mapped, derived from stored results.
	dto.SSH = wirelessSSHStatus{Status: "not_run", Supported: []string{}, Unsupported: []string{}}
	if rows, e := s.queries.ListSSHCliResults(ctx, id); e == nil && len(rows) > 0 {
		var last time.Time
		anyRan, anyFail := false, false
		for _, rw := range rows {
			if rw.Source != sshCLISource {
				continue
			}
			if rw.CollectedAt.After(last) {
				last = rw.CollectedAt
			}
			switch rw.Status {
			case "parsed", "not_parsed":
				dto.SSH.Supported = append(dto.SSH.Supported, rw.Command)
				dto.SSH.ParsedRows += int(rw.ParsedRows)
				anyRan = true
			case "unsupported":
				dto.SSH.Unsupported = append(dto.SSH.Unsupported, rw.Command)
			case "failed", "timeout":
				anyFail = true
			}
		}
		for _, a := range dto.APs {
			if a.Source == sshCLISource {
				dto.SSH.APs++
			}
		}
		for _, c := range dto.Clients {
			if c.Source == sshCLISource {
				dto.SSH.Clients++
			}
		}
		switch {
		case dto.SSH.APs > 0 || dto.SSH.Clients > 0:
			dto.SSH.Status = "collected"
		case anyRan:
			dto.SSH.Status = "partial"
		case len(dto.SSH.Unsupported) > 0:
			dto.SSH.Status = "unsupported"
		case anyFail:
			dto.SSH.Status = "failed"
		}
		if !last.IsZero() {
			ts := last.UTC().Format("2006-01-02T15:04:05Z07:00")
			dto.SSH.LastRun = &ts
		}
	}

	// Controller-reported summary (kept separate from parsed rows).
	if cs, e := s.queries.GetWirelessControllerSummary(ctx, id); e == nil {
		ts := cs.CollectedAt.UTC().Format("2006-01-02T15:04:05Z07:00")
		dto.Summary = wirelessControllerSummary{
			Has: true, Source: cs.SummarySource, CollectionStatus: cs.CollectionStatus,
			Networks: int(cs.NetworksCount), Switches: int(cs.SwitchesCount), APTotal: int(cs.ApTotal),
			AdoptionPrimary: int(cs.AdoptionPrimary), AdoptionBackup: int(cs.AdoptionBackup),
			ActiveAPs: int(cs.ActiveAps), NonActiveAPs: int(cs.NonActiveAps), ClientsTotal: int(cs.ClientsTotal),
			ParsedAPRows: int(cs.ParsedApRows), ParsedClientRows: int(cs.ParsedClientRows), ParsedSSIDRows: int(cs.ParsedSsidRows),
			APStatusExposed: cs.ActiveAps > 0 || cs.NonActiveAps > 0,
			Detail:          cs.Detail, CollectedAt: &ts,
		}
	}

	// Next action: honest guidance when the rich roster isn't collected.
	switch {
	case dto.Collection.HasAPIProfile && !dto.Collection.APDataKnown:
		dto.Collection.NextAction = "Extreme XCC profile configured — Run Collection (or Test Connection) to pull AP/SSID/client data."
	case !dto.Collection.HasAPIProfile:
		dto.Collection.NextAction = "Configure Extreme XCC profile to collect AP/SSID/client data."
	default:
		dto.Collection.NextAction = ""
	}

	writeJSON(w, http.StatusOK, dto)
}

// productFromSysDescr extracts the product name from a vendor sysDescr of the
// shape "Vendor Product - Model, version…" (e.g. Extreme:
// "Extreme Networks ExtremeCloud IQ Controller - VE6120 Medium, …" →
// "ExtremeCloud IQ Controller"). Falls back to "" when not derivable.
func productFromSysDescr(sysDescr, vendor string) string {
	d := strings.TrimSpace(sysDescr)
	if d == "" {
		return ""
	}
	if i := strings.Index(d, " - "); i >= 0 {
		d = strings.TrimSpace(d[:i])
	}
	// Strip a leading vendor prefix ("Extreme Networks ") if present.
	if vendor != "" && strings.HasPrefix(strings.ToLower(d), strings.ToLower(vendor)+" ") {
		d = strings.TrimSpace(d[len(vendor):])
	}
	if len(d) > 80 {
		return ""
	}
	return d
}
