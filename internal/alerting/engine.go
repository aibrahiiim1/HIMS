package alerting

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/coralsearesorts/hims/internal/storage/postgres/db"
)

// Repo is the storage slice the engine needs. *db.Queries satisfies it; a
// fake is used in tests.
type Repo interface {
	ListEnabledAlertRules(ctx context.Context) ([]db.AlertRule, error)
	ListEnabledChecksWithDevice(ctx context.Context) ([]db.ListEnabledChecksWithDeviceRow, error)
	OpenAlert(ctx context.Context, arg db.OpenAlertParams) (db.Alert, error)
	SetAlertWorkOrder(ctx context.Context, arg db.SetAlertWorkOrderParams) error
	ResolveRecoveredAlerts(ctx context.Context) ([]db.ResolveRecoveredAlertsRow, error)
	CreateWorkOrder(ctx context.Context, arg db.CreateWorkOrderParams) (db.WorkOrder, error)
	AddWorkOrderEvent(ctx context.Context, arg db.AddWorkOrderEventParams) (db.WorkOrderEvent, error)
	// Alert Engine finalization (#6): suppression, timeline, escalation.
	ListActiveMaintenanceWindows(ctx context.Context) ([]db.MaintenanceWindow, error)
	AddAlertEvent(ctx context.Context, arg db.AddAlertEventParams) (db.AlertEvent, error)
	EscalateStaleAlerts(ctx context.Context) ([]db.EscalateStaleAlertsRow, error)
}

// Engine evaluates alert rules against current monitoring state.
type Engine struct {
	repo Repo
	log  *slog.Logger
}

// NewEngine wires the engine. A nil logger uses the slog default.
func NewEngine(repo Repo, log *slog.Logger) *Engine {
	if log == nil {
		log = slog.Default()
	}
	return &Engine{repo: repo, log: log}
}

// Result summarizes one evaluation pass.
type Result struct {
	Opened     int
	WorkOrders int
	Resolved   int
	Suppressed int
	Escalated  int
}

// suppression is the set of scopes currently under a maintenance window.
type suppression struct {
	global    bool
	devices   map[uuid.UUID]bool
	locations map[uuid.UUID]bool
}

func (s suppression) blocks(c db.ListEnabledChecksWithDeviceRow) bool {
	if s.global || s.devices[c.DeviceID] {
		return true
	}
	return c.DeviceLocationID != nil && s.locations[*c.DeviceLocationID]
}

// Evaluate runs one full pass:
//  1. auto-resolve alerts whose check recovered (and note their work orders);
//  2. open alerts for checks newly matching an enabled rule, atomically;
//  3. for a newly-opened alert on an auto-work-order rule, create + link a WO.
//
// It is safe to call every monitoring sweep: opens are idempotent (ON
// CONFLICT), so a still-down check won't spawn duplicate alerts or WOs.
func (e *Engine) Evaluate(ctx context.Context) (Result, error) {
	var res Result

	// (1) Resolve recovered alerts first, so a flap that recovered this tick
	// frees its (rule, check) slot before we re-open below.
	recovered, err := e.repo.ResolveRecoveredAlerts(ctx)
	if err != nil {
		return res, fmt.Errorf("resolve recovered: %w", err)
	}
	for _, a := range recovered {
		res.Resolved++
		e.event(ctx, a.ID, "resolved", "system", "Auto: device recovered; alert resolved.")
		if a.WorkOrderID != nil {
			e.note(ctx, *a.WorkOrderID, "Auto: device recovered; alert resolved.")
		}
	}

	// Suppression: devices/sites/global currently under a maintenance window do
	// not fire new alerts (auto-resume is implicit — the window simply expires).
	supp := e.loadSuppression(ctx)

	rules, err := e.repo.ListEnabledAlertRules(ctx)
	if err != nil {
		return res, fmt.Errorf("list rules: %w", err)
	}
	checks, err := e.repo.ListEnabledChecksWithDevice(ctx)
	if err != nil {
		return res, fmt.Errorf("list checks: %w", err)
	}

	for _, rule := range rules {
		r := Rule{
			TriggerStatus:  rule.TriggerStatus,
			MinFailures:    int(rule.MinFailures),
			DeviceCategory: rule.DeviceCategory,
		}
		for _, c := range checks {
			if ctx.Err() != nil {
				return res, ctx.Err()
			}
			if !Matches(r, CheckState{
				Status:         c.LastStatus,
				Failures:       int(c.ConsecutiveFailures),
				DeviceCategory: c.DeviceCategory,
			}) {
				continue
			}
			if supp.blocks(c) {
				res.Suppressed++
				continue
			}
			e.fire(ctx, rule, c, &res)
		}
	}

	// (4) Escalate open, unacknowledged alerts that have aged past their rule's
	// escalation window. Runs regardless of whether any rules matched this pass.
	e.escalate(ctx, &res)
	return res, nil
}

