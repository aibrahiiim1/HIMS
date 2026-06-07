package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/coralsearesorts/hims/internal/mibpack"
	"github.com/coralsearesorts/hims/internal/snmp"
	"github.com/coralsearesorts/hims/internal/storage/postgres/db"
)

var (
	errBadID    = errors.New("invalid device_id")
	errBadIP    = errors.New("invalid ip")
	errNoTarget = errors.New("device_id or ip is required")
)

// tableResult is the honest per-table outcome surfaced to the operator.
type tableResult struct {
	Table   string            `json:"table"`
	RootOID string            `json:"root_oid"`
	Purpose string            `json:"purpose"`
	Status  string            `json:"status"` // supported|empty|timeout|no_such_object|error
	Count   int               `json:"count"`
	Sample  []map[string]any  `json:"sample,omitempty"` // first few rows (col→value), preview only
	Mapped  int               `json:"mapped"`           // rows persisted into a wireless_* table
	Detail  string            `json:"detail,omitempty"`
}

// resolveMibTarget resolves a device + community for a MIB walk from request body.
func (s *Server) resolveMibTarget(ctx context.Context, deviceID, ip, community string) (db.Device, string, error) {
	var dev db.Device
	var err error
	switch {
	case deviceID != "":
		id, perr := uuid.Parse(deviceID)
		if perr != nil {
			return dev, "", errBadID
		}
		dev, err = s.queries.GetDevice(ctx, id)
	case ip != "":
		addr, perr := netip.ParseAddr(strings.TrimSpace(ip))
		if perr != nil {
			return dev, "", errBadIP
		}
		dev, err = s.queries.LiveDeviceByIP(ctx, &addr)
	default:
		return dev, "", errNoTarget
	}
	return dev, community, err
}

