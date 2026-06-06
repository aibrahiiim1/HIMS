package api

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/xuri/excelize/v2"

	"github.com/coralsearesorts/hims/internal/domain"
	"github.com/coralsearesorts/hims/internal/storage/postgres/db"
)

// manualDeviceReq is the operator-entered device for the Manual Add input mode
// (a device that can't be auto-discovered). Only name is required.
type manualDeviceReq struct {
	Name       string  `json:"name"`
	Category   string  `json:"category"`
	PrimaryIP  string  `json:"primary_ip"`
	Hostname   string  `json:"hostname"`
	Vendor     string  `json:"vendor"`
	Model      string  `json:"model"`
	Serial     string  `json:"serial"`
	OSVersion  string  `json:"os_version"`
	LocationID *string `json:"location_id"`
	VLAN       string  `json:"vlan"`
	Class      string  `json:"class"`
	Location   string  `json:"location"`
}

// createManualDevice handles POST /devices — operator-entered inventory. The
// device is stamped metadata.source=manual so it is distinguishable from a
// live-collected device; collection can later reconcile it by (primary_ip,
// location) if it becomes discoverable.
func (s *Server) createManualDevice(w http.ResponseWriter, r *http.Request) {
	var req manualDeviceReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}
	params, err := manualDeviceParams(req)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	dev, err := s.queries.CreateDevice(r.Context(), params)
	if err != nil {
		writeErr(w, err)
		return
	}
	s.audit(r, "inventory", "device.create", "device", dev.ID.String(), "Manually added device "+dev.Name, nil)
	writeJSON(w, http.StatusCreated, dev)
}

func manualDeviceParams(req manualDeviceReq) (db.CreateDeviceParams, error) {
	name := strings.TrimSpace(req.Name)
	if name == "" {
		return db.CreateDeviceParams{}, errBadRequest("name is required")
	}
	cat := strings.TrimSpace(req.Category)
	if cat == "" {
		cat = string(domain.CatUnknown)
	}
	if !validCategory(cat) {
		return db.CreateDeviceParams{}, errBadRequest("invalid category " + strconv.Quote(cat) + "; use one of: " + strings.Join(categoryList, ", "))
	}
	var ipPtr *netip.Addr
	if v := strings.TrimSpace(req.PrimaryIP); v != "" {
		ip, err := netip.ParseAddr(v)
		if err != nil {
			return db.CreateDeviceParams{}, errBadRequest("invalid primary_ip: " + v)
		}
		ipPtr = &ip
	}
	return db.CreateDeviceParams{
		LocationID: parseUUIDPtr(req.LocationID),
		PrimaryIp:  ipPtr,
		Hostname:   strPtr(req.Hostname),
		Name:       name,
		Vendor:     strPtr(req.Vendor),
		Model:      strPtr(req.Model),
		Serial:     strPtr(req.Serial),
		OsVersion:  strPtr(req.OSVersion),
		Category:   cat,
		// status stays in the CMDB vocabulary (up/down/warning/unknown); the
		// manual/csv origin is recorded in metadata.source, not status.
		Status:       "unknown",
		Driver:       nil,
		CredentialID: nil,
		Metadata:     []byte(`{"source":"manual"}`),
		Vlan:         strPtr(req.VLAN),
		DeviceClass:  strPtr(req.Class),
		Location:     strPtr(req.Location),
	}, nil
}

// updateDeviceReq is a PARTIAL update: every field is a pointer, so only the
// keys present in the JSON body are changed. Absent keys keep their stored value.
// An empty string for a nullable field (vendor/model/…) clears it.
type updateDeviceReq struct {
	Name                       *string `json:"name"`
	Category                   *string `json:"category"`
	Subtype                    *string `json:"subtype"`
	Vendor                     *string `json:"vendor"`
	Model                      *string `json:"model"`
	Serial                     *string `json:"serial"`
	OSVersion                  *string `json:"os_version"`
	Hostname                   *string `json:"hostname"`
	VLAN                       *string `json:"vlan"`
	Class                      *string `json:"class"`
	Location                   *string `json:"location"`
	LocationID                 *string `json:"location_id"` // "" clears; omitted keeps
	Notes                      *string `json:"notes"`
	Criticality                *string `json:"criticality"`
	MonitoringEnabled          *bool   `json:"monitoring_enabled"`
	ClassificationLocked       *bool   `json:"classification_locked"`
	ManualClassificationReason *string `json:"manual_classification_reason"`
}

