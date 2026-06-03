// Package apply is HIMS's discovery→persist worker: it takes the HostResult a
// discovery run produced (probe → classify → collect Facts) and writes it into
// the CMDB — reconciling the device by (primary_ip, location), binding the
// credential that authenticated, applying inferred roles, and upserting every
// inventory snapshot the driver collected. It is the integrator that turns the
// engines + drivers into a live system.
//
// Persistence pattern (matches the source-scoped upsert + prune discipline):
// every row written this run is stamped last_seen = pollStart; afterwards the
// DeleteStale* calls remove rows older than pollStart, so a poll that no longer
// sees an interface/VLAN/neighbor prunes it. Writes are scoped to
// collection_source = "snmp".
package apply

import (
	"context"
	"net/netip"
	"time"

	"github.com/google/uuid"

	"github.com/coralsearesorts/hims/internal/discovery"
	"github.com/coralsearesorts/hims/internal/domain"
	"github.com/coralsearesorts/hims/internal/driver"
	"github.com/coralsearesorts/hims/internal/storage/postgres/db"
)

const sourceSNMP = "snmp"
const sourceAXL = "axl"

// Writer is the persistence surface the applier needs. *db.Queries satisfies
// it; tests use a fake. Narrow-by-intent so the write set is auditable.
type Writer interface {
	LiveDeviceByIPAndLocation(ctx context.Context, arg db.LiveDeviceByIPAndLocationParams) (db.Device, error)
	LiveDeviceByIP(ctx context.Context, primaryIp *netip.Addr) (db.Device, error)
	CreateDevice(ctx context.Context, arg db.CreateDeviceParams) (db.Device, error)
	UpdateDiscoveredDevice(ctx context.Context, arg db.UpdateDiscoveredDeviceParams) (db.Device, error)
	SetDeviceCredential(ctx context.Context, arg db.SetDeviceCredentialParams) error
	AddDeviceRole(ctx context.Context, arg db.AddDeviceRoleParams) error
	UpsertDeviceFact(ctx context.Context, arg db.UpsertDeviceFactParams) error

	UpsertInterface(ctx context.Context, arg db.UpsertInterfaceParams) (db.Interface, error)
	DeleteStaleInterfaces(ctx context.Context, arg db.DeleteStaleInterfacesParams) error
	UpsertVlan(ctx context.Context, arg db.UpsertVlanParams) (db.Vlan, error)
	DeleteStaleVlans(ctx context.Context, arg db.DeleteStaleVlansParams) error
	UpsertMAC(ctx context.Context, arg db.UpsertMACParams) error
	DeleteStaleMACEntries(ctx context.Context, arg db.DeleteStaleMACEntriesParams) error
	UpsertNeighbor(ctx context.Context, arg db.UpsertNeighborParams) (db.Neighbor, error)
	DeleteStaleNeighbors(ctx context.Context, arg db.DeleteStaleNeighborsParams) error
	UpsertServerStorage(ctx context.Context, arg db.UpsertServerStorageParams) error
	DeleteStaleServerStorage(ctx context.Context, arg db.DeleteStaleServerStorageParams) error

	UpsertFirewallStatus(ctx context.Context, arg db.UpsertFirewallStatusParams) error
	UpsertVpnTunnel(ctx context.Context, arg db.UpsertVpnTunnelParams) error
	DeleteStaleVpnTunnels(ctx context.Context, arg db.DeleteStaleVpnTunnelsParams) error
	UpsertHAMember(ctx context.Context, arg db.UpsertHAMemberParams) error
	DeleteStaleHAMembers(ctx context.Context, arg db.DeleteStaleHAMembersParams) error
	UpsertLicense(ctx context.Context, arg db.UpsertLicenseParams) error
	DeleteStaleLicenses(ctx context.Context, arg db.DeleteStaleLicensesParams) error

	UpsertBMCInfo(ctx context.Context, arg db.UpsertBMCInfoParams) error
	UpsertBMCSensor(ctx context.Context, arg db.UpsertBMCSensorParams) error
	DeleteStaleBMCSensors(ctx context.Context, arg db.DeleteStaleBMCSensorsParams) error

	UpsertVM(ctx context.Context, arg db.UpsertVMParams) (db.VirtualMachine, error)
	UpsertCameraInfo(ctx context.Context, arg db.UpsertCameraInfoParams) (db.CameraInfo, error)
	UpsertWLANControllerInfo(ctx context.Context, arg db.UpsertWLANControllerInfoParams) (db.WlanControllerInfo, error)
	UpsertAccessPoint(ctx context.Context, arg db.UpsertAccessPointParams) (db.AccessPoint, error)
	UpsertPrinterSupply(ctx context.Context, arg db.UpsertPrinterSupplyParams) error
	DeleteStalePrinterSupplies(ctx context.Context, arg db.DeleteStalePrinterSuppliesParams) error
	UpsertPbxPhone(ctx context.Context, arg db.UpsertPbxPhoneParams) error
	DeleteStalePbxPhones(ctx context.Context, arg db.DeleteStalePbxPhonesParams) error
	UpsertUPSStatus(ctx context.Context, arg db.UpsertUPSStatusParams) error
}

