package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/coralsearesorts/hims/internal/backup"
	"github.com/jackc/pgx/v5"
)

// Selective restore. The operator uploads a HIMS config snapshot, picks which tables to restore,
// and the server upserts only those tables back into the live database. Safety:
//   - Only allow-listed tables are restorable (config/inventory data) — never users/roles/permissions
//     (auth) or credentials (no secret is in the snapshot, so they can't be meaningfully restored).
//   - Column + primary-key names are validated against the LIVE schema (no SQL injection from a
//     crafted file); rows upsert on the real PK.
//   - Tables run in FK-safe order; a row that violates a constraint (e.g. references a device/location
//     not restored) is skipped and counted, never aborting the whole restore.

// restoreOrder is the FK-safe order; the map value is the table's allow-listed flag. Parents first.
var restoreOrder = []string{
	"locations", "device_templates", "vendor_fingerprints", "systems",
	"devices", "device_lifecycle", "work_orders", "alert_rules", "report_schedules",
}

func isRestorable(t string) bool {
	for _, x := range restoreOrder {
		if x == t {
			return true
		}
	}
	return false
}

// restoreBackup handles POST /admin/backup/restore?tables=devices,locations — body is the raw
// snapshot JSON. Restores the selected (allow-listed) tables and returns per-table counts.
func (s *Server) restoreBackup(w http.ResponseWriter, r *http.Request) {
	if s.pool == nil {
		http.Error(w, "raw DB pool not configured", http.StatusServiceUnavailable)
		return
	}
	sel := map[string]bool{}
	for _, t := range strings.Split(r.URL.Query().Get("tables"), ",") {
		if t = strings.TrimSpace(t); t != "" {
			sel[t] = true
		}
	}
	if len(sel) == 0 {
		http.Error(w, "select at least one table to restore (?tables=...)", http.StatusBadRequest)
		return
	}
	for t := range sel {
		if !isRestorable(t) {
			http.Error(w, "table not restorable: "+t+" (auth/credential tables are excluded)", http.StatusBadRequest)
			return
		}
	}
	data, err := io.ReadAll(io.LimitReader(r.Body, 128<<20))
	if err != nil {
		writeErr(w, err)
		return
	}
	if _, err := backup.Validate(data); err != nil {
		http.Error(w, "invalid backup archive: "+err.Error(), http.StatusBadRequest)
		return
	}
	var arc struct {
		Tables map[string]json.RawMessage `json:"tables"`
	}
	if err := json.Unmarshal(data, &arc); err != nil {
		http.Error(w, "invalid archive: "+err.Error(), http.StatusBadRequest)
		return
	}

	ctx := r.Context()
	results := make([]map[string]any, 0, len(sel))
	for _, table := range restoreOrder { // FK-safe order
		if !sel[table] {
			continue
		}
		raw, ok := arc.Tables[table]
		if !ok {
			results = append(results, map[string]any{"table": table, "restored": 0, "skipped": 0, "error": "not present in this backup"})
			continue
		}
		var rows []map[string]any
		if err := json.Unmarshal(raw, &rows); err != nil {
			results = append(results, map[string]any{"table": table, "restored": 0, "skipped": 0, "error": "rows not a JSON array: " + err.Error()})
			continue
		}
		restored, skipped, firstErr := s.restoreTable(ctx, table, rows)
		results = append(results, map[string]any{"table": table, "restored": restored, "skipped": skipped, "error": firstErr})
	}
	s.audit(r, "config", "backup.restore", "backup", "", "Restored tables: "+strings.Join(keysOf(sel), ", "), map[string]any{"results": results})
	writeJSON(w, http.StatusOK, map[string]any{"restored": true, "results": results})
}

