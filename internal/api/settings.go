package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/coralsearesorts/hims/internal/storage/postgres/db"
)

// settingDefaults are the int-valued knobs the scan + collectors read, with
// their fallback values. Unknown keys are rejected by PUT so the surface stays
// closed. Bounds guard against footguns (e.g. a 0ms timeout).
var settingDefaults = map[string]int{
	"snmp_timeout_ms":  3000,
	"tcp_timeout_ms":   500,
	"scan_concurrency": 16,
	"http_timeout_ms":  20000,
	"winrm_timeout_ms": 60000,
}

var settingBounds = map[string][2]int{
	"snmp_timeout_ms":  {200, 30000},
	"tcp_timeout_ms":   {100, 10000},
	"scan_concurrency": {1, 64},
	"http_timeout_ms":  {1000, 120000},
	"winrm_timeout_ms": {5000, 300000},
}

// resolveSettings reads the stored settings, filling defaults for any missing
// or unparseable key. Always returns a complete map.
func (s *Server) resolveSettings(ctx context.Context) map[string]int {
	out := make(map[string]int, len(settingDefaults))
	for k, v := range settingDefaults {
		out[k] = v
	}
	rows, err := s.queries.ListSettings(ctx)
	if err != nil {
		return out
	}
	for _, row := range rows {
		if _, ok := out[row.Key]; !ok {
			continue
		}
		if n, err := strconv.Atoi(row.Value); err == nil {
			out[row.Key] = n
		}
	}
	return out
}

func (s *Server) getSettings(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.resolveSettings(r.Context()))
}

// updateSettings upserts the provided known keys after bounds-checking. Unknown
// keys and out-of-range values are rejected with 400.
func (s *Server) updateSettings(w http.ResponseWriter, r *http.Request) {
	var req map[string]int
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}
	for k, v := range req {
		b, ok := settingBounds[k]
		if !ok {
			http.Error(w, "unknown setting: "+k, http.StatusBadRequest)
			return
		}
		if v < b[0] || v > b[1] {
			http.Error(w, k+" out of range ("+strconv.Itoa(b[0])+".."+strconv.Itoa(b[1])+")", http.StatusBadRequest)
			return
		}
	}
	for k, v := range req {
		if err := s.queries.UpsertSetting(r.Context(), db.UpsertSettingParams{Key: k, Value: strconv.Itoa(v)}); err != nil {
			writeErr(w, err)
			return
		}
	}
	if len(req) > 0 {
		s.audit(r, "config", "settings.update", "settings", "", "Updated system settings", map[string]any{"keys": len(req)})
	}
	writeJSON(w, http.StatusOK, s.resolveSettings(r.Context()))
}

// scanSettings reads the discovery-scan timeouts + concurrency.
func (s *Server) scanSettings(ctx context.Context) (snmp, tcp time.Duration, concurrency int) {
	m := s.resolveSettings(ctx)
	return time.Duration(m["snmp_timeout_ms"]) * time.Millisecond,
		time.Duration(m["tcp_timeout_ms"]) * time.Millisecond,
		m["scan_concurrency"]
}
