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