// restoreTable upserts rows into one table, validating columns/PK against the live schema.
func (s *Server) restoreTable(ctx context.Context, table string, rows []map[string]any) (restored, skipped int, firstErr string) {
	cols, jsonb, nullableFK, pk, err := s.tableSchema(ctx, table)
	if err != nil || pk == "" {
		return 0, len(rows), "could not read live schema for " + table
	}
	for _, row := range rows {
		var names []string
		var args []any
		for k, v := range row {
			if !cols[k] {
				continue // ignore keys not present in the live schema
			}
			names = append(names, k)
			args = append(args, coerceValue(v, jsonb[k]))
		}
		if len(names) == 0 {
			skipped++
			continue
		}
		colList := make([]string, len(names))
		phList := make([]string, len(names))
		setList := make([]string, 0, len(names))
		for i, n := range names {
			q := pgx.Identifier{n}.Sanitize()
			colList[i] = q
			phList[i] = "$" + itoa(i+1)
			if n != pk {
				setList = append(setList, q+" = EXCLUDED."+q)
			}
		}
		conflict := " ON CONFLICT (" + pgx.Identifier{pk}.Sanitize() + ") DO "
		if len(setList) == 0 {
			conflict += "NOTHING"
		} else {
			conflict += "UPDATE SET " + strings.Join(setList, ", ")
		}
		sql := "INSERT INTO " + pgx.Identifier{table}.Sanitize() + " (" + strings.Join(colList, ", ") + ") VALUES (" + strings.Join(phList, ", ") + ")" + conflict
		_, err := s.pool.Exec(ctx, sql, args...)
		if err != nil && strings.Contains(err.Error(), "foreign key") {
			// A referenced parent row wasn't restored (e.g. credentials weren't selected). Null the
			// nullable FK columns and retry once — keep the row, drop the dangling link. It re-binds
			// later (e.g. on the next successful collection).
			changed := false
			for i, n := range names {
				if nullableFK[n] && args[i] != nil {
					args[i] = nil
					changed = true
				}
			}
			if changed {
				_, err = s.pool.Exec(ctx, sql, args...)
			}
		}
		if err != nil {
			skipped++
			if firstErr == "" {
				firstErr = shortErr(err)
			}
			continue
		}
		restored++
	}
	return restored, skipped, firstErr
}

// tableSchema returns the live column set, the jsonb-column set, the nullable foreign-key column set,
// and the single-column primary key.
func (s *Server) tableSchema(ctx context.Context, table string) (cols, jsonb, nullableFK map[string]bool, pk string, err error) {
	cols, jsonb, nullableFK = map[string]bool{}, map[string]bool{}, map[string]bool{}
	rows, err := s.pool.Query(ctx, "SELECT column_name, data_type FROM information_schema.columns WHERE table_schema='public' AND table_name=$1", table)
	if err != nil {
		return nil, nil, nil, "", err
	}
	for rows.Next() {
		var c, dt string
		if rows.Scan(&c, &dt) == nil {
			cols[c] = true
			if dt == "jsonb" || dt == "json" {
				jsonb[c] = true
			}
		}
	}
	rows.Close()
	// Nullable FK columns — safe to null on a dangling reference during restore.
	if fkRows, e := s.pool.Query(ctx,
		`SELECT a.attname FROM pg_constraint con
		 JOIN pg_attribute a ON a.attrelid=con.conrelid AND a.attnum=ANY(con.conkey)
		 WHERE con.contype='f' AND con.conrelid=$1::regclass AND a.attnotnull=false`, table); e == nil {
		for fkRows.Next() {
			var c string
			if fkRows.Scan(&c) == nil {
				nullableFK[c] = true
			}
		}
		fkRows.Close()
	}
	_ = s.pool.QueryRow(ctx,
		`SELECT a.attname FROM pg_index i JOIN pg_attribute a ON a.attrelid=i.indrelid AND a.attnum=ANY(i.indkey)
		 WHERE i.indrelid=$1::regclass AND i.indisprimary LIMIT 1`, table).Scan(&pk)
	return cols, jsonb, nullableFK, pk, nil
}

// coerceValue prepares a JSON value for a parameterized INSERT. jsonb columns ALWAYS get a JSON
// literal (so a scalar like a string becomes a valid json document); nested objects/arrays are
// re-marshalled too; other scalars pass through (Postgres applies text/numeric→target assignment
// casts for uuid/timestamptz/inet/int/etc.). nil stays NULL.
func coerceValue(v any, isJSONB bool) any {
	if v == nil {
		return nil
	}
	if isJSONB {
		// sqlc models jsonb columns as []byte, which Go's json.Marshal encodes as a base64 STRING in
		// the snapshot (e.g. "W10=" == "[]"). Reverse that: if a string base64-decodes to valid JSON,
		// use the decoded JSON document; otherwise marshal the value as a JSON literal.
		if str, ok := v.(string); ok {
			if dec, derr := base64.StdEncoding.DecodeString(str); derr == nil && json.Valid(dec) {
				return string(dec)
			}
		}
		b, err := json.Marshal(v)
		if err != nil {
			return nil
		}
		return string(b)
	}
	switch v.(type) {
	case map[string]any, []any:
		b, err := json.Marshal(v)
		if err != nil {
			return nil
		}
		return string(b)
	default:
		return v
	}
}

func keysOf(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
