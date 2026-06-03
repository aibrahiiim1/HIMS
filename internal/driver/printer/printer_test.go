package printer

import (
	"context"
	"strings"
	"testing"

	"github.com/coralsearesorts/hims/internal/domain"
	"github.com/coralsearesorts/hims/internal/driver"
	"github.com/coralsearesorts/hims/internal/mibs"
	"github.com/coralsearesorts/hims/internal/snmp"
)

func TestFingerprint(t *testing.T) {
	d := New()
	if m := d.Fingerprint(driver.Probe{SNMPSysDescr: "HP LaserJet MFP M428"}); m.Confidence != 70 || m.Category != domain.CatPrinter {
		t.Fatalf("laserjet = %+v; want 70 printer", m)
	}
	if m := d.Fingerprint(driver.Probe{OpenTCPPorts: []int{9100}}); m.Confidence != 62 {
		t.Fatalf("port 9100 = %+v; want 62", m)
	}
	if d.Fingerprint(driver.Probe{SNMPSysDescr: "Linux server"}).Confidence != 0 {
		t.Fatal("non-printer should not match")
	}
}

// fakeSNMP emits canned PDUs for the Printer-MIB walks.
type fakeSNMP struct{ pdus map[string][]snmp.PDU } // keyed by walk root

func (f fakeSNMP) Connect(context.Context) error { return nil }
func (f fakeSNMP) Get(context.Context, ...string) ([]snmp.PDU, error) {
	return []snmp.PDU{{OID: "1.3.6.1.2.1.1.5.0", Type: snmp.TypeOctetString, Value: "PRN-LOBBY"}}, nil
}
func (f fakeSNMP) BulkWalk(_ context.Context, root string, fn snmp.WalkFunc) error {
	for k, pdus := range f.pdus {
		if strings.HasPrefix(k, root) || strings.HasPrefix(root, k) {
			for _, p := range pdus {
				if err := fn(p); err != nil {
					return err
				}
			}
		}
	}
	return nil
}
func (f fakeSNMP) Walk(ctx context.Context, root string, fn snmp.WalkFunc) error {
	return f.BulkWalk(ctx, root, fn)
}
func (f fakeSNMP) Close() error { return nil }

func TestCollect_SuppliesAndPageCount(t *testing.T) {
	se := mibs.PrtMarkerSuppliesEntry
	pdus := map[string][]snmp.PDU{
		se: {
			{OID: se + ".6.1.1", Type: snmp.TypeOctetString, Value: "Black Toner"},
			{OID: se + ".8.1.1", Type: snmp.TypeInt, Value: 1000}, // max capacity
			{OID: se + ".9.1.1", Type: snmp.TypeInt, Value: 250},  // level → 25%
			{OID: se + ".6.1.2", Type: snmp.TypeOctetString, Value: "Drum"},
			{OID: se + ".8.1.2", Type: snmp.TypeInt, Value: 100},
			{OID: se + ".9.1.2", Type: snmp.TypeInt, Value: -2}, // unknown → nil pct
		},
		mibs.PrtMarkerLifeCountEntry: {
			{OID: mibs.PrtMarkerLifeCountEntry + ".1.1", Type: snmp.TypeInt, Value: 48213},
		},
	}
	d := New()
	f, err := d.Collect(&Session{Client: fakeSNMP{pdus: pdus}, Ctx: context.Background()}, driver.Probe{})
	if err != nil {
		t.Fatal(err)
	}
	if len(f.PrinterSupplies) != 2 {
		t.Fatalf("got %d supplies; want 2", len(f.PrinterSupplies))
	}
	var black, drum *driver.PrinterSupplySnap
	for i := range f.PrinterSupplies {
		switch f.PrinterSupplies[i].Description {
		case "Black Toner":
			black = &f.PrinterSupplies[i]
		case "Drum":
			drum = &f.PrinterSupplies[i]
		}
	}
	if black == nil || black.Pct == nil || *black.Pct != 25 {
		t.Fatalf("black toner pct wrong: %+v", black)
	}
	if drum == nil || drum.Pct != nil { // level -2 (unknown) → no pct
		t.Fatalf("drum pct should be nil (unknown level): %+v", drum)
	}
	if f.KV["printer.page_count"] != "48213" {
		t.Fatalf("page count = %q; want 48213", f.KV["printer.page_count"])
	}
}

func TestCollect_WrongSession(t *testing.T) {
	if _, err := New().Collect(&driver.SessionBase{}, driver.Probe{}); err == nil {
		t.Fatal("expected error for non-printer session")
	}
}
