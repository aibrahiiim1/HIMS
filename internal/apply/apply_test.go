package apply

import (
	"context"
	"errors"
	"net/netip"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/coralsearesorts/hims/internal/credresolver"
	"github.com/coralsearesorts/hims/internal/discovery"
	"github.com/coralsearesorts/hims/internal/domain"
	"github.com/coralsearesorts/hims/internal/driver"
	"github.com/coralsearesorts/hims/internal/storage/postgres/db"
)

// fakeWriter records calls; LiveDeviceByIPAndLocation returns existing if set.
type fakeWriter struct {
	existing   *db.Device
	created    []db.CreateDeviceParams
	updated    []db.UpdateDiscoveredDeviceParams
	creds      []db.SetDeviceCredentialParams
	roles      []db.AddDeviceRoleParams
	facts      []db.UpsertDeviceFactParams
	ifaces     []db.UpsertInterfaceParams
	vlans      []db.UpsertVlanParams
	neighbors  []db.UpsertNeighborParams
	bmcInfo    []db.UpsertBMCInfoParams
	bmcSensors []db.UpsertBMCSensorParams
	vms        []db.UpsertVMParams
	staleCalls int
}

func (f *fakeWriter) LiveDeviceByIPAndLocation(_ context.Context, _ db.LiveDeviceByIPAndLocationParams) (db.Device, error) {
	if f.existing != nil {
		return *f.existing, nil
	}
	return db.Device{}, errors.New("not found")
}
func (f *fakeWriter) LiveDeviceByIP(_ context.Context, _ *netip.Addr) (db.Device, error) {
	if f.existing != nil {
		return *f.existing, nil
	}
	return db.Device{}, errors.New("not found")
}
func (f *fakeWriter) CreateDevice(_ context.Context, arg db.CreateDeviceParams) (db.Device, error) {
	f.created = append(f.created, arg)
	return db.Device{ID: uuid.New(), Name: arg.Name}, nil
}
func (f *fakeWriter) UpdateDiscoveredDevice(_ context.Context, arg db.UpdateDiscoveredDeviceParams) (db.Device, error) {
	f.updated = append(f.updated, arg)
	return db.Device{ID: arg.ID, Name: arg.Name}, nil
}
func (f *fakeWriter) SetDeviceCredential(_ context.Context, arg db.SetDeviceCredentialParams) error {
	f.creds = append(f.creds, arg)
	return nil
}
func (f *fakeWriter) AddDeviceRole(_ context.Context, arg db.AddDeviceRoleParams) error {
	f.roles = append(f.roles, arg)
	return nil
}
func (f *fakeWriter) UpsertDeviceFact(_ context.Context, arg db.UpsertDeviceFactParams) error {
	f.facts = append(f.facts, arg)
	return nil
}
func (f *fakeWriter) UpsertInterface(_ context.Context, arg db.UpsertInterfaceParams) (db.Interface, error) {
	f.ifaces = append(f.ifaces, arg)
	return db.Interface{}, nil
}
func (f *fakeWriter) DeleteStaleInterfaces(_ context.Context, _ db.DeleteStaleInterfacesParams) error {
	f.staleCalls++
	return nil
}
func (f *fakeWriter) UpsertVlan(_ context.Context, arg db.UpsertVlanParams) (db.Vlan, error) {
	f.vlans = append(f.vlans, arg)
	return db.Vlan{}, nil
}
func (f *fakeWriter) DeleteStaleVlans(_ context.Context, _ db.DeleteStaleVlansParams) error {
	f.staleCalls++
	return nil
}
func (f *fakeWriter) UpsertMAC(_ context.Context, _ db.UpsertMACParams) error { return nil }
func (f *fakeWriter) DeleteStaleMACEntries(_ context.Context, _ db.DeleteStaleMACEntriesParams) error {
	return nil
}
func (f *fakeWriter) UpsertNeighbor(_ context.Context, arg db.UpsertNeighborParams) (db.Neighbor, error) {
	f.neighbors = append(f.neighbors, arg)
	return db.Neighbor{}, nil
}
func (f *fakeWriter) DeleteStaleNeighbors(_ context.Context, _ db.DeleteStaleNeighborsParams) error {
	f.staleCalls++
	return nil
}
func (f *fakeWriter) UpsertServerStorage(_ context.Context, _ db.UpsertServerStorageParams) error {
	return nil
}
func (f *fakeWriter) DeleteStaleServerStorage(_ context.Context, _ db.DeleteStaleServerStorageParams) error {
	return nil
}
func (f *fakeWriter) UpsertFirewallStatus(_ context.Context, _ db.UpsertFirewallStatusParams) error {
	return nil
}
func (f *fakeWriter) UpsertVpnTunnel(_ context.Context, _ db.UpsertVpnTunnelParams) error { return nil }
func (f *fakeWriter) DeleteStaleVpnTunnels(_ context.Context, _ db.DeleteStaleVpnTunnelsParams) error {
	return nil
}
func (f *fakeWriter) UpsertHAMember(_ context.Context, _ db.UpsertHAMemberParams) error { return nil }
func (f *fakeWriter) DeleteStaleHAMembers(_ context.Context, _ db.DeleteStaleHAMembersParams) error {
	return nil
}
func (f *fakeWriter) UpsertLicense(_ context.Context, _ db.UpsertLicenseParams) error { return nil }
func (f *fakeWriter) DeleteStaleLicenses(_ context.Context, _ db.DeleteStaleLicensesParams) error {
	return nil
}
func (f *fakeWriter) UpsertBMCInfo(_ context.Context, arg db.UpsertBMCInfoParams) error {
	f.bmcInfo = append(f.bmcInfo, arg)
	return nil
}
func (f *fakeWriter) UpsertBMCSensor(_ context.Context, arg db.UpsertBMCSensorParams) error {
	f.bmcSensors = append(f.bmcSensors, arg)
	return nil
}
func (f *fakeWriter) DeleteStaleBMCSensors(_ context.Context, _ db.DeleteStaleBMCSensorsParams) error {
	return nil
}
func (f *fakeWriter) UpsertVM(_ context.Context, arg db.UpsertVMParams) (db.VirtualMachine, error) {
	f.vms = append(f.vms, arg)
	return db.VirtualMachine{}, nil
}
func (f *fakeWriter) UpsertCameraInfo(_ context.Context, _ db.UpsertCameraInfoParams) (db.CameraInfo, error) {
	return db.CameraInfo{}, nil
}
func (f *fakeWriter) UpsertWLANControllerInfo(_ context.Context, _ db.UpsertWLANControllerInfoParams) (db.WlanControllerInfo, error) {
	return db.WlanControllerInfo{}, nil
}
func (f *fakeWriter) UpsertAccessPoint(_ context.Context, _ db.UpsertAccessPointParams) (db.AccessPoint, error) {
	return db.AccessPoint{}, nil
}
func (f *fakeWriter) DeleteStaleAccessPoints(_ context.Context, _ db.DeleteStaleAccessPointsParams) error {
	return nil
}
func (f *fakeWriter) UpsertWirelessSSID(_ context.Context, _ db.UpsertWirelessSSIDParams) (db.WirelessSsid, error) {
	return db.WirelessSsid{}, nil
}
func (f *fakeWriter) DeleteStaleWirelessSSIDs(_ context.Context, _ db.DeleteStaleWirelessSSIDsParams) error {
	return nil
}
func (f *fakeWriter) UpsertWirelessClient(_ context.Context, _ db.UpsertWirelessClientParams) (db.WirelessClient, error) {
	return db.WirelessClient{}, nil
}
func (f *fakeWriter) DeleteStaleWirelessClients(_ context.Context, _ db.DeleteStaleWirelessClientsParams) error {
	return nil
}
func (f *fakeWriter) UpsertWirelessRadio(_ context.Context, _ db.UpsertWirelessRadioParams) (db.WirelessRadioStatus, error) {
	return db.WirelessRadioStatus{}, nil
}
func (f *fakeWriter) DeleteStaleWirelessRadios(_ context.Context, _ db.DeleteStaleWirelessRadiosParams) error {
	return nil
}
func (f *fakeWriter) InsertWirelessEvent(_ context.Context, _ db.InsertWirelessEventParams) error {
	return nil
}
func (f *fakeWriter) DeleteWirelessEventsForSource(_ context.Context, _ db.DeleteWirelessEventsForSourceParams) error {
	return nil
}
func (f *fakeWriter) UpsertPrinterSupply(_ context.Context, _ db.UpsertPrinterSupplyParams) error {
	return nil
}
func (f *fakeWriter) DeleteStalePrinterSupplies(_ context.Context, _ db.DeleteStalePrinterSuppliesParams) error {
	return nil
}
func (f *fakeWriter) UpsertUPSStatus(_ context.Context, _ db.UpsertUPSStatusParams) error { return nil }
func (f *fakeWriter) UpsertPbxPhone(_ context.Context, _ db.UpsertPbxPhoneParams) error   { return nil }
func (f *fakeWriter) DeleteStalePbxPhones(_ context.Context, _ db.DeleteStalePbxPhonesParams) error {
	return nil
}

