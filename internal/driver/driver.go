// Package driver is HIMS's plugin engine. Every vendor/platform is a
// Driver; the core never branches on vendor names (ADR 0001). A driver
// knows how to: fingerprint a probed host (identify + confidence), declare
// the device category + detail template it owns, and — once the transport
// lands in Phase 1 — authenticate and collect normalized facts.
//
// Phase 0 implements the identification half (Fingerprint + Registry +
// best-match resolution), which is fully unit-testable without any network
// transport. Collect(session) is introduced with the SNMP/SSH transport in
// Phase 1 via the optional Collector interface below.
package driver

import (
	"net/netip"
	"sort"
	"time"

	"github.com/coralsearesorts/hims/internal/domain"
)

// Probe is the light-discovery evidence a driver scores against. It is
// transport-agnostic: whatever the discovery stage learned cheaply (open
// ports, SNMP sysObjectID/sysDescr, HTTP banner, mDNS, …).
type Probe struct {
	IP              netip.Addr
	OpenTCPPorts    []int
	OpenUDPPorts    []int
	SNMPSysObjectID string // e.g. ".1.3.6.1.4.1.11.2.3.7.11.x" (HP)
	SNMPSysDescr    string
	SNMPSysName     string // sysName.0 — the host's own administrative name
	SNMPSysContact  string // sysContact.0
	SNMPSysLocation string // sysLocation.0
	HTTPServer      string // Server: header / page title hint
	Hints           map[string]string
}

// HasTCPPort reports whether the probe found a given open TCP port.
func (p Probe) HasTCPPort(port int) bool {
	for _, x := range p.OpenTCPPorts {
		if x == port {
			return true
		}
	}
	return false
}

// Match is a driver's self-assessment for a probe. Confidence is 0..100;
// 0 means "not mine". The registry picks the highest-confidence match.
type Match struct {
	Confidence int
	Category   domain.DeviceCategory
}

// NoMatch is the conventional zero result.
var NoMatch = Match{Confidence: 0, Category: domain.CatUnknown}

// Driver is the identification + template surface every plugin implements.
type Driver interface {
	// Name is the stable driver id (e.g. "aruba_hpe", "cisco_ios").
	Name() string
	// Fingerprint scores how well this driver matches the probe.
	Fingerprint(p Probe) Match
	// Template is the detail-template id for devices this driver owns
	// (e.g. "switch"). Multiple drivers may share a template.
	Template() string
}

// Facts is a driver's normalized collection output (Phase 1 consumers). It
// is plain data so it can be assembled and asserted in tests without a
// transport. Roles allow a single device to be multi-role.
type Facts struct {
	Vendor    string
	Model     string
	Serial    string
	OSVersion string
	Hostname  string
	Roles     []domain.DeviceCategory
	// KV are normalized dotted facts (e.g. "hardware.uptime_s").
	KV map[string]string
	// Raw is the unparsed snapshot kept for audit/debug.
	Raw map[string]any
	// Typed collections for switch inventory.
	Interfaces []InterfaceSnap
	VLANs      []VLANSnap
	MACs       []MACSnap
	Neighbors  []NeighborSnap
	// Server inventory (HOST-RESOURCES-MIB).
	Storage []StorageSnap
	// Firewall current-state (FortiGate).
	FirewallStatus *FirewallStatusSnap
	VpnTunnels     []VpnTunnelSnap
	HAMembers      []HAMemberSnap
	Licenses       []LicenseSnap
	// BMC out-of-band inventory + health (Redfish: iLO/iDRAC).
	BMC        *BMCSnap
	BMCSensors []BMCSensorSnap
	// Virtual machines (vSphere host→VM map).
	VMs []VMSnap
	// Camera inventory (ONVIF).
	Camera *CameraSnap
	// Wireless controller + AP inventory (vendor REST: UniFi/Omada/Ruckus/Extreme).
	WLAN       *WLANSnap
	APs        []APSnap
	SSIDs      []SSIDSnap
	Stations   []WirelessClientSnap
	Radios     []RadioSnap
	WLANEvents []WirelessEventSnap
	// Printer marker supplies (Printer-MIB).
	PrinterSupplies []PrinterSupplySnap
	// UPS status (UPS-MIB).
	UPS *UPSSnap
	// PBX phone registry (Cisco CUCM AXL).
	Phones []PhoneSnap
}