// loadSuppression reads the active maintenance windows once per pass.
func (e *Engine) loadSuppression(ctx context.Context) suppression {
	sp := suppression{devices: map[uuid.UUID]bool{}, locations: map[uuid.UUID]bool{}}
	ws, err := e.repo.ListActiveMaintenanceWindows(ctx)
	if err != nil {
		e.log.Warn("alerting: list maintenance windows failed", "error", err)
		return sp
	}
	for _, w := range ws {
		switch w.Scope {
		case "global":
			sp.global = true
		case "device":
			if w.DeviceID != nil {
				sp.devices[*w.DeviceID] = true
			}
		case "site":
			if w.LocationID != nil {
				sp.locations[*w.LocationID] = true
			}
		}
	}
	return sp
}

// escalate flags stale open/unacked alerts and records the transition.
func (e *Engine) escalate(ctx context.Context, res *Result) {
	rows, err := e.repo.EscalateStaleAlerts(ctx)
	if err != nil {
		e.log.Warn("alerting: escalate stale failed", "error", err)
		return
	}
	for _, a := range rows {
		res.Escalated++
		e.event(ctx, a.ID, "escalated", "system", "Alert open and unacknowledged past its escalation window.")
		if a.WorkOrderID != nil {
			e.note(ctx, *a.WorkOrderID, "Auto: alert escalated (still unacknowledged).")
		}
	}
}

// event appends a row to an alert's lifecycle timeline (best-effort).
func (e *Engine) event(ctx context.Context, alertID uuid.UUID, kind, actor, note string) {
	_, _ = e.repo.AddAlertEvent(ctx, db.AddAlertEventParams{AlertID: alertID, Kind: kind, Actor: actor, Note: note})
}

// fire opens an alert for a matching check; on a genuine open it spawns the
// linked work order when the rule asks for it.
func (e *Engine) fire(ctx context.Context, rule db.AlertRule, c db.ListEnabledChecksWithDeviceRow, res *Result) {
	checkID := c.ID
	msg := alertMessage(rule, c)
	alert, err := e.repo.OpenAlert(ctx, db.OpenAlertParams{
		RuleID:   rule.ID,
		DeviceID: c.DeviceID,
		CheckID:  &checkID,
		Severity: rule.Severity,
		Message:  msg,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return // already open — idempotent
		}
		e.log.Warn("alerting: open alert failed", "rule", rule.ID, "check", checkID, "error", err)
		return
	}
	res.Opened++
	e.event(ctx, alert.ID, "opened", "system", msg)

	if !rule.AutoWorkOrder {
		return
	}
	devID := c.DeviceID
	wo, err := e.repo.CreateWorkOrder(ctx, db.CreateWorkOrderParams{
		DeviceID:    &devID,
		Title:       msg,
		ProblemType: "network",
		Priority:    rule.WorkOrderPriority,
		Status:      "open",
	})
	if err != nil {
		e.log.Warn("alerting: auto work-order failed", "alert", alert.ID, "error", err)
		return
	}
	note := "Auto-created from alert: " + msg
	_, _ = e.repo.AddWorkOrderEvent(ctx, db.AddWorkOrderEventParams{
		WorkOrderID: wo.ID, EventType: "created", Note: &note,
	})
	if err := e.repo.SetAlertWorkOrder(ctx, db.SetAlertWorkOrderParams{ID: alert.ID, WorkOrderID: &wo.ID}); err != nil {
		e.log.Warn("alerting: link work-order failed", "alert", alert.ID, "wo", wo.ID, "error", err)
		return
	}
	res.WorkOrders++
}

func (e *Engine) note(ctx context.Context, woID uuid.UUID, text string) {
	_, _ = e.repo.AddWorkOrderEvent(ctx, db.AddWorkOrderEventParams{
		WorkOrderID: woID, EventType: "note", Note: &text,
	})
}

// alertMessage renders a human-readable headline for an alert / WO title.
func alertMessage(rule db.AlertRule, c db.ListEnabledChecksWithDeviceRow) string {
	port := ""
	if c.TargetPort != nil {
		port = fmt.Sprintf(":%d", *c.TargetPort)
	}
	ip := ""
	if c.DeviceIp != nil && c.DeviceIp.IsValid() {
		ip = " (" + c.DeviceIp.String() + ")"
	}
	return fmt.Sprintf("%s%s — %s%s check is %s (%d consecutive failures)",
		c.DeviceName, ip, c.Kind, port, strings.ToUpper(c.LastStatus), c.ConsecutiveFailures)
}
