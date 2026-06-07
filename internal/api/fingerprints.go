package api

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/netip"
	"strconv"
	"strings"

	"github.com/google/uuid"

	"github.com/coralsearesorts/hims/internal/fingerprint"
	"github.com/coralsearesorts/hims/internal/osdiscovery"
	"github.com/coralsearesorts/hims/internal/storage/postgres/db"
)

// reclassifyFP is the fingerprint verdict applied during a device reclassify:
// a category override (+ canonical vendor/model when the match is specific).
type reclassifyFP struct {
	category   string
	confidence int
	vendor     string
	model      string
	source     string // evidence source tag, e.g. "vendor_fingerprint:oid"
	detail     string // human-readable rule, for the evidence trail
}

// reclassifyFingerprint builds fingerprint evidence from a device's stored RAW
// SNMP identity facts (sysObjectID / sysDescr / sysName) plus the live probe's
// HTTP/SSH/port signals, matches the effective library (operator ∪ built-in), and
// returns the top fingerprint as an override (req #5: fingerprints affect
// reclassify, not only scans). A zero value (category "") means no hit.
func (s *Server) reclassifyFingerprint(ctx context.Context, d db.Device, obs osdiscovery.Observation) reclassifyFP {
	facts, _ := s.queries.ListDeviceFacts(ctx, d.ID)
	fact := func(key string) string {
		for _, f := range facts {
			if f.Key == key && f.Value != nil {
				return *f.Value
			}
		}
		return ""
	}
	sysDescr := fact("snmp.sysdescr")
	if sysDescr == "" {
		sysDescr = obs.SNMPSysDescr
	}
	if sysDescr == "" && d.OsVersion != nil {
		sysDescr = *d.OsVersion // SNMP-only devices stash sysDescr in os_version
	}
	ev := fingerprint.Evidence{
		SysObjectID: fact("snmp.sysobjectid"),
		SysDescr:    sysDescr,
		SysName:     fact("snmp.sysname"),
		HTTPServer:  obs.HTTPServer,
		SSHBanner:   obs.SSHBanner,
		Ports:       obs.OpenTCP,
	}
	results := fingerprint.Match(ev, s.scanFingerprintLibrary(ctx))
	if len(results) == 0 {
		return reclassifyFP{}
	}
	top := results[0]
	out := reclassifyFP{
		category:   fingerprint.CanonicalCategory(top.DeviceType),
		confidence: top.Confidence,
		source:     "vendor_fingerprint:" + top.Kind,
		detail:     top.Vendor + " / " + top.DeviceType + " (" + top.Kind + ":" + top.Pattern + ")",
	}
	if top.Confidence >= 85 {
		out.vendor = top.Vendor
		// Precedence: the winning fingerprint's EXPLICIT model wins; otherwise the
		// model is derived from sysDescr (the VE6120 built-in path).
		if top.Model != "" {
			out.model = top.Model
		} else {
			out.model = fingerprint.ModelFromSysDescr(sysDescr)
		}
	}
	return out
}

// scanFingerprintLibrary assembles the effective fingerprint library used by a
// scan (and by reclassify): operator-defined ENABLED prints first — so they win
// ties over the built-ins (Match is a stable sort) — then every built-in print
// whose (kind,pattern) the operator hasn't already stored. A DB row that exists
// but is DISABLED therefore SUPPRESSES its built-in counterpart, honouring an
// explicit operator opt-out. On any DB error it degrades to the built-in library
// so classification never silently loses fingerprinting.
func (s *Server) scanFingerprintLibrary(ctx context.Context) []fingerprint.Print {
	rows, err := s.queries.ListVendorFingerprints(ctx)
	if err != nil {
		return fingerprint.Library()
	}
	lib := dbToPrints(rows, true) // enabled operator prints, highest precedence
	have := make(map[string]bool, len(rows))
	for _, r := range rows { // every stored row (incl. disabled) shadows the built-in
		have[r.Kind+"|"+r.Pattern] = true
	}
	for _, p := range fingerprint.Library() {
		if !have[p.Kind+"|"+p.Pattern] {
			lib = append(lib, p)
		}
	}
	return lib
}

// dbToPrints converts stored vendor fingerprints (enabled only) to the pure
// matcher's Print type.
func dbToPrints(rows []db.VendorFingerprint, enabledOnly bool) []fingerprint.Print {
	out := make([]fingerprint.Print, 0, len(rows))
	for _, r := range rows {
		if enabledOnly && !r.Enabled {
			continue
		}
		out = append(out, fingerprint.Print{
			Kind: r.Kind, Pattern: r.Pattern, Vendor: r.Vendor,
			DeviceType: r.DeviceType, Confidence: int(r.Confidence), Model: r.Model,
		})
	}
	return out
}

