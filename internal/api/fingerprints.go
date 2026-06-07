package api

import (
	"context"
	"net/http"

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
		out.model = fingerprint.ModelFromSysDescr(sysDescr)
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
			DeviceType: r.DeviceType, Confidence: int(r.Confidence),
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
			Confidence: int32(p.Confidence), Enabled: true,
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

func derefStr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