var validCriticality = map[string]bool{"": true, "low": true, "normal": true, "high": true, "critical": true}

// updateDevice handles PATCH /devices/{id} — operator edit of identity +
// management attributes. Partial (only provided fields change), audited, and
// site-scope/RBAC enforced by middleware. Setting classification_locked makes the
// operator's identity (category/vendor/model/serial/name) authoritative — future
// discovery scans will not overwrite it (see apply.reconcile).
func (s *Server) updateDevice(w http.ResponseWriter, r *http.Request) {
	ctx, id, ok := pathDevice(w, r)
	if !ok {
		return
	}
	var req updateDeviceReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}
	cur, err := s.queries.GetDevice(ctx, id)
	if err != nil {
		writeErr(w, err)
		return
	}

	// Merge provided fields over the current values.
	name := cur.Name
	if req.Name != nil {
		if strings.TrimSpace(*req.Name) == "" {
			http.Error(w, "name cannot be empty", http.StatusBadRequest)
			return
		}
		name = strings.TrimSpace(*req.Name)
	}
	cat := cur.Category
	if req.Category != nil {
		cat = strings.TrimSpace(*req.Category)
		if cat == "" {
			cat = string(domain.CatUnknown)
		}
		if !validCategory(cat) {
			http.Error(w, "invalid category "+strconv.Quote(cat)+"; use one of: "+strings.Join(categoryList, ", "), http.StatusBadRequest)
			return
		}
	}
	crit := cur.Criticality
	if req.Criticality != nil {
		crit = strings.TrimSpace(*req.Criticality)
		if !validCriticality[crit] {
			http.Error(w, "invalid criticality; use one of: low, normal, high, critical (or empty)", http.StatusBadRequest)
			return
		}
	}

	params := db.UpdateDeviceParams{
		ID: id, Name: name, Category: cat,
		Vendor:                     mergeNullStr(req.Vendor, cur.Vendor),
		Model:                      mergeNullStr(req.Model, cur.Model),
		Serial:                     mergeNullStr(req.Serial, cur.Serial),
		OsVersion:                  mergeNullStr(req.OSVersion, cur.OsVersion),
		Hostname:                   mergeNullStr(req.Hostname, cur.Hostname),
		Vlan:                       mergeNullStr(req.VLAN, cur.Vlan),
		DeviceClass:                mergeNullStr(req.Class, cur.DeviceClass),
		Location:                   mergeNullStr(req.Location, cur.Location),
		LocationID:                 cur.LocationID,
		Subtype:                    mergeStr(req.Subtype, cur.Subtype),
		Notes:                      mergeStr(req.Notes, cur.Notes),
		Criticality:                crit,
		MonitoringEnabled:          mergeBool(req.MonitoringEnabled, cur.MonitoringEnabled),
		ClassificationLocked:       mergeBool(req.ClassificationLocked, cur.ClassificationLocked),
		ManualClassificationReason: mergeStr(req.ManualClassificationReason, cur.ManualClassificationReason),
	}
	if req.LocationID != nil {
		params.LocationID = parseUUIDPtr(req.LocationID) // "" → nil (clear)
	}

	dev, err := s.queries.UpdateDevice(ctx, params)
	if err != nil {
		writeErr(w, err)
		return
	}

	// Audit: record WHAT changed (field names only — no need to log every value).
	changed := changedDeviceFields(cur, dev)
	s.audit(r, "inventory", "device.update", "device", id.String(),
		"Edited device "+dev.Name, map[string]any{
			"fields":                changed,
			"classification_locked": dev.ClassificationLocked,
		})
	writeJSON(w, http.StatusOK, dev)
}

