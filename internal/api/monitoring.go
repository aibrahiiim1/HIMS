package api

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/coralsearesorts/hims/internal/monitoring"
	"github.com/coralsearesorts/hims/internal/storage/postgres/db"
)

// ---- Monitoring: checks ----------------------------------------------------

type registerCheckReq struct {
	DeviceID        string  `json:"device_id"`
	Kind            string  `json:"kind"`             // tcp | snmp
	TargetPort      *int32  `json:"target_port"`      // optional; derived from category if absent
	OID             *string `json:"oid"`              // snmp only; defaults to sysUpTime
	IntervalSeconds int32   `json:"interval_seconds"` // default 60
	DownThreshold   int32   `json:"down_threshold"`   // default 2
}

func (s *Server) listMonitoringChecks(w http.ResponseWriter, r *http.Request) {
	rows, err := s.queries.ListMonitoringChecks(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rows)
}

func (s *Server) registerMonitoringCheck(w http.ResponseWriter, r *http.Request) {
	var req registerCheckReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}
	id := parseUUIDPtr(&req.DeviceID)
	if id == nil {
		http.Error(w, "device_id is required", http.StatusBadRequest)
		return
	}
	kind := orDefault(req.Kind, "tcp")
	if kind != "tcp" && kind != "snmp" {
		http.Error(w, "kind must be tcp or snmp", http.StatusBadRequest)
		return
	}
	port := req.TargetPort
	if port == nil {
		if kind == "snmp" {
			p := int32(161)
			port = &p
		} else {
			// Derive from device category (e.g. switch→22, firewall→443).
			dev, err := s.queries.GetDevice(r.Context(), *id)
			if err != nil {
				writeErr(w, err)
				return
			}
			p := int32(monitoring.DefaultPort(string(dev.Category)))
			port = &p
		}
	}
	var oid *string
	if kind == "snmp" {
		o := monitoring.SysUpTimeOID
		if req.OID != nil && *req.OID != "" {
			o = *req.OID
		}
		oid = &o
	}
	interval := req.IntervalSeconds
	if interval < 10 {
		interval = 60
	}
	threshold := req.DownThreshold
	if threshold < 1 {
		threshold = 2
	}
	chk, err := s.queries.UpsertMonitoringCheck(r.Context(), db.UpsertMonitoringCheckParams{
		DeviceID:        *id,
		Kind:            kind,
		TargetPort:      port,
		Oid:             oid,
		IntervalSeconds: interval,
		DownThreshold:   threshold,
		Enabled:         true,
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	// A check an operator adds beyond the device's reachability check is
	// "supplemental": polled and shown, but it must NOT flip the device's
	// online/offline status or the inventory counts. So whenever the device
	// already has another reachability check, demote this one to supplemental.
	if existing, lerr := s.queries.ListMonitoringChecksByDevice(r.Context(), *id); lerr == nil {
		otherReach := 0
		for _, c := range existing {
			// "supplemental" is the explicit opt-in; anything else (the
			// "reachability" default + legacy empty role) is a reachability check.
			if c.ID != chk.ID && c.Role != "supplemental" {
				otherReach++
			}
		}
		if otherReach > 0 && chk.Role != "supplemental" {
			if rerr := s.queries.SetMonitoringCheckRole(r.Context(), db.SetMonitoringCheckRoleParams{ID: chk.ID, Role: "supplemental"}); rerr == nil {
				chk.Role = "supplemental"
			}
		}
	}
	writeJSON(w, http.StatusCreated, chk)
}

type setEnabledReq struct {
	Enabled bool `json:"enabled"`
}

func (s *Server) setMonitoringCheckEnabled(w http.ResponseWriter, r *http.Request) {
	ctx, id, ok := pathUUID(w, r, "id")
	if !ok {
		return
	}
	var req setEnabledReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}
	chk, err := s.queries.SetMonitoringCheckEnabled(ctx, db.SetMonitoringCheckEnabledParams{
		ID: id, Enabled: req.Enabled,
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, chk)
}

func (s *Server) deleteMonitoringCheck(w http.ResponseWriter, r *http.Request) {
	ctx, id, ok := pathUUID(w, r, "id")
	if !ok {
		return
	}
	if err := s.queries.DeleteMonitoringCheck(ctx, id); err != nil {
		writeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) deviceMonitoringChecks(w http.ResponseWriter, r *http.Request) {
	ctx, id, ok := pathDevice(w, r)
	if !ok {
		return
	}
	rows, err := s.queries.ListMonitoringChecksByDevice(ctx, id)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rows)
}

func (s *Server) deviceMonitoringSamples(w http.ResponseWriter, r *http.Request) {
	ctx, id, ok := pathDevice(w, r)
	if !ok {
		return
	}
	limit := int32(200)
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 2000 {
			limit = int32(n)
		}
	}
	rows, err := s.queries.ListMonitoringSamplesByDevice(ctx, db.ListMonitoringSamplesByDeviceParams{
		DeviceID: id, Limit: limit,
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rows)
}

func (s *Server) monitoringOverview(w http.ResponseWriter, r *http.Request) {
	rows, err := s.queries.MonitoringStatusOverview(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rows)
}

// seedMonitoringDefaults registers a default TCP check for every monitored
// device that lacks one. Operator-triggered (idempotent).
func (s *Server) seedMonitoringDefaults(w http.ResponseWriter, r *http.Request) {
	n, err := s.mon.SeedDefaults(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]int{"seeded": n})
}

// runMonitoringNow runs one sweep on demand (handy when the scheduled
// collector loop isn't running, e.g. in dev).
func (s *Server) runMonitoringNow(w http.ResponseWriter, r *http.Request) {
	n, err := s.mon.RunDue(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]int{"polled": n})
}
