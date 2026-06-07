package api

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/coralsearesorts/hims/internal/domain"
	"github.com/coralsearesorts/hims/internal/mibpack"
	"github.com/coralsearesorts/hims/internal/snmp"
	"github.com/coralsearesorts/hims/internal/storage/postgres/db"
)

// MIB packs make uploaded/built-in MIBs ACTUALLY drive SNMP collection (the old
// /mibs page only stored text). A pack = metadata + raw files + mapping rows
// (table → root OID → purpose → column map). Built-in ∪ user; user wins by
// priority; applies-to matches a device by sysObjectID prefix / sysDescr / category.

const mibSource = "snmp_wireless_mib"

// ---- applies-to + DTO ------------------------------------------------------

type mibAppliesTo struct {
	SysObjectIDPrefixes []string `json:"sysobjectid_prefixes"`
	SysDescrContains    []string `json:"sysdescr_contains"`
	Categories          []string `json:"categories"`
}

type mibPackDTO struct {
	db.MibPack
	TableCount int `json:"table_count"`
	FileCount  int `json:"file_count"`
}

func parseAppliesTo(b []byte) mibAppliesTo {
	var a mibAppliesTo
	if len(b) > 0 {
		_ = json.Unmarshal(b, &a)
	}
	return a
}

// ---- built-in Extreme / HiPath Wireless Controller pack --------------------

type builtinTable struct {
	name, oid, purpose string
	cols               map[string]int // domain field → column subID (0 = row index)
}

// builtinHiPathPack is the first real MIB pack: the Extreme / HiPath / Enterasys
// wireless controller MIB (HWC). Root OIDs are derived from the HWC-MIB tree
// (hiPathWirelessMgmt = 1.3.6.1.4.1.5624.1.2). On a given firmware some tables
// are empty — the collector reports that honestly. Column maps follow apEntry /
// wlanEntry / muEntry. col 0 means "use the row index" (e.g. MU MAC index).
func builtinHiPathTables() []builtinTable {
	return []builtinTable{
		{"apTable", "1.3.6.1.4.1.5624.1.2.5.1.2", "aps",
			map[string]int{"ap_name": 2, "ap_serial": 4, "ap_firmware": 7, "ap_mac": 13, "ap_ip": 14}},
		{"apRadioStatusTable", "1.3.6.1.4.1.5624.1.2.5.2.4", "radios",
			map[string]int{"radio_channel": 1, "radio_power": 2}},
		{"apStatsTable", "1.3.6.1.4.1.5624.1.2.5.2.2", "stats", map[string]int{}},
		{"wlanTable", "1.3.6.1.4.1.5624.1.2.3.4.4", "ssids",
			map[string]int{"ssid_name": 4, "ssid_ssid": 5, "ssid_status": 7}},
		{"wlanStatsTable", "1.3.6.1.4.1.5624.1.2.3.4.5", "stats", map[string]int{}},
		// muTable (mobileUnits.2) — the documented client roster, indexed by MAC.
		// Columns per muEntry: 1=MAC 2=IP 3=user 6=SSID 12=AP-name 19=BSSID.
		// Empty on fw 10.05 (reported honestly); correct for firmware that exposes it.
		{"muTable", "1.3.6.1.4.1.5624.1.2.6.2", "clients",
			map[string]int{"client_mac": 1, "client_ip": 2, "client_hostname": 3, "client_ssid": 6, "client_ap": 12}},
		{"assocTable", "1.3.6.1.4.1.5624.1.2.7.2", "clients",
			map[string]int{"client_mac": 0}},
		{"topologyTable", "1.3.6.1.4.1.5624.1.2.4.1.1", "operational", map[string]int{}},
		{"siteTable", "1.3.6.1.4.1.5624.1.2.10.4", "operational", map[string]int{}},
		// Operational/event sub-trees observed live on the controller (raw capture
		// only; the operator can add column mappings via the mapping editor).
		{"mobileUnitsObjects", "1.3.6.1.4.1.5624.1.2.6", "operational", map[string]int{}},
	}
}

const builtinHiPathName = "Extreme / HiPath Wireless Controller MIB"