// Applier persists discovery results.
type Applier struct {
	w   Writer
	now func() time.Time
}

// New builds an Applier. now defaults to time.Now (overridable in tests).
func New(w Writer) *Applier { return &Applier{w: w, now: func() time.Time { return time.Now().UTC() }} }

// Apply persists one HostResult and returns the device id. A host that isn't
// alive is skipped (uuid.Nil, nil). An unrecognized-but-alive host is still
// enrolled (category "unknown") so operators see it.
func (a *Applier) Apply(ctx context.Context, res discovery.HostResult, locationID *uuid.UUID) (uuid.UUID, error) {
	if !res.Alive {
		return uuid.Nil, nil
	}
	poll := a.now()

	// Category comes from the match — a driver fingerprint, or a caller like
	// AD-import that knows the category from the OS even with no driver.
	category := string(domain.CatUnknown)
	if res.Match.Category != "" {
		category = string(res.Match.Category)
	}
	var driverName *string
	if res.MatchedDrv != nil {
		n := res.MatchedDrv.Name()
		driverName = &n
	}
	name, hostname, vendor, model, serial, osVersion := identity(res)

	// Reconcile by (primary_ip, location); update if found, else create.
	dev, err := a.reconcile(ctx, res.IP, locationID, db.CreateDeviceParams{
		LocationID: locationID, PrimaryIp: &res.IP, Hostname: hostname, Name: name,
		Vendor: vendor, Model: model, Serial: serial, OsVersion: osVersion,
		Category: category, Status: "up", Driver: driverName, Metadata: []byte("{}"),
	})
	if err != nil {
		return uuid.Nil, err
	}

	// Bind the authenticating credential (bind-on-success).
	if res.BoundCred != nil {
		_ = a.w.SetDeviceCredential(ctx, db.SetDeviceCredentialParams{ID: dev.ID, CredentialID: &res.BoundCred.ID})
	}

	// Inferred roles (port-based candidates; source = "port").
	for _, role := range discovery.InferRoles(res.OpenPorts) {
		_ = a.w.AddDeviceRole(ctx, db.AddDeviceRoleParams{DeviceID: dev.ID, Role: string(role), Source: "port"})
	}

	if res.Facts != nil {
		a.applyFacts(ctx, dev.ID, res.Facts, poll)
	}
	return dev.ID, nil
}

