package api

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/coralsearesorts/hims/internal/operations"
	"github.com/coralsearesorts/hims/internal/storage/postgres/db"
)

// ---- Work orders -----------------------------------------------------------

type createWorkOrderReq struct {
	DeviceID       *string `json:"device_id"`
	LocationID     *string `json:"location_id"`
	Title          string  `json:"title"`
	ProblemType    string  `json:"problem_type"`
	Priority       string  `json:"priority"`
	AssignedTo     *string `json:"assigned_to"`
	Diagnosis      *string `json:"diagnosis"`
	ActionTaken    *string `json:"action_taken"`
	SpareParts     *string `json:"spare_parts"`
	ExternalVendor *string `json:"external_vendor"`
	Cost           float64 `json:"cost"`
}

// woDTO is a work order enriched with its linked device name and derived SLA
// (deadline + standing). SLA is computed from created_at + priority policy, so
// no stored deadline can drift out of sync.
type woDTO struct {
	db.WorkOrder
	DeviceName string    `json:"device_name"`
	DueAt      time.Time `json:"due_at"`
	SLAStatus  string    `json:"sla_status"`
}

func enrichWO(wo db.WorkOrder, deviceName string, now time.Time) woDTO {
	return woDTO{
		WorkOrder:  wo,
		DeviceName: deviceName,
		DueAt:      operations.SLADeadline(wo.CreatedAt, wo.Priority),
		SLAStatus:  string(operations.ComputeSLAStatus(wo.CreatedAt, wo.Priority, wo.Status, wo.ResolvedAt, now)),
	}
}

func (s *Server) listWorkOrders(w http.ResponseWriter, r *http.Request) {
	rows, err := s.queries.ListWorkOrdersWithDevice(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}
	now := time.Now().UTC()
	out := make([]woDTO, len(rows))
	for i, row := range rows {
		wo := db.WorkOrder{
			ID: row.ID, DeviceID: row.DeviceID, LocationID: row.LocationID, Title: row.Title,
			ProblemType: row.ProblemType, Priority: row.Priority, Status: row.Status,
			AssignedTo: row.AssignedTo, Diagnosis: row.Diagnosis, ActionTaken: row.ActionTaken,
			SpareParts: row.SpareParts, ExternalVendor: row.ExternalVendor, Cost: row.Cost,
			CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt, ResolvedAt: row.ResolvedAt,
		}
		out[i] = enrichWO(wo, derefStr(row.DeviceName), now)
	}
	writeJSON(w, http.StatusOK, out)
}

// deviceWorkOrders handles GET /devices/{id}/work-orders — the device's ticket
// history, SLA-enriched.
func (s *Server) deviceWorkOrders(w http.ResponseWriter, r *http.Request) {
	ctx, id, ok := pathDevice(w, r)
	if !ok {
		return
	}
	rows, err := s.queries.ListWorkOrdersByDevice(ctx, &id)
	if err != nil {
		writeErr(w, err)
		return
	}
	now := time.Now().UTC()
	out := make([]woDTO, len(rows))
	for i, wo := range rows {
		out[i] = enrichWO(wo, "", now)
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) createWorkOrder(w http.ResponseWriter, r *http.Request) {
	var req createWorkOrderReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}
	if req.Title == "" {
		http.Error(w, "title is required", http.StatusBadRequest)
		return
	}
	p := db.CreateWorkOrderParams{
		DeviceID:       parseUUIDPtr(req.DeviceID),
		LocationID:     parseUUIDPtr(req.LocationID),
		Title:          req.Title,
		ProblemType:    orDefault(req.ProblemType, "other"),
		Priority:       orDefault(req.Priority, "medium"),
		Status:         "open",
		AssignedTo:     req.AssignedTo,
		Diagnosis:      req.Diagnosis,
		ActionTaken:    req.ActionTaken,
		SpareParts:     req.SpareParts,
		ExternalVendor: req.ExternalVendor,
		Cost:           req.Cost,
	}
	wo, err := s.queries.CreateWorkOrder(r.Context(), p)
	if err != nil {
		writeErr(w, err)
		return
	}
	// Seed the timeline with a "created" event.
	_, _ = s.queries.AddWorkOrderEvent(r.Context(), db.AddWorkOrderEventParams{
		WorkOrderID: wo.ID,
		EventType:   "created",
		Note:        strptr("Work order created"),
	})
	s.audit(r, "work_order", "work_order.create", "work_order", wo.ID.String(), "Created work order: "+wo.Title, map[string]any{"priority": wo.Priority})
	writeJSON(w, http.StatusCreated, wo)
}

func (s *Server) getWorkOrder(w http.ResponseWriter, r *http.Request) {
	ctx, id, ok := pathUUID(w, r, "id")
	if !ok {
		return
	}
	wo, err := s.queries.GetWorkOrder(ctx, id)
	if err != nil {
		writeErr(w, err)
		return
	}
	events, _ := s.queries.ListWorkOrderEvents(ctx, id)
	alerts, _ := s.queries.ListAlertsByWorkOrder(ctx, &id)
	deviceName := ""
	if wo.DeviceID != nil {
		if d, err := s.queries.GetDevice(ctx, *wo.DeviceID); err == nil {
			deviceName = d.Name
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"work_order":    enrichWO(wo, deviceName, time.Now().UTC()),
		"events":        events,
		"linked_alerts": alerts,
	})
}