// ---- built-in Ruckus ZoneDirector pack -------------------------------------

const builtinRuckusZDName = "Ruckus ZoneDirector Wireless MIB"

// builtinRuckusZDTables maps the RUCKUS-ZD-WLAN-MIB roster tables
// (ruckusZDWLANModule = 1.3.6.1.4.1.25053.1.2.2.1). Column subIDs are the MIB
// column numbers (verified against RUCKUS-ZD-WLAN-MIB). Unlike the Extreme HWC,
// ZoneDirector DOES expose the full AP/WLAN/client roster over SNMP, so these
// tables populate directly.
func builtinRuckusZDTables() []builtinTable {
	return []builtinTable{
		// ruckusZDWLANAPTable (ruckusZDWLANAPInfo.1): per-AP inventory + NumSta.
		{"ruckusZDWLANAPTable", "1.3.6.1.4.1.25053.1.2.2.1.1.2.1", "aps",
			map[string]int{"ap_mac": 1, "ap_name": 2, "ap_status": 3, "ap_model": 4, "ap_serial": 5, "ap_firmware": 7, "ap_ip": 10, "ap_client_count": 15}},
		// ruckusZDWLANTable (ruckusZDWLANInfo.1): configured WLANs (all SSIDs).
		{"ruckusZDWLANTable", "1.3.6.1.4.1.25053.1.2.2.1.1.1.1", "ssids",
			map[string]int{"ssid_ssid": 1, "ssid_name": 1, "ssid_vlan": 7, "ssid_client_count": 12}},
		// ruckusZDWLANStaTable (ruckusZDWLANStaInfo.1): associated wireless clients.
		{"ruckusZDWLANStaTable", "1.3.6.1.4.1.25053.1.2.2.1.1.3.1", "clients",
			map[string]int{"client_mac": 1, "client_ap": 2, "client_ssid": 4, "client_hostname": 5, "client_band": 6, "client_ip": 8, "client_rssi": 81}},
	}
}

// SeedBuiltinMibPacks is the exported startup hook (called from cmd/hims-api).
func (s *Server) SeedBuiltinMibPacks(ctx context.Context) error { return s.seedBuiltinMibPacks(ctx) }

// seedBuiltinMibPacks creates each built-in pack if absent. Idempotent per-pack
// (so adding a new built-in pack seeds it on the next restart without disturbing
// existing packs or operator-uploaded ones).
func (s *Server) seedBuiltinMibPacks(ctx context.Context) error {
	packs, err := s.queries.ListMibPacks(ctx)
	if err != nil {
		return err
	}
	have := map[string]bool{}
	for _, p := range packs {
		if p.Source == "builtin" {
			have[p.Name] = true
		}
	}

	if !have[builtinHiPathName] {
		applies, _ := json.Marshal(mibAppliesTo{
			SysObjectIDPrefixes: []string{"1.3.6.1.4.1.1916", "1.3.6.1.4.1.5624"},
			SysDescrContains:    []string{"ExtremeCloud IQ Controller", "Summit WM", "HiPath Wireless"},
			Categories:          []string{string(domain.CatWirelessController)},
		})
		if err := s.seedOnePack(ctx, builtinHiPathName, "Extreme Networks", "HWC",
			"Built-in HiPath/Enterasys wireless controller MIB (apTable/wlanTable/muTable/etc.), root 1.3.6.1.4.1.5624.1.2.",
			applies, "1.3.6.1.4.1.5624.1.2", builtinHiPathTables()); err != nil {
			return err
		}
	}

	if !have[builtinRuckusZDName] {
		// Vendor-exact match only (sysObjectID 25053 / sysDescr) — deliberately NO
		// broad category, so it never shadows another vendor's wireless controller.
		applies, _ := json.Marshal(mibAppliesTo{
			SysObjectIDPrefixes: []string{"1.3.6.1.4.1.25053"},
			SysDescrContains:    []string{"ruckus", "zonedirector", "zd1", "zd3", "zd5"},
		})
		if err := s.seedOnePack(ctx, builtinRuckusZDName, "Ruckus Wireless", "ZD",
			"Built-in Ruckus ZoneDirector WLAN MIB (AP/WLAN/station tables), root 1.3.6.1.4.1.25053.1.2.2.1. ZoneDirector exposes the full roster over SNMP.",
			applies, "1.3.6.1.4.1.25053.1.2.2.1", builtinRuckusZDTables()); err != nil {
			return err
		}
	}
	return nil
}

