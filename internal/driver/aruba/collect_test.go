package aruba

import (
	"context"
	"errors"
	"testing"

	"github.com/coralsearesorts/hims/internal/driver"
	"github.com/coralsearesorts/hims/internal/mibs"
	"github.com/coralsearesorts/hims/internal/snmp"
)

// fakeClient is an in-memory SNMP double for unit tests.
type fakeClient struct {
	gets    map[string]snmp.PDU
	walks   map[string][]snmp.PDU
	errGet  error
	errWalk error
}

func (f *fakeClient) Connect(context.Context) error { return nil }
func (f *fakeClient) Close() error                  { return nil }

func (f *fakeClient) Get(_ context.Context, oids ...string) ([]snmp.PDU, error) {
	if f.errGet != nil {
		return nil, f.errGet
	}
	out := make([]snmp.PDU, len(oids))
	for i, o := range oids {
		if p, ok := f.gets[o]; ok {
			out[i] = p
			out[i].OID = o
		} else {
			out[i] = snmp.PDU{OID: o, Type: snmp.TypeNoSuchInstance}
		}
	}
	return out, nil
}

func (f *fakeClient) Walk(ctx context.Context, root string, fn snmp.WalkFunc) error {
	return f.bulkWalk(root, fn)
}
func (f *fakeClient) BulkWalk(_ context.Context, root string, fn snmp.WalkFunc) error {
	return f.bulkWalk(root, fn)
}
func (f *fakeClient) bulkWalk(root string, fn snmp.WalkFunc) error {
	if f.errWalk != nil {
		return f.errWalk
	}
	for key, pdus := range f.walks {
		if !snmp.HasOIDPrefix(key, root) && key != root {
			continue
		}
		for _, p := range pdus {
			if err := fn(p); err != nil {
				return err
			}
		}
	}
	return nil
}

func str(oid, val string) snmp.PDU {
	return snmp.PDU{OID: oid, Type: snmp.TypeOctetString, Value: []byte(val)}
}
func gauge(oid string, v uint) snmp.PDU {
	return snmp.PDU{OID: oid, Type: snmp.TypeGauge32, Value: v}
}

func TestCollect_SysInfo(t *testing.T) {
	fc := &fakeClient{
		gets: map[string]snmp.PDU{
			mibs.SysName:  str(mibs.SysName, "SWITCH-B1-F1"),
			mibs.SysDescr: str(mibs.SysDescr, "HP J9773A 2530-24G, revision YA.16.04"),
		},
	}
	d := New()
	sess := &Session{Client: fc, Ctx: context.Background()}
	f, err := d.Collect(sess, driver.Probe{})
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if f.Hostname != "SWITCH-B1-F1" {
		t.Errorf("Hostname = %q, want SWITCH-B1-F1", f.Hostname)
	}
	if f.OSVersion == "" {
		t.Error("OSVersion should be extracted from sysDescr")
	}
}

func TestCollect_InterfaceCountAndPortRole(t *testing.T) {
	ifX := mibs.IfXEntry1
	fc := &fakeClient{
		gets: map[string]snmp.PDU{},
		walks: map[string][]snmp.PDU{
			// ifXTable: port 1 named "Gi1/0/1", port 2 "Gi1/0/2"
			ifX: {
				str(ifX+".1.1", "Gi1/0/1"),
				str(ifX+".1.2", "Gi1/0/2"),
			},
			// ifTable: port 1 oper=up admin=up, port 2 admin=down
			mibs.IfEntry: {
				gauge(mibs.IfEntry+".7.1", 1), // admin up
				gauge(mibs.IfEntry+".8.1", 1), // oper up
				gauge(mibs.IfEntry+".7.2", 2), // admin down
				gauge(mibs.IfEntry+".8.2", 2), // oper down
			},
		},
	}
	d := New()
	sess := &Session{Client: fc, Ctx: context.Background()}
	f, err := d.Collect(sess, driver.Probe{})
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if len(f.Interfaces) == 0 {
		t.Fatal("expected interfaces to be populated")
	}
	for _, iface := range f.Interfaces {
		if iface.AdminStatus == 2 && iface.PortRole != "disabled" {
			t.Errorf("admin-down port should have role=disabled, got %s", iface.PortRole)
		}
	}
}

func TestCollect_VLANs(t *testing.T) {
	fc := &fakeClient{
		gets: map[string]snmp.PDU{},
		walks: map[string][]snmp.PDU{
			mibs.Dot1qVlanStaticEntry: {
				str(mibs.Dot1qVlanStaticEntry+".1.10", "MGMT"),
				str(mibs.Dot1qVlanStaticEntry+".1.20", "SERVERS"),
			},
		},
	}
	d := New()
	sess := &Session{Client: fc, Ctx: context.Background()}
	f, err := d.Collect(sess, driver.Probe{})
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if len(f.VLANs) != 2 {
		t.Fatalf("expected 2 VLANs, got %d", len(f.VLANs))
	}
}

func TestCollect_MACFDBPopulated(t *testing.T) {
	// Q-BRIDGE FDB: index = (vlan_id=1, MAC=aa:bb:cc:dd:ee:ff) → port 5
	fdbOID := mibs.Dot1qTpFdbEntry + ".2." + "1.170.187.204.221.238.255" // col 2 = port
	fc := &fakeClient{
		gets: map[string]snmp.PDU{},
		walks: map[string][]snmp.PDU{
			mibs.Dot1qTpFdbEntry: {
				gauge(fdbOID, 5),
			},
		},
	}
	d := New()
	sess := &Session{Client: fc, Ctx: context.Background()}
	f, err := d.Collect(sess, driver.Probe{})
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if len(f.MACs) == 0 {
		t.Fatal("expected MAC FDB entries")
	}
}

func TestCollect_LLDPNeighbors(t *testing.T) {
	// LLDP index: (timeMark=0, localPortNum=1, remIndex=1)
	base := mibs.LldpRemEntry
	fc := &fakeClient{
		gets: map[string]snmp.PDU{},
		walks: map[string][]snmp.PDU{
			base: {
				str(base+".9.0.1.1", "core-switch"), // col 9 = remSysName
				str(base+".7.0.1.1", "Gi1/0/48"),    // col 7 = remPortID
			},
		},
	}
	d := New()
	sess := &Session{Client: fc, Ctx: context.Background()}
	f, err := d.Collect(sess, driver.Probe{})
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if len(f.Neighbors) == 0 {
		t.Fatal("expected LLDP neighbor")
	}
	if f.Neighbors[0].RemSysName != "core-switch" {
		t.Errorf("RemSysName = %q, want core-switch", f.Neighbors[0].RemSysName)
	}
}

func TestCollect_WalkErrorDoesNotPanic(t *testing.T) {
	fc := &fakeClient{
		gets:    map[string]snmp.PDU{},
		errWalk: errors.New("timeout"),
	}
	d := New()
	sess := &Session{Client: fc, Ctx: context.Background()}
	_, err := d.Collect(sess, driver.Probe{})
	// Walk errors are non-fatal (we still return whatever we collected);
	// Collect itself must not return the walk error.
	if err != nil {
		t.Fatalf("Collect should not propagate walk errors, got %v", err)
	}
}
