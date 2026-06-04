package monitoring

import (
	"context"
	"errors"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/google/uuid"

	"github.com/coralsearesorts/hims/internal/secret"
	"github.com/coralsearesorts/hims/internal/snmp"
	"github.com/coralsearesorts/hims/internal/storage/postgres/db"
)

// errNoCredential marks an snmp check on a device with no bound credential.
var errNoCredential = errors.New("no bound credential")

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
	GetCredential(ctx context.Context, id uuid.UUID) (db.Credential, error)
}

// Engine runs the scheduled monitoring loop.
type Engine struct {
	repo   Repo
	poller *Poller
	log    *slog.Logger

	// AfterSweep, if set, runs at the end of each Loop sweep — used to chain
	// the alerting engine (evaluate rules against the just-updated statuses)
	// without monitoring importing alerting (dependency inversion).
	AfterSweep func(ctx context.Context)

	// cipher, when set, lets snmp-metric checks decrypt the device's bound
	// community in-memory. When nil, snmp checks are skipped (reachability still
	// runs). Held atomically so the API can swap it in at runtime (key unlock)
	// while a sweep is in flight without a data race.
	cipher atomic.Pointer[secret.Cipher]
}

// SetCipher swaps the engine's credential cipher (used for snmp-metric checks).
// Safe to call concurrently with a running Loop. Pass nil to disable snmp checks.
func (e *Engine) SetCipher(c *secret.Cipher) { e.cipher.Store(c) }

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
		// snmp checks need a cipher to decrypt the bound community; without
		// one (e.g. before the key is unlocked) they're skipped, not failed.
		if c.Kind == "snmp" && e.cipher.Load() == nil {
			continue
		}
		e.runOne(ctx, c)
		polled++
	}
	return polled, nil
}

// runOne probes a single check (by kind) and persists the outcome.
func (e *Engine) runOne(ctx context.Context, c db.MonitoringCheck) {
	dev, err := e.repo.GetDevice(ctx, c.DeviceID)
	if err != nil {
		e.log.Warn("monitoring: device lookup failed", "check", c.ID, "error", err)
		return
	}
	if dev.PrimaryIp == nil || !dev.PrimaryIp.IsValid() {
		return // nothing to probe
	}

	var res Result
	switch c.Kind {
	case "snmp":
		res = e.probeSNMP(ctx, dev, c)
	default: // "tcp"
		port := 443
		if c.TargetPort != nil {
			port = int(*c.TargetPort)
		}
		res = e.poller.ProbeTCP(ctx, *dev.PrimaryIp, port)
	}

	status, failures := Evaluate(res.OK, int(c.ConsecutiveFailures), int(c.DownThreshold))
	latencyMs := float64(res.Latency.Microseconds()) / 1000.0
	var errStr *string
	if res.Err != nil {
		s := res.Err.Error() // a transport error string; never a secret
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
		ValueNum:  res.Value,
		Error:     errStr,
	}); err != nil {
		e.log.Warn("monitoring: insert sample failed", "check", c.ID, "error", err)
	}

	e.rollupDevice(ctx, c.DeviceID)
}

// probeSNMP resolves the device's bound credential, decrypts the community
// in-memory, and polls the check's OID. The community is never logged.
func (e *Engine) probeSNMP(ctx context.Context, dev db.Device, c db.MonitoringCheck) Result {
	if dev.CredentialID == nil {
		return Result{OK: false, Err: errNoCredential}
	}
	cph := e.cipher.Load()
	if cph == nil {
		return Result{OK: false, Err: errors.New("no encryption key loaded")}
	}
	cred, err := e.repo.GetCredential(ctx, *dev.CredentialID)
	if err != nil {
		return Result{OK: false, Err: err}
	}
	secret, err := cph.Open(cred.EncryptedBlob, cred.KeyID)
	if err != nil {
		return Result{OK: false, Err: err} // decrypt/keyID error — no secret in the message
	}
	port := 161
	if c.TargetPort != nil {
		port = int(*c.TargetPort)
	}
	oid := ""
	if c.Oid != nil {
		oid = *c.Oid
	}
	if cred.Kind == "snmp_v3" {
		v3, perr := snmp.ParseV3JSON(secret)
		if perr != nil {
			return Result{OK: false, Err: perr}
		}
		return e.poller.ProbeSNMPv3(ctx, *dev.PrimaryIp, port, v3, oid)
	}
	return e.poller.ProbeSNMP(ctx, *dev.PrimaryIp, port, string(secret), oid)
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
		if e.AfterSweep != nil && ctx.Err() == nil {
			e.AfterSweep(ctx)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
		}
	}
}
