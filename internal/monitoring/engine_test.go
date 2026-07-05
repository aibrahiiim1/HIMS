package monitoring

import (
	"context"
	"encoding/base64"
	"errors"
	"net"
	"net/netip"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/coralsearesorts/hims/internal/secret"
	"github.com/coralsearesorts/hims/internal/storage/postgres/db"
)

// fakeRepo is an in-memory Repo for engine tests.
type fakeRepo struct {
	due         []db.MonitoringCheck
	devices     map[uuid.UUID]db.Device
	byDevice    map[uuid.UUID][]db.MonitoringCheck
	samples     []db.InsertMonitoringSampleParams
	recorded    []db.RecordMonitoringResultParams
	devStatus   map[uuid.UUID]string
	devSignal   map[uuid.UUID]string
	needSeed    []db.ListDevicesNeedingDefaultCheckRow
	upserts     []db.UpsertMonitoringCheckParams
	needSNMP    []db.ListDevicesNeedingSNMPHealthCheckRow
	snmpUpserts []db.UpsertSupplementalSNMPCheckParams
	creds       map[uuid.UUID]db.Credential
	sviCleanups int
	sviRemoved  int64
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
func (f *fakeRepo) UpdateDeviceReachability(_ context.Context, arg db.UpdateDeviceReachabilityParams) error {
	if f.devStatus == nil {
		f.devStatus = map[uuid.UUID]string{}
	}
	f.devStatus[arg.ID] = arg.Status
	if f.devSignal == nil {
		f.devSignal = map[uuid.UUID]string{}
	}
	f.devSignal[arg.ID] = arg.ReachabilitySignal + "|" + arg.ReachabilityConfidence
	return nil
}
func (f *fakeRepo) ListDevicesNeedingDefaultCheck(context.Context) ([]db.ListDevicesNeedingDefaultCheckRow, error) {
	return f.needSeed, nil
}
func (f *fakeRepo) UpsertMonitoringCheck(_ context.Context, arg db.UpsertMonitoringCheckParams) (db.MonitoringCheck, error) {
	f.upserts = append(f.upserts, arg)
	return db.MonitoringCheck{}, nil
}
func (f *fakeRepo) ListDevicesNeedingSNMPHealthCheck(context.Context, []string) ([]db.ListDevicesNeedingSNMPHealthCheckRow, error) {
	return f.needSNMP, nil
}
func (f *fakeRepo) UpsertSupplementalSNMPCheck(_ context.Context, arg db.UpsertSupplementalSNMPCheckParams) (db.MonitoringCheck, error) {
	f.snmpUpserts = append(f.snmpUpserts, arg)
	return db.MonitoringCheck{}, nil
}
func (f *fakeRepo) DeleteSupplementalSNMPForSVIGateways(context.Context) (int64, error) {
	f.sviCleanups++
	return f.sviRemoved, nil
}
func (f *fakeRepo) GetCredential(_ context.Context, id uuid.UUID) (db.Credential, error) {
	if c, ok := f.creds[id]; ok {
		return c, nil
	}
	return db.Credential{}, errors.New("credential not found")
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

// TestRunDue_WindowsLivenessFallback is the regression for the Win11 workstation
// (172.21.60.20) stuck "down": its seeded check targets a port the box doesn't
// serve (443), but RDP (3389) is open. A Windows host must probe its real mgmt
// surface and come up, not stay down on the wrong port.
func TestRunDue_WindowsLivenessFallback(t *testing.T) {
	devID := uuid.New()
	chkID := uuid.New()
	ip := netip.MustParseAddr("172.21.60.20")
	port := int32(443) // wrong port for a Windows box — closed
	chk := db.MonitoringCheck{
		ID: chkID, DeviceID: devID, Kind: "tcp", TargetPort: &port,
		DownThreshold: 2, ConsecutiveFailures: 5, LastStatus: "down",
	}
	f := &fakeRepo{
		due:      []db.MonitoringCheck{chk},
		devices:  map[uuid.UUID]db.Device{devID: {ID: devID, PrimaryIp: &ip, Category: "endpoint", OsFamily: "windows"}},
		byDevice: map[uuid.UUID][]db.MonitoringCheck{devID: {chk}},
	}
	// Only RDP/3389 accepts; 443 and everything else refuse.
	poller := NewPoller(func(_ context.Context, _, addr string) (net.Conn, error) {
		if _, p, _ := net.SplitHostPort(addr); p == "3389" {
			return fakeConn{}, nil
		}
		return nil, errors.New("refused")
	}, time.Second)
	e := NewEngine(f, poller, nil)

	if _, err := e.RunDue(context.Background()); err != nil {
		t.Fatal(err)
	}
	if f.recorded[0].LastStatus != string(StatusUp) {
		t.Fatalf("windows host with open RDP recorded %q; want up", f.recorded[0].LastStatus)
	}
	if f.devStatus[devID] != string(StatusUp) {
		t.Fatalf("device status = %q; want up", f.devStatus[devID])
	}
}

func TestSeedSNMPHealthChecks(t *testing.T) {
	id := uuid.New()
	f := &fakeRepo{needSNMP: []db.ListDevicesNeedingSNMPHealthCheckRow{{ID: id, Category: "switch"}}}
	e := NewEngine(f, NewPoller(nil, time.Second), nil)
	n, err := e.SeedSNMPHealthChecks(context.Background())
	if err != nil || n != 1 {
		t.Fatalf("SeedSNMPHealthChecks = %d,%v; want 1,nil", n, err)
	}
	if len(f.snmpUpserts) != 1 || f.snmpUpserts[0].DeviceID != id {
		t.Fatalf("snmp upsert = %+v", f.snmpUpserts)
	}
	if f.snmpUpserts[0].Oid == nil || *f.snmpUpserts[0].Oid != SysUpTimeOID {
		t.Fatalf("snmp check OID = %v; want sysUpTime", f.snmpUpserts[0].Oid)
	}
}

// TestSeedSNMPHealthChecks_SkipsSVIGateway is the regression for 172.21.210.250:
// a VLAN-gateway/SVI IP (category switch, but attributed to an owning switch) must
// NEVER get a direct SNMP supplemental check — SNMP lives on the owning switch's
// management IP. The seeder must (a) run the SVI cleanup and (b) skip is_svi_gateway
// rows so a re-seed after cleanup can't recreate the check.
func TestSeedSNMPHealthChecks_SkipsSVIGateway(t *testing.T) {
	normal, svi := uuid.New(), uuid.New()
	f := &fakeRepo{
		sviRemoved: 1, // pretend one stale SVI check existed and was cleaned up
		needSNMP: []db.ListDevicesNeedingSNMPHealthCheckRow{
			{ID: normal, Category: "switch", IsSviGateway: false},
			{ID: svi, Category: "switch", IsSviGateway: true},
		},
	}
	e := NewEngine(f, NewPoller(nil, time.Second), nil)
	n, err := e.SeedSNMPHealthChecks(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if f.sviCleanups != 1 {
		t.Fatalf("SVI cleanup called %d times; want exactly 1", f.sviCleanups)
	}
	if n != 1 || len(f.snmpUpserts) != 1 {
		t.Fatalf("seeded %d checks (%d upserts); want exactly 1 (the non-SVI switch)", n, len(f.snmpUpserts))
	}
	if f.snmpUpserts[0].DeviceID != normal {
		t.Fatalf("seeded the wrong device: got %v, want the non-SVI %v", f.snmpUpserts[0].DeviceID, normal)
	}
	for _, u := range f.snmpUpserts {
		if u.DeviceID == svi {
			t.Fatalf("SVI gateway %v must NOT get a direct SNMP supplemental check", svi)
		}
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

func TestRunDue_SNMPMetricWithCipher(t *testing.T) {
	cipher, err := secret.NewCipher(base64.StdEncoding.EncodeToString(make([]byte, 32)))
	if err != nil {
		t.Fatal(err)
	}
	blob, keyID, _ := cipher.Seal([]byte("s3cr3t-community"))

	devID, chkID, credID := uuid.New(), uuid.New(), uuid.New()
	ip := netip.MustParseAddr("10.0.0.20")
	oid := "1.3.6.1.2.1.1.3.0"
	chk := db.MonitoringCheck{ID: chkID, DeviceID: devID, Kind: "snmp", Oid: &oid, DownThreshold: 1, LastStatus: "unknown"}
	f := &fakeRepo{
		due:      []db.MonitoringCheck{chk},
		devices:  map[uuid.UUID]db.Device{devID: {ID: devID, PrimaryIp: &ip, Category: "switch", CredentialID: &credID}},
		byDevice: map[uuid.UUID][]db.MonitoringCheck{devID: {chk}},
		creds:    map[uuid.UUID]db.Credential{credID: {ID: credID, EncryptedBlob: blob, KeyID: keyID, Kind: "snmp_v2c"}},
	}
	withFakeSNMP(t, &fakeSNMP{value: uint64(98765)}, nil)
	e := NewEngine(f, NewPoller(nil, time.Second), nil)
	e.SetCipher(cipher)

	n, err := e.RunDue(context.Background())
	if err != nil || n != 1 {
		t.Fatalf("RunDue = %d,%v; want 1,nil", n, err)
	}
	if f.recorded[0].LastStatus != string(StatusUp) {
		t.Fatalf("snmp check status = %q; want up", f.recorded[0].LastStatus)
	}
	if len(f.samples) != 1 || f.samples[0].ValueNum == nil || *f.samples[0].ValueNum != 98765 {
		t.Fatalf("snmp sample value not recorded: %+v", f.samples)
	}
}

func TestRunDue_SNMPSkippedWithoutCipher(t *testing.T) {
	chk := db.MonitoringCheck{ID: uuid.New(), DeviceID: uuid.New(), Kind: "snmp"}
	f := &fakeRepo{due: []db.MonitoringCheck{chk}}
	e := NewEngine(f, NewPoller(nil, time.Second), nil) // no cipher
	n, _ := e.RunDue(context.Background())
	if n != 0 || len(f.samples) != 0 {
		t.Fatalf("snmp check should skip without cipher; n=%d samples=%d", n, len(f.samples))
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

// TestRollupDevice_WarningKeepsSignal locks the degrade-only contract: a device
// whose reachability check is UP but which a failing SUPPLEMENTAL check degraded
// to "warning" is still REACHABLE and must keep its winning signal + confidence.
// A supplemental failure can never blank the reachability evidence.
func TestRollupDevice_WarningKeepsSignal(t *testing.T) {
	dev := uuid.New()
	reach := db.MonitoringCheck{
		ID: uuid.New(), DeviceID: dev, Kind: "tcp", Role: "reachability",
		LastStatus: "up", LastSignal: "tcp/80",
		LastEvidence: []byte(`{"up":["tcp/80","tcp/443"]}`), // 2 up -> high
	}
	supp := db.MonitoringCheck{
		ID: uuid.New(), DeviceID: dev, Kind: "snmp", Role: "supplemental", LastStatus: "down",
	}
	f := &fakeRepo{byDevice: map[uuid.UUID][]db.MonitoringCheck{dev: {reach, supp}}}
	e := NewEngine(f, NewPoller(nil, time.Second), nil)
	e.rollupDevice(context.Background(), dev)

	if got := f.devStatus[dev]; got != "warning" {
		t.Fatalf("device status = %q, want warning (reachable but supplemental down)", got)
	}
	if got := f.devSignal[dev]; got != "tcp/80|high" {
		t.Fatalf("warning device signal = %q, want tcp/80|high (evidence preserved)", got)
	}
}

// TestRollupDevice_DownBlanksSignal is the complement: a genuinely offline device
// (reachability check down) carries no proving signal.
func TestRollupDevice_DownBlanksSignal(t *testing.T) {
	dev := uuid.New()
	reach := db.MonitoringCheck{
		ID: uuid.New(), DeviceID: dev, Kind: "tcp", Role: "reachability",
		LastStatus: "down", LastSignal: "", LastEvidence: []byte(`{"down":["tcp/80"]}`),
	}
	f := &fakeRepo{byDevice: map[uuid.UUID][]db.MonitoringCheck{dev: {reach}}}
	e := NewEngine(f, NewPoller(nil, time.Second), nil)
	e.rollupDevice(context.Background(), dev)
	if got := f.devSignal[dev]; got != "|none" {
		t.Fatalf("offline device signal = %q, want |none", got)
	}
}
