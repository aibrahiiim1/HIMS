package api

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/coralsearesorts/hims/internal/mibs"
	"github.com/coralsearesorts/hims/internal/snmp"
	"github.com/coralsearesorts/hims/internal/storage/postgres/db"
	"github.com/google/uuid"
)

// snmpIfResult summarises an IF-MIB interface-identity pass.
type snmpIfResult struct {
	IfCount    int    `json:"interfaces"`
	PrimaryMAC string `json:"primary_mac,omitempty"`
	OUI        string `json:"oui,omitempty"`
	OUIVendor  string `json:"oui_vendor,omitempty"`
	Reason     string `json:"reason,omitempty"` // set when no usable MAC (ifPhysAddress_unavailable)
}

// i16ptr returns a pointer to n (api has i32ptr already, not i16ptr).
func i16ptr(n int16) *int16 { return &n }

// macOUI returns the 24-bit OUI (first three octets, upper-case) of a colon MAC.
func macOUI(mac string) string {
	parts := strings.Split(mac, ":")
	if len(parts) < 3 {
		return ""
	}
	return strings.ToUpper(parts[0] + ":" + parts[1] + ":" + parts[2])
}

// ouiVendor maps a few well-known OUI prefixes to a vendor. A full IEEE OUI registry
// is intentionally NOT embedded; the OUI itself is recorded as evidence regardless, and
// the authoritative vendor for an SNMP device comes from its enterprise PEN. "" = unknown.
func ouiVendor(oui string) string {
	switch oui {
	case "00:01:02", "00:04:76", "00:0A:5E", "00:50:DA", "08:00:8F": // legacy 3Com ranges
		return "3Com/HPE"
	}
	return ""
}

// isZeroMAC reports an all-zero or empty MAC (no usable physical address).
func isZeroMAC(mac string) bool {
	return mac == "" || mac == "00:00:00:00:00:00"
}