func (a *Applier) reconcile(ctx context.Context, ip netip.Addr, locationID *uuid.UUID, create db.CreateDeviceParams) (db.Device, error) {
	// ONE live device per IP. Match by primary_ip alone (not by scope), so the
	// same physical device scanned with a site, without a site, or by a
	// different job always updates the SAME row — never a duplicate. The update
	// touches only discovered fields, preserving operator-set
	// location_id/vlan/class. A DB unique index on primary_ip (live, non-null)
	// is the hard backstop; if a concurrent job wins the insert race, we catch
	// the conflict and update instead.
	update := func(id uuid.UUID) (db.Device, error) {
		return a.w.UpdateDiscoveredDevice(ctx, db.UpdateDiscoveredDeviceParams{
			ID: id, Hostname: create.Hostname, Name: create.Name, Vendor: create.Vendor,
			Model: create.Model, Serial: create.Serial, OsVersion: create.OsVersion,
			Category: create.Category, Driver: create.Driver, Status: create.Status,
		})
	}
	if existing, err := a.w.LiveDeviceByIP(ctx, &ip); err == nil {
		return update(existing.ID)
	}
	dev, err := a.w.CreateDevice(ctx, create)
	if err != nil {
		// Lost an insert race (unique index). Re-find and update instead.
		if existing, e2 := a.w.LiveDeviceByIP(ctx, &ip); e2 == nil {
			return update(existing.ID)
		}
		return db.Device{}, err
	}
	return dev, nil
}

