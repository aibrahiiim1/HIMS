package api

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/coralsearesorts/hims/internal/config"
	"github.com/coralsearesorts/hims/internal/ssh"
	"github.com/coralsearesorts/hims/internal/storage/postgres/db"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// configBackupDTO is the metadata-only view of a backup. The config content
// itself (which can contain communities/keys/hashes) is NEVER in this shape —
// it is only ever returned by the explicit, key-gated content/diff endpoints.
type configBackupDTO struct {
	ID         string `json:"id"`
	DeviceID   string `json:"device_id"`
	CapturedAt string `json:"captured_at"`
	CapturedBy string `json:"captured_by"`
	Source     string `json:"source"`
	Driver     string `json:"driver"`
	Command    string `json:"command"`
	Sha256     string `json:"sha256"`
	SizeBytes  int    `json:"size_bytes"`
	Changed    bool   `json:"changed"`
}

func rfc3339(t time.Time) string { return t.Format("2006-01-02T15:04:05Z07:00") }

// splitUserPass splits a "username:password" credential secret on the first
// colon. With no colon the whole string is the username (empty password).
func splitUserPass(s string) (user, pass string) {
	if i := strings.IndexByte(s, ':'); i >= 0 {
		return s[:i], s[i+1:]
	}
	return s, ""
}

