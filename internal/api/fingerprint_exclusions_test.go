package api

import (
	"testing"

	"github.com/coralsearesorts/hims/internal/fingerprint"
)

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
