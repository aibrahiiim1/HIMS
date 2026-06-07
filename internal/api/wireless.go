package api

import (
	"net/http"
	"strings"

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

type wirelessDTO struct {
	Identity   wirelessIdentity       `json:"identity"`
	Collection wirelessCollection     `json:"collection"`
	Counts     map[string]int         `json:"counts"`
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

	ident := wirelessIdentity{
		Name:        dev.Name,
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
