package api

import (
	"encoding/json"
	"net/http"

	"github.com/coralsearesorts/hims/internal/storage/postgres/db"
)

// ---- Alert rules -----------------------------------------------------------

type alertRuleReq struct {
	Name              string  `json:"name"`
	TriggerStatus     string  `json:"trigger_status"`
	MinFailures       int32   `json:"min_failures"`
	DeviceCategory    *string `json:"device_category"`
	Severity          string  `json:"severity"`
	AutoWorkOrder     bool    `json:"auto_work_order"`
	WorkOrderPriority string  `json:"work_order_priority"`
}

func (s *Server) listAlertRules(w http.ResponseWriter, r *http.Request) {
	rows, err := s.queries.ListAlertRules(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rows)
}

func (s *Server) createAlertRule(w http.ResponseWriter, r *http.Request) {
	var req alertRuleReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}
	if req.Name == "" {
		http.Error(w, "name is required", http.StatusBadRequest)
		return
	}
	cat := req.DeviceCategory
	if cat != nil && *cat == "" {
		cat = nil // empty string means "any category"
	}
	minF := req.MinFailures
	if minF < 0 {
		minF = 0
	}
	rule, err := s.queries.CreateAlertRule(r.Context(), db.CreateAlertRuleParams{
		Name:              req.Name,
		TriggerStatus:     orDefault(req.TriggerStatus, "down"),
		MinFailures:       minF,
		DeviceCategory:    cat,
		Severity:          orDefault(req.Severity, "warning"),
		AutoWorkOrder:     req.AutoWorkOrder,
		WorkOrderPriority: orDefault(req.WorkOrderPriority, "high"),
		Enabled:           true,
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, rule)
}

func (s *Server) setAlertRuleEnabled(w http.ResponseWriter, r *http.Request) {
	ctx, id, ok := pathUUID(w, r, "id")
	if !ok {
		return
	}
	var req setEnabledReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}
	rule, err := s.queries.SetAlertRuleEnabled(ctx, db.SetAlertRuleEnabledParams{ID: id, Enabled: req.Enabled})
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rule)
}

func (s *Server) deleteAlertRule(w http.ResponseWriter, r *http.Request) {
	ctx, id, ok := pathUUID(w, r, "id")
	if !ok {
		return
	}
	if err := s.queries.DeleteAlertRule(ctx, id); err != nil {
		writeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---- Alerts ----------------------------------------------------------------

func (s *Server) listAlerts(w http.ResponseWriter, r *http.Request) {
	rows, err := s.queries.ListAlerts(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rows)
}

func (s *Server) acknowledgeAlert(w http.ResponseWriter, r *http.Request) {
	ctx, id, ok := pathUUID(w, r, "id")
	if !ok {
		return
	}
	a, err := s.queries.AcknowledgeAlert(ctx, id)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, a)
}

func (s *Server) resolveAlert(w http.ResponseWriter, r *http.Request) {
	ctx, id, ok := pathUUID(w, r, "id")
	if !ok {
		return
	}
	a, err := s.queries.ResolveAlert(ctx, id)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, a)
}

// evaluateAlerts runs one rule-evaluation pass on demand (the scheduled pass
// runs after each monitoring sweep in the collector).
func (s *Server) evaluateAlerts(w http.ResponseWriter, r *http.Request) {
	res, err := s.alerts.Evaluate(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}