// seedOnePack creates a built-in pack + its mapped tables.
func (s *Server) seedOnePack(ctx context.Context, name, vendor, version, desc string, applies []byte, root string, tables []builtinTable) error {
	meta, _ := json.Marshal(map[string]any{"root": root, "table_count": len(tables)})
	pack, err := s.queries.CreateMibPack(ctx, db.CreateMibPackParams{
		Name: name, Vendor: vendor, Category: string(domain.CatWirelessController),
		Source: "builtin", Enabled: true, Priority: 100, Version: version, Description: desc,
		AppliesTo: applies, ParseMeta: meta,
	})
	if err != nil {
		return err
	}
	for _, t := range tables {
		cm, _ := json.Marshal(t.cols)
		_, _ = s.queries.UpsertMibPackTable(ctx, db.UpsertMibPackTableParams{
			PackID: pack.ID, TableName: t.name, RootOid: t.oid, Purpose: t.purpose, ColumnMap: cm, Enabled: true,
		})
	}
	return nil
}

// matchMibPack returns the best applicable pack for a device (user > builtin by
// priority), matching sysObjectID prefix / sysDescr / category against stored facts.
func (s *Server) matchMibPack(ctx context.Context, dev db.Device) (db.MibPack, bool) {
	packs, err := s.queries.ListMibPacks(ctx)
	if err != nil {
		return db.MibPack{}, false
	}
	sysOID, sysDescr := s.deviceSysIdentity(ctx, dev)
	// Pick the MOST SPECIFIC match, not merely the first: a sysObjectID-prefix hit
	// (vendor-exact) outranks a sysDescr substring, which outranks a bare category
	// match. Without this, a pack that lists a broad category (e.g. the HiPath pack
	// claiming all wireless_controllers) would shadow the vendor-exact Ruckus pack
	// for a Ruckus device just because it sorts earlier. ListMibPacks orders
	// user-first by priority, so on a score tie the more-preferred pack wins.
	best := db.MibPack{}
	bestScore := 0
	for _, p := range packs {
		if !p.Enabled {
			continue
		}
		sc := mibMatchScore(parseAppliesTo(p.AppliesTo), sysOID, sysDescr, dev.Category)
		if sc > bestScore {
			best, bestScore = p, sc
		}
	}
	if bestScore > 0 {
		return best, true
	}
	return db.MibPack{}, false
}

// mibMatchScore ranks how specifically a pack's AppliesTo matches a device:
// sysObjectID prefix = 3 (vendor-exact), sysDescr substring = 2, category = 1,
// no match = 0. Used by matchMibPack to prefer the vendor-exact pack.
func mibMatchScore(a mibAppliesTo, sysOID, sysDescr, category string) int {
	oid := strings.TrimPrefix(sysOID, ".")
	for _, p := range a.SysObjectIDPrefixes {
		p = strings.TrimPrefix(p, ".")
		if p != "" && (oid == p || strings.HasPrefix(oid, p+".")) {
			return 3
		}
	}
	low := strings.ToLower(sysDescr)
	for _, c := range a.SysDescrContains {
		if c != "" && strings.Contains(low, strings.ToLower(c)) {
			return 2
		}
	}
	for _, c := range a.Categories {
		if c != "" && c == category {
			return 1
		}
	}
	return 0
}

func mibApplies(a mibAppliesTo, sysOID, sysDescr, category string) bool {
	oid := strings.TrimPrefix(sysOID, ".")
	for _, p := range a.SysObjectIDPrefixes {
		p = strings.TrimPrefix(p, ".")
		if p != "" && (oid == p || strings.HasPrefix(oid, p+".")) {
			return true
		}
	}
	low := strings.ToLower(sysDescr)
	for _, c := range a.SysDescrContains {
		if c != "" && strings.Contains(low, strings.ToLower(c)) {
			return true
		}
	}
	for _, c := range a.Categories {
		if c != "" && c == category {
			return true
		}
	}
	return false
}

