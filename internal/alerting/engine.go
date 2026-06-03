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
		if a.WorkOrderID != nil {
			e.note(ctx, *a.WorkOrderID, "Auto: device recovered; alert resolved.")
		}
	}

	rules, err := e.repo.ListEnabledAlertRules(ctx)
	if err != nil {
		return res, fmt.Errorf("list rules: %w", err)
	}
	if len(rules) == 0 {
		return res, nil
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
			e.fire(ctx, rule, c, &res)
		}
	}
	return res, nil
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
