package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/netip"
	"sort"
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
	Table   string           `json:"table"`
	RootOID string           `json:"root_oid"`
	Purpose string           `json:"purpose"`
	Status  string           `json:"status"` // supported|empty|timeout|no_such_object|error
	Count   int              `json:"count"`  // interpreted table rows (distinct indices)
	RawVars int              `json:"raw_vars"` // raw varbinds captured under the root
	Sample  []map[string]any `json:"sample,omitempty"` // first few rows (col→value), preview only
	Mapped  int              `json:"mapped"`           // rows persisted into a wireless_* table
	Detail  string           `json:"detail,omitempty"`
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
	var apN, ssidN, cliN, radioN, evtN int
	pid := packID
	eventsCleared := false
	const rawCap = 20000 // bound raw capture per table (sparse counter trees can be huge)
	for _, t := range tables {
		if !t.Enabled {
			continue
		}
		cm := parseColMap(t.ColumnMap)
		// Targeted column walk: when a table has a column map, walk ONLY the mapped
		// column sub-trees (entry.<col>) rather than the entire (often 50-column)
		// table. On large controllers — a Ruckus ZD with 200+ APs and 700+ clients
		// is ~36k varbinds for the full tables but ~7k for the ~7 mapped columns —
		// this keeps the walk an order of magnitude smaller: inside the scan/timeout
		// budget, and below the raw cap that would otherwise silently truncate rows.
		// Operational tables (no column map) still get a full subtree walk so the
		// Explorer keeps faithful raw capture for field discovery.
		var vars []mibpack.RawVar
		var status mibpack.TableStatus
		var detail string
		if cols := mappedColumns(cm); len(cols) > 0 {
			entry := strings.TrimSuffix(t.RootOid, ".") + ".1"
			for _, col := range cols {
				cv, st, d := mibpack.RawWalk(ctx, c, entry+"."+strconv.Itoa(col), 0)
				vars = append(vars, cv...)
				if st == mibpack.StatusSupported {
					status = st
				}
				if d != "" {
					detail = d
				}
			}
			if status == "" {
				status = mibpack.StatusEmpty
			}
		} else {
			vars, status, detail = mibpack.RawWalk(ctx, c, t.RootOid, 0)
		}
		tr := tableResult{Table: t.TableName, RootOID: t.RootOid, Purpose: t.Purpose, Status: string(status), RawVars: len(vars), Detail: detail}

		// Raw rows (always; honest capture even when nothing maps) — replace the
		// prior capture for this device+table, then store each varbind with type.
		_ = s.queries.DeleteMibWalkRows(ctx, db.DeleteMibWalkRowsParams{DeviceID: dev.ID, TableName: t.TableName})
		for i, v := range vars {
			if i >= rawCap {
				break
			}
			_ = s.queries.InsertMibWalkRow(ctx, db.InsertMibWalkRowParams{
				DeviceID: dev.ID, PackID: &pid, TableName: t.TableName, Oid: v.OID,
				Idx: mibpack.OIDSuffix(v.OID, t.RootOid), RawValue: truncStr(v.Value, 512), ValType: v.Type,
			})
		}

		// Interpret the varbinds as a table (boundary-correct column/index split).
		rows := mibpack.GroupRows(vars, t.RootOid, maxRows)
		tr.Count = len(rows)
		for i, row := range rows {
			if i < 5 {
				tr.Sample = append(tr.Sample, rowPreview(row))
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
				// Per-AP client count when the MIB exposes it (e.g. Ruckus ZD
				// ruckusZDWLANAPNumSta). 0 is the honest default when unmapped.
				cc := 0
				if v, e := strconv.Atoi(strings.TrimSpace(colVal(row, cm, "ap_client_count"))); e == nil && v > 0 {
					cc = v
				}
				_, _ = s.queries.UpsertAccessPoint(ctx, db.UpsertAccessPointParams{
					ControllerDeviceID: dev.ID, Name: name, Mac: nzPtr(mac), Model: nzPtr(colVal(row, cm, "ap_model")),
					Ip: ip, Status: nz(normAPStatus(colVal(row, cm, "ap_status")), "unknown"), ClientCount: int32(cc),
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
				scc := 0
				if v, e := strconv.Atoi(strings.TrimSpace(colVal(row, cm, "ssid_client_count"))); e == nil && v > 0 {
					scc = v
				}
				_, _ = s.queries.UpsertWirelessSSID(ctx, db.UpsertWirelessSSIDParams{
					ControllerDeviceID: dev.ID, Name: name, Status: nz(colVal(row, cm, "ssid_status"), "unknown"),
					Security: colVal(row, cm, "ssid_security"), Band: colVal(row, cm, "ssid_band"), Vlan: colVal(row, cm, "ssid_vlan"),
					ClientCount: int32(scc), Source: mibSource,
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
			case "events":
				msg := colVal(row, cm, "event_message")
				if msg == "" {
					continue
				}
				if !eventsCleared { // replace the MIB-sourced event set once per run
					_ = s.queries.DeleteWirelessEventsForSource(ctx, db.DeleteWirelessEventsForSourceParams{ControllerDeviceID: dev.ID, Source: mibSource})
					eventsCleared = true
				}
				_ = s.queries.InsertWirelessEvent(ctx, db.InsertWirelessEventParams{
					ControllerDeviceID: dev.ID, At: poll, Severity: nz(colVal(row, cm, "event_severity"), "info"),
					Category: t.TableName, Message: truncStr(msg, 480), Source: mibSource,
				})
				evtN++
			}
		}
		tr.Mapped = map[string]int{"aps": apN, "ssids": ssidN, "clients": cliN, "radios": radioN, "events": evtN}[t.Purpose]
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
	// §4.5 precedence: if an enabled vendor profile (REST/XML) is bound to this
	// controller, that is the PRIMARY collector — even when invoked via the SNMP
	// "collect now" action. Only when no profile exists do we fall back to the MIB.
	if handled, pok, pdetail := s.collectWirelessForDevice(ctx, dev, nil); handled {
		s.audit(r, "inventory", "wireless.collect_primary", "device", id.String(), "REST/XML wireless collection on "+dev.Name, map[string]any{"detail": pdetail})
		writeJSON(w, http.StatusOK, map[string]any{"collected": pok, "detail": pdetail, "source": "vendor_profile"})
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

// probeSysIdentity does a one-shot SNMP GET of sysObjectID + sysDescr and persists
// them as device facts so MIB-pack matching has a vendor-exact signal even for a
// device added manually (no scan-time fingerprint). Best-effort: a missing or
// non-SNMP bound credential simply skips it (pack matching falls back to category).
func (s *Server) probeSysIdentity(ctx context.Context, dev db.Device, community string) {
	c, err := s.snmpClientForDevice(ctx, dev, community, 6*time.Second)
	if err != nil {
		return
	}
	defer c.Close()
	pdus, err := c.Get(ctx, "1.3.6.1.2.1.1.2.0", "1.3.6.1.2.1.1.1.0") // sysObjectID.0, sysDescr.0
	if err != nil {
		return
	}
	if len(pdus) > 0 {
		if v := snmp.PDUString(pdus[0]); v != "" {
			_ = s.queries.UpsertDeviceFact(ctx, db.UpsertDeviceFactParams{DeviceID: dev.ID, Key: "snmp.sysobjectid", Value: &v, Driver: "snmp"})
		}
	}
	if len(pdus) > 1 {
		if v := snmp.PDUString(pdus[1]); v != "" {
			_ = s.queries.UpsertDeviceFact(ctx, db.UpsertDeviceFactParams{DeviceID: dev.ID, Key: "snmp.sysdescr", Value: &v, Driver: "snmp"})
		}
	}
}

// collectWirelessMib resolves the applicable pack and runs the collection.
func (s *Server) collectWirelessMib(ctx context.Context, dev db.Device, community string) ([]tableResult, string, bool) {
	// Self-heal pack selection: a wireless_controller added via the manual REST/XML
	// "Add controller" flow has no SNMP fingerprint, so it would match only the broad
	// category and shadow the vendor-exact pack (e.g. the HiPath pack swallowing a
	// Ruckus device). Probe sysObjectID/sysDescr once and persist them so matchMibPack
	// has a vendor-exact signal on this and future runs.
	if oid, _ := s.deviceSysIdentity(ctx, dev); oid == "" {
		s.probeSysIdentity(ctx, dev, community)
	}
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

// ---- MIB Explorer ----------------------------------------------------------

type explorerSample struct {
	Index string `json:"index"`
	Value string `json:"value"`
}

type explorerGroup struct {
	ColumnOID     string           `json:"column_oid"`
	Name          string           `json:"name"`              // documented MIB name (or nearest), "" if unknown
	Table         string           `json:"table"`             // pack table this subtree was captured under
	Purpose       string           `json:"purpose,omitempty"` // mapped purpose, if any
	Field         string           `json:"field,omitempty"`   // domain field this column maps to, if any
	Mapped        bool             `json:"mapped"`
	ValueType     string           `json:"value_type"`
	Rows          int              `json:"rows"`
	LastCollected string           `json:"last_collected,omitempty"`
	Samples       []explorerSample `json:"samples"`
}

type explorerResp struct {
	DeviceID  string          `json:"device_id"`
	TotalRows int             `json:"total_rows"`
	Groups    []explorerGroup `json:"groups"`
}

// mibColumnKey returns the column OID (group key) + index label for a walked
// OID. When the OID sits under a known table entry (rootOID.1) it splits into
// the true SNMP column + index; otherwise it falls back to "OID minus the last
// sub-identifier" so scalars and undocumented subtrees still group sensibly.
func mibColumnKey(oid, rootOID string) (key, idx string) {
	if rootOID != "" {
		entryRoot := strings.TrimSuffix(rootOID, ".") + ".1"
		if col, ix, ok := snmp.ColumnAndIndex(oid, entryRoot); ok {
			parts := make([]string, len(ix))
			for i, v := range ix {
				parts[i] = strconv.FormatUint(uint64(v), 10)
			}
			return entryRoot + "." + strconv.FormatUint(uint64(col), 10), strings.Join(parts, ".")
		}
	}
	if i := strings.LastIndexByte(oid, '.'); i >= 0 {
		return oid[:i], oid[i+1:]
	}
	return oid, ""
}

// mibExplorer handles GET /devices/{id}/mib-explorer — the OID-tree grouping of
// a device's captured raw rows: column OID, documented name (where known), pack
// table, mapped domain field, value type, row count, and sample rows.
func (s *Server) mibExplorer(w http.ResponseWriter, r *http.Request) {
	ctx, id, ok := pathDevice(w, r)
	if !ok {
		return
	}
	rows, err := s.queries.ListMibWalkRows(ctx, db.ListMibWalkRowsParams{DeviceID: id, Limit: 20000})
	if err != nil {
		writeErr(w, err)
		return
	}

	// Build table_name → root and columnOID → (purpose, field) from the packs
	// that produced these rows, so groups can show which domain field they feed.
	rootByTable := map[string]string{}
	type fieldInfo struct{ purpose, field string }
	colField := map[string]fieldInfo{}
	seenPack := map[uuid.UUID]bool{}
	for _, rw := range rows {
		if rw.PackID == nil || seenPack[*rw.PackID] {
			continue
		}
		seenPack[*rw.PackID] = true
		ts, _ := s.queries.ListMibPackTables(ctx, *rw.PackID)
		for _, t := range ts {
			rootByTable[t.TableName] = t.RootOid
			entryRoot := strings.TrimSuffix(t.RootOid, ".") + ".1"
			for field, col := range parseColMap(t.ColumnMap) {
				if col <= 0 {
					continue // col 0 = row index, not a distinct column OID
				}
				colField[entryRoot+"."+strconv.Itoa(col)] = fieldInfo{t.Purpose, field}
			}
		}
	}

	groups := map[string]*explorerGroup{}
	var order []string
	seenOID := map[string]bool{}
	total := 0
	for _, rw := range rows {
		if seenOID[rw.Oid] { // the same OID may be captured under overlapping table roots
			continue
		}
		seenOID[rw.Oid] = true
		total++
		key, idx := mibColumnKey(rw.Oid, rootByTable[rw.TableName])
		g := groups[key]
		if g == nil {
			g = &explorerGroup{
				ColumnOID: key, Name: mibOIDName(key), Table: rw.TableName, ValueType: rw.ValType,
				LastCollected: rw.CollectedAt.UTC().Format("2006-01-02T15:04:05Z07:00"),
			}
			if fi, ok := colField[key]; ok {
				g.Purpose, g.Field, g.Mapped = fi.purpose, fi.field, true
			}
			groups[key] = g
			order = append(order, key)
		}
		g.Rows++
		if len(g.Samples) < 5 {
			g.Samples = append(g.Samples, explorerSample{Index: idx, Value: rw.RawValue})
		}
	}

	out := make([]explorerGroup, 0, len(order))
	for _, k := range order {
		out = append(out, *groups[k])
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Rows != out[j].Rows {
			return out[i].Rows > out[j].Rows
		}
		return out[i].ColumnOID < out[j].ColumnOID
	})
	writeJSON(w, http.StatusOK, explorerResp{DeviceID: id.String(), TotalRows: total, Groups: out})
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

// mappedColumns returns the distinct positive column subIDs referenced by a
// column map (col 0 = "use row index", so it's not a column to walk). Empty
// result ⇒ the table has no real columns mapped, so walkTables falls back to a
// full subtree walk (covers operational tables + index-only mappings).
func mappedColumns(cm map[string]int) []int {
	seen := map[int]bool{}
	var out []int
	for _, c := range cm {
		if c > 0 && !seen[c] {
			seen[c] = true
			out = append(out, c)
		}
	}
	return out
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
