package api

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/coralsearesorts/hims/internal/fingerprint"
	"github.com/coralsearesorts/hims/internal/storage/postgres/db"
	"github.com/google/uuid"
)

// fpRow builds a stored vendor_fingerprints row for the seed-plan tests.
func fpRow(kind, pattern, vendor, dtype string, conf int32, source string, exclusions []byte) db.VendorFingerprint {
	if exclusions == nil {
		exclusions = []byte("[]")
	}
	return db.VendorFingerprint{
		ID: uuid.New(), Kind: kind, Pattern: pattern, Vendor: vendor, DeviceType: dtype,
		Confidence: conf, Enabled: true, Priority: 100, Source: source, Exclusions: exclusions,
	}
}

func planByPattern(plan []seedAction, pattern string) (seedAction, bool) {
	for _, a := range plan {
		if a.Print.Pattern == pattern {
			return a, true
		}
	}
	return seedAction{}, false
}

// TestSeedPlan_RefreshesDriftedBuiltinExclusions is the core follow-up regression:
// an existing BUILT-IN HP ".11" switch row seeded BEFORE exclusions existed (so it
// holds []) must be planned for REFRESH against a catalog entry that carries the
// JetDirect exclusion — i.e. the shipped exclusion propagates to the DB row. The
// row id is reused (update in place), so no duplicate is created.
func TestSeedPlan_RefreshesDriftedBuiltinExclusions(t *testing.T) {
	hpRow := fpRow("oid", "1.3.6.1.4.1.11", "Aruba/HPE", "switch", 78, "builtin", []byte("[]"))
	lib := []fingerprint.Print{{
		Kind: fingerprint.KindOID, Pattern: "1.3.6.1.4.1.11", Vendor: "Aruba/HPE", DeviceType: "switch", Confidence: 78,
		Exclusions: []fingerprint.Exclusion{{Kind: fingerprint.KindOID, Pattern: "1.3.6.1.4.1.11.2.3.9"}},
	}}
	plan := planBuiltinSeed([]db.VendorFingerprint{hpRow}, lib)
	a, ok := planByPattern(plan, "1.3.6.1.4.1.11")
	if !ok || a.Action != seedRefresh {
		t.Fatalf("expected HP .11 row to be REFRESHED, got %+v", a)
	}
	if a.ExistingID != hpRow.ID {
		t.Errorf("refresh must reuse the existing row id (no duplicate); got %v want %v", a.ExistingID, hpRow.ID)
	}
}

// TestSeedPlan_IdempotentWhenInSync: once a built-in row matches the catalog
// (incl. exclusions), a re-seed plans it as up-to-date — no redundant write.
func TestSeedPlan_IdempotentWhenInSync(t *testing.T) {
	inSync := fpRow("oid", "1.3.6.1.4.1.11", "Aruba/HPE", "switch", 78, "builtin",
		[]byte(`[{"kind":"oid","pattern":"1.3.6.1.4.1.11.2.3.9"}]`))
	lib := []fingerprint.Print{{
		Kind: fingerprint.KindOID, Pattern: "1.3.6.1.4.1.11", Vendor: "Aruba/HPE", DeviceType: "switch", Confidence: 78,
		Exclusions: []fingerprint.Exclusion{{Kind: fingerprint.KindOID, Pattern: "1.3.6.1.4.1.11.2.3.9"}},
	}}
	a, _ := planByPattern(planBuiltinSeed([]db.VendorFingerprint{inSync}, lib), "1.3.6.1.4.1.11")
	if a.Action != seedUpToDate {
		t.Fatalf("expected up-to-date (no write) when row matches catalog, got %v", a.Action)
	}
}

// TestSeedPlan_PreservesOperatorRow: a 'user' row that shares a built-in
// (kind,pattern) is PRESERVED — the operator's rule is authoritative and must
// not be clobbered by built-in metadata.
func TestSeedPlan_PreservesOperatorRow(t *testing.T) {
	userRow := fpRow("oid", "1.3.6.1.4.1.11", "MyVendor", "router", 99, "user",
		[]byte(`[{"kind":"service","pattern":"custom"}]`))
	lib := []fingerprint.Print{{
		Kind: fingerprint.KindOID, Pattern: "1.3.6.1.4.1.11", Vendor: "Aruba/HPE", DeviceType: "switch", Confidence: 78,
		Exclusions: []fingerprint.Exclusion{{Kind: fingerprint.KindOID, Pattern: "1.3.6.1.4.1.11.2.3.9"}},
	}}
	a, _ := planByPattern(planBuiltinSeed([]db.VendorFingerprint{userRow}, lib), "1.3.6.1.4.1.11")
	if a.Action != seedPreserve {
		t.Fatalf("expected operator row PRESERVED, got %v", a.Action)
	}
}

