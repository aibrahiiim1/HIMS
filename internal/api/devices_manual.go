package api

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"strconv"
	"strings"

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
	}, nil
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
func (s *Server) importDevicesCSV(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()
	reader := csv.NewReader(io.LimitReader(r.Body, 8<<20))
	reader.FieldsPerRecord = -1 // tolerate ragged rows
	reader.TrimLeadingSpace = true

	header, err := reader.Read()
	if err != nil {
		http.Error(w, "empty or invalid CSV (need a header row): "+err.Error(), http.StatusBadRequest)
		return
	}
	colIdx := map[string]int{}
	for i, h := range header {
		colIdx[strings.ToLower(strings.TrimSpace(h))] = i
	}
	if _, ok := colIdx["name"]; !ok {
		http.Error(w, "CSV must have a 'name' column", http.StatusBadRequest)
		return
	}

	get := func(row []string, key string) string {
		if i, ok := colIdx[key]; ok && i < len(row) {
			return strings.TrimSpace(row[i])
		}
		return ""
	}

	res := csvImportResult{}
	line := 1
	for {
		row, err := reader.Read()
		if err == io.EOF {
			break
		}
		line++
		if err != nil {
			res.Failed++
			res.Errors = append(res.Errors, fmt.Sprintf("line %d: %v", line, err))
			continue
		}
		var locPtr *string
		if v := get(row, "location_id"); v != "" {
			locPtr = &v
		}
		params, perr := manualDeviceParams(manualDeviceReq{
			Name: get(row, "name"), Category: get(row, "category"), PrimaryIP: get(row, "primary_ip"),
			Hostname: get(row, "hostname"), Vendor: get(row, "vendor"), Model: get(row, "model"),
			Serial: get(row, "serial"), OSVersion: get(row, "os_version"), LocationID: locPtr,
		})
		if perr != nil {
			res.Failed++
			res.Errors = append(res.Errors, fmt.Sprintf("line %d: %v", line, perr))
			continue
		}
		params.Metadata = []byte(`{"source":"csv_import"}`)
		if _, err := s.queries.CreateDevice(r.Context(), params); err != nil {
			res.Failed++
			res.Errors = append(res.Errors, fmt.Sprintf("line %d (%s): %v", line, params.Name, err))
			continue
		}
		res.Created++
	}
	writeJSON(w, http.StatusOK, res)
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
