package monitoring

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/coralsearesorts/hims/internal/storage/postgres/db"
)

// fakeRepo is an in-memory Repo for engine tests.
type fakeRepo struct {
	due       []db.MonitoringCheck
	devices   map[uuid.UUID]db.Device
	byDevice  map[uuid.UUID][]db.MonitoringCheck
	samples   []db.InsertMonitoringSampleParams
	recorded  []db.RecordMonitoringResultParams
	devStatus map[uuid.UUID]string
	needSeed  []db.ListDevicesNeedingDefaultCheckRow
	upserts   []db.UpsertMonitoringCheckParams
}

func (f *fakeRepo) ListDueMonitoringChecks(context.Context) ([]db.MonitoringCheck, error) {
	return f.due, nil
}
func (f *fakeRepo) GetDevice(_ context.Context, id uuid.UUID) (db.Device, error) {
	d, ok := f.devices[id]
	if !ok {
		return db.Device{}, errors.New("not found")
	}
	return d, nil
}
func (f *fakeRepo) RecordMonitoringResult(_ context.Context, arg db.RecordMonitoringResultParams) (db.MonitoringCheck, error) {
	f.recorded = append(f.recorded, arg)
	// Reflect the new status into byDevice so rollup sees it.
	for i, c := range f.byDevice[deviceOf(f, arg.ID)] {
		if c.ID == arg.ID {
			f.byDevice[c.DeviceID][i].LastStatus = arg.LastStatus
			f.byDevice[c.DeviceID][i].ConsecutiveFailures = arg.ConsecutiveFailures
		}
	}
	return db.MonitoringCheck{}, nil
}
func (f *fakeRepo) InsertMonitoringSample(_ context.Context, arg db.InsertMonitoringSampleParams) error {
	f.samples = append(f.samples, arg)
	return nil
}
func (f *fakeRepo) ListMonitoringChecksByDevice(_ context.Context, id uuid.UUID) ([]db.MonitoringCheck, error) {
	return f.byDevice[id], nil
}
func (f *fakeRepo) UpdateDeviceMonitoringStatus(_ context.Context, arg db.UpdateDeviceMonitoringStatusParams) error {
	if f.devStatus == nil {
		f.devStatus = map[uuid.UUID]string{}
	}
	f.devStatus[arg.ID] = arg.Status
	return nil
}
func (f *fakeRepo) ListDevicesNeedingDefaultCheck(context.Context) ([]db.ListDevicesNeedingDefaultCheckRow, error) {
	return f.needSeed, nil
}
func (f *fakeRepo) UpsertMonitoringCheck(_ context.Context, arg db.UpsertMonitoringCheckParams) (db.MonitoringCheck, error) {
	f.upserts = append(f.upserts, arg)
	return db.MonitoringCheck{}, nil
}

func deviceOf(f *fakeRepo, checkID uuid.UUID) uuid.UUID {
	for _, c := range f.due {
		if c.ID == checkID {
			return c.DeviceID
		}
	}
	return uuid.Nil
}

func TestRunDue_DownAfterFailure(t *testing.T) {
	devID := uuid.New()
	chkID := uuid.New()
	ip := netip.MustParseAddr("10.0.0.7")
	port := int32(22)
	chk := db.MonitoringCheck{
		ID: chkID, DeviceID: devID, Kind: "tcp", TargetPort: &port,
		DownThreshold: 1, ConsecutiveFailures: 0, LastStatus: "unknown",
	}
	f := &fakeRepo{
		due:      []db.MonitoringCheck{chk},
		devices:  map[uuid.UUID]db.Device{devID: {ID: devID, PrimaryIp: &ip, Category: "switch"}},
		byDevice: map[uuid.UUID][]db.MonitoringCheck{devID: {chk}},
	}
	// Dialer always fails → down (threshold 1).
	poller := NewPoller(func(context.Context, string, string) (net.Conn, error) {
		return nil, errors.New("refused")
	}, time.Second)
	e := NewEngine(f, poller, nil)

	n, err := e.RunDue(context.Background())
	if err != nil || n != 1 {
		t.Fatalf("RunDue = %d,%v; want 1,nil", n, err)
	}
	if len(f.recorded) != 1 || f.recorded[0].LastStatus != string(StatusDown) {
		t.Fatalf("recorded = %+v; want one down", f.recorded)
	}
	if len(f.samples) != 1 || f.samples[0].Status != string(StatusDown) {
		t.Fatalf("samples = %+v; want one down sample", f.samples)
	}
	if f.samples[0].Error == nil {
		t.Fatalf("down sample should carry an error string")
	}
	if f.devStatus[devID] != string(StatusDown) {
		t.Fatalf("device status = %q; want down", f.devStatus[devID])
	}
}

func TestRunDue_UpOnSuccess(t *testing.T) {
	devID := uuid.New()
	chkID := uuid.New()
	ip := netip.MustParseAddr("10.0.0.8")
	port := int32(443)
	chk := db.MonitoringCheck{
		ID: chkID, DeviceID: devID, Kind: "tcp", TargetPort: &port,
		DownThreshold: 2, ConsecutiveFailures: 3, LastStatus: "down",
	}
	f := &fakeRepo{
		due:      []db.MonitoringCheck{chk},
		devices:  map[uuid.UUID]db.Device{devID: {ID: devID, PrimaryIp: &ip, Category: "firewall"}},
		byDevice: map[uuid.UUID][]db.MonitoringCheck{devID: {chk}},
	}
	poller := NewPoller(func(context.Context, string, string) (net.Conn, error) {
		return fakeConn{}, nil
	}, time.Second)
	e := NewEngine(f, poller, nil)

	if _, err := e.RunDue(context.Background()); err != nil {
		t.Fatal(err)
	}
	if f.recorded[0].LastStatus != string(StatusUp) || f.recorded[0].ConsecutiveFailures != 0 {
		t.Fatalf("recovery not recorded: %+v", f.recorded[0])
	}
	if f.devStatus[devID] != string(StatusUp) {
		t.Fatalf("device status = %q; want up", f.devStatus[devID])
	}
}

func TestRunDue_SkipsNonTCP(t *testing.T) {
	chk := db.MonitoringCheck{ID: uuid.New(), DeviceID: uuid.New(), Kind: "snmp"}
	f := &fakeRepo{due: []db.MonitoringCheck{chk}}
	e := NewEngine(f, NewPoller(nil, time.Second), nil)
	n, _ := e.RunDue(context.Background())
	if n != 0 || len(f.samples) != 0 {
		t.Fatalf("snmp check should be skipped in core; n=%d samples=%d", n, len(f.samples))
	}
}

func TestSeedDefaults(t *testing.T) {
	ip := netip.MustParseAddr("10.0.0.1")
	f := &fakeRepo{needSeed: []db.ListDevicesNeedingDefaultCheckRow{
		{ID: uuid.New(), PrimaryIp: &ip, Category: "switch"},
		{ID: uuid.New(), PrimaryIp: &ip, Category: "firewall"},
	}}
	e := NewEngine(f, NewPoller(nil, time.Second), nil)
	n, err := e.SeedDefaults(context.Background())
	if err != nil || n != 2 {
		t.Fatalf("SeedDefaults = %d,%v; want 2,nil", n, err)
	}
	if *f.upserts[0].TargetPort != 22 || *f.upserts[1].TargetPort != 443 {
		t.Fatalf("seeded ports wrong: %d,%d", *f.upserts[0].TargetPort, *f.upserts[1].TargetPort)
	}
}
