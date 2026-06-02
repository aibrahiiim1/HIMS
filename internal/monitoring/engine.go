package monitoring

import (
	"context"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/coralsearesorts/hims/internal/storage/postgres/db"
)

// Repo is the slice of the storage layer the engine needs. *db.Queries
// satisfies it; tests use a fake. Keeping it narrow documents the engine's
// data dependencies and keeps the unit tests DB-free.
type Repo interface {
	ListDueMonitoringChecks(ctx context.Context) ([]db.MonitoringCheck, error)
	GetDevice(ctx context.Context, id uuid.UUID) (db.Device, error)
	RecordMonitoringResult(ctx context.Context, arg db.RecordMonitoringResultParams) (db.MonitoringCheck, error)
	InsertMonitoringSample(ctx context.Context, arg db.InsertMonitoringSampleParams) error
	ListMonitoringChecksByDevice(ctx context.Context, deviceID uuid.UUID) ([]db.MonitoringCheck, error)
	UpdateDeviceMonitoringStatus(ctx context.Context, arg db.UpdateDeviceMonitoringStatusParams) error
	ListDevicesNeedingDefaultCheck(ctx context.Context) ([]db.ListDevicesNeedingDefaultCheckRow, error)
	UpsertMonitoringCheck(ctx context.Context, arg db.UpsertMonitoringCheckParams) (db.MonitoringCheck, error)
}

// Engine runs the scheduled monitoring loop.
type Engine struct {
	repo   Repo
	poller *Poller
	log    *slog.Logger
}

// NewEngine wires the engine. A nil logger uses the slog default.
func NewEngine(repo Repo, poller *Poller, log *slog.Logger) *Engine {
	if log == nil {
		log = slog.Default()
	}
	return &Engine{repo: repo, poller: poller, log: log}
}

// SeedDefaults registers a default TCP check for every device that has a
// reachable IP but no check yet. Idempotent: the upsert is keyed on
// (device, kind, port), so re-running it changes nothing for seeded devices.
// Returns the number of checks created/updated.
func (e *Engine) SeedDefaults(ctx context.Context) (int, error) {
	rows, err := e.repo.ListDevicesNeedingDefaultCheck(ctx)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, d := range rows {
		port := int32(DefaultPort(d.Category))
		if _, err := e.repo.UpsertMonitoringCheck(ctx, db.UpsertMonitoringCheckParams{
			DeviceID:        d.ID,
			Kind:            "tcp",
			TargetPort:      &port,
			IntervalSeconds: 60,
			DownThreshold:   2,
			Enabled:         true,
		}); err != nil {
			e.log.Warn("seed default check failed", "device", d.ID, "error", err)
			continue
		}
		n++
	}
	if n > 0 {
		e.log.Info("seeded default monitoring checks", "count", n)
	}
	return n, nil
}

// RunDue polls every check whose interval has elapsed, records a sample and
// the rolled-up status, and reflects the worst per-device status onto the
// device row. Returns the number of checks polled. Errors on individual
// checks are logged and skipped so one bad device never stalls the sweep.
func (e *Engine) RunDue(ctx context.Context) (int, error) {
	checks, err := e.repo.ListDueMonitoringChecks(ctx)
	if err != nil {
		return 0, err
	}
	polled := 0
	for _, c := range checks {
		if ctx.Err() != nil {
			return polled, ctx.Err()
		}
		// Phase 6 core executes TCP checks only; snmp-metric checks (6B)
		// need credential decrypt and are left untouched.
		if c.Kind != "tcp" {
			continue
		}
		e.runOne(ctx, c)
		polled++
	}
	return polled, nil
}

// runOne polls a single TCP check and persists the outcome.
func (e *Engine) runOne(ctx context.Context, c db.MonitoringCheck) {
	dev, err := e.repo.GetDevice(ctx, c.DeviceID)
	if err != nil {
		e.log.Warn("monitoring: device lookup failed", "check", c.ID, "error", err)
		return
	}
	if dev.PrimaryIp == nil || !dev.PrimaryIp.IsValid() {
		return // nothing to dial
	}
	port := 443
	if c.TargetPort != nil {
		port = int(*c.TargetPort)
	}

	res := e.poller.ProbeTCP(ctx, *dev.PrimaryIp, port)
	status, failures := Evaluate(res.OK, int(c.ConsecutiveFailures), int(c.DownThreshold))

	latencyMs := float64(res.Latency.Microseconds()) / 1000.0
	var errStr *string
	if res.Err != nil {
		s := res.Err.Error()
		errStr = &s
	}

	if _, err := e.repo.RecordMonitoringResult(ctx, db.RecordMonitoringResultParams{
		ID:                  c.ID,
		LastStatus:          string(status),
		LastLatencyMs:       &latencyMs,
		ConsecutiveFailures: int32(failures),
	}); err != nil {
		e.log.Warn("monitoring: record result failed", "check", c.ID, "error", err)
	}
	if err := e.repo.InsertMonitoringSample(ctx, db.InsertMonitoringSampleParams{
		CheckID:   c.ID,
		DeviceID:  c.DeviceID,
		Status:    string(status),
		LatencyMs: &latencyMs,
		Error:     errStr,
	}); err != nil {
		e.log.Warn("monitoring: insert sample failed", "check", c.ID, "error", err)
	}

	e.rollupDevice(ctx, c.DeviceID)
}

// rollupDevice recomputes the device's status from all its checks and writes
// it onto the device row (the live badge for device lists).
func (e *Engine) rollupDevice(ctx context.Context, deviceID uuid.UUID) {
	checks, err := e.repo.ListMonitoringChecksByDevice(ctx, deviceID)
	if err != nil {
		e.log.Warn("monitoring: rollup list failed", "device", deviceID, "error", err)
		return
	}
	statuses := make([]Status, 0, len(checks))
	for _, c := range checks {
		statuses = append(statuses, Status(c.LastStatus))
	}
	dev := RollupDevice(statuses)
	if err := e.repo.UpdateDeviceMonitoringStatus(ctx, db.UpdateDeviceMonitoringStatusParams{
		ID:     deviceID,
		Status: string(dev),
	}); err != nil {
		e.log.Warn("monitoring: device status update failed", "device", deviceID, "error", err)
	}
}

// Loop runs RunDue every tick until the context is cancelled. The collector's
// scheduled-monitoring mode calls this. Each tick is best-effort; a failed
// sweep is logged and retried on the next tick.
func (e *Engine) Loop(ctx context.Context, tick time.Duration) error {
	if tick <= 0 {
		tick = 30 * time.Second
	}
	t := time.NewTicker(tick)
	defer t.Stop()
	e.log.Info("monitoring loop started", "tick", tick.String())
	for {
		if n, err := e.RunDue(ctx); err != nil && ctx.Err() == nil {
			e.log.Warn("monitoring sweep error", "error", err)
		} else if n > 0 {
			e.log.Info("monitoring sweep complete", "polled", n)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
		}
	}
}
