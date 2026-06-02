package topology

import (
	"context"
	"net/netip"
	"testing"

	"github.com/google/uuid"

	"github.com/coralsearesorts/hims/internal/storage/postgres/db"
)

// fakeQuerier is an in-memory topology.Querier for unit tests.
type fakeQuerier struct {
	arpRows      []db.FindMACByIPRow
	macRows      []db.SearchByMACRow
	devRow       *db.SearchByIPRow
	hostnameRows []db.SearchByHostnameRow
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
func (f *fakeQuerier) ListTopologyLinks(_ context.Context, _ uuid.UUID) ([]db.TopologyLink, error) {
	return nil, nil
}
func (f *fakeQuerier) ListAllTopologyLinks(_ context.Context) ([]db.ListAllTopologyLinksRow, error) {
	return nil, nil
}
func (f *fakeQuerier) UpsertTopologyLink(_ context.Context, _ db.UpsertTopologyLinkParams) error {
	return nil
}
func (f *fakeQuerier) ListNeighbors(_ context.Context, _ uuid.UUID) ([]db.Neighbor, error) {
	return nil, nil
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