// PhoneSnap is one phone registered to a PBX / call manager.
type PhoneSnap struct {
	Name        string
	Model       string
	Description string
	DevicePool  string
}

// UPSSnap is a UPS's current battery/load status (one row per device).
type UPSSnap struct {
	Manufacturer  string
	Model         string
	BatteryStatus string // normal | low | depleted | unknown
	ChargePct     *int32
	RuntimeMin    *int32
	LoadPct       *int32
}

// PrinterSupplySnap is one printer marker supply (toner/ink/drum) reading.
type PrinterSupplySnap struct {
	Index       int32
	Description string
	Level       int64
	MaxCapacity int64
	Pct         *int32 // nil when the device reports unknown/some-remaining
}

// WLANSnap is a wireless controller summary. Source records HOW it was collected
// (e.g. extreme_xcc_api, cloud_xiq, unifi) so the UI can be honest about coverage.
type WLANSnap struct {
	Vendor         string
	Version        string
	ControllerName string
	Model          string
	Serial         string
	APCount        int32
	ClientCount    int32
	SSIDCount      int32
	Source         string
}

// APSnap is one access point under a controller.
type APSnap struct {
	Name        string
	MAC         string
	Model       string
	IP          string
	Status      string // online | offline | unknown
	ClientCount int32
	Serial      string
	Firmware    string
	Band        string
	Site        string // controller-reported site/zone (XIQC hostSite; ZD location/group)
	Uptime      string // AP uptime string when the controller exposes it
}

// SSIDSnap is one SSID / WLAN service advertised by a controller.
type SSIDSnap struct {
	Name        string
	Status      string // enabled | disabled | unknown
	Security    string
	Band        string
	VLAN        string
	ClientCount int32
}

// WirelessClientSnap is one associated station.
type WirelessClientSnap struct {
	MAC      string
	IP       string
	Hostname string
	APName   string
	SSID     string
	RSSI     *int32 // signal in dBm (negative, e.g. -60)
	SNR      *int32 // signal-to-noise ratio in dB (XIQC: rss−noise; ZD: rssi field)
	Band     string
	RxBytes  *int64
	TxBytes  *int64
	// ConnectedSince is a pre-formatted local time string (ZD first-assoc); empty
	// when the controller exposes no association time (XIQC station record).
	ConnectedSince string
	// Channel is the radio channel the station is on; used only to derive each
	// SSID's in-use band (not persisted). nil when unknown.
	Channel *int32
}

// RadioSnap is one AP radio's operating status.
type RadioSnap struct {
	APName      string
	Radio       string
	Band        string
	Channel     *int32
	PowerDBm    *int32
	ClientCount int32
}

// WirelessEventSnap is one controller event / alarm.
type WirelessEventSnap struct {
	At       time.Time
	Severity string // info | warning | critical
	Category string
	Message  string
}

// CameraSnap is an IP camera's ONVIF inventory (one row per device).
type CameraSnap struct {
	Manufacturer string
	Model        string
	Resolution   string
	RTSPUrl      string
	ONVIFUrl     string
}

// VMSnap is one virtual machine under a virtualization host.
type VMSnap struct {
	Name       string
	PowerState string // on | off | suspended | unknown
	VCPU       int32
	MemMB      int32
	GuestOS    string
	IP         string
}

// BMCSnap is a server's out-of-band controller summary (one row per device).
type BMCSnap struct {
	Vendor          string // HPE | Dell
	ControllerKind  string // iLO | iDRAC
	Model           string
	Serial          string
	FirmwareVersion string
	PowerState      string
	Health          string
}

// BMCSensorSnap is one fan / PSU / temperature / storage health reading.
type BMCSensorSnap struct {
	Kind       string // fan | psu | temperature | storage
	Name       string
	Status     string
	Reading    float64
	Unit       string
	HasReading bool
}

// FirewallStatusSnap is the one-row-per-firewall HA + session summary.
type FirewallStatusSnap struct {
	HAMode        string // standalone | active-active | active-passive | unknown
	HAGroupName   string
	HAMemberCount int32
	SessionCount  *int64
}

// VpnTunnelSnap is one IPsec phase-2 tunnel.
type VpnTunnelSnap struct {
	TunnelName string
	P1Name     string
	RemoteGW   *netip.Addr
	Status     string // up | down
	InOctets   *int64
	OutOctets  *int64
}