// deviceSysIdentity reads the stored snmp.sysobjectid / snmp.sysdescr facts.
func (s *Server) deviceSysIdentity(ctx context.Context, dev db.Device) (sysOID, sysDescr string) {
	facts, _ := s.queries.ListDeviceFacts(ctx, dev.ID)
	for _, f := range facts {
		if f.Value == nil {
			continue
		}
		switch f.Key {
		case "snmp.sysobjectid":
			sysOID = *f.Value
		case "snmp.sysdescr":
			sysDescr = *f.Value
		}
	}
	if sysDescr == "" && dev.OsVersion != nil {
		sysDescr = *dev.OsVersion
	}
	return
}

// ---- SNMP client for a device walk ----------------------------------------

// snmpClientForDevice opens an SNMP v2c client for a device. An explicit
// community overrides; otherwise the device's bound credential is decrypted.
func (s *Server) snmpClientForDevice(ctx context.Context, dev db.Device, community string, timeout time.Duration) (snmp.Client, error) {
	if dev.PrimaryIp == nil || !dev.PrimaryIp.IsValid() {
		return nil, fmt.Errorf("device has no IP")
	}
	if community == "" {
		if dev.CredentialID == nil {
			return nil, fmt.Errorf("no SNMP community: device has no bound credential and none was supplied")
		}
		dec, err := s.scanDecrypt(ctx, *dev.CredentialID)
		if err != nil {
			return nil, fmt.Errorf("could not decrypt bound credential")
		}
		if dec.Kind != domain.CredSNMPv2c {
			return nil, fmt.Errorf("bound credential is not SNMP v2c")
		}
		community = dec.Community
	}
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	t := snmp.Target{Addr: *dev.PrimaryIp, Port: 161, Version: snmp.V2c, Community: community, Timeout: timeout, Retries: 1, MaxReps: 20}
	c, err := snmp.NewClient(t)
	if err != nil {
		return nil, err
	}
	if err := c.Connect(ctx); err != nil {
		return nil, err
	}
	return c, nil
}

// ---- handlers: pack CRUD ---------------------------------------------------