type fakeSwitch struct{}

func (fakeSwitch) Name() string                          { return "aruba_hpe" }
func (fakeSwitch) Fingerprint(driver.Probe) driver.Match { return driver.NoMatch }
func (fakeSwitch) Template() string                      { return "switch" }

func switchResult() discovery.HostResult {
	return discovery.HostResult{
		IP:         netip.MustParseAddr("10.0.0.10"),
		Alive:      true,
		OpenPorts:  []int{22, 53, 161}, // 53 → dns role
		MatchedDrv: fakeSwitch{},
		Match:      driver.Match{Confidence: 90, Category: domain.CatSwitch},
		BoundCred:  &credresolver.CredRef{ID: uuid.New(), Kind: domain.CredSNMPv2c},
		Facts: &driver.Facts{
			Hostname: "SW-LOBBY", Vendor: "Aruba", KV: map[string]string{"cpu.load_pct": "5"},
			Interfaces: []driver.InterfaceSnap{{IfIndex: 1, IfName: "1/1/1", PortRole: "access"}, {IfIndex: 2, IfName: "1/1/2"}},
			VLANs:      []driver.VLANSnap{{VLANID: 10, Name: "guests"}},
			Neighbors:  []driver.NeighborSnap{{LocalIfIndex: 1, RemSysName: "core", Protocol: "lldp"}},
		},
	}
}