// mergeStr returns *p (when provided) else the current NOT-NULL string.
func mergeStr(p *string, cur string) string {
	if p != nil {
		return strings.TrimSpace(*p)
	}
	return cur
}

// mergeNullStr returns a nullable string: *p (empty → nil to clear) when provided,
// else the current pointer unchanged.
func mergeNullStr(p *string, cur *string) *string {
	if p != nil {
		return strPtr(strings.TrimSpace(*p))
	}
	return cur
}

func mergeBool(p *bool, cur bool) bool {
	if p != nil {
		return *p
	}
	return cur
}

// changedDeviceFields lists the identity/management fields that differ between
// before and after, for the audit detail.
func changedDeviceFields(a, b db.Device) []string {
	var out []string
	add := func(name string, changed bool) {
		if changed {
			out = append(out, name)
		}
	}
	eqp := func(x, y *string) bool {
		if x == nil || y == nil {
			return x == y
		}
		return *x == *y
	}
	add("name", a.Name != b.Name)
	add("category", a.Category != b.Category)
	add("subtype", a.Subtype != b.Subtype)
	add("vendor", !eqp(a.Vendor, b.Vendor))
	add("model", !eqp(a.Model, b.Model))
	add("serial", !eqp(a.Serial, b.Serial))
	add("os_version", !eqp(a.OsVersion, b.OsVersion))
	add("hostname", !eqp(a.Hostname, b.Hostname))
	add("vlan", !eqp(a.Vlan, b.Vlan))
	add("class", !eqp(a.DeviceClass, b.DeviceClass))
	add("location", !eqp(a.Location, b.Location))
	add("location_id", a.LocationID != b.LocationID)
	add("notes", a.Notes != b.Notes)
	add("criticality", a.Criticality != b.Criticality)
	add("monitoring_enabled", a.MonitoringEnabled != b.MonitoringEnabled)
	add("classification_locked", a.ClassificationLocked != b.ClassificationLocked)
	return out
}

// deleteDevice handles DELETE /devices/{id} — hard delete (cascades inventory).
func (s *Server) deleteDevice(w http.ResponseWriter, r *http.Request) {
	ctx, id, ok := pathDevice(w, r)
	if !ok {
		return
	}
	if err := s.queries.DeleteDevice(ctx, id); err != nil {
		writeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type bulkAssignReq struct {
	IDs        []string `json:"ids"`
	VLAN       *string  `json:"vlan"`
	Class      *string  `json:"class"`
	LocationID *string  `json:"location_id"` // a locations-tree node id
}

// bulkAssignDevices handles POST /devices/bulk-assign — set vlan/class and/or
// the location-tree node on a multi-selection. Only fields present (non-null)
// in the request are changed; the rest are kept (COALESCE).
func (s *Server) bulkAssignDevices(w http.ResponseWriter, r *http.Request) {
	var req bulkAssignReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}
	ids := make([]uuid.UUID, 0, len(req.IDs))
	for _, str := range req.IDs {
		id, err := uuid.Parse(str)
		if err != nil {
			http.Error(w, "invalid device id: "+str, http.StatusBadRequest)
			return
		}
		ids = append(ids, id)
	}
	if len(ids) == 0 {
		http.Error(w, "no ids provided", http.StatusBadRequest)
		return
	}
	if req.VLAN == nil && req.Class == nil && req.LocationID == nil {
		http.Error(w, "provide at least one of vlan, class, location_id", http.StatusBadRequest)
		return
	}
	n, err := s.queries.BulkAssignClassification(r.Context(), db.BulkAssignClassificationParams{
		Ids: ids, Vlan: req.VLAN, DeviceClass: req.Class, LocationID: parseUUIDPtr(req.LocationID),
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]int64{"updated": n})
}

type bulkDeleteReq struct {
	IDs []string `json:"ids"`
}

// bulkDeleteDevices handles POST /devices/bulk-delete — multi-select delete.
func (s *Server) bulkDeleteDevices(w http.ResponseWriter, r *http.Request) {
	var req bulkDeleteReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}
	ids := make([]uuid.UUID, 0, len(req.IDs))
	for _, str := range req.IDs {
		id, err := uuid.Parse(str)
		if err != nil {
			http.Error(w, "invalid device id: "+str, http.StatusBadRequest)
			return
		}
		ids = append(ids, id)
	}
	if len(ids) == 0 {
		http.Error(w, "no ids provided", http.StatusBadRequest)
		return
	}
	n, err := s.queries.DeleteDevices(r.Context(), ids)
	if err != nil {
		writeErr(w, err)
		return
	}
	s.audit(r, "inventory", "device.bulk_delete", "device", "", "Deleted devices", map[string]any{"count": n})
	writeJSON(w, http.StatusOK, map[string]int64{"deleted": n})
}