func (s *Server) listMibPacks(w http.ResponseWriter, r *http.Request) {
	packs, err := s.queries.ListMibPacks(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}
	out := make([]mibPackDTO, 0, len(packs))
	for _, p := range packs {
		d := mibPackDTO{MibPack: p}
		if ts, e := s.queries.ListMibPackTables(r.Context(), p.ID); e == nil {
			d.TableCount = len(ts)
		}
		if fs, e := s.queries.ListMibPackFiles(r.Context(), p.ID); e == nil {
			d.FileCount = len(fs)
		}
		out = append(out, d)
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) getMibPack(w http.ResponseWriter, r *http.Request) {
	id, ok := parseUUIDParam(w, r, "id")
	if !ok {
		return
	}
	ctx := r.Context()
	pack, err := s.queries.GetMibPack(ctx, id)
	if err != nil {
		writeErr(w, err)
		return
	}
	files, _ := s.queries.ListMibPackFiles(ctx, id)
	tables, _ := s.queries.ListMibPackTables(ctx, id)
	writeJSON(w, http.StatusOK, map[string]any{"pack": pack, "files": files, "tables": tables})
}

type mibPackReq struct {
	Name        string         `json:"name"`
	Vendor      string         `json:"vendor"`
	Category    string         `json:"category"`
	Priority    *int32         `json:"priority"`
	Version     string         `json:"version"`
	Description string         `json:"description"`
	Enabled     *bool          `json:"enabled"`
	AppliesTo   map[string]any `json:"applies_to"`
}

func (s *Server) createMibPack(w http.ResponseWriter, r *http.Request) {
	var req mibPackReq
	if !decodeJSON(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.Name) == "" {
		http.Error(w, "name is required", http.StatusBadRequest)
		return
	}
	applies, _ := json.Marshal(req.AppliesTo)
	prio := int32(100)
	if req.Priority != nil {
		prio = *req.Priority
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	pack, err := s.queries.CreateMibPack(r.Context(), db.CreateMibPackParams{
		Name: req.Name, Vendor: req.Vendor, Category: req.Category, Source: "user", Enabled: enabled,
		Priority: prio, Version: req.Version, Description: req.Description, AppliesTo: applies, ParseMeta: []byte("{}"),
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	s.audit(r, "config", "mib_pack.create", "mib_pack", pack.ID.String(), "Created MIB pack "+pack.Name, nil)
	writeJSON(w, http.StatusCreated, pack)
}

func (s *Server) updateMibPack(w http.ResponseWriter, r *http.Request) {
	id, ok := parseUUIDParam(w, r, "id")
	if !ok {
		return
	}
	var req mibPackReq
	if !decodeJSON(w, r, &req) {
		return
	}
	cur, err := s.queries.GetMibPack(r.Context(), id)
	if err != nil {
		writeErr(w, err)
		return
	}
	applies := cur.AppliesTo
	if req.AppliesTo != nil {
		applies, _ = json.Marshal(req.AppliesTo)
	}
	prio := cur.Priority
	if req.Priority != nil {
		prio = *req.Priority
	}
	enabled := cur.Enabled
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	row, err := s.queries.UpdateMibPack(r.Context(), db.UpdateMibPackParams{
		ID: id, Name: nz(req.Name, cur.Name), Vendor: nz(req.Vendor, cur.Vendor), Category: nz(req.Category, cur.Category),
		Enabled: enabled, Priority: prio, Version: nz(req.Version, cur.Version), Description: nz(req.Description, cur.Description), AppliesTo: applies,
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	action := "mib_pack.update"
	if req.Enabled != nil && !*req.Enabled {
		action = "mib_pack.disable"
	}
	s.audit(r, "config", action, "mib_pack", id.String(), "Updated MIB pack "+row.Name, nil)
	writeJSON(w, http.StatusOK, row)
}

func (s *Server) deleteMibPack(w http.ResponseWriter, r *http.Request) {
	id, ok := parseUUIDParam(w, r, "id")
	if !ok {
		return
	}
	if err := s.queries.DeleteMibPack(r.Context(), id); err != nil {
		writeErr(w, err)
		return
	}
	s.audit(r, "config", "mib_pack.delete", "mib_pack", id.String(), "Deleted MIB pack", nil)
	w.WriteHeader(http.StatusNoContent)
}

// ---- mapping editor --------------------------------------------------------

type mibTableReq struct {
	TableName string         `json:"table_name"`
	RootOID   string         `json:"root_oid"`
	Purpose   string         `json:"purpose"`
	ColumnMap map[string]int `json:"column_map"`
	Enabled   *bool          `json:"enabled"`
}

func (s *Server) upsertMibPackTable(w http.ResponseWriter, r *http.Request) {
	id, ok := parseUUIDParam(w, r, "id")
	if !ok {
		return
	}
	var req mibTableReq
	if !decodeJSON(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.TableName) == "" || strings.TrimSpace(req.RootOID) == "" {
		http.Error(w, "table_name and root_oid are required", http.StatusBadRequest)
		return
	}
	cm, _ := json.Marshal(req.ColumnMap)
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	row, err := s.queries.UpsertMibPackTable(r.Context(), db.UpsertMibPackTableParams{
		PackID: id, TableName: req.TableName, RootOid: req.RootOID, Purpose: req.Purpose, ColumnMap: cm, Enabled: enabled,
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	s.audit(r, "config", "mib_pack.mapping", "mib_pack", id.String(), "Mapped table "+req.TableName+" → "+req.Purpose, nil)
	writeJSON(w, http.StatusOK, row)
}

// ---- upload (file or zip) + parse -----------------------------------------

func (s *Server) uploadMibPack(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if err := r.ParseMultipartForm(32 << 20); err != nil { // 32 MiB
		http.Error(w, "could not parse upload: "+err.Error(), http.StatusBadRequest)
		return
	}
	packName := strings.TrimSpace(r.FormValue("name"))
	vendor := r.FormValue("vendor")
	category := r.FormValue("category")
	file, hdr, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "no file uploaded (field 'file')", http.StatusBadRequest)
		return
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, 64<<20))
	if err != nil {
		http.Error(w, "could not read file", http.StatusBadRequest)
		return
	}
	if packName == "" {
		packName = strings.TrimSuffix(hdr.Filename, ".zip")
	}

	// Collect (filename, content) — one for a plain MIB, many for a ZIP.
	type up struct {
		name    string
		content []byte
	}
	var files []up
	if strings.HasSuffix(strings.ToLower(hdr.Filename), ".zip") {
		zr, zerr := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
		if zerr != nil {
			http.Error(w, "invalid zip: "+zerr.Error(), http.StatusBadRequest)
			return
		}
		for _, zf := range zr.File {
			if zf.FileInfo().IsDir() || !mibLikeName(zf.Name) {
				continue
			}
			rc, e := zf.Open()
			if e != nil {
				continue
			}
			c, _ := io.ReadAll(io.LimitReader(rc, 16<<20))
			rc.Close()
			files = append(files, up{name: baseName(zf.Name), content: c})
		}
		if len(files) == 0 {
			http.Error(w, "zip contained no .mib/.txt/.my files", http.StatusBadRequest)
			return
		}
	} else {
		if !mibLikeName(hdr.Filename) {
			http.Error(w, "unsupported file type (expect .mib/.txt/.my/.zip)", http.StatusBadRequest)
			return
		}
		files = append(files, up{name: baseName(hdr.Filename), content: raw})
	}

	pack, err := s.queries.CreateMibPack(ctx, db.CreateMibPackParams{
		Name: packName, Vendor: vendor, Category: category, Source: "user", Enabled: true, Priority: 50,
		Version: "", Description: "Uploaded " + hdr.Filename, AppliesTo: []byte("{}"), ParseMeta: []byte("{}"),
	})
	if err != nil {
		writeErr(w, err)
		return
	}

	var modules, allTables, warnings []string
	objects := 0
	for _, f := range files {
		p := mibpack.Parse(string(f.content))
		status, detail := "ok", ""
		if p.Module == "" {
			status, detail = "parse_error", "no MODULE DEFINITIONS header"
		}
		if p.Module != "" {
			modules = append(modules, p.Module)
		}
		allTables = append(allTables, p.Tables...)
		warnings = append(warnings, p.Warnings...)
		objects += p.ObjectCount
		_ = s.queries.InsertMibPackFile(ctx, db.InsertMibPackFileParams{
			PackID: pack.ID, Filename: f.name, ModuleName: p.Module, Content: f.content,
			SizeBytes: int32(len(f.content)), ParseStatus: status, ParseDetail: detail,
		})
	}
	sort.Strings(allTables)
	meta, _ := json.Marshal(map[string]any{
		"modules": modules, "tables": dedupe(allTables), "object_count": objects,
		"table_count": len(dedupe(allTables)), "warnings": warnings, "files": len(files),
	})
	_ = s.queries.SetMibPackParseMeta(ctx, db.SetMibPackParseMetaParams{ID: pack.ID, ParseMeta: meta})
	s.audit(r, "config", "mib_pack.upload", "mib_pack", pack.ID.String(),
		"Uploaded MIB pack "+packName+" ("+strconv.Itoa(len(files))+" file(s))",
		map[string]any{"files": len(files), "modules": modules, "tables": len(dedupe(allTables))})
	writeJSON(w, http.StatusCreated, map[string]any{
		"pack_id": pack.ID.String(), "files": len(files), "modules": modules,
		"tables": dedupe(allTables), "object_count": objects, "warnings": warnings,
	})
}

func mibLikeName(n string) bool {
	n = strings.ToLower(n)
	return strings.HasSuffix(n, ".mib") || strings.HasSuffix(n, ".txt") || strings.HasSuffix(n, ".my") || !strings.Contains(baseName(n), ".")
}

func baseName(p string) string {
	p = strings.ReplaceAll(p, "\\", "/")
	if i := strings.LastIndexByte(p, '/'); i >= 0 {
		return p[i+1:]
	}
	return p
}

func dedupe(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}