// walkTables walks the given pack tables on a connected client, returns honest
// per-table results, persists raw rows (always), and — when persist is true —
// maps rows into the wireless_* tables with source=snmp_wireless_mib.
func (s *Server) walkTables(ctx context.Context, dev db.Device, c snmp.Client, packID uuid.UUID, tables []db.MibPackTable, maxRows int, persist bool) []tableResult {
	poll := time.Now().UTC()
	var out []tableResult
	var apN, ssidN, cliN, radioN int
	pid := packID
	for _, t := range tables {
		if !t.Enabled {
			continue
		}
		res := mibpack.WalkTable(ctx, c, t.RootOid, maxRows)
		tr := tableResult{Table: t.TableName, RootOID: t.RootOid, Purpose: t.Purpose, Status: string(res.Status), Count: res.Count, Detail: res.Detail}
		// Raw rows (always) — replace prior capture for this device+table.
		_ = s.queries.DeleteMibWalkRows(ctx, db.DeleteMibWalkRowsParams{DeviceID: dev.ID, TableName: t.TableName})
		cm := parseColMap(t.ColumnMap)
		for i, row := range res.Rows {
			if i < 5 {
				tr.Sample = append(tr.Sample, rowPreview(row))
			}
			for col, oid := range row.OIDs {
				_ = s.queries.InsertMibWalkRow(ctx, db.InsertMibWalkRowParams{
					DeviceID: dev.ID, PackID: &pid, TableName: t.TableName, Oid: oid,
					Idx: row.Index, RawValue: truncStr(row.Cols[col], 512),
				})
			}
			if !persist {
				continue
			}
			switch t.Purpose {
			case "aps":
				name := colVal(row, cm, "ap_name")
				mac := colVal(row, cm, "ap_mac")
				if name == "" {
					name = mac
				}
				if name == "" {
					continue
				}
				var ip *netip.Addr
				if a, e := netip.ParseAddr(colVal(row, cm, "ap_ip")); e == nil {
					ip = &a
				}
				_, _ = s.queries.UpsertAccessPoint(ctx, db.UpsertAccessPointParams{
					ControllerDeviceID: dev.ID, Name: name, Mac: nzPtr(mac), Model: nzPtr(colVal(row, cm, "ap_model")),
					Ip: ip, Status: nz(normAPStatus(colVal(row, cm, "ap_status")), "unknown"), ClientCount: 0,
					Serial: colVal(row, cm, "ap_serial"), Firmware: colVal(row, cm, "ap_firmware"), Source: mibSource,
				})
				apN++
			case "ssids":
				name := colVal(row, cm, "ssid_name")
				if name == "" {
					name = colVal(row, cm, "ssid_ssid")
				}
				if name == "" {
					continue
				}
				_, _ = s.queries.UpsertWirelessSSID(ctx, db.UpsertWirelessSSIDParams{
					ControllerDeviceID: dev.ID, Name: name, Status: nz(colVal(row, cm, "ssid_status"), "unknown"),
					Security: colVal(row, cm, "ssid_security"), Band: colVal(row, cm, "ssid_band"), Vlan: colVal(row, cm, "ssid_vlan"), Source: mibSource,
				})
				ssidN++
			case "clients":
				mac := colVal(row, cm, "client_mac")
				if mac == "" {
					continue
				}
				var rssi *int32
				if v, e := strconv.Atoi(colVal(row, cm, "client_rssi")); e == nil {
					vv := int32(v)
					rssi = &vv
				}
				_, _ = s.queries.UpsertWirelessClient(ctx, db.UpsertWirelessClientParams{
					ControllerDeviceID: dev.ID, Mac: mac, Ip: colVal(row, cm, "client_ip"), Hostname: colVal(row, cm, "client_hostname"),
					ApName: colVal(row, cm, "client_ap"), Ssid: colVal(row, cm, "client_ssid"), Rssi: rssi, Band: colVal(row, cm, "client_band"), Source: mibSource,
				})
				cliN++
			case "radios":
				ap := colVal(row, cm, "radio_ap")
				if ap == "" {
					ap = row.Index
				}
				var ch, pw *int32
				if v, e := strconv.Atoi(colVal(row, cm, "radio_channel")); e == nil {
					vv := int32(v)
					ch = &vv
				}
				if v, e := strconv.Atoi(colVal(row, cm, "radio_power")); e == nil {
					vv := int32(v)
					pw = &vv
				}
				_, _ = s.queries.UpsertWirelessRadio(ctx, db.UpsertWirelessRadioParams{
					ControllerDeviceID: dev.ID, ApName: ap, Radio: nz(colVal(row, cm, "radio_name"), row.Index),
					Band: colVal(row, cm, "radio_band"), Channel: ch, PowerDbm: pw, Source: mibSource,
				})
				radioN++
			}
		}
		tr.Mapped = map[string]int{"aps": apN, "ssids": ssidN, "clients": cliN, "radios": radioN}[t.Purpose]
		out = append(out, tr)
	}
	if persist {
		// Prune stale rows of this source + write a controller-info row.
		_ = s.queries.DeleteStaleAccessPoints(ctx, db.DeleteStaleAccessPointsParams{ControllerDeviceID: dev.ID, Source: mibSource, CollectedAt: poll})
		_ = s.queries.DeleteStaleWirelessSSIDs(ctx, db.DeleteStaleWirelessSSIDsParams{ControllerDeviceID: dev.ID, Source: mibSource, CollectedAt: poll})
		_ = s.queries.DeleteStaleWirelessClients(ctx, db.DeleteStaleWirelessClientsParams{ControllerDeviceID: dev.ID, Source: mibSource, CollectedAt: poll})
		_ = s.queries.DeleteStaleWirelessRadios(ctx, db.DeleteStaleWirelessRadiosParams{ControllerDeviceID: dev.ID, Source: mibSource, CollectedAt: poll})
		if apN > 0 || ssidN > 0 || cliN > 0 {
			ven := derefStr(dev.Vendor)
			_, _ = s.queries.UpsertWLANControllerInfo(ctx, db.UpsertWLANControllerInfoParams{
				DeviceID: dev.ID, Vendor: nzPtr(ven), Version: dev.OsVersion, ApCount: int32(apN), ClientCount: int32(cliN),
				Source: mibSource, ControllerName: dev.Name, Model: derefStr(dev.Model), SsidCount: int32(ssidN),
			})
		}
	}
	return out
}