// csvImportResult summarizes a bulk import run.
type csvImportResult struct {
	Created int      `json:"created"`
	Failed  int      `json:"failed"`
	Errors  []string `json:"errors,omitempty"`
}

// importDevicesCSV handles POST /devices/import-csv — bulk manual assets. Body
// is text/csv with a header row. Recognized columns (case-insensitive, any
// subset, in any order): name, category, primary_ip, hostname, vendor, model,
// serial, os_version, location_id. "name" is required per row. Rows that fail
// are reported but do not abort the batch.
// importDevicesCSV handles POST /devices/import-csv (pasted text/csv body).
func (s *Server) importDevicesCSV(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()
	reader := csv.NewReader(io.LimitReader(r.Body, 8<<20))
	reader.FieldsPerRecord = -1
	reader.TrimLeadingSpace = true
	rows, err := reader.ReadAll()
	if err != nil {
		http.Error(w, "invalid CSV: "+err.Error(), http.StatusBadRequest)
		return
	}
	s.importRows(w, r, rows)
}

// importDevicesFile handles POST /devices/import-file (multipart "file") for a
// .csv or .xlsx upload — same columns + location resolution as the paste path.
func (s *Server) importDevicesFile(w http.ResponseWriter, r *http.Request) {
	f, hdr, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "no file uploaded (field 'file'): "+err.Error(), http.StatusBadRequest)
		return
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, 16<<20))
	if err != nil {
		http.Error(w, "read failed: "+err.Error(), http.StatusBadRequest)
		return
	}
	var rows [][]string
	if strings.HasSuffix(strings.ToLower(hdr.Filename), ".xlsx") {
		rows, err = xlsxRows(data)
	} else {
		rows, err = csv.NewReader(bytes.NewReader(data)).ReadAll()
	}
	if err != nil {
		http.Error(w, "parse failed: "+err.Error(), http.StatusBadRequest)
		return
	}
	s.importRows(w, r, rows)
}

