package backup

import (
	"encoding/json"
	"testing"
	"time"
)

func TestBuildAndValidateRoundTrip(t *testing.T) {
	tables := map[string]json.RawMessage{
		"devices":   json.RawMessage(`[{"id":"a"},{"id":"b"},{"id":"c"}]`),
		"locations": json.RawMessage(`[{"id":"x"}]`),
		"empty":     json.RawMessage(`[]`),
	}
	data, nt, nr, err := Build(tables, time.Date(2026, 6, 4, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if nt != 3 || nr != 4 {
		t.Fatalf("Build counts: tables=%d rows=%d, want 3/4", nt, nr)
	}
	s, err := Validate(data)
	if err != nil {
		t.Fatal(err)
	}
	if s.Format != FormatVersion || s.TotalRows != 4 || len(s.Tables) != 3 {
		t.Fatalf("summary = %+v", s)
	}
}

func TestValidateRejectsGarbage(t *testing.T) {
	if _, err := Validate([]byte("not json")); err == nil {
		t.Error("garbage should fail validation")
	}
	if _, err := Validate([]byte(`{"hello":"world"}`)); err == nil {
		t.Error("missing envelope should fail validation")
	}
	// A table that isn't an array must be rejected.
	bad := `{"meta":{"format":1},"tables":{"devices":{"not":"array"}}}`
	if _, err := Validate([]byte(bad)); err == nil {
		t.Error("non-array table should fail validation")
	}
}

func TestValidateRejectsNewerFormat(t *testing.T) {
	future := `{"meta":{"format":99},"tables":{}}`
	if _, err := Validate([]byte(future)); err == nil {
		t.Error("newer format should be rejected")
	}
}