// captureConfigBackup handles POST /devices/{id}/config-backups — pulls the
// running-config over SSH using the device's bound credential, stores it
// AES-256-GCM encrypted, and flags whether it differs from the previous capture
// (the basis for drift, #11). The protocol work (which command, legacy KEX
// retry) is automatic; the operator only binds a credential.
func (s *Server) captureConfigBackup(w http.ResponseWriter, r *http.Request) {
	ctx, id, ok := pathDevice(w, r)
	if !ok {
		return
	}
	cph := s.cipher()
	if cph == nil {
		http.Error(w, "encryption key not configured (set HIMS_ENCRYPTION_KEY / unlock)", http.StatusServiceUnavailable)
		return
	}
	dev, err := s.queries.GetDevice(ctx, id)
	if err != nil {
		writeErr(w, err)
		return
	}
	driver := ""
	if dev.Driver != nil {
		driver = *dev.Driver
	}
	cmd := config.CommandFor(driver)
	if cmd == "" {
		http.Error(w, fmt.Sprintf("no config-backup command for driver %q (supported: cisco_ios, aruba_hpe, extreme_switch, fortigate, huawei_vrp, …)", driver), http.StatusBadRequest)
		return
	}
	if dev.PrimaryIp == nil {
		http.Error(w, "device has no primary IP to connect to", http.StatusBadRequest)
		return
	}
	if dev.CredentialID == nil {
		http.Error(w, "device has no bound credential — bind an 'ssh' credential first", http.StatusBadRequest)
		return
	}
	cred, err := s.queries.GetCredential(ctx, *dev.CredentialID)
	if err != nil {
		writeErr(w, err)
		return
	}
	if cred.Kind != "ssh" {
		http.Error(w, fmt.Sprintf("bound credential is %q, need an 'ssh' credential for config backup", cred.Kind), http.StatusBadRequest)
		return
	}
	plain, err := cph.Open(cred.EncryptedBlob, cred.KeyID)
	if err != nil {
		http.Error(w, "could not decrypt credential (key mismatch?)", http.StatusServiceUnavailable)
		return
	}
	user, pass := splitUserPass(string(plain))
	host := dev.PrimaryIp.String()

	// First try modern algorithms; on a handshake failure retry with legacy
	// KEX/ciphers for old switches. The password is never logged.
	creds := ssh.Creds{Username: user, Password: pass}
	content, sshErr := ssh.Run(ctx, host, 22, creds, false, cmd, 25*time.Second)
	if sshErr != nil && strings.Contains(sshErr.Error(), "handshake") {
		content, sshErr = ssh.Run(ctx, host, 22, creds, true, cmd, 25*time.Second)
	}
	// plain/user/pass go out of scope here; never logged or returned.
	if sshErr != nil {
		// The error string is transport-level (dial/handshake/auth); it never
		// contains the password.
		http.Error(w, "ssh capture failed: "+sshErr.Error(), http.StatusBadGateway)
		return
	}
	if strings.TrimSpace(content) == "" {
		http.Error(w, "device returned an empty config", http.StatusBadGateway)
		return
	}

	hash := config.Hash(content)
	changed := true
	if latest, err := s.queries.GetLatestConfigBackup(ctx, id); err == nil {
		changed = latest.Sha256 != hash
	} else if !errors.Is(err, pgx.ErrNoRows) {
		writeErr(w, err)
		return
	}

	blob, keyID, err := cph.Seal([]byte(content))
	if err != nil {
		writeErr(w, err)
		return
	}
	actor := r.Header.Get("X-Actor")
	if actor == "" {
		actor = "operator"
	}
	row, err := s.queries.InsertConfigBackup(ctx, db.InsertConfigBackupParams{
		DeviceID:         id,
		CapturedBy:       actor,
		Source:           "ssh",
		Driver:           driver,
		Command:          cmd,
		ContentEncrypted: blob,
		KeyID:            keyID,
		Sha256:           hash,
		SizeBytes:        int32(len(content)),
		Changed:          changed,
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	s.audit(r, "config", "config.backup.capture", "device", id.String(),
		fmt.Sprintf("Captured config backup for %s (%d bytes, changed=%v)", dev.Name, len(content), changed), nil)
	writeJSON(w, http.StatusCreated, configBackupDTO{
		ID: row.ID.String(), DeviceID: row.DeviceID.String(), CapturedAt: rfc3339(row.CapturedAt),
		CapturedBy: row.CapturedBy, Source: row.Source, Driver: row.Driver, Command: row.Command,
		Sha256: row.Sha256, SizeBytes: int(row.SizeBytes), Changed: row.Changed,
	})
}

// listDeviceConfigBackups handles GET /devices/{id}/config-backups — version
// history (metadata only, never the content).
func (s *Server) listDeviceConfigBackups(w http.ResponseWriter, r *http.Request) {
	ctx, id, ok := pathDevice(w, r)
	if !ok {
		return
	}
	limit := int32(50)
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 500 {
			limit = int32(n)
		}
	}
	rows, err := s.queries.ListConfigBackupsByDevice(ctx, db.ListConfigBackupsByDeviceParams{DeviceID: id, Limit: limit})
	if err != nil {
		writeErr(w, err)
		return
	}
	out := make([]configBackupDTO, len(rows))
	for i, b := range rows {
		out[i] = configBackupDTO{
			ID: b.ID.String(), DeviceID: b.DeviceID.String(), CapturedAt: rfc3339(b.CapturedAt),
			CapturedBy: b.CapturedBy, Source: b.Source, Driver: b.Driver, Command: b.Command,
			Sha256: b.Sha256, SizeBytes: int(b.SizeBytes), Changed: b.Changed,
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// getConfigBackupContent handles GET /config-backups/{id}/content — decrypts and
// returns one version's plaintext. Key-gated; the only path the config text
// ever leaves the server.
func (s *Server) getConfigBackupContent(w http.ResponseWriter, r *http.Request) {
	ctx, id, ok := pathUUID(w, r, "id")
	if !ok {
		return
	}
	cph := s.cipher()
	if cph == nil {
		http.Error(w, "encryption key not configured", http.StatusServiceUnavailable)
		return
	}
	row, err := s.queries.GetConfigBackupContent(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		writeErr(w, err)
		return
	}
	plain, err := cph.Open(row.ContentEncrypted, row.KeyID)
	if err != nil {
		http.Error(w, "could not decrypt config (sealed with a different key)", http.StatusServiceUnavailable)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"id":          row.ID.String(),
		"device_id":   row.DeviceID.String(),
		"captured_at": rfc3339(row.CapturedAt),
		"command":     row.Command,
		"driver":      row.Driver,
		"sha256":      row.Sha256,
		"content":     string(plain),
	})
}

// diffConfigBackups handles GET /config-backups/diff?a=&b= — unified line diff
// between two stored versions. Both are decrypted in memory; only the diff (and
// add/remove counts) is returned.
func (s *Server) diffConfigBackups(w http.ResponseWriter, r *http.Request) {
	cph := s.cipher()
	if cph == nil {
		http.Error(w, "encryption key not configured", http.StatusServiceUnavailable)
		return
	}
	aID, err := uuid.Parse(r.URL.Query().Get("a"))
	if err != nil {
		http.Error(w, "query param 'a' must be a backup UUID", http.StatusBadRequest)
		return
	}
	bID, err := uuid.Parse(r.URL.Query().Get("b"))
	if err != nil {
		http.Error(w, "query param 'b' must be a backup UUID", http.StatusBadRequest)
		return
	}
	open := func(id uuid.UUID) (db.GetConfigBackupContentRow, string, error) {
		row, err := s.queries.GetConfigBackupContent(r.Context(), id)
		if err != nil {
			return row, "", err
		}
		plain, err := cph.Open(row.ContentEncrypted, row.KeyID)
		return row, string(plain), err
	}
	aRow, aText, err := open(aID)
	if err != nil {
		http.Error(w, "could not load/decrypt backup a: "+err.Error(), http.StatusBadRequest)
		return
	}
	bRow, bText, err := open(bID)
	if err != nil {
		http.Error(w, "could not load/decrypt backup b: "+err.Error(), http.StatusBadRequest)
		return
	}
	lines, stat := config.Diff(aText, bText)
	writeJSON(w, http.StatusOK, map[string]any{
		"a":       map[string]string{"id": aRow.ID.String(), "captured_at": rfc3339(aRow.CapturedAt), "sha256": aRow.Sha256},
		"b":       map[string]string{"id": bRow.ID.String(), "captured_at": rfc3339(bRow.CapturedAt), "sha256": bRow.Sha256},
		"added":   stat.Added,
		"removed": stat.Removed,
		"lines":   lines,
	})
}

// configOverview handles GET /config/overview — fleet KPIs + recent captures
// for the Config Backup page.
func (s *Server) configOverview(w http.ResponseWriter, r *http.Request) {
	stats, err := s.queries.CountConfigBackupStats(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}
	recent, err := s.queries.ListRecentConfigBackups(r.Context(), 50)
	if err != nil {
		writeErr(w, err)
		return
	}
	type recentItem struct {
		ID         string `json:"id"`
		DeviceID   string `json:"device_id"`
		DeviceName string `json:"device_name"`
		CapturedAt string `json:"captured_at"`
		CapturedBy string `json:"captured_by"`
		Source     string `json:"source"`
		Driver     string `json:"driver"`
		Sha256     string `json:"sha256"`
		SizeBytes  int    `json:"size_bytes"`
		Changed    bool   `json:"changed"`
	}
	items := make([]recentItem, len(recent))
	for i, b := range recent {
		items[i] = recentItem{
			ID: b.ID.String(), DeviceID: b.DeviceID.String(), DeviceName: b.DeviceName,
			CapturedAt: rfc3339(b.CapturedAt), CapturedBy: b.CapturedBy, Source: b.Source,
			Driver: b.Driver, Sha256: b.Sha256, SizeBytes: int(b.SizeBytes), Changed: b.Changed,
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"total_backups":     stats.TotalBackups,
		"devices_backed_up": stats.DevicesBackedUp,
		"changed_today":     stats.ChangedToday,
		"recent":            items,
	})
}
