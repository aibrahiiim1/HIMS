package api

import (
	"net/http"

	"github.com/coralsearesorts/hims/internal/fingerprint"
	"github.com/coralsearesorts/hims/internal/storage/postgres/db"
)

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
	rows, err := s.queries.ListVendorFingerprints(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}
	results := fingerprint.Match(ev, dbToPrints(rows, true))
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
	rows, err := s.queries.ListVendorFingerprints(ctx)
	if err != nil {
		writeErr(w, err)
		return
	}
	results := fingerprint.Match(ev, dbToPrints(rows, true))
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