// TestSeedPlan_CreatesMissingNoDuplicates: a catalog pattern absent from the DB
// is CREATE; a present one is never CREATE (no duplicate rows). Verified by
// counting actions across a mixed library.
func TestSeedPlan_CreatesMissingNoDuplicates(t *testing.T) {
	existing := []db.VendorFingerprint{
		fpRow("oid", "1.3.6.1.4.1.11", "Aruba/HPE", "switch", 78, "builtin", []byte("[]")),
	}
	lib := []fingerprint.Print{
		{Kind: "oid", Pattern: "1.3.6.1.4.1.11", Vendor: "Aruba/HPE", DeviceType: "switch", Confidence: 78},   // present → not create
		{Kind: "oid", Pattern: "1.3.6.1.4.1.9999", Vendor: "NewVendor", DeviceType: "router", Confidence: 80}, // absent → create
	}
	plan := planBuiltinSeed(existing, lib)
	creates := 0
	for _, a := range plan {
		if a.Action == seedCreate {
			creates++
			if a.Print.Pattern != "1.3.6.1.4.1.9999" {
				t.Errorf("create planned for an already-present pattern %q (would duplicate)", a.Print.Pattern)
			}
		}
	}
	if creates != 1 {
		t.Fatalf("expected exactly 1 create (the missing pattern), got %d", creates)
	}
}

// TestFpExclusionsJSONRoundTrip proves operator-defined exclusions survive the
// JSONB column round-trip (Create/Update store fpExclusionsJSON, dbToPrints reads
// fpExclusionsFromJSON) with kind + pattern intact — so a saved exclusion still
// suppresses its rule after a reload.
func TestFpExclusionsJSONRoundTrip(t *testing.T) {
	in := []fingerprint.Exclusion{
		{Kind: fingerprint.KindOID, Pattern: "1.3.6.1.4.1.11.2.3.9"},
		{Kind: fingerprint.KindService, Pattern: "jetdirect"},
	}
	got := fpExclusionsFromJSON(fpExclusionsJSON(in))
	if len(got) != 2 {
		t.Fatalf("expected 2 exclusions after round-trip, got %d (%+v)", len(got), got)
	}
	if got[0].Kind != fingerprint.KindOID || got[0].Pattern != "1.3.6.1.4.1.11.2.3.9" {
		t.Errorf("exclusion[0] mangled: %+v", got[0])
	}
	if got[1].Kind != fingerprint.KindService || got[1].Pattern != "jetdirect" {
		t.Errorf("exclusion[1] mangled: %+v", got[1])
	}
}

// TestFpExclusionsJSONEmptyNormalizesToBracket: nil/empty must serialize to "[]"
// to satisfy the column's NOT NULL DEFAULT '[]' and round-trip cleanly (no nulls
// in the JSONB column).
func TestFpExclusionsJSONEmptyNormalizesToBracket(t *testing.T) {
	for _, in := range [][]fingerprint.Exclusion{nil, {}} {
		if s := string(fpExclusionsJSON(in)); s != "[]" {
			t.Errorf("expected \"[]\" for empty input, got %q", s)
		}
	}
}

// TestFpExclusionsFromJSONMalformedIsSafe: a malformed/empty blob must yield no
// exclusions (the rule fires unconditionally) rather than erroring — classification
// must never break because a stored exclusions blob is corrupt.
func TestFpExclusionsFromJSONMalformedIsSafe(t *testing.T) {
	for _, b := range [][]byte{nil, []byte(""), []byte("not json"), []byte("{")} {
		if got := fpExclusionsFromJSON(b); got != nil {
			t.Errorf("expected nil for malformed/empty %q, got %+v", string(b), got)
		}
	}
}

// --- Phase 5 SC3: API response shape (structured exclusions, never base64) ---

// TestVendorFingerprintDTO_StructuredNotBase64: the list/create/update DTO must
// expose exclusions as a JSON array, NOT the raw db []byte (which would base64).
func TestVendorFingerprintDTO_StructuredNotBase64(t *testing.T) {
	row := fpRow("oid", "1.3.6.1.4.1.11", "Aruba/HPE", "switch", 78, "builtin",
		[]byte(`[{"kind":"oid","pattern":"1.3.6.1.4.1.11.2.3.9"},{"kind":"service","pattern":"jetdirect"}]`))
	dto := toVendorFingerprintDTO(row)
	if len(dto.Exclusions) != 2 || dto.Exclusions[0].Pattern != "1.3.6.1.4.1.11.2.3.9" || dto.Exclusions[1].Kind != fingerprint.KindService {
		t.Fatalf("structured exclusions not decoded: %+v", dto.Exclusions)
	}
	b, err := json.Marshal(dto)
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	if !strings.Contains(s, `"exclusions":[{`) {
		t.Errorf("exclusions must marshal as a JSON array, got: %s", s)
	}
	if strings.Contains(s, `"exclusions":"`) { // a base64 string would look like this
		t.Errorf("exclusions must NOT be a base64 string: %s", s)
	}
}

// TestVendorFingerprintDTO_EmptyIsArrayNotNull: rows with []/nil/malformed
// exclusions emit `[]` (non-nil, not base64, not null) so the UI shows "no
// exclusions" cleanly and never crashes.
func TestVendorFingerprintDTO_EmptyIsArrayNotNull(t *testing.T) {
	for _, blob := range [][]byte{[]byte("[]"), nil, []byte("not json")} {
		dto := toVendorFingerprintDTO(fpRow("oid", "1.2.3", "V", "switch", 50, "builtin", blob))
		if dto.Exclusions == nil {
			t.Errorf("blob %q: exclusions must be a non-nil empty slice", string(blob))
		}
		b, _ := json.Marshal(dto)
		if !strings.Contains(string(b), `"exclusions":[]`) {
			t.Errorf("blob %q: empty exclusions must marshal to []: %s", string(blob), string(b))
		}
	}
}
