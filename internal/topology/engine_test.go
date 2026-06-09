package topology

import (
	"context"
	"net/netip"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/coralsearesorts/hims/internal/storage/postgres/db"
)

// fakeQuerier is an in-memory topology.Querier for unit tests.
type fakeQuerier struct {
	arpRows      []db.FindMACByIPRow
	macRows      []db.SearchByMACRow
	resolveRows  []db.ResolveIPToMACRow
	devRow       *db.SearchByIPRow
	hostnameRows []db.SearchByHostnameRow
	devices      map[uuid.UUID]db.Device
	macTables    map[uuid.UUID][]db.ListMACForDeviceRow
	arpTables    map[uuid.UUID][]db.ListARPForDeviceRow
	links        map[uuid.UUID][]db.TopologyLink
	allLinks     []db.ListAllTopologyLinksRow
	// FDB inference inputs/outputs.
	allDevices []db.Device
	fabricMACs []db.ListFabricInterfaceMACsRow
	portCounts map[uuid.UUID][]db.MACCountByPortRow
	upserted   []db.UpsertTopologyLinkParams
}

func (f *fakeQuerier) GetDevice(_ context.Context, id uuid.UUID) (db.Device, error) {
	if d, ok := f.devices[id]; ok {
		return d, nil
	}
	return db.Device{}, nil
}
func (f *fakeQuerier) ListMACForDevice(_ context.Context, id uuid.UUID) ([]db.ListMACForDeviceRow, error) {
	return f.macTables[id], nil
}
func (f *fakeQuerier) ListARPForDevice(_ context.Context, id uuid.UUID) ([]db.ListARPForDeviceRow, error) {
	return f.arpTables[id], nil
}

func (f *fakeQuerier) FindMACByIP(_ context.Context, _ netip.Addr) ([]db.FindMACByIPRow, error) {
	return f.arpRows, nil
}
func (f *fakeQuerier) FindMACOnSwitches(_ context.Context, _ string) ([]db.FindMACOnSwitchesRow, error) {
	return nil, nil
}
func (f *fakeQuerier) SearchByIP(_ context.Context, _ *netip.Addr) (db.SearchByIPRow, error) {
	if f.devRow != nil {
		return *f.devRow, nil
	}
	return db.SearchByIPRow{}, nil
}
func (f *fakeQuerier) SearchByHostname(_ context.Context, _ *string) ([]db.SearchByHostnameRow, error) {
	return f.hostnameRows, nil
}
func (f *fakeQuerier) SearchByMAC(_ context.Context, _ string) ([]db.SearchByMACRow, error) {
	return f.macRows, nil
}
func (f *fakeQuerier) ResolveIPToMAC(_ context.Context, _ string) ([]db.ResolveIPToMACRow, error) {
	return f.resolveRows, nil
}
func (f *fakeQuerier) ListTopologyLinks(_ context.Context, id uuid.UUID) ([]db.TopologyLink, error) {
	return f.links[id], nil
}
func (f *fakeQuerier) DeleteStaleTopologyLinks(_ context.Context, _ time.Time) (int64, error) {
	return 0, nil
}
func (f *fakeQuerier) ListAllTopologyLinks(_ context.Context) ([]db.ListAllTopologyLinksRow, error) {
	return f.allLinks, nil
}
func (f *fakeQuerier) UpsertTopologyLink(_ context.Context, arg db.UpsertTopologyLinkParams) error {
	f.upserted = append(f.upserted, arg)
	return nil
}
func (f *fakeQuerier) ListNeighbors(_ context.Context, _ uuid.UUID) ([]db.Neighbor, error) {
	return nil, nil
}
func (f *fakeQuerier) ListAllDevices(_ context.Context) ([]db.Device, error) {
	return f.allDevices, nil
}
func (f *fakeQuerier) ListFabricInterfaceMACs(_ context.Context) ([]db.ListFabricInterfaceMACsRow, error) {
	return f.fabricMACs, nil
}
func (f *fakeQuerier) MACCountByPort(_ context.Context, id uuid.UUID) ([]db.MACCountByPortRow, error) {
	return f.portCounts[id], nil
}

func TestSearchMAC_NoResults(t *testing.T) {
	e := New(&fakeQuerier{})
	res, err := e.SearchMAC(context.Background(), "aa:bb:cc:dd:ee:ff")
	if err != nil {
		t.Fatal(err)
	}
	if res.QueryType != "mac" {
		t.Fatalf("QueryType = %q, want mac", res.QueryType)
	}
	if len(res.SwitchPort) != 0 {
		t.Fatalf("expected 0 ports, got %d", len(res.SwitchPort))
	}
}