type updateWorkOrderReq struct {
	Status         string  `json:"status"`
	Priority       string  `json:"priority"`
	AssignedTo     *string `json:"assigned_to"`
	Diagnosis      *string `json:"diagnosis"`
	ActionTaken    *string `json:"action_taken"`
	SpareParts     *string `json:"spare_parts"`
	ExternalVendor *string `json:"external_vendor"`
	Cost           float64 `json:"cost"`
	Note           string  `json:"note"` // optional timeline note
}

func (s *Server) updateWorkOrder(w http.ResponseWriter, r *http.Request) {
	ctx, id, ok := pathUUID(w, r, "id")
	if !ok {
		return
	}
	var req updateWorkOrderReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}
	prev, err := s.queries.GetWorkOrder(ctx, id)
	if err != nil {
		writeErr(w, err)
		return
	}
	wo, err := s.queries.UpdateWorkOrder(ctx, db.UpdateWorkOrderParams{
		ID:             id,
		Status:         orDefault(req.Status, prev.Status),
		Priority:       orDefault(req.Priority, prev.Priority),
		AssignedTo:     req.AssignedTo,
		Diagnosis:      req.Diagnosis,
		ActionTaken:    req.ActionTaken,
		SpareParts:     req.SpareParts,
		ExternalVendor: req.ExternalVendor,
		Cost:           req.Cost,
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	// Record a timeline event for a status change and/or an explicit note.
	if req.Status != "" && req.Status != prev.Status {
		_, _ = s.queries.AddWorkOrderEvent(ctx, db.AddWorkOrderEventParams{
			WorkOrderID: id, EventType: "status_change",
			Note: strptr(prev.Status + " → " + req.Status),
		})
	}
	if req.Note != "" {
		_, _ = s.queries.AddWorkOrderEvent(ctx, db.AddWorkOrderEventParams{
			WorkOrderID: id, EventType: "note", Note: &req.Note,
		})
	}
	s.audit(r, "work_order", "work_order.update", "work_order", id.String(), "Updated work order: "+wo.Title, map[string]any{"status": wo.Status})
	writeJSON(w, http.StatusOK, wo)
}

// ---- Systems & licenses ----------------------------------------------------

type createSystemReq struct {
	Name          string  `json:"name"`
	Vendor        *string `json:"vendor"`
	LocationID    *string `json:"location_id"`
	LicenseExpiry *string `json:"license_expiry"` // YYYY-MM-DD
	SupportExpiry *string `json:"support_expiry"`
	Cost          float64 `json:"cost"`
	Notes         *string `json:"notes"`
}

// systemDTO enriches the row with computed expiry statuses.
type systemDTO struct {
	db.System
	LicenseStatus string `json:"license_status"`
	SupportStatus string `json:"support_status"`
	OverallStatus string `json:"overall_status"`
}

func (s *Server) listSystems(w http.ResponseWriter, r *http.Request) {
	rows, err := s.queries.ListSystems(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}
	now := time.Now().UTC()
	out := make([]systemDTO, len(rows))
	for i, row := range rows {
		lic := operations.ComputeLicenseStatus(row.LicenseExpiry, now)
		sup := operations.ComputeLicenseStatus(row.SupportExpiry, now)
		out[i] = systemDTO{
			System:        row,
			LicenseStatus: string(lic),
			SupportStatus: string(sup),
			OverallStatus: string(operations.WorstStatus(lic, sup)),
		}
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) createSystem(w http.ResponseWriter, r *http.Request) {
	var req createSystemReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}
	if req.Name == "" {
		http.Error(w, "name is required", http.StatusBadRequest)
		return
	}
	sys, err := s.queries.CreateSystem(r.Context(), db.CreateSystemParams{
		Name:          req.Name,
		Vendor:        req.Vendor,
		LocationID:    parseUUIDPtr(req.LocationID),
		LicenseExpiry: parseDatePtr(req.LicenseExpiry),
		SupportExpiry: parseDatePtr(req.SupportExpiry),
		Cost:          req.Cost,
		Notes:         req.Notes,
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	s.audit(r, "config", "system.create", "system", sys.ID.String(), "Created system/license: "+sys.Name, nil)
	writeJSON(w, http.StatusCreated, sys)
}

// ---- helpers ---------------------------------------------------------------

func parseUUIDPtr(s *string) *uuid.UUID {
	if s == nil || *s == "" {
		return nil
	}
	id, err := uuid.Parse(*s)
	if err != nil {
		return nil
	}
	return &id
}

func parseDatePtr(s *string) *time.Time {
	if s == nil || *s == "" {
		return nil
	}
	t, err := time.Parse("2006-01-02", *s)
	if err != nil {
		return nil
	}
	return &t
}

func orDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

func strptr(s string) *string { return &s }

// chiURLParam re-exports for handlers in this file (kept local to avoid an
// import just for the alias).
var _ = chi.URLParam
