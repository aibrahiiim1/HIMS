package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/coralsearesorts/hims/internal/backup"
	"github.com/coralsearesorts/hims/internal/storage/postgres/db"
	"github.com/jackc/pgx/v5"
)

// Backup & Restore (#25). HIMS produces a portable configuration snapshot
// (JSON, no raw secrets) in-process, validates uploaded snapshots, tracks
// backup runs, and reports DR readiness. A full database backup with encrypted
// secrets is an operator pg_dump (DR runbook) — the readiness checklist insists
// the encryption key is backed up off-box, since secrets are useless without it.

func rawJSON(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		return json.RawMessage("[]")
	}
	return b
}

// exportBackup handles GET /admin/backup/export — builds + streams a config
// snapshot and records a backup run. Credentials are exported as metadata only
// (no encrypted blob); notification-channel + config-backup secrets are
// excluded (recover those from a full pg_dump).
func (s *Server) exportBackup(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	tables := map[string]json.RawMessage{}

	add := func(name string, fetch func() (any, error)) error {
		v, err := fetch()
		if err != nil {
			return err
		}
		tables[name] = rawJSON(v)
		return nil
	}

	type step struct {
		name  string
		fetch func() (any, error)
	}
	steps := []step{
		{"devices", func() (any, error) { return s.queries.ListAllDevices(ctx) }},
		{"locations", func() (any, error) { return s.queries.ListLocations(ctx) }},
		{"device_lifecycle", func() (any, error) { return s.queries.ListAssetLifecycle(ctx) }},
		{"work_orders", func() (any, error) { return s.queries.ListWorkOrders(ctx) }},
		{"systems", func() (any, error) { return s.queries.ListSystems(ctx) }},
		{"alert_rules", func() (any, error) { return s.queries.ListAlertRules(ctx) }},
		{"vendor_fingerprints", func() (any, error) { return s.queries.ListVendorFingerprints(ctx) }},
		{"device_templates", func() (any, error) { return s.queries.ListDeviceTemplates(ctx) }},
		{"report_schedules", func() (any, error) { return s.queries.ListReportSchedules(ctx) }},
		{"users", func() (any, error) { return s.queries.ListUsers(ctx) }},
		{"roles", func() (any, error) { return s.queries.ListRoles(ctx) }},
		{"permissions", func() (any, error) { return s.queries.ListPermissions(ctx) }},
		{"credentials", func() (any, error) { // metadata only — never the blob
			rows, err := s.queries.ListCredentials(ctx)
			if err != nil {
				return nil, err
			}
			out := make([]credentialDTO, len(rows))
			for i, c := range rows {
				out[i] = toCredentialDTO(c)
			}
			return out, nil
		}},
	}
	for _, st := range steps {
		if err := add(st.name, st.fetch); err != nil {
			writeErr(w, err)
			return
		}
	}

	data, nt, nr, err := backup.Build(tables, time.Now().UTC())
	if err != nil {
		writeErr(w, err)
		return
	}
	actor := r.Header.Get("X-Actor")
	if actor == "" {
		actor = "operator"
	}
	_, _ = s.queries.InsertBackupRun(ctx, db.InsertBackupRunParams{
		Kind: "config_export", Status: "success", Tables: int32(nt), Rows: int32(nr),
		SizeBytes: int64(len(data)), Actor: actor, Detail: "configuration snapshot",
	})
	s.audit(r, "config", "backup.export", "backup", "", fmt.Sprintf("Exported config snapshot (%d tables, %d rows)", nt, nr), nil)
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", "attachment; filename=\"hims-config-"+time.Now().UTC().Format("20060102-1504")+".json\"")
	_, _ = w.Write(data)
}

// validateBackup handles POST /admin/backup/validate — parses an uploaded
// archive (raw JSON body) and reports its structure. Read-only.
func (s *Server) validateBackup(w http.ResponseWriter, r *http.Request) {
	data, err := io.ReadAll(io.LimitReader(r.Body, 64<<20))
	if err != nil {
		writeErr(w, err)
		return
	}
	summary, err := backup.Validate(data)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "summary": summary})
}

func (s *Server) listBackupRuns(w http.ResponseWriter, r *http.Request) {
	rows, err := s.queries.ListBackupRuns(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rows)
}

type recordExternalReq struct {
	SizeBytes int64  `json:"size_bytes"`
	Detail    string `json:"detail"`
}

// recordExternalBackup handles POST /admin/backup/record-external — an operator
// logs a full pg_dump they ran off-box, so DR readiness reflects real backups.
func (s *Server) recordExternalBackup(w http.ResponseWriter, r *http.Request) {
	var req recordExternalReq
	if !decodeJSON(w, r, &req) {
		return
	}
	actor := r.Header.Get("X-Actor")
	if actor == "" {
		actor = "operator"
	}
	row, err := s.queries.InsertBackupRun(r.Context(), db.InsertBackupRunParams{
		Kind: "external_pg_dump", Status: "success", SizeBytes: req.SizeBytes, Actor: actor,
		Detail: orDefault(req.Detail, "external pg_dump"),
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	s.audit(r, "config", "backup.record_external", "backup", "", "Recorded external pg_dump backup", nil)
	writeJSON(w, http.StatusCreated, row)
}

// drReadiness handles GET /admin/dr-readiness — real recovery-readiness signals
// + a DR checklist.
func (s *Server) drReadiness(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	keyLoaded := false
	fingerprint := ""
	if c := s.cipher(); c != nil {
		keyLoaded = true
		fingerprint = c.Fingerprint()
	}

	var lastAt *time.Time
	lastKind := ""
	if last, err := s.queries.LastSuccessfulBackup(ctx); err == nil {
		lastAt = &last.At
		lastKind = last.Kind
	} else if !errors.Is(err, pgx.ErrNoRows) {
		writeErr(w, err)
		return
	}
	recentBackup := false
	var ageHours float64 = -1
	if lastAt != nil {
		ageHours = time.Since(*lastAt).Hours()
		recentBackup = ageHours <= 24*7
	}

	devCount := countOrZero(s.queries.ListAllDevices(ctx))
	credCount := countOrZero(s.queries.ListCredentials(ctx))

	type check struct {
		Item string `json:"item"`
		OK   bool   `json:"ok"`
		Note string `json:"note"`
	}
	checklist := []check{
		{"Database reachable", true, "API is querying Postgres"},
		{"Encryption key loaded & fingerprinted", keyLoaded, ternary(keyLoaded, "key present: "+fingerprint, "no key — credential secrets cannot be decrypted")},
		{"Recent backup (≤ 7 days)", recentBackup, ternary(lastAt != nil, lastKind, "no backup recorded yet")},
		{"Encryption key backed up off-box", false, "Manual: store HIMS_ENCRYPTION_KEY in your secrets manager — encrypted secrets are unrecoverable without it"},
		{"Full DB pg_dump scheduled off-box", lastKind == "external_pg_dump", "Run pg_dump of the HIMS database to off-site storage; record it here"},
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"db_connected":     true,
		"key_loaded":       keyLoaded,
		"key_fingerprint":  fingerprint,
		"last_backup_at":   lastAt,
		"last_backup_kind": lastKind,
		"last_backup_age_hours": ageHours,
		"recent_backup":    recentBackup,
		"device_count":     devCount,
		"credential_count": credCount,
		"checklist":        checklist,
	})
}

func countOrZero[T any](v []T, err error) int {
	if err != nil {
		return 0
	}
	return len(v)
}

func ternary(cond bool, a, b string) string {
	if cond {
		return a
	}
	return b
}
