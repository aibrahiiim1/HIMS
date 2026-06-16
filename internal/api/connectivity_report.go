package api

import (
	"net/http"
	"time"
)

// Connectivity report read surface. Powers the Reports → Connectivity tab:
// "how is each device connected (which credential/protocol worked)" and, for
// non-managed devices, "which credentials were tried and failed".
//
// One bulk query over credential_test_results returns the LATEST outcome per
// (device, credential, kind) across the whole fleet, so the frontend can group
// by device without N+1 per-device calls. The frontend joins this with the
// device list (management/managed_by) and the credential list (id -> name).
// Read-only analytics via the raw pool, mirroring Endpoint Intelligence.

type connAttemptDTO struct {
	DeviceID       string `json:"device_id"`
	CredentialID   string `json:"credential_id,omitempty"`
	CredentialName string `json:"credential_name"`
	Kind           string `json:"kind"`
	Protocol       string `json:"protocol"`
	Category       string `json:"category"` // success | auth_failed | unreachable | collection_error | ...
	Success        bool   `json:"success"`
	Detail         string `json:"detail"`
	TestedAt       string `json:"tested_at"`
}

// connectivityAttempts — GET /reports/credential-attempts
func (s *Server) connectivityAttempts(w http.ResponseWriter, r *http.Request) {
	if s.pool == nil {
		http.Error(w, "analytics pool not configured", http.StatusServiceUnavailable)
		return
	}
	// Latest result per (device, credential, kind). UUIDs cast to text so the
	// scan is independent of any uuid codec registration on the pool.
	const q = `
		SELECT DISTINCT ON (device_id, credential_id, kind)
		       device_id::text,
		       COALESCE(credential_id::text, ''),
		       credential_name, kind, protocol, category, success, detail, tested_at
		FROM credential_test_results
		ORDER BY device_id, credential_id, kind, tested_at DESC`
	rows, err := s.pool.Query(r.Context(), q)
	if err != nil {
		writeErr(w, err)
		return
	}
	defer rows.Close()

	out := make([]connAttemptDTO, 0, 256)
	for rows.Next() {
		var (
			d        connAttemptDTO
			testedAt time.Time
		)
		if err := rows.Scan(&d.DeviceID, &d.CredentialID, &d.CredentialName, &d.Kind,
			&d.Protocol, &d.Category, &d.Success, &d.Detail, &testedAt); err != nil {
			writeErr(w, err)
			return
		}
		d.TestedAt = testedAt.Format(time.RFC3339)
		out = append(out, d)
	}
	if err := rows.Err(); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}
