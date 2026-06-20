package api

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/coralsearesorts/hims/internal/storage/postgres/db"
)

// Database reset / initialize. Lets an admin wipe selected categories of data so the database can be
// returned to an empty state. DESTRUCTIVE + irreversible — gated to the rbac.manage permission (the
// /admin/* prefix), requires an explicit confirm token, runs all selected categories in ONE
// transaction (all-or-nothing), and is audited. Identity/auth (users, roles, sessions), the
// encryption key binding, schema migrations, app settings, and site/subnet definitions are NEVER
// wiped here — clearing them would lock the operator out or break the install.

// resetCategory: ordered SQL run for one category. DELETE (not TRUNCATE) so FK ON DELETE
// CASCADE / SET NULL behave correctly and unselected categories aren't pulled in by a CASCADE.
type resetCategory struct {
	Key   string   // stable key used by the UI checkbox
	Label string   // human label
	Count string   // SQL counting the primary entity (for the pre-wipe summary)
	Stmts []string // ordered statements; DELETE rows are summed into the result
}

// resetCategories — ordered so cross-category FKs resolve (devices first: DELETE FROM devices
// cascades all device-scoped child tables; then the rest).
var resetCategories = []resetCategory{
	{Key: "devices", Label: "Devices & all collected inventory (topology, virtualization, CCTV, monitoring, OS, etc.)",
		Count: "SELECT count(*) FROM devices WHERE deleted_at IS NULL",
		Stmts: []string{"DELETE FROM devices"}}, // cascades interfaces/mac/arp/os_*/vh_*/vm_*/alerts/monitoring/...
	{Key: "topology", Label: "Topology / L2-L3 fabric only (interfaces, MAC, ARP, neighbors, VLANs) — keeps devices",
		Count: "SELECT count(*) FROM mac_addresses",
		Stmts: []string{"DELETE FROM topology_links", "DELETE FROM mac_addresses", "DELETE FROM arp_entries", "DELETE FROM neighbors", "DELETE FROM vlans", "DELETE FROM port_vlans", "DELETE FROM interfaces"}},
	{Key: "monitoring", Label: "Monitoring checks & samples",
		Count: "SELECT count(*) FROM monitoring_samples",
		Stmts: []string{"DELETE FROM monitoring_samples", "DELETE FROM monitoring_checks"}},
	{Key: "alerts", Label: "Alerts, alert rules, maintenance windows, snoozes",
		Count: "SELECT count(*) FROM alerts",
		Stmts: []string{"DELETE FROM alert_events", "DELETE FROM alerts", "DELETE FROM alert_rules", "DELETE FROM maintenance_windows", "DELETE FROM remediation_snoozes"}},
	{Key: "discovery", Label: "Discovery jobs, results & events",
		Count: "SELECT count(*) FROM discovery_results",
		Stmts: []string{"DELETE FROM discovery_job_events", "DELETE FROM discovery_results", "DELETE FROM discovery_jobs"}},
	{Key: "credentials", Label: "Credentials, groups, bindings, subnet creds, vendor profiles & credential-test history",
		Count: "SELECT count(*) FROM credentials",
		Stmts: []string{"UPDATE devices SET credential_id = NULL", "DELETE FROM credential_test_results", "DELETE FROM credential_test_runs", "DELETE FROM credential_bindings", "DELETE FROM credential_group_members", "DELETE FROM credential_groups", "DELETE FROM subnet_credentials", "DELETE FROM vendor_connection_profiles", "DELETE FROM credentials"}},
	{Key: "logs", Label: "Audit log, notification log, agent jobs, wireless events",
		Count: "SELECT count(*) FROM audit_log",
		Stmts: []string{"DELETE FROM audit_log", "DELETE FROM notification_log", "DELETE FROM agent_jobs", "DELETE FROM wireless_events"}},
	{Key: "netflow", Label: "NetFlow records",
		Count: "SELECT count(*) FROM flow_records",
		Stmts: []string{"DELETE FROM flow_records"}},
	{Key: "workorders", Label: "Work orders (incl. events & parts)",
		Count: "SELECT count(*) FROM work_orders",
		Stmts: []string{"DELETE FROM work_order_parts", "DELETE FROM work_order_events", "DELETE FROM work_orders"}},
	// NOTE: backups (backup_runs / config_backups) are deliberately NOT wipeable here — the reset must
	// never delete the backup history or the automatic pre-reset safety backup it just took. Manage
	// backups individually from Backup History instead.
}