// collectSNMPInterfaces runs an IF-MIB identity pass against an SNMP-authenticated device:
// ifXTable (ifName/ifAlias/ifHighSpeed) + ifTable (ifDescr/ifType/ifPhysAddress/admin/oper).
// It upserts every interface (with its MAC), selects a primary MAC, and derives the OUI +
// (where known) vendor. If no interface exposes a usable MAC it records
// ifPhysAddress_unavailable with the reason — never left silently blank. SNMP must already
// authenticate (community bound or supplied); this NEVER sprays credentials.
func (s *Server) collectSNMPInterfaces(ctx context.Context, dev db.Device, community string, timeout time.Duration) (snmpIfResult, error) {
	var res snmpIfResult
	c, err := s.snmpClientForDevice(ctx, dev, community, timeout)
	if err != nil {
		return res, err
	}
	if err := c.Connect(ctx); err != nil {
		return res, err
	}
	defer c.Close()

	type ifrow struct {
		name, descr, alias, mac    string
		ifType, admin, oper, speed int
	}
	rows := map[int32]*ifrow{}
	get := func(i int32) *ifrow {
		r := rows[i]
		if r == nil {
			r = &ifrow{}
			rows[i] = r
		}
		return r
	}

	// ifXTable (RFC 2863) — names/alias/high-speed; best-effort (older agents omit it).
	_ = c.BulkWalk(ctx, mibs.IfXEntry1, func(p snmp.PDU) error {
		col, idx, ok := snmp.ColumnAndIndex(p.OID, mibs.IfXEntry1)
		if !ok || len(idx) != 1 {
			return nil
		}
		r := get(int32(idx[0]))
		switch int(col) {
		case mibs.IfXColName:
			r.name = snmp.PDUString(p)
		case mibs.IfXColAlias:
			r.alias = snmp.PDUString(p)
		case mibs.IfXColHighSpeed:
			if v, ok := snmp.PDUInt64(p); ok {
				r.speed = int(v)
			}
		}
		return nil
	})

	// ifTable (RFC 1213) — descr/type/MAC/status. This is the authoritative MAC source.
	if err := c.BulkWalk(ctx, mibs.IfEntry, func(p snmp.PDU) error {
		col, idx, ok := snmp.ColumnAndIndex(p.OID, mibs.IfEntry)
		if !ok || len(idx) != 1 {
			return nil
		}
		r := get(int32(idx[0]))
		switch int(col) {
		case mibs.IfDescr:
			r.descr = snmp.PDUString(p)
		case mibs.IfType:
			if v, ok := snmp.PDUInt64(p); ok {
				r.ifType = int(v)
			}
		case mibs.IfPhysAddress:
			if m := snmp.PDUMACAddress(p); m != "" {
				r.mac = m
			}
		case mibs.IfAdminStatus:
			if v, ok := snmp.PDUInt64(p); ok {
				r.admin = int(v)
			}
		case mibs.IfOperStatus:
			if v, ok := snmp.PDUInt64(p); ok {
				r.oper = int(v)
			}
		}
		return nil
	}); err != nil {
		return res, fmt.Errorf("ifTable walk: %w", err)
	}

	poll := time.Now()
	idxs := make([]int32, 0, len(rows))
	for i := range rows {
		idxs = append(idxs, i)
	}
	sort.Slice(idxs, func(a, b int) bool { return idxs[a] < idxs[b] })

	for _, i := range idxs {
		r := rows[i]
		_, _ = s.queries.UpsertInterface(ctx, db.UpsertInterfaceParams{
			DeviceID: dev.ID, IfIndex: i, IfName: nonEmptyStr(r.name), IfDescr: nonEmptyStr(r.descr),
			IfAlias: nonEmptyStr(r.alias), IfType: i32ptr(int32(r.ifType)), Mac: nonEmptyStr(strings.ToLower(r.mac)),
			SpeedMbps: i32ptr(int32(r.speed)), AdminStatus: i16ptr(int16(r.admin)), OperStatus: i16ptr(int16(r.oper)),
			PortRole: "unknown", CollectionSource: "snmp", LastSeenAt: poll,
		})
	}
	res.IfCount = len(rows)

	// Primary MAC: prefer an ethernet (ifType=6 ethernetCsmacd) interface with a real
	// (non-zero) MAC and the lowest ifIndex; else any interface with a usable MAC.
	for _, i := range idxs {
		if r := rows[i]; r.ifType == 6 && !isZeroMAC(r.mac) {
			res.PrimaryMAC = strings.ToLower(r.mac)
			break
		}
	}
	if res.PrimaryMAC == "" {
		for _, i := range idxs {
			if r := rows[i]; !isZeroMAC(r.mac) {
				res.PrimaryMAC = strings.ToLower(r.mac)
				break
			}
		}
	}

	if res.PrimaryMAC == "" {
		res.Reason = fmt.Sprintf("ifPhysAddress_unavailable: device answered SNMP but no interface exposed a usable (non-zero) ifPhysAddress (%d interfaces seen)", len(rows))
		s.upsertFact(ctx, dev.ID, "snmp.ifphysaddress_status", res.Reason)
		return res, nil
	}
	res.OUI = macOUI(res.PrimaryMAC)
	res.OUIVendor = ouiVendor(res.OUI)
	s.upsertFact(ctx, dev.ID, "snmp.primary_mac", res.PrimaryMAC)
	s.upsertFact(ctx, dev.ID, "snmp.mac_oui", res.OUI)
	if res.OUIVendor != "" {
		s.upsertFact(ctx, dev.ID, "snmp.mac_oui_vendor", res.OUIVendor)
	}
	return res, nil
}

func (s *Server) upsertFact(ctx context.Context, devID uuid.UUID, key, val string) {
	v := val
	_ = s.queries.UpsertDeviceFact(ctx, db.UpsertDeviceFactParams{DeviceID: devID, Key: key, Value: &v, Driver: "snmp"})
}

// collectSNMPInterfacesHandler is POST /devices/{id}/collect-snmp-interfaces — an explicit
// IF-MIB interface MAC/OUI pass for an SNMP-managed device (uses the bound community; never
// sprays). Returns the captured primary MAC/OUI, or ifPhysAddress_unavailable with a reason.
func (s *Server) collectSNMPInterfacesHandler(w http.ResponseWriter, r *http.Request) {
	ctx, id, ok := pathUUID(w, r, "id")
	if !ok {
		return
	}
	dev, err := s.queries.GetDevice(ctx, id)
	if err != nil {
		writeErr(w, err)
		return
	}
	res, err := s.collectSNMPInterfaces(ctx, dev, "", 8*time.Second)
	if err != nil {
		http.Error(w, "SNMP interface pass failed: "+err.Error(), http.StatusBadGateway)
		return
	}
	writeJSON(w, http.StatusOK, res)
}
