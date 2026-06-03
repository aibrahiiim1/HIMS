package ups

import (
	"context"
	"testing"

	"github.com/coralsearesorts/hims/internal/domain"
	"github.com/coralsearesorts/hims/internal/driver"
	"github.com/coralsearesorts/hims/internal/mibs"
	"github.com/coralsearesorts/hims/internal/snmp"
)

func TestFingerprint(t *testing.T) {
	d := New()
	if m := d.Fingerprint(driver.Probe{SNMPSysDescr: "APC Smart-UPS 1500", OpenTCPPorts: []int{161}}); m.Confidence != 68 || m.Category != domain.CatUPS {
		t.Fatalf("APC = %+v; want 68 ups", m)
	}
	if d.Fingerprint(driver.Probe{SNMPSysDescr: "Cisco IOS"}).Confidence != 0 {
		t.Fatal("switch should not match ups")
	}
}

type fakeSNMP struct {
	scalars map[string]snmp.PDU
	load    []snmp.PDU
}

func (f fakeSNMP) Connect(context.Context) error { return nil }
func (f fakeSNMP) Get(_ context.Context, oids ...string) ([]snmp.PDU, error) {
	var out []snmp.PDU
	for _, o := range oids {
		if p, ok := f.scalars[o]; ok {
			out = append(out, p)
		}
	}
	return out, nil
}
func (f fakeSNMP) BulkWalk(_ context.Context, root string, fn snmp.WalkFunc) error {
	if root == mibs.UpsOutputPercentLoadCol {
		for _, p := range f.load {
			if err := fn(p); err != nil {
				return err
			}
		}
	}
	return nil
}
func (f fakeSNMP) Walk(ctx context.Context, root string, fn snmp.WalkFunc) error {
	return f.BulkWalk(ctx, root, fn)
}
func (f fakeSNMP) Close() error { return nil }

func TestCollect_OnBatteryLow(t *testing.T) {
	cl := fakeSNMP{
		scalars: map[string]snmp.PDU{
			mibs.UpsIdentManufacturer:   {OID: mibs.UpsIdentManufacturer, Type: snmp.TypeOctetString, Value: "APC"},
			mibs.UpsIdentModel:          {OID: mibs.UpsIdentModel, Type: snmp.TypeOctetString, Value: "Smart-UPS 1500"},
			mibs.UpsBatteryStatus:       {OID: mibs.UpsBatteryStatus, Type: snmp.TypeInt, Value: 3}, // low
			mibs.UpsEstChargeRemaining:  {OID: mibs.UpsEstChargeRemaining, Type: snmp.TypeInt, Value: 42},
			mibs.UpsEstMinutesRemaining: {OID: mibs.UpsEstMinutesRemaining, Type: snmp.TypeInt, Value: 11},
		},
		load: []snmp.PDU{
			{OID: mibs.UpsOutputPercentLoadCol + ".1", Type: snmp.TypeInt, Value: 35},
			{OID: mibs.UpsOutputPercentLoadCol + ".2", Type: snmp.TypeInt, Value: 48},
		},
	}
	f, err := New().Collect(&Session{Client: cl, Ctx: context.Background()}, driver.Probe{})
	if err != nil {
		t.Fatal(err)
	}
	if f.UPS == nil || f.UPS.Manufacturer != "APC" || f.UPS.Model != "Smart-UPS 1500" {
		t.Fatalf("identity wrong: %+v", f.UPS)
	}
	if f.UPS.BatteryStatus != "low" {
		t.Fatalf("battery = %q; want low", f.UPS.BatteryStatus)
	}
	if f.UPS.ChargePct == nil || *f.UPS.ChargePct != 42 || f.UPS.RuntimeMin == nil || *f.UPS.RuntimeMin != 11 {
		t.Fatalf("charge/runtime wrong: %+v", f.UPS)
	}
	if f.UPS.LoadPct == nil || *f.UPS.LoadPct != 48 { // max across lines
		t.Fatalf("load = %v; want 48", f.UPS.LoadPct)
	}
}

func TestCollect_WrongSession(t *testing.T) {
	if _, err := New().Collect(&driver.SessionBase{}, driver.Probe{}); err == nil {
		t.Fatal("expected error for non-ups session")
	}
}