// seedVendorFingerprints handles POST /vendor-fingerprints/seed — imports the
// comprehensive built-in library, skipping any (kind,pattern) already present.
// Idempotent: re-seeding creates nothing.
func (s *Server) seedVendorFingerprints(w http.ResponseWriter, r *http.Request) {
	existing, err := s.queries.ListVendorFingerprints(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}
	have := make(map[string]bool, len(existing))
	for _, e := range existing {
		have[e.Kind+"|"+e.Pattern] = true
	}
	created, skipped := 0, 0
	for _, p := range fingerprint.Library() {
		if have[p.Kind+"|"+p.Pattern] {
			skipped++
			continue
		}
		if _, err := s.queries.CreateVendorFingerprint(r.Context(), db.CreateVendorFingerprintParams{
			Kind: p.Kind, Pattern: p.Pattern, Vendor: p.Vendor, DeviceType: p.DeviceType,
			Confidence: int32(p.Confidence), Enabled: true, Model: "", Priority: 100, Source: "builtin",
		}); err != nil {
			writeErr(w, err)
			return
		}
		created++
	}
	s.audit(r, "config", "fingerprint.seed", "vendor_fingerprint", "", "Seeded built-in fingerprint library", map[string]any{"created": created, "skipped": skipped})
	writeJSON(w, http.StatusOK, map[string]int{"created": created, "skipped": skipped, "library_size": len(fingerprint.Library())})
}

// matchVendorFingerprints handles POST /vendor-fingerprints/match — runs the
// supplied evidence against the operator's enabled fingerprint library and
// returns ranked vendor/device-type suggestions. The match-test tool.
func (s *Server) matchVendorFingerprints(w http.ResponseWriter, r *http.Request) {
	var ev fingerprint.Evidence
	if !decodeJSON(w, r, &ev) {
		return
	}
	// Match against the SAME effective library a scan uses (operator ∪ built-in),
	// so the test tool's verdict is exactly what classification would decide.
	results := fingerprint.Match(ev, s.scanFingerprintLibrary(r.Context()))
	writeJSON(w, http.StatusOK, map[string]any{"evidence": ev, "results": results})
}

// deviceFingerprintSuggestion handles GET /devices/{id}/fingerprint-suggestion —
// builds evidence from the device's stored facts (sysDescr fragment in
// os_version + vendor) and returns library matches. Useful for "what is this
// device?" and for validating/curating the library against real inventory.
func (s *Server) deviceFingerprintSuggestion(w http.ResponseWriter, r *http.Request) {
	ctx, id, ok := pathDevice(w, r)
	if !ok {
		return
	}
	dev, err := s.queries.GetDevice(ctx, id)
	if err != nil {
		writeErr(w, err)
		return
	}
	descr := ""
	if dev.OsVersion != nil {
		descr = *dev.OsVersion
	}
	if dev.Vendor != nil && *dev.Vendor != "" {
		descr = descr + " " + *dev.Vendor
	}
	ev := fingerprint.Evidence{SysDescr: descr}
	results := fingerprint.Match(ev, s.scanFingerprintLibrary(ctx))
	writeJSON(w, http.StatusOK, map[string]any{
		"device_id":      id.String(),
		"current_vendor": derefStr(dev.Vendor),
		"current_category": dev.Category,
		"evidence":       ev,
		"results":        results,
	})
}

// ---- Test Fingerprint Against Device (req #2/#3) --------------------------

type testDeviceReq struct {
	DeviceID string `json:"device_id"`
	IP       string `json:"ip"`
}