// applyFacts upserts KV facts + every inventory collection, then prunes rows
// not seen this poll (last_seen < poll under collection_source=snmp).
func (a *Applier) applyFacts(ctx context.Context, devID uuid.UUID, f *driver.Facts, poll time.Time) {
	for k, v := range f.KV {
		val := v
		_ = a.w.UpsertDeviceFact(ctx, db.UpsertDeviceFactParams{
			DeviceID: devID, Key: k, Value: &val, Driver: driverTag(f), ValueJson: nil,
		})
	}

	// Interfaces.
	if len(f.Interfaces) > 0 {
		for _, i := range f.Interfaces {
			_, _ = a.w.UpsertInterface(ctx, db.UpsertInterfaceParams{
				DeviceID: devID, IfIndex: i.IfIndex, IfName: nonEmpty(i.IfName), IfDescr: nonEmpty(i.IfDescr),
				IfAlias: nonEmpty(i.IfAlias), IfType: i32ptr(int32(i.IfType)), Mac: nonEmpty(i.MAC),
				SpeedMbps: i32ptr(int32(i.SpeedMbps)), AdminStatus: i16ptr(i.AdminStatus), OperStatus: i16ptr(i.OperStatus),
				PortRole: orUnknown(i.PortRole), CollectionSource: sourceSNMP, LastSeenAt: poll,
			})
		}
		_ = a.w.DeleteStaleInterfaces(ctx, db.DeleteStaleInterfacesParams{DeviceID: devID, LastSeenAt: poll, CollectionSource: sourceSNMP})
	}

	// VLANs.
	if len(f.VLANs) > 0 {
		for _, v := range f.VLANs {
			_, _ = a.w.UpsertVlan(ctx, db.UpsertVlanParams{
				DeviceID: devID, VlanID: int32(v.VLANID), Name: nonEmpty(v.Name), CollectionSource: sourceSNMP, LastSeenAt: poll,
			})
		}
		_ = a.w.DeleteStaleVlans(ctx, db.DeleteStaleVlansParams{DeviceID: devID, LastSeenAt: poll, CollectionSource: sourceSNMP})
	}

	// MAC / FDB.
	if len(f.MACs) > 0 {
		for _, m := range f.MACs {
			_ = a.w.UpsertMAC(ctx, db.UpsertMACParams{
				DeviceID: devID, Mac: m.MAC, VlanID: int32(m.VLANID), IfIndex: i32ptr(int32(m.IfIndex)),
				FdbStatus: int16(m.Status), CollectionSource: sourceSNMP, LastSeenAt: poll,
			})
		}
		_ = a.w.DeleteStaleMACEntries(ctx, db.DeleteStaleMACEntriesParams{DeviceID: devID, LastSeenAt: poll, CollectionSource: sourceSNMP})
	}

	// Neighbors (LLDP/CDP).
	if len(f.Neighbors) > 0 {
		for _, n := range f.Neighbors {
			_, _ = a.w.UpsertNeighbor(ctx, db.UpsertNeighborParams{
				DeviceID: devID, LocalIfIndex: i32ptr(int32(n.LocalIfIndex)), LocalIfName: nonEmpty(n.LocalIfName),
				RemChassisID: nonEmpty(n.RemChassisID), RemSysName: nonEmpty(n.RemSysName), RemSysDesc: nonEmpty(n.RemSysDesc),
				RemPortID: nonEmpty(n.RemPortID), RemPortDesc: nonEmpty(n.RemPortDesc), RemMgmtIp: n.RemMgmtIP,
				Protocol: orUnknown(n.Protocol), CollectionSource: sourceSNMP, LastSeenAt: poll,
			})
		}
		_ = a.w.DeleteStaleNeighbors(ctx, db.DeleteStaleNeighborsParams{DeviceID: devID, LastSeenAt: poll, CollectionSource: sourceSNMP})
	}

	// Server storage.
	if len(f.Storage) > 0 {
		for _, s := range f.Storage {
			_ = a.w.UpsertServerStorage(ctx, db.UpsertServerStorageParams{
				DeviceID: devID, HrIndex: s.Index, Descr: nonEmpty(s.Descr), StorageType: orUnknown(s.Type),
				TotalBytes: i64ptr(s.TotalBytes), UsedBytes: i64ptr(s.UsedBytes), CollectionSource: sourceSNMP, LastSeenAt: poll,
			})
		}
		_ = a.w.DeleteStaleServerStorage(ctx, db.DeleteStaleServerStorageParams{DeviceID: devID, LastSeenAt: poll, CollectionSource: sourceSNMP})
	}

	a.applyFirewall(ctx, devID, f, poll)
	a.applyBMC(ctx, devID, f, poll)

	// Wireless controller + APs (vendor REST).
	if f.WLAN != nil {
		_, _ = a.w.UpsertWLANControllerInfo(ctx, db.UpsertWLANControllerInfoParams{
			DeviceID: devID, Vendor: nonEmpty(f.WLAN.Vendor), Version: nonEmpty(f.WLAN.Version),
			ApCount: f.WLAN.APCount, ClientCount: f.WLAN.ClientCount,
		})
	}
	for _, ap := range f.APs {
		_, _ = a.w.UpsertAccessPoint(ctx, db.UpsertAccessPointParams{
			ControllerDeviceID: devID, Name: ap.Name, Mac: nonEmpty(ap.MAC), Model: nonEmpty(ap.Model),
			Ip: parseIP(ap.IP), Status: orDefaultAPStatus(ap.Status), ClientCount: ap.ClientCount,
		})
	}

	// Printer marker supplies (Printer-MIB).
	if len(f.PrinterSupplies) > 0 {
		for _, s := range f.PrinterSupplies {
			_ = a.w.UpsertPrinterSupply(ctx, db.UpsertPrinterSupplyParams{
				DeviceID: devID, SupplyIndex: s.Index, Description: nonEmpty(s.Description),
				Level: i64ptr(s.Level), MaxCapacity: i64ptr(s.MaxCapacity), Pct: s.Pct,
				CollectionSource: sourceSNMP, LastSeenAt: poll,
			})
		}
		_ = a.w.DeleteStalePrinterSupplies(ctx, db.DeleteStalePrinterSuppliesParams{DeviceID: devID, LastSeenAt: poll, CollectionSource: sourceSNMP})
	}

	// UPS status (UPS-MIB).
	if f.UPS != nil {
		_ = a.w.UpsertUPSStatus(ctx, db.UpsertUPSStatusParams{
			DeviceID: devID, Manufacturer: nonEmpty(f.UPS.Manufacturer), Model: nonEmpty(f.UPS.Model),
			BatteryStatus: orDefaultBattery(f.UPS.BatteryStatus), ChargePct: f.UPS.ChargePct,
			RuntimeMin: f.UPS.RuntimeMin, LoadPct: f.UPS.LoadPct, LastSeenAt: poll,
		})
	}

	// Camera inventory (ONVIF).
	if f.Camera != nil {
		_, _ = a.w.UpsertCameraInfo(ctx, db.UpsertCameraInfoParams{
			DeviceID: devID, Manufacturer: nonEmpty(f.Camera.Manufacturer), Model: nonEmpty(f.Camera.Model),
			Resolution: nonEmpty(f.Camera.Resolution), RtspUrl: nonEmpty(f.Camera.RTSPUrl), OnvifUrl: nonEmpty(f.Camera.ONVIFUrl),
		})
	}

	// PBX phone registry (Cisco CUCM AXL).
	if len(f.Phones) > 0 {
		for _, p := range f.Phones {
			_ = a.w.UpsertPbxPhone(ctx, db.UpsertPbxPhoneParams{
				DeviceID: devID, Name: p.Name, Model: nonEmpty(p.Model),
				Description: nonEmpty(p.Description), DevicePool: nonEmpty(p.DevicePool),
				CollectionSource: sourceAXL, LastSeenAt: poll,
			})
		}
		_ = a.w.DeleteStalePbxPhones(ctx, db.DeleteStalePbxPhonesParams{DeviceID: devID, LastSeenAt: poll, CollectionSource: sourceAXL})
	}

	// Virtual machines (vSphere host→VM map).
	for _, vm := range f.VMs {
		_, _ = a.w.UpsertVM(ctx, db.UpsertVMParams{
			HostDeviceID: devID, Name: vm.Name, PowerState: orDefaultPower(vm.PowerState),
			Vcpu: i32ptr(vm.VCPU), MemMb: i32ptr(vm.MemMB), GuestOs: nonEmpty(vm.GuestOS),
			PrimaryIp: parseIP(vm.IP),
		})
	}
}