func TestSearchMAC_FindsSwitch(t *testing.T) {
	swID := uuid.New()
	swIP := netip.MustParseAddr("172.21.96.24")
	ifIdx := int32(17)
	ifName := "GigabitEthernet1/0/17"
	role := "edge"
	q := &fakeQuerier{
		macRows: []db.SearchByMACRow{
			{
				Mac:        "aa:bb:cc:dd:ee:ff",
				VlanID:     15,
				IfIndex:    &ifIdx,
				DeviceID:   swID,
				SwitchName: "SW-B1-F2-ACC01",
				SwitchIp:   &swIP,
				IfName:     &ifName,
				PortRole:   &role,
			},
		},
	}
	e := New(q)
	res, err := e.SearchMAC(context.Background(), "aa:bb:cc:dd:ee:ff")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.SwitchPort) != 1 {
		t.Fatalf("expected 1 switch-port entry, got %d", len(res.SwitchPort))
	}
	sp := res.SwitchPort[0]
	if sp.SwitchID != swID {
		t.Errorf("SwitchID mismatch")
	}
	if sp.VLANID != 15 {
		t.Errorf("VLANID = %d, want 15", sp.VLANID)
	}
	if sp.IfName == nil || *sp.IfName != ifName {
		t.Errorf("IfName = %v, want %s", sp.IfName, ifName)
	}
	if sp.PortRole == nil || *sp.PortRole != "edge" {
		t.Errorf("PortRole = %v, want edge", sp.PortRole)
	}
}