// testDeviceFingerprint handles POST /vendor-fingerprints/test-device. Given a
// device (by id or IP), it loads that device's RAW stored SNMP identity facts,
// runs the effective library, and returns: matched/not-matched, the raw SNMP
// values used, which rule matched, the resulting vendor/category/model, and the
// confidence — i.e. exactly what a scan/reclassify would decide for this device.
func (s *Server) testDeviceFingerprint(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var req testDeviceReq
	if !decodeJSON(w, r, &req) {
		return
	}
	var dev db.Device
	var err error
	switch {
	case req.DeviceID != "":
		id, perr := uuid.Parse(req.DeviceID)
		if perr != nil {
			http.Error(w, "invalid device_id", http.StatusBadRequest)
			return
		}
		dev, err = s.queries.GetDevice(ctx, id)
	case req.IP != "":
		ip, perr := netip.ParseAddr(strings.TrimSpace(req.IP))
		if perr != nil {
			http.Error(w, "invalid ip", http.StatusBadRequest)
			return
		}
		dev, err = s.queries.LiveDeviceByIP(ctx, &ip)
	default:
		http.Error(w, "device_id or ip is required", http.StatusBadRequest)
		return
	}
	if err != nil {
		http.Error(w, "device not found", http.StatusNotFound)
		return
	}

	facts, _ := s.queries.ListDeviceFacts(ctx, dev.ID)
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
		sysDescr = *dev.OsVersion
	}
	ev := fingerprint.Evidence{
		SysObjectID: fact("snmp.sysobjectid"),
		SysDescr:    sysDescr,
		SysName:     fact("snmp.sysname"),
	}
	lib := s.scanFingerprintLibrary(ctx)
	results := fingerprint.Match(ev, lib)

	resp := map[string]any{
		"device_id":        dev.ID.String(),
		"device_name":      dev.Name,
		"current_category": dev.Category,
		"current_vendor":   derefStr(dev.Vendor),
		"current_model":    derefStr(dev.Model),
		"raw_snmp": map[string]string{ // the exact values the rules were tested against
			"sysobjectid":  ev.SysObjectID,
			"sysdescr":     ev.SysDescr,
			"sysname":      ev.SysName,
			"syscontact":   fact("snmp.syscontact"),
			"syslocation":  fact("snmp.syslocation"),
		},
		"matched": len(results) > 0,
		"results": results,
	}
	if len(results) > 0 {
		top := results[0]
		// The winning rule's explicit model wins; else derive from sysDescr.
		model := top.Model
		if model == "" {
			model = fingerprint.ModelFromSysDescr(sysDescr)
		}
		resp["top"] = map[string]any{
			"kind":       top.Kind,
			"pattern":    top.Pattern,
			"rule":       top.Kind + ":" + top.Pattern,
			"vendor":     top.Vendor,
			"category":   fingerprint.CanonicalCategory(top.DeviceType),
			"model":      model,
			"confidence": top.Confidence,
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

// ---- Import / Export (req #4) ---------------------------------------------

type fingerprintExport struct {
	Kind       string `json:"kind"`
	Pattern    string `json:"pattern"`
	Vendor     string `json:"vendor"`
	DeviceType string `json:"device_type"`
	Model      string `json:"model"`
	Confidence int    `json:"confidence"`
	Priority   int    `json:"priority"`
	Enabled    bool   `json:"enabled"`
	Source     string `json:"source"`
}

// exportVendorFingerprints handles GET /vendor-fingerprints/export?format=json|csv —
// streams the whole library as a downloadable file for backup / transfer.
func (s *Server) exportVendorFingerprints(w http.ResponseWriter, r *http.Request) {
	rows, err := s.queries.ListVendorFingerprints(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}
	format := r.URL.Query().Get("format")
	if format == "csv" {
		w.Header().Set("Content-Type", "text/csv; charset=utf-8")
		w.Header().Set("Content-Disposition", "attachment; filename=\"vendor-fingerprints.csv\"")
		cw := csv.NewWriter(w)
		_ = cw.Write([]string{"kind", "pattern", "vendor", "device_type", "model", "confidence", "priority", "enabled", "source"})
		for _, r := range rows {
			_ = cw.Write([]string{
				r.Kind, r.Pattern, r.Vendor, r.DeviceType, r.Model,
				strconv.Itoa(int(r.Confidence)), strconv.Itoa(int(r.Priority)),
				strconv.FormatBool(r.Enabled), r.Source,
			})
		}
		cw.Flush()
		return
	}
	out := make([]fingerprintExport, 0, len(rows))
	for _, r := range rows {
		out = append(out, fingerprintExport{
			Kind: r.Kind, Pattern: r.Pattern, Vendor: r.Vendor, DeviceType: r.DeviceType,
			Model: r.Model, Confidence: int(r.Confidence), Priority: int(r.Priority),
			Enabled: r.Enabled, Source: r.Source,
		})
	}
	w.Header().Set("Content-Disposition", "attachment; filename=\"vendor-fingerprints.json\"")
	writeJSON(w, http.StatusOK, out)
}

// importVendorFingerprints handles POST /vendor-fingerprints/import. Accepts a
// JSON array (Content-Type application/json) or CSV (text/csv) body with the same
// columns as export. Each row is upserted by (kind,pattern); imported rows are
// marked source='user' so they outrank the builtin catalog. Idempotent + audited.
func (s *Server) importVendorFingerprints(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	body, err := io.ReadAll(io.LimitReader(r.Body, 4<<20)) // 4 MiB cap
	if err != nil {
		http.Error(w, "could not read body", http.StatusBadRequest)
		return
	}
	var items []fingerprintExport
	ct := r.Header.Get("Content-Type")
	if strings.Contains(ct, "csv") || (len(body) > 0 && body[0] != '[' && body[0] != '{') {
		items, err = parseFingerprintCSV(body)
	} else {
		err = json.Unmarshal(body, &items)
	}
	if err != nil {
		http.Error(w, "parse error: "+err.Error(), http.StatusBadRequest)
		return
	}
	imported, failed := 0, 0
	var errs []string
	for i, it := range items {
		if !fpKinds[it.Kind] || strings.TrimSpace(it.Pattern) == "" {
			failed++
			if len(errs) < 10 {
				errs = append(errs, "row "+strconv.Itoa(i+1)+": invalid kind/pattern")
			}
			continue
		}
		conf := int32(it.Confidence)
		if conf <= 0 || conf > 100 {
			conf = 50
		}
		prio := int32(it.Priority)
		if prio <= 0 {
			prio = 100
		}
		if _, err := s.queries.UpsertVendorFingerprint(ctx, db.UpsertVendorFingerprintParams{
			Kind: it.Kind, Pattern: it.Pattern, Vendor: it.Vendor, DeviceType: it.DeviceType,
			Confidence: conf, Enabled: it.Enabled || it.Source == "", Model: it.Model,
			Priority: prio, Source: "user", // imported rules are operator-owned
		}); err != nil {
			failed++
			if len(errs) < 10 {
				errs = append(errs, "row "+strconv.Itoa(i+1)+": "+err.Error())
			}
			continue
		}
		imported++
	}
	s.audit(r, "config", "fingerprint.import", "vendor_fingerprint", "",
		"Imported vendor fingerprints", map[string]any{"imported": imported, "failed": failed})
	writeJSON(w, http.StatusOK, map[string]any{"imported": imported, "failed": failed, "errors": errs})
}

// parseFingerprintCSV parses an export-shaped CSV (header row required) into
// import items. Unknown/extra columns are ignored; missing optional columns default.
func parseFingerprintCSV(body []byte) ([]fingerprintExport, error) {
	cr := csv.NewReader(strings.NewReader(string(body)))
	cr.FieldsPerRecord = -1
	recs, err := cr.ReadAll()
	if err != nil {
		return nil, err
	}
	if len(recs) < 2 {
		return nil, errors.New("csv has no data rows")
	}
	col := map[string]int{}
	for i, h := range recs[0] {
		col[strings.ToLower(strings.TrimSpace(h))] = i
	}
	get := func(rec []string, name string) string {
		if i, ok := col[name]; ok && i < len(rec) {
			return strings.TrimSpace(rec[i])
		}
		return ""
	}
	var out []fingerprintExport
	for _, rec := range recs[1:] {
		if len(rec) == 0 || strings.TrimSpace(strings.Join(rec, "")) == "" {
			continue
		}
		conf, _ := strconv.Atoi(get(rec, "confidence"))
		prio, _ := strconv.Atoi(get(rec, "priority"))
		enabled := true
		if e := get(rec, "enabled"); e != "" {
			enabled, _ = strconv.ParseBool(e)
		}
		out = append(out, fingerprintExport{
			Kind: strings.ToLower(get(rec, "kind")), Pattern: get(rec, "pattern"),
			Vendor: get(rec, "vendor"), DeviceType: get(rec, "device_type"),
			Model: get(rec, "model"), Confidence: conf, Priority: prio, Enabled: enabled,
			Source: "user",
		})
	}
	return out, nil
}

func derefStr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// isUniqueViolation reports whether err is a Postgres unique-constraint violation
// (SQLSTATE 23505) — e.g. inserting a vendor fingerprint whose (kind,pattern)
// already exists. Surfaced so handlers can return a clear 409 instead of a bare
// 500. Matches on the pgx error string (which carries "SQLSTATE 23505").
func isUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	return strings.Contains(s, "23505") || strings.Contains(s, "duplicate key value")
}