func orDefaultPower(s string) string {
	switch s {
	case "on", "off", "suspended":
		return s
	default:
		return "unknown"
	}
}

func orDefaultBattery(s string) string {
	switch s {
	case "normal", "low", "depleted":
		return s
	default:
		return "unknown"
	}
}

func orDefaultAPStatus(s string) string {
	switch s {
	case "online", "offline":
		return s
	default:
		return "unknown"
	}
}

func parseIP(s string) *netip.Addr {
	if s == "" {
		return nil
	}
	a, err := netip.ParseAddr(s)
	if err != nil {
		return nil
	}
	return &a
}

// applyBMC persists the Redfish out-of-band controller summary + sensors.
func (a *Applier) applyBMC(ctx context.Context, devID uuid.UUID, f *driver.Facts, poll time.Time) {
	if f.BMC == nil {
		return
	}
	b := f.BMC
	_ = a.w.UpsertBMCInfo(ctx, db.UpsertBMCInfoParams{
		DeviceID: devID, Vendor: nonEmpty(b.Vendor), ControllerKind: nonEmpty(b.ControllerKind),
		Model: nonEmpty(b.Model), Serial: nonEmpty(b.Serial), FirmwareVersion: nonEmpty(b.FirmwareVersion),
		PowerState: nonEmpty(b.PowerState), Health: nonEmpty(b.Health), LastSeenAt: poll,
	})
	for _, s := range f.BMCSensors {
		_ = a.w.UpsertBMCSensor(ctx, db.UpsertBMCSensorParams{
			DeviceID: devID, Kind: s.Kind, Name: s.Name, Status: nonEmpty(s.Status),
			Reading: f64ptr(s.Reading), Unit: nonEmpty(s.Unit), HasReading: s.HasReading,
			CollectionSource: "redfish", LastSeenAt: poll,
		})
	}
	if len(f.BMCSensors) > 0 {
		_ = a.w.DeleteStaleBMCSensors(ctx, db.DeleteStaleBMCSensorsParams{DeviceID: devID, LastSeenAt: poll, CollectionSource: "redfish"})
	}
}