// testMibPackDevice handles POST /mib-packs/{id}/test-device {device_id|ip, community, max_rows, timeout, root_oid?, table?}.
func (s *Server) testMibPackDevice(w http.ResponseWriter, r *http.Request) {
	id, ok := parseUUIDParam(w, r, "id")
	if !ok {
		return
	}
	ctx := r.Context()
	var body struct {
		DeviceID  string `json:"device_id"`
		IP        string `json:"ip"`
		Community string `json:"community"`
		RootOID   string `json:"root_oid"`
		MaxRows   int    `json:"max_rows"`
		TimeoutS  int    `json:"timeout_s"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	pack, err := s.queries.GetMibPack(ctx, id)
	if err != nil {
		writeErr(w, err)
		return
	}
	dev, community, derr := s.resolveMibTarget(ctx, body.DeviceID, body.IP, body.Community)
	if derr != nil {
		http.Error(w, derr.Error(), http.StatusBadRequest)
		return
	}
	maxRows := body.MaxRows
	if maxRows <= 0 || maxRows > 500 {
		maxRows = 50
	}
	timeout := time.Duration(body.TimeoutS) * time.Second
	c, err := s.snmpClientForDevice(ctx, dev, community, timeout)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "detail": err.Error()})
		return
	}
	defer c.Close()

	var tables []db.MibPackTable
	if strings.TrimSpace(body.RootOID) != "" {
		// Ad-hoc walk of a single root OID (operator-supplied).
		tables = []db.MibPackTable{{PackID: id, TableName: "adhoc", RootOid: strings.TrimSpace(body.RootOID), Purpose: "stats", Enabled: true, ColumnMap: []byte("{}")}}
	} else {
		tables, _ = s.queries.ListMibPackTables(ctx, id)
	}
	results := s.walkTables(ctx, dev, c, id, tables, maxRows, false)
	supported := 0
	for _, t := range results {
		if t.Status == string(mibpack.StatusSupported) {
			supported++
		}
	}
	detail := strconv.Itoa(supported) + "/" + strconv.Itoa(len(results)) + " tables responded on " + dev.Name
	did := dev.ID
	_ = s.queries.SetMibPackTested(ctx, db.SetMibPackTestedParams{ID: id, LastTestDetail: detail, LastMatchedDevice: &did})
	s.audit(r, "config", "mib_pack.test", "mib_pack", id.String(), "Tested MIB pack "+pack.Name+" against "+dev.Name, map[string]any{"supported": supported, "tables": len(results)})
	writeJSON(w, http.StatusOK, map[string]any{"ok": supported > 0, "device": dev.Name, "detail": detail, "results": results})
}

// runWirelessMibCollection handles POST /devices/{id}/collect-wireless-mib.
func (s *Server) runWirelessMibCollection(w http.ResponseWriter, r *http.Request) {
	ctx, id, ok := pathDevice(w, r)
	if !ok {
		return
	}
	dev, err := s.queries.GetDevice(ctx, id)
	if err != nil {
		writeErr(w, err)
		return
	}
	res, detail, ok2 := s.collectWirelessMib(ctx, dev, "")
	if !ok2 && res == nil {
		writeJSON(w, http.StatusOK, map[string]any{"collected": false, "detail": detail})
		return
	}
	s.audit(r, "inventory", "mib_pack.collect", "device", id.String(), "SNMP wireless MIB collection on "+dev.Name, map[string]any{"detail": detail})
	writeJSON(w, http.StatusOK, map[string]any{"collected": ok2, "detail": detail, "results": res})
}

// collectWirelessMib resolves the applicable pack and runs the collection.
func (s *Server) collectWirelessMib(ctx context.Context, dev db.Device, community string) ([]tableResult, string, bool) {
	pack, found := s.matchMibPack(ctx, dev)
	if !found {
		return nil, "No applicable MIB pack for this device (vendor/sysObjectID/category did not match any enabled pack).", false
	}
	tables, _ := s.queries.ListMibPackTables(ctx, pack.ID)
	if len(tables) == 0 {
		return nil, "MIB pack '" + pack.Name + "' matched but has no mapped tables.", false
	}
	c, err := s.snmpClientForDevice(ctx, dev, community, 6*time.Second)
	if err != nil {
		return nil, "SNMP MIB collection failed: " + err.Error(), false
	}
	defer c.Close()
	results := s.walkTables(ctx, dev, c, pack.ID, tables, 0, true)
	supported, mapped := 0, 0
	for _, t := range results {
		if t.Status == string(mibpack.StatusSupported) {
			supported++
		}
		mapped += t.Mapped
	}
	did := dev.ID
	_ = s.queries.SetMibPackCollected(ctx, db.SetMibPackCollectedParams{ID: pack.ID, LastMatchedDevice: &did})
	detail := "Pack '" + pack.Name + "': " + strconv.Itoa(supported) + "/" + strconv.Itoa(len(results)) + " tables supported, " + strconv.Itoa(mapped) + " row(s) mapped into wireless tables (source=snmp_wireless_mib)."
	if mapped == 0 {
		detail += " No AP/SSID/client rows exposed by this firmware on the mapped tables — recorded honestly as raw rows; use Test/View Raw Rows + the mapping editor to target the tables this device populates."
	}
	return results, detail, supported > 0
}

// listMibWalkRows handles GET /devices/{id}/mib-rows.
func (s *Server) listMibWalkRows(w http.ResponseWriter, r *http.Request) {
	ctx, id, ok := pathDevice(w, r)
	if !ok {
		return
	}
	rows, err := s.queries.ListMibWalkRows(ctx, db.ListMibWalkRowsParams{DeviceID: id, Limit: 1000})
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rows)
}

// ---- mapping helpers -------------------------------------------------------

func parseColMap(b []byte) map[string]int {
	m := map[string]int{}
	if len(b) > 0 {
		_ = json.Unmarshal(b, &m)
	}
	return m
}

// colVal returns a domain field's value from a walked row per the column map.
// A mapped column of 0 means "use the row index"; for MAC fields the dotted
// index is rendered as a MAC address.
func colVal(row mibpack.Row, cm map[string]int, field string) string {
	col, ok := cm[field]
	if !ok {
		return ""
	}
	if col == 0 {
		if strings.Contains(field, "mac") {
			return indexToMAC(row.Index)
		}
		return row.Index
	}
	return row.Cols[uint32(col)]
}

func indexToMAC(idx string) string {
	parts := strings.Split(idx, ".")
	if len(parts) != 6 {
		return idx
	}
	hexb := make([]string, 6)
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 || n > 255 {
			return idx
		}
		hexb[i] = byteHex(byte(n))
	}
	return strings.Join(hexb, ":")
}

func byteHex(b byte) string {
	const h = "0123456789abcdef"
	return string([]byte{h[b>>4], h[b&0x0f]})
}

func normAPStatus(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "1", "up", "online", "active", "approved", "registered":
		return "online"
	case "0", "2", "down", "offline", "inactive":
		return "offline"
	default:
		return ""
	}
}

func rowPreview(row mibpack.Row) map[string]any {
	m := map[string]any{"_index": row.Index}
	for col, v := range row.Cols {
		m[strconv.FormatUint(uint64(col), 10)] = truncStr(v, 120)
	}
	return m
}

func truncStr(s string, n int) string {
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}