func TestApply_CreatePathPersistsEverything(t *testing.T) {
	f := &fakeWriter{}
	a := New(f)
	a.now = func() time.Time { return time.Unix(1700000000, 0).UTC() }

	loc := uuid.New()
	id, err := a.Apply(context.Background(), switchResult(), &loc)
	if err != nil || id == uuid.Nil {
		t.Fatalf("Apply = %v,%v; want id,nil", id, err)
	}
	if len(f.created) != 1 || f.created[0].Name != "SW-LOBBY" || f.created[0].Category != "switch" {
		t.Fatalf("device not created correctly: %+v", f.created)
	}
	if len(f.creds) != 1 {
		t.Fatalf("credential not bound: %+v", f.creds)
	}
	if !hasRole(f.roles, "dns") {
		t.Fatalf("dns role (port 53) not applied: %+v", f.roles)
	}
	if len(f.ifaces) != 2 || len(f.vlans) != 1 || len(f.neighbors) != 1 {
		t.Fatalf("inventory not persisted: ifaces=%d vlans=%d neighbors=%d", len(f.ifaces), len(f.vlans), len(f.neighbors))
	}
	if len(f.facts) != 1 {
		t.Fatalf("KV facts not persisted: %+v", f.facts)
	}
	if f.ifaces[0].CollectionSource != "snmp" || !f.ifaces[0].LastSeenAt.Equal(time.Unix(1700000000, 0).UTC()) {
		t.Fatalf("interface source/poll-stamp wrong: %+v", f.ifaces[0])
	}
	if f.staleCalls < 3 { // interfaces + vlans + neighbors prune
		t.Fatalf("stale-prune not called for each collection: %d", f.staleCalls)
	}
}

func TestApply_ReconcileUpdatesExisting(t *testing.T) {
	existingID := uuid.New()
	ip := netip.MustParseAddr("10.0.0.10")
	f := &fakeWriter{existing: &db.Device{ID: existingID, PrimaryIp: &ip}}
	a := New(f)
	loc := uuid.New()

	id, err := a.Apply(context.Background(), switchResult(), &loc)
	if err != nil {
		t.Fatal(err)
	}
	if id != existingID {
		t.Fatalf("reconcile returned %v; want existing %v", id, existingID)
	}
	if len(f.created) != 0 || len(f.updated) != 1 {
		t.Fatalf("expected update not create: created=%d updated=%d", len(f.created), len(f.updated))
	}
}

func TestApply_PersistsBMC(t *testing.T) {
	f := &fakeWriter{}
	a := New(f)
	res := discovery.HostResult{
		IP: netip.MustParseAddr("10.0.0.51"), Alive: true, OpenPorts: []int{443},
		MatchedDrv: fakeSwitch{}, Match: driver.Match{Category: domain.CatServer},
		Facts: &driver.Facts{
			KV:  map[string]string{},
			BMC: &driver.BMCSnap{Vendor: "Dell", ControllerKind: "iDRAC", Model: "R740", Health: "OK"},
			BMCSensors: []driver.BMCSensorSnap{
				{Kind: "fan", Name: "Fan 1", Status: "OK", Reading: 30, Unit: "Percent", HasReading: true},
				{Kind: "psu", Name: "PSU 1", Status: "OK"},
			},
		},
	}
	if _, err := a.Apply(context.Background(), res, nil); err != nil {
		t.Fatal(err)
	}
	if len(f.bmcInfo) != 1 || f.bmcInfo[0].ControllerKind == nil || *f.bmcInfo[0].ControllerKind != "iDRAC" {
		t.Fatalf("bmc_info not persisted: %+v", f.bmcInfo)
	}
	if len(f.bmcSensors) != 2 {
		t.Fatalf("bmc sensors = %d; want 2", len(f.bmcSensors))
	}
}

func TestApply_NotAliveSkips(t *testing.T) {
	f := &fakeWriter{}
	a := New(f)
	id, err := a.Apply(context.Background(), discovery.HostResult{Alive: false}, nil)
	if err != nil || id != uuid.Nil {
		t.Fatalf("dead host = %v,%v; want nil,nil", id, err)
	}
	if len(f.created) != 0 {
		t.Fatalf("dead host should not create a device")
	}
}

func hasRole(roles []db.AddDeviceRoleParams, want string) bool {
	for _, r := range roles {
		if r.Role == want {
			return true
		}
	}
	return false
}