// applyFirewall persists the FortiGate current-state collections.
func (a *Applier) applyFirewall(ctx context.Context, devID uuid.UUID, f *driver.Facts, poll time.Time) {
	if f.FirewallStatus != nil {
		s := f.FirewallStatus
		_ = a.w.UpsertFirewallStatus(ctx, db.UpsertFirewallStatusParams{
			DeviceID: devID, HaMode: orUnknown(s.HAMode), HaGroupName: nonEmpty(s.HAGroupName),
			HaMemberCount: s.HAMemberCount, SessionCount: s.SessionCount, CollectionSource: sourceSNMP, LastSeenAt: poll,
		})
	}
	if len(f.VpnTunnels) > 0 {
		for _, t := range f.VpnTunnels {
			_ = a.w.UpsertVpnTunnel(ctx, db.UpsertVpnTunnelParams{
				DeviceID: devID, TunnelName: t.TunnelName, P1Name: nonEmpty(t.P1Name), RemoteGw: t.RemoteGW,
				Status: orUnknown(t.Status), InOctets: t.InOctets, OutOctets: t.OutOctets, CollectionSource: sourceSNMP, LastSeenAt: poll,
			})
		}
		_ = a.w.DeleteStaleVpnTunnels(ctx, db.DeleteStaleVpnTunnelsParams{DeviceID: devID, LastSeenAt: poll, CollectionSource: sourceSNMP})
	}
	if len(f.HAMembers) > 0 {
		for _, m := range f.HAMembers {
			_ = a.w.UpsertHAMember(ctx, db.UpsertHAMemberParams{
				DeviceID: devID, Serial: m.Serial, Hostname: nonEmpty(m.Hostname), CpuPct: m.CPUPct, MemPct: m.MemPct,
				SessionCount: m.SessionCount, SyncStatus: orUnknown(m.SyncStatus), CollectionSource: sourceSNMP, LastSeenAt: poll,
			})
		}
		_ = a.w.DeleteStaleHAMembers(ctx, db.DeleteStaleHAMembersParams{DeviceID: devID, LastSeenAt: poll, CollectionSource: sourceSNMP})
	}
	if len(f.Licenses) > 0 {
		for _, l := range f.Licenses {
			_ = a.w.UpsertLicense(ctx, db.UpsertLicenseParams{
				DeviceID: devID, Contract: l.Contract, Expiry: nonEmpty(l.Expiry), CollectionSource: sourceSNMP, LastSeenAt: poll,
			})
		}
		_ = a.w.DeleteStaleLicenses(ctx, db.DeleteStaleLicensesParams{DeviceID: devID, LastSeenAt: poll, CollectionSource: sourceSNMP})
	}
}

func driverTag(f *driver.Facts) string {
	if f.Vendor != "" {
		return f.Vendor
	}
	return "snmp"
}

func i16ptr(v int16) *int16     { return &v }
func i32ptr(v int32) *int32     { return &v }
func i64ptr(v int64) *int64     { return &v }
func f64ptr(v float64) *float64 { return &v }
func orUnknown(s string) string {
	if s == "" {
		return "unknown"
	}
	return s
}

func identity(res discovery.HostResult) (name string, hostname, vendor, model, serial, osVersion *string) {
	name = res.IP.String()
	if res.Facts != nil {
		if res.Facts.Hostname != "" {
			name = res.Facts.Hostname
			hostname = strptr(res.Facts.Hostname)
		}
		vendor = nonEmpty(res.Facts.Vendor)
		model = nonEmpty(res.Facts.Model)
		serial = nonEmpty(res.Facts.Serial)
		osVersion = nonEmpty(res.Facts.OSVersion)
	}
	return
}

func nonEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
func strptr(s string) *string { return &s }