func TestGraph_LayersAndConfidence(t *testing.T) {
	core, dist, acc, fw := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	now := time.Now().UTC()
	link := func(a uuid.UUID, ac string, b uuid.UUID, bn, bc, src string) db.ListAllTopologyLinksRow {
		bb := b
		bnn := bn
		bcc := bc
		return db.ListAllTopologyLinksRow{
			LocalDeviceID: a, LocalName: "L", LocalCategory: ac,
			RemoteDeviceID: &bb, RemoteName: &bnn, RemoteCategory: &bcc,
			LinkSource: src, LastSeenAt: now,
		}
	}
	// core links to dist, acc and fw (degree 3); dist links to core + acc (degree 2);
	// acc links to core + dist (degree 2)... make acc degree 1 by only linking acc<-core.
	q := &fakeQuerier{allLinks: []db.ListAllTopologyLinksRow{
		link(core, "switch", dist, "DIST", "switch", "lldp"),
		link(core, "switch", fw, "FW", "firewall", "lldp"),
		link(dist, "switch", core, "CORE", "switch", "cdp"),
		link(acc, "switch", core, "CORE", "switch", "arp"), // acc only via ARP → low confidence
	}}
	g, err := New(q).Graph(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	layer := map[uuid.UUID]string{}
	for _, n := range g.Nodes {
		layer[n.ID] = n.Layer
	}
	if layer[core] != "core" {
		t.Errorf("core layer = %q, want core", layer[core])
	}
	if layer[fw] != "edge" {
		t.Errorf("firewall layer = %q, want edge", layer[fw])
	}
	if layer[acc] != "access" {
		t.Errorf("access layer = %q, want access (degree 1)", layer[acc])
	}
	// Edges deduped (core<->dist reported from both sides collapses to one).
	if len(g.Edges) != 3 {
		t.Fatalf("edges = %d, want 3 (deduped)", len(g.Edges))
	}
	// The core<->acc edge is arp-sourced → low confidence.
	for _, e := range g.Edges {
		if (e.SourceID == core && e.TargetID == acc) || (e.SourceID == acc && e.TargetID == core) {
			if e.Confidence != "low" {
				t.Errorf("core<->acc confidence = %q, want low (arp)", e.Confidence)
			}
		}
	}
}

func TestSearchMAC_PathAndConfidence(t *testing.T) {
	mac := "aa:bb:cc:dd:ee:ff"
	accID, fwID := uuid.New(), uuid.New()
	ifIdx := int32(17)
	ifName := "Gi1/0/17"
	uplink := "Gi1/0/24"
	role := "access"
	swIP := netip.MustParseAddr("172.21.96.24")
	now := time.Now().UTC()
	q := &fakeQuerier{
		macRows: []db.SearchByMACRow{
			{Mac: mac, VlanID: 15, IfIndex: &ifIdx, DeviceID: accID, SwitchName: "SW-ACC01", SwitchIp: &swIP, IfName: &ifName, PortRole: &role},
		},
		macTables: map[uuid.UUID][]db.ListMACForDeviceRow{
			accID: {{Mac: mac, VlanID: 15, CollectionSource: "snmp", LastSeenAt: now}},
		},
		devices: map[uuid.UUID]db.Device{
			fwID: {ID: fwID, Name: "FW-EDGE", Category: "firewall"},
		},
		links: map[uuid.UUID][]db.TopologyLink{
			accID: {{LocalDeviceID: accID, RemoteDeviceID: &fwID, LocalIfName: &uplink, LinkSource: "lldp"}},
		},
	}
	e := New(q)
	res, err := e.SearchMAC(context.Background(), mac)
	if err != nil {
		t.Fatal(err)
	}
	// Source + freshness attribution on the access port.
	sp := res.SwitchPort[0]
	if sp.Source == nil || *sp.Source != "snmp" {
		t.Errorf("Source = %v, want snmp", sp.Source)
	}
	if sp.LastSeenAt == nil {
		t.Error("LastSeenAt not attributed")
	}
	// Path: endpoint -> access -> firewall (via LLDP).
	roles := []string{}
	for _, p := range res.Path {
		roles = append(roles, p.Role)
	}
	if len(res.Path) < 3 || res.Path[1].Role != "access" || res.Path[len(res.Path)-1].Role != "firewall" {
		t.Fatalf("unexpected path roles %v", roles)
	}
	// LLDP-corroborated, fresh, access port -> high confidence.
	if res.Confidence != "high" {
		t.Errorf("Confidence = %q, want high (reasons: %v)", res.Confidence, res.ConfidenceReasons)
	}
}

func TestSearchIP_ViaARP(t *testing.T) {
	mac := "aa:bb:cc:dd:ee:ff"
	devID := uuid.New()
	swIP := netip.MustParseAddr("172.21.96.24")
	ifIdx := int32(5)
	q := &fakeQuerier{
		arpRows: []db.FindMACByIPRow{
			{Mac: mac, DeviceID: devID},
		},
		macRows: []db.SearchByMACRow{
			{
				Mac:        mac,
				VlanID:     1,
				IfIndex:    &ifIdx,
				DeviceID:   devID,
				SwitchName: "SW-CORE",
				SwitchIp:   &swIP,
			},
		},
	}
	e := New(q)
	ip := netip.MustParseAddr("172.21.15.44")
	res, err := e.SearchIP(context.Background(), ip)
	if err != nil {
		t.Fatal(err)
	}
	if res.QueryType != "ip" {
		t.Errorf("QueryType = %q, want ip", res.QueryType)
	}
	if res.MAC == nil || *res.MAC != mac {
		t.Errorf("MAC = %v, want %s", res.MAC, mac)
	}
	if len(res.SwitchPort) != 1 {
		t.Fatalf("expected 1 switch-port, got %d", len(res.SwitchPort))
	}
}

func sp(s string) *string { return &s }
func i32p(i int32) *int32 { return &i }

// TestInferLinksFromFDB models the real cross-vendor gap: a Cisco access switch
// (cisco) whose MAC is learned by three Aruba switches — on an edge port of the
// nearest one (b12, 12 MACs) and on trunk ports of two aggregation switches
// (core 376, b11 328). The Cisco speaks no LLDP toward them, so only the FDB
// connects it. Expect exactly one inferred 'mac' link: cisco ↔ b12 (the edge
// port), with the trunk observations and any LLDP-proven pair ignored.
func TestInferLinksFromFDB(t *testing.T) {
	cisco := uuid.New()
	b12 := uuid.New()
	core := uuid.New()
	b11 := uuid.New()
	mac := "10:8c:cf:2d:ee:40"

	q := &fakeQuerier{
		allDevices: []db.Device{
			{ID: cisco, Name: "CHV_Mall_POE_SW01", Category: "switch", Vendor: sp("Cisco")},
			{ID: b12, Name: "CHV-B12-1", Category: "switch", Vendor: sp("Aruba/HPE")},
			{ID: core, Name: "CHV-CORE", Category: "switch", Vendor: sp("Aruba/HPE")},
			{ID: b11, Name: "CHV-B11-1", Category: "switch", Vendor: sp("Aruba/HPE")},
		},
		fabricMACs: []db.ListFabricInterfaceMACsRow{
			{DeviceID: cisco, Mac: sp("10-8C-CF-2D-EE-40")}, // different separators/case on purpose
		},
		macTables: map[uuid.UUID][]db.ListMACForDeviceRow{
			b12:  {{Mac: mac, IfIndex: i32p(25), IfName: sp("25")}},
			core: {{Mac: mac, IfIndex: i32p(21), IfName: sp("21")}},
			b11:  {{Mac: mac, IfIndex: i32p(28), IfName: sp("28")}},
		},
		portCounts: map[uuid.UUID][]db.MACCountByPortRow{
			b12:  {{IfIndex: i32p(25), MacCount: 12}},  // edge port — wins
			core: {{IfIndex: i32p(21), MacCount: 376}}, // trunk — skipped
			b11:  {{IfIndex: i32p(28), MacCount: 328}}, // trunk — skipped
		},
	}
	e := New(q)
	n, err := e.InferLinksFromFDB(context.Background())
	if err != nil {
		t.Fatalf("InferLinksFromFDB: %v", err)
	}
	if n != 1 || len(q.upserted) != 1 {
		t.Fatalf("expected exactly 1 inferred link, got n=%d upserted=%d", n, len(q.upserted))
	}
	got := q.upserted[0]
	if got.LocalDeviceID != b12 || got.RemoteDeviceID == nil || *got.RemoteDeviceID != cisco {
		t.Fatalf("expected b12 ↔ cisco, got local=%v remote=%v", got.LocalDeviceID, got.RemoteDeviceID)
	}
	if got.LinkSource != "mac" {
		t.Fatalf("expected link_source=mac, got %q", got.LinkSource)
	}
	if got.LocalIfIndex == nil || *got.LocalIfIndex != 25 {
		t.Fatalf("expected edge port 25, got %v", got.LocalIfIndex)
	}
}

// TestInferLinksFromFDB_TrunkOnlySkipped: when a device only appears behind
// high-fan-out trunk ports (no edge-like evidence), no link is inferred — we do
// not guess a direct attachment we can't substantiate.
func TestInferLinksFromFDB_TrunkOnlySkipped(t *testing.T) {
	a := uuid.New()
	b := uuid.New()
	mac := "aa:bb:cc:dd:ee:ff"
	q := &fakeQuerier{
		allDevices: []db.Device{
			{ID: a, Name: "A", Category: "switch"},
			{ID: b, Name: "B", Category: "switch"},
		},
		fabricMACs: []db.ListFabricInterfaceMACsRow{{DeviceID: a, Mac: sp(mac)}},
		macTables:  map[uuid.UUID][]db.ListMACForDeviceRow{b: {{Mac: mac, IfIndex: i32p(1)}}},
		portCounts: map[uuid.UUID][]db.MACCountByPortRow{b: {{IfIndex: i32p(1), MacCount: 500}}},
	}
	if n, err := New(q).InferLinksFromFDB(context.Background()); err != nil || n != 0 {
		t.Fatalf("expected 0 links for trunk-only evidence, got n=%d err=%v", n, err)
	}
}

// TestInferLinksFromFDB_SkipsLLDPProvenPair: if the pair is already connected by
// authoritative LLDP/CDP, the FDB pass must not add a redundant 'mac' link.
func TestInferLinksFromFDB_SkipsLLDPProvenPair(t *testing.T) {
	a := uuid.New()
	b := uuid.New()
	mac := "aa:bb:cc:00:11:22"
	q := &fakeQuerier{
		allDevices: []db.Device{
			{ID: a, Name: "A", Category: "switch"},
			{ID: b, Name: "B", Category: "switch"},
		},
		fabricMACs: []db.ListFabricInterfaceMACsRow{{DeviceID: a, Mac: sp(mac)}},
		macTables:  map[uuid.UUID][]db.ListMACForDeviceRow{b: {{Mac: mac, IfIndex: i32p(3)}}},
		portCounts: map[uuid.UUID][]db.MACCountByPortRow{b: {{IfIndex: i32p(3), MacCount: 2}}},
		allLinks:   []db.ListAllTopologyLinksRow{{LocalDeviceID: b, RemoteDeviceID: &a, LinkSource: "lldp"}},
	}
	if n, err := New(q).InferLinksFromFDB(context.Background()); err != nil || n != 0 {
		t.Fatalf("expected 0 links when pair already LLDP-proven, got n=%d err=%v", n, err)
	}
}