func resetCategoryByKey(k string) *resetCategory {
	for i := range resetCategories {
		if resetCategories[i].Key == k {
			return &resetCategories[i]
		}
	}
	return nil
}

// databaseSummary (GET /admin/database/summary) lists each wipeable category + its current row
// count, so the UI can show "Devices (552)" beside each checkbox before anything is deleted.
func (s *Server) databaseSummary(w http.ResponseWriter, r *http.Request) {
	if s.pool == nil {
		http.Error(w, "raw DB pool not configured", http.StatusServiceUnavailable)
		return
	}
	ctx := r.Context()
	out := make([]map[string]any, 0, len(resetCategories))
	for _, c := range resetCategories {
		var n int64
		_ = s.pool.QueryRow(ctx, c.Count).Scan(&n)
		out = append(out, map[string]any{"key": c.Key, "label": c.Label, "count": n})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"categories": out,
		"protected":  []string{"users", "roles", "permissions", "sessions", "encryption key", "schema migrations", "app settings", "locations/subnets"},
	})
}

// resetDatabase (POST /admin/database/reset) wipes the selected categories. Body:
// {"categories":["devices","alerts"],"confirm":"ERASE"}. All selected categories run in one
// transaction — if any statement fails, nothing is deleted.
func (s *Server) resetDatabase(w http.ResponseWriter, r *http.Request) {
	if s.pool == nil {
		http.Error(w, "raw DB pool not configured", http.StatusServiceUnavailable)
		return
	}
	var req struct {
		Categories []string `json:"categories"`
		Confirm    string   `json:"confirm"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}
	if strings.ToUpper(strings.TrimSpace(req.Confirm)) != "ERASE" {
		http.Error(w, `confirmation required: send {"confirm":"ERASE"}`, http.StatusBadRequest)
		return
	}
	if len(req.Categories) == 0 {
		http.Error(w, "select at least one category to wipe", http.StatusBadRequest)
		return
	}
	// Resolve + validate selection, preserving the safe execution order in resetCategories.
	selected := map[string]bool{}
	for _, k := range req.Categories {
		if resetCategoryByKey(k) == nil {
			http.Error(w, "unknown category: "+k, http.StatusBadRequest)
			return
		}
		selected[k] = true
	}

	ctx := r.Context()

	// Safety: take a configuration snapshot FIRST and store it as a downloadable backup run, so the
	// wipe is always recoverable to the extent the snapshot covers. If the backup itself fails, abort
	// the whole reset — never wipe without a backup.
	var backupID int64
	if data, nt, nr, berr := s.buildConfigSnapshot(ctx); berr != nil {
		http.Error(w, "pre-reset backup failed: "+berr.Error()+" — nothing was deleted", http.StatusInternalServerError)
		return
	} else if run, ierr := s.queries.InsertBackupRunWithContent(ctx, db.InsertBackupRunWithContentParams{
		Kind: "pre_reset", Status: "success", Tables: int32(nt), Rows: int32(nr),
		SizeBytes: int64(len(data)), Actor: s.actor(r), Detail: "automatic backup before database reset", Content: data,
	}); ierr != nil {
		http.Error(w, "pre-reset backup could not be saved: "+ierr.Error()+" — nothing was deleted", http.StatusInternalServerError)
		return
	} else {
		backupID = run.ID
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		writeErr(w, err)
		return
	}
	defer tx.Rollback(ctx)

	counts := map[string]int64{}
	for _, c := range resetCategories { // ordered
		if !selected[c.Key] {
			continue
		}
		var n int64
		for _, stmt := range c.Stmts {
			tag, err := tx.Exec(ctx, stmt)
			if err != nil {
				http.Error(w, "reset failed on ["+c.Key+"]: "+err.Error()+" — nothing was deleted (rolled back)", http.StatusInternalServerError)
				return
			}
			if strings.HasPrefix(stmt, "DELETE") {
				n += tag.RowsAffected()
			}
		}
		counts[c.Key] = n
	}
	if err := tx.Commit(ctx); err != nil {
		writeErr(w, err)
		return
	}

	s.audit(r, "admin", "database.reset", "database", "", "Database reset: wiped "+strings.Join(req.Categories, ", "), map[string]any{"categories": req.Categories, "counts": counts, "backup_id": backupID})
	writeJSON(w, http.StatusOK, map[string]any{"reset": true, "deleted": counts, "backup_id": backupID})
}