// xlsxRows reads the first worksheet of an .xlsx file into rows.
func xlsxRows(data []byte) ([][]string, error) {
	xl, err := excelize.OpenReader(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	defer xl.Close()
	sheets := xl.GetSheetList()
	if len(sheets) == 0 {
		return nil, fmt.Errorf("workbook has no sheets")
	}
	return xl.GetRows(sheets[0])
}

// importRows is the shared bulk-import core: row 0 is the header (any subset of
// the recognized columns), "name" required; "location" cells resolve to a tree
// node by name or full path. Per-row failures are reported; the batch continues.
func (s *Server) importRows(w http.ResponseWriter, r *http.Request, rows [][]string) {
	if len(rows) < 1 {
		http.Error(w, "empty file (need a header row)", http.StatusBadRequest)
		return
	}
	colIdx := map[string]int{}
	for i, h := range rows[0] {
		colIdx[strings.ToLower(strings.TrimSpace(h))] = i
	}
	if _, ok := colIdx["name"]; !ok {
		http.Error(w, "file must have a 'name' column", http.StatusBadRequest)
		return
	}
	locByKey := s.locationLookup(r.Context()) // name/path (lowercased) -> id

	get := func(row []string, key string) string {
		if i, ok := colIdx[key]; ok && i < len(row) {
			return strings.TrimSpace(row[i])
		}
		return ""
	}
	res := csvImportResult{}
	for n := 1; n < len(rows); n++ {
		row := rows[n]
		line := n + 1
		if strings.TrimSpace(strings.Join(row, "")) == "" {
			continue // skip blank rows
		}
		req := manualDeviceReq{
			Name: get(row, "name"), Category: get(row, "category"), PrimaryIP: get(row, "primary_ip"),
			Hostname: get(row, "hostname"), Vendor: get(row, "vendor"), Model: get(row, "model"),
			Serial: get(row, "serial"), OSVersion: get(row, "os_version"),
			VLAN: get(row, "vlan"), Class: get(row, "class"),
		}
		// "location" column: resolve a tree node by name or path; else keep as a
		// free-text label. "location_id" column (a uuid) wins if present.
		if lid := get(row, "location_id"); lid != "" {
			req.LocationID = &lid
		} else if loc := get(row, "location"); loc != "" {
			if id, ok := locByKey[strings.ToLower(loc)]; ok {
				ids := id
				req.LocationID = &ids
			} else {
				req.Location = loc
			}
		}
		params, perr := manualDeviceParams(req)
		if perr != nil {
			res.Failed++
			res.Errors = append(res.Errors, fmt.Sprintf("row %d: %v", line, perr))
			continue
		}
		params.Metadata = []byte(`{"source":"import"}`)
		if _, err := s.queries.CreateDevice(r.Context(), params); err != nil {
			res.Failed++
			res.Errors = append(res.Errors, fmt.Sprintf("row %d (%s): %v", line, params.Name, err))
			continue
		}
		res.Created++
	}
	writeJSON(w, http.StatusOK, res)
}

// locationLookup builds a lowercased {name|path -> id} map for import resolution.
func (s *Server) locationLookup(ctx context.Context) map[string]string {
	out := map[string]string{}
	locs, err := s.queries.ListLocations(ctx)
	if err != nil {
		return out
	}
	byID := map[uuid.UUID]db.Location{}
	for _, l := range locs {
		byID[l.ID] = l
	}
	var path func(l db.Location) string
	path = func(l db.Location) string {
		if l.ParentID != nil {
			if p, ok := byID[*l.ParentID]; ok {
				return path(p) + " / " + l.Name
			}
		}
		return l.Name
	}
	for _, l := range locs {
		out[strings.ToLower(l.Name)] = l.ID.String()
		out[strings.ToLower(path(l))] = l.ID.String()
	}
	return out
}

// categoryList mirrors the devices.category CHECK constraint (migration
// 000004). Manual/CSV input is validated against it so the operator gets a
// clear 400 with the allowed set instead of a raw DB constraint 500.
var categoryList = []string{
	string(domain.CatUnknown), string(domain.CatSwitch), string(domain.CatRouter),
	string(domain.CatFirewall), string(domain.CatAccessPoint), string(domain.CatWirelessController),
	string(domain.CatServer), string(domain.CatVirtualHost), string(domain.CatVirtualMachine),
	string(domain.CatStorage), string(domain.CatNVR), string(domain.CatCamera),
	string(domain.CatPrinter), string(domain.CatIPPhone), string(domain.CatPBX),
	string(domain.CatVoiceGateway), string(domain.CatDatabase), string(domain.CatDirectory),
	string(domain.CatDNS), string(domain.CatDHCP), string(domain.CatFingerprint),
	string(domain.CatEndpoint), string(domain.CatUPS), string(domain.CatISPRouter),
	string(domain.CatApplication),
}

func validCategory(c string) bool {
	for _, v := range categoryList {
		if v == c {
			return true
		}
	}
	return false
}

func strPtr(s string) *string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	return &s
}
