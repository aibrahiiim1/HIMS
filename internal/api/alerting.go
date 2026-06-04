package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/coralsearesorts/hims/internal/storage/postgres/db"
)

// alertActor resolves who performed an action. There is no auth subject yet,
// so it defaults to "operator" but honors an X-Actor header if present.
func alertActor(r *http.Request) string {
	if a := r.Header.Get("X-Actor"); a != "" {
		return a
	}
	return "operator"
}

// ---- Alert rules -----------------------------------------------------------

type alertRuleReq struct {
	Name                 string  `json:"name"`
	TriggerStatus        string  `json:"trigger_status"`
	MinFailures          int32   `json:"min_failures"`
	DeviceCategory       *string `json:"device_category"`
	Severity             string  `json:"severity"`
	AutoWorkOrder        bool    `json:"auto_work_order"`
	WorkOrderPriority    string  `json:"work_order_priority"`
	EscalateAfterMinutes int32   `json:"escalate_after_minutes"`
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
	esc := req.EscalateAfterMinutes
	if esc < 0 {
		esc = 0
	}
	rule, err := s.queries.CreateAlertRule(r.Context(), db.CreateAlertRuleParams{
		Name:                 req.Name,
		TriggerStatus:        orDefault(req.TriggerStatus, "down"),
		MinFailures:          minF,
		DeviceCategory:       cat,
		Severity:             orDefault(req.Severity, "warning"),
		AutoWorkOrder:        req.AutoWorkOrder,
		WorkOrderPriority:    orDefault(req.WorkOrderPriority, "high"),
		Enabled:              true,
		EscalateAfterMinutes: esc,
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
	actor := alertActor(r)
	a, err := s.queries.AcknowledgeAlertBy(ctx, db.AcknowledgeAlertByParams{ID: id, AcknowledgedBy: &actor})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) { // already acked/resolved — idempotent
			if cur, gerr := s.queries.GetAlert(ctx, id); gerr == nil {
				writeJSON(w, http.StatusOK, cur)
				return
			}
		}
		writeErr(w, err)
		return
	}
	_, _ = s.queries.AddAlertEvent(ctx, db.AddAlertEventParams{AlertID: id, Kind: "acknowledged", Actor: actor})
	writeJSON(w, http.StatusOK, a)
}

func (s *Server) resolveAlert(w http.ResponseWriter, r *http.Request) {
	ctx, id, ok := pathUUID(w, r, "id")
	if !ok {
		return
	}
	actor := alertActor(r)
	a, err := s.queries.ResolveAlert(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) { // already resolved — idempotent
			if cur, gerr := s.queries.GetAlert(ctx, id); gerr == nil {
				writeJSON(w, http.StatusOK, cur)
				return
			}
		}
		writeErr(w, err)
		return
	}
	_, _ = s.queries.AddAlertEvent(ctx, db.AddAlertEventParams{AlertID: id, Kind: "resolved", Actor: actor, Note: "Manually resolved."})
	writeJSON(w, http.StatusOK, a)
}

// alertTimeline returns an alert's full lifecycle (opened/ack/resolve/note/escalated).
func (s *Server) alertTimeline(w http.ResponseWriter, r *http.Request) {
	ctx, id, ok := pathUUID(w, r, "id")
	if !ok {
		return
	}
	rows, err := s.queries.ListAlertEvents(ctx, id)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rows)
}

// addAlertNote appends an operator note to an alert's timeline.
func (s *Server) addAlertNote(w http.ResponseWriter, r *http.Request) {
	ctx, id, ok := pathUUID(w, r, "id")
	if !ok {
		return
	}
	var req struct {
		Note string `json:"note"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Note == "" {
		http.Error(w, "note is required", http.StatusBadRequest)
		return
	}
	ev, err := s.queries.AddAlertEvent(ctx, db.AddAlertEventParams{AlertID: id, Kind: "note", Actor: alertActor(r), Note: req.Note})
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, ev)
}

// ---- Maintenance windows (alert suppression) ------------------------------

func (s *Server) listMaintenanceWindows(w http.ResponseWriter, r *http.Request) {
	rows, err := s.queries.ListMaintenanceWindows(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rows)
}

func (s *Server) createMaintenanceWindow(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Scope      string  `json:"scope"`
		DeviceID   *string `json:"device_id"`
		LocationID *string `json:"location_id"`
		Reason     string  `json:"reason"`
		StartsAt   string  `json:"starts_at"`
		EndsAt     string  `json:"ends_at"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	scope := orDefault(req.Scope, "device")
	starts, err := time.Parse(time.RFC3339, req.StartsAt)
	if err != nil {
		http.Error(w, "starts_at must be RFC3339", http.StatusBadRequest)
		return
	}
	ends, err := time.Parse(time.RFC3339, req.EndsAt)
	if err != nil {
		http.Error(w, "ends_at must be RFC3339", http.StatusBadRequest)
		return
	}
	if !ends.After(starts) {
		http.Error(w, "ends_at must be after starts_at", http.StatusBadRequest)
		return
	}
	arg := db.CreateMaintenanceWindowParams{
		Scope: scope, Reason: req.Reason, StartsAt: starts, EndsAt: ends, CreatedBy: alertActor(r),
	}
	if scope == "device" {
		id, perr := parseOptUUID(req.DeviceID)
		if perr != nil || id == nil {
			http.Error(w, "device_id is required for scope=device", http.StatusBadRequest)
			return
		}
		arg.DeviceID = id
	}
	if scope == "site" {
		id, perr := parseOptUUID(req.LocationID)
		if perr != nil || id == nil {
			http.Error(w, "location_id is required for scope=site", http.StatusBadRequest)
			return
		}
		arg.LocationID = id
	}
	mw, err := s.queries.CreateMaintenanceWindow(r.Context(), arg)
	if err != nil {
		writeErr(w, err)
		return
	}
	s.audit(r, "monitoring", "maintenance.window.create", "maintenance_window", mw.ID.String(), "Scheduled a maintenance window", map[string]any{"scope": scope, "starts_at": starts, "ends_at": ends})
	writeJSON(w, http.StatusCreated, mw)
}

func (s *Server) deleteMaintenanceWindow(w http.ResponseWriter, r *http.Request) {
	ctx, id, ok := pathUUID(w, r, "id")
	if !ok {
		return
	}
	if err := s.queries.DeleteMaintenanceWindow(ctx, id); err != nil {
		writeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func parseOptUUID(s *string) (*uuid.UUID, error) {
	if s == nil || *s == "" {
		return nil, nil
	}
	id, err := uuid.Parse(*s)
	if err != nil {
		return nil, err
	}
	return &id, nil
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
