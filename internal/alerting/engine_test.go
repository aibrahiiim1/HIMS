package alerting

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/coralsearesorts/hims/internal/storage/postgres/db"
)

type fakeRepo struct {
	rules       []db.AlertRule
	checks      []db.ListEnabledChecksWithDeviceRow
	recovered   []db.ResolveRecoveredAlertsRow
	open        map[string]bool // dedup key rule|check → open
	opened      []db.OpenAlertParams
	wos         []db.CreateWorkOrderParams
	linked      []db.SetAlertWorkOrderParams
	events      []db.AddWorkOrderEventParams
	windows     []db.MaintenanceWindow
	alertEvents []db.AddAlertEventParams
	escalated   []db.EscalateStaleAlertsRow
}

func (f *fakeRepo) ListEnabledAlertRules(context.Context) ([]db.AlertRule, error) {
	return f.rules, nil
}
func (f *fakeRepo) ListEnabledChecksWithDevice(context.Context) ([]db.ListEnabledChecksWithDeviceRow, error) {
	return f.checks, nil
}
func (f *fakeRepo) ResolveRecoveredAlerts(context.Context) ([]db.ResolveRecoveredAlertsRow, error) {
	return f.recovered, nil
}
func (f *fakeRepo) OpenAlert(_ context.Context, arg db.OpenAlertParams) (db.Alert, error) {
	key := arg.RuleID.String() + "|" + arg.CheckID.String()
	if f.open[key] {
		return db.Alert{}, pgx.ErrNoRows // conflict → already open
	}
	f.open[key] = true
	f.opened = append(f.opened, arg)
	return db.Alert{ID: uuid.New(), RuleID: arg.RuleID, DeviceID: arg.DeviceID}, nil
}
func (f *fakeRepo) SetAlertWorkOrder(_ context.Context, arg db.SetAlertWorkOrderParams) error {
	f.linked = append(f.linked, arg)
	return nil
}
func (f *fakeRepo) CreateWorkOrder(_ context.Context, arg db.CreateWorkOrderParams) (db.WorkOrder, error) {
	f.wos = append(f.wos, arg)
	return db.WorkOrder{ID: uuid.New()}, nil
}
func (f *fakeRepo) AddWorkOrderEvent(_ context.Context, arg db.AddWorkOrderEventParams) (db.WorkOrderEvent, error) {
	f.events = append(f.events, arg)
	return db.WorkOrderEvent{}, nil
}
func (f *fakeRepo) ListActiveMaintenanceWindows(context.Context) ([]db.MaintenanceWindow, error) {
	return f.windows, nil
}
func (f *fakeRepo) AddAlertEvent(_ context.Context, arg db.AddAlertEventParams) (db.AlertEvent, error) {
	f.alertEvents = append(f.alertEvents, arg)
	return db.AlertEvent{}, nil
}
func (f *fakeRepo) EscalateStaleAlerts(context.Context) ([]db.EscalateStaleAlertsRow, error) {
	return f.escalated, nil
}

func downCheck() db.ListEnabledChecksWithDeviceRow {
	port := int32(22)
	return db.ListEnabledChecksWithDeviceRow{
		ID: uuid.New(), DeviceID: uuid.New(), Kind: "tcp", TargetPort: &port,
		LastStatus: "down", ConsecutiveFailures: 3, DeviceName: "SW-LOBBY", DeviceCategory: "switch",
	}
}

func TestEvaluate_OpensAndBridgesWorkOrder(t *testing.T) {
	rule := db.AlertRule{
		ID: uuid.New(), TriggerStatus: "down", MinFailures: 1, Severity: "critical",
		AutoWorkOrder: true, WorkOrderPriority: "high", Enabled: true,
	}
	f := &fakeRepo{rules: []db.AlertRule{rule}, checks: []db.ListEnabledChecksWithDeviceRow{downCheck()}, open: map[string]bool{}}
	e := NewEngine(f, nil)

	res, err := e.Evaluate(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if res.Opened != 1 || res.WorkOrders != 1 {
		t.Fatalf("res = %+v; want opened=1 wo=1", res)
	}
	if len(f.wos) != 1 || f.wos[0].Priority != "high" || f.wos[0].DeviceID == nil {
		t.Fatalf("work order not created correctly: %+v", f.wos)
	}
	if len(f.linked) != 1 {
		t.Fatalf("alert not linked to WO: %+v", f.linked)
	}
}

func TestEvaluate_Idempotent(t *testing.T) {
	rule := db.AlertRule{ID: uuid.New(), TriggerStatus: "down", MinFailures: 1, Severity: "warning", Enabled: true}
	chk := downCheck()
	f := &fakeRepo{rules: []db.AlertRule{rule}, checks: []db.ListEnabledChecksWithDeviceRow{chk}, open: map[string]bool{}}
	e := NewEngine(f, nil)

	r1, _ := e.Evaluate(context.Background())
	r2, _ := e.Evaluate(context.Background())
	if r1.Opened != 1 {
		t.Fatalf("first pass opened = %d; want 1", r1.Opened)
	}
	if r2.Opened != 0 {
		t.Fatalf("second pass opened = %d; want 0 (idempotent)", r2.Opened)
	}
}

func TestEvaluate_NoWorkOrderWhenNotFlagged(t *testing.T) {
	rule := db.AlertRule{ID: uuid.New(), TriggerStatus: "down", MinFailures: 1, Severity: "info", AutoWorkOrder: false, Enabled: true}
	f := &fakeRepo{rules: []db.AlertRule{rule}, checks: []db.ListEnabledChecksWithDeviceRow{downCheck()}, open: map[string]bool{}}
	e := NewEngine(f, nil)
	res, _ := e.Evaluate(context.Background())
	if res.Opened != 1 || res.WorkOrders != 0 {
		t.Fatalf("res = %+v; want opened=1 wo=0", res)
	}
}

func TestEvaluate_SuppressedByMaintenanceWindow(t *testing.T) {
	rule := db.AlertRule{ID: uuid.New(), TriggerStatus: "down", MinFailures: 1, Severity: "critical", Enabled: true}
	chk := downCheck()
	// Device-scope window covering exactly this device suppresses the alert.
	f := &fakeRepo{
		rules:   []db.AlertRule{rule},
		checks:  []db.ListEnabledChecksWithDeviceRow{chk},
		open:    map[string]bool{},
		windows: []db.MaintenanceWindow{{Scope: "device", DeviceID: &chk.DeviceID}},
	}
	e := NewEngine(f, nil)
	res, err := e.Evaluate(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if res.Opened != 0 || res.Suppressed != 1 {
		t.Fatalf("res = %+v; want opened=0 suppressed=1 (device under maintenance)", res)
	}
}

func TestEvaluate_ResolveNotesWorkOrder(t *testing.T) {
	woID := uuid.New()
	f := &fakeRepo{
		recovered: []db.ResolveRecoveredAlertsRow{{ID: uuid.New(), WorkOrderID: &woID, Message: "x"}},
		open:      map[string]bool{},
	}
	e := NewEngine(f, nil)
	res, _ := e.Evaluate(context.Background())
	if res.Resolved != 1 {
		t.Fatalf("resolved = %d; want 1", res.Resolved)
	}
	if len(f.events) != 1 || f.events[0].EventType != "note" {
		t.Fatalf("recovered WO should get a note event: %+v", f.events)
	}
}
