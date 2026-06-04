// Package backup builds and validates HIMS configuration snapshots — a
// portable JSON archive of the operational tables (inventory, locations,
// lifecycle, work orders, templates, fingerprints, alert rules, schedules,
// RBAC) used for migration, audit and restore validation (#25). Raw credential
// secrets are NOT included (they live only in the encrypted DB / full pg_dump);
// the archive is config + inventory, safe to download. Pure logic — the SQL
// extraction lives in the API layer.
package backup

import (
	"encoding/json"
	"fmt"
	"time"
)

// Format version of the archive envelope.
const FormatVersion = 1

// Archive is the snapshot envelope: metadata + one JSON array per table.
type Archive struct {
	Meta   Meta                       `json:"meta"`
	Tables map[string]json.RawMessage `json:"tables"`
}

// Meta describes how/when the archive was produced.
type Meta struct {
	Format    int       `json:"format"`
	Generated time.Time `json:"generated"`
	AppName   string    `json:"app"`
	Note      string    `json:"note"`
}

// TableSummary is the per-table row count discovered when validating.
type TableSummary struct {
	Table string `json:"table"`
	Rows  int    `json:"rows"`
}

// Summary is the result of validating an archive.
type Summary struct {
	Format    int            `json:"format"`
	Generated time.Time      `json:"generated"`
	Tables    []TableSummary `json:"tables"`
	TotalRows int            `json:"total_rows"`
}

// Build wraps per-table JSON arrays into an archive and marshals it (indented
// for human readability). Each value in tables must already be a JSON array.
func Build(tables map[string]json.RawMessage, generated time.Time) ([]byte, int, int, error) {
	a := Archive{
		Meta:   Meta{Format: FormatVersion, Generated: generated, AppName: "HIMS", Note: "configuration snapshot — excludes raw credential secrets"},
		Tables: tables,
	}
	b, err := json.MarshalIndent(a, "", "  ")
	if err != nil {
		return nil, 0, 0, err
	}
	// Count tables + rows for the run record.
	rows := 0
	for _, raw := range tables {
		var arr []json.RawMessage
		if json.Unmarshal(raw, &arr) == nil {
			rows += len(arr)
		}
	}
	return b, len(tables), rows, nil
}

// Validate parses an archive and reports its structure — used by the restore-
// validation endpoint to confirm a backup file is well-formed before an
// operator relies on it. It does not mutate anything.
func Validate(data []byte) (Summary, error) {
	var a Archive
	if err := json.Unmarshal(data, &a); err != nil {
		return Summary{}, fmt.Errorf("not a valid HIMS archive: %w", err)
	}
	if a.Meta.Format == 0 || a.Tables == nil {
		return Summary{}, fmt.Errorf("missing archive envelope (meta/tables)")
	}
	if a.Meta.Format > FormatVersion {
		return Summary{}, fmt.Errorf("archive format v%d is newer than supported v%d", a.Meta.Format, FormatVersion)
	}
	s := Summary{Format: a.Meta.Format, Generated: a.Meta.Generated}
	for name, raw := range a.Tables {
		var arr []json.RawMessage
		if err := json.Unmarshal(raw, &arr); err != nil {
			return Summary{}, fmt.Errorf("table %q is not a JSON array: %w", name, err)
		}
		s.Tables = append(s.Tables, TableSummary{Table: name, Rows: len(arr)})
		s.TotalRows += len(arr)
	}
	return s, nil
}