// HAMemberSnap is one HA cluster member (fgHaStatsTable row).
type HAMemberSnap struct {
	Serial       string
	Hostname     string
	CPUPct       *int32
	MemPct       *int32
	SessionCount *int64
	SyncStatus   string // synchronized | unsynchronized | unknown
}

// LicenseSnap is one FortiGuard/support contract.
type LicenseSnap struct {
	Contract string
	Expiry   string
}

// Collector is the optional capability a driver gains once the Phase 1
// transport exists. Kept separate so Phase 0 drivers can register and be
// fingerprint-resolved before any collection code is written. Session is a
// marker the transport package will satisfy in Phase 1.
type Collector interface {
	Driver
	Collect(sess Session, p Probe) (Facts, error)
}

// Session is the transport handle a Collector uses. Phase 1 SNMP transport
// implements this. Implementations embed SessionBase to satisfy the interface.
type Session interface{ IsSession() }

// SessionBase is an embeddable marker satisfying Session. Embed it in
// concrete session types (e.g. aruba.Session) so they satisfy this interface
// without needing to import or implement anything beyond embedding.
type SessionBase struct{}

// IsSession implements Session.
func (*SessionBase) IsSession() {}

// InterfaceSnap is a single interface's inventory from one poll.
type InterfaceSnap struct {
	IfIndex     int32
	IfName      string
	IfDescr     string
	IfAlias     string
	IfType      int
	MAC         string
	SpeedMbps   int
	AdminStatus int16
	OperStatus  int16
	PortRole    string
}

// VLANSnap is one VLAN from the Q-BRIDGE catalog.
type VLANSnap struct {
	VLANID int
	Name   string
}

// StorageSnap is one server storage volume (RAM or filesystem) from
// HOST-RESOURCES-MIB.
type StorageSnap struct {
	Index      int32
	Descr      string
	Type       string // ram | disk | virtual | other
	TotalBytes int64
	UsedBytes  int64
}

// MACSnap is one FDB row (MAC → port + VLAN).
type MACSnap struct {
	MAC     string
	VLANID  int
	IfIndex int
	Status  int
}

// NeighborSnap is one LLDP/CDP neighbor.
type NeighborSnap struct {
	LocalIfIndex int
	LocalIfName  string
	RemChassisID string
	RemPortID    string
	RemPortDesc  string
	RemSysName   string
	RemSysDesc   string
	// RemMgmtIP is the neighbor's management IP, nil when not advertised.
	RemMgmtIP *netip.Addr
	Protocol  string
}

// Registry holds the registered drivers and resolves the best match for a
// probe. Safe to build once at startup; reads are lock-free thereafter.
type Registry struct {
	drivers []Driver
	byName  map[string]Driver
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry {
	return &Registry{byName: make(map[string]Driver)}
}

// Register adds a driver. Panics on a duplicate name — registration happens
// at startup and a collision is a programming error.
func (r *Registry) Register(d Driver) {
	if _, dup := r.byName[d.Name()]; dup {
		panic("driver: duplicate registration: " + d.Name())
	}
	r.byName[d.Name()] = d
	r.drivers = append(r.drivers, d)
}

// Get returns a driver by name, or nil.
func (r *Registry) Get(name string) Driver { return r.byName[name] }

// Names returns the registered driver names in registration order.
func (r *Registry) Names() []string {
	out := make([]string, len(r.drivers))
	for i, d := range r.drivers {
		out[i] = d.Name()
	}
	return out
}

// Best returns the highest-confidence driver match for a probe, or
// (nil, NoMatch) when no driver claims it (confidence 0). Ties break by
// driver name for determinism.
func (r *Registry) Best(p Probe) (Driver, Match) {
	type scored struct {
		d Driver
		m Match
	}
	var hits []scored
	for _, d := range r.drivers {
		if m := d.Fingerprint(p); m.Confidence > 0 {
			hits = append(hits, scored{d, m})
		}
	}
	if len(hits) == 0 {
		return nil, NoMatch
	}
	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].m.Confidence != hits[j].m.Confidence {
			return hits[i].m.Confidence > hits[j].m.Confidence
		}
		return hits[i].d.Name() < hits[j].d.Name()
	})
	return hits[0].d, hits[0].m
}
