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
	// The HP/Canon printers that were misclassified as Aruba switches must now match
	// the printer driver from their sysDescr (172.21.60.42/.39/.73/.159 + Canon v1).
	for _, descr := range []string{
		"HP ETHERNET MULTI-ENVIRONMENT",
		"HP ETHERNET MULTI-ENVIRONMENT,ROM none,JETDIRECT,JD149",
		"Canon iR-ADV 4045 /P",
		"Canon LBP6780 /P",
		"UTAX_TA Printing System",
	} {
		if m := d.Fingerprint(driver.Probe{SNMPSysDescr: descr}); m.Confidence < 70 || m.Category != domain.CatPrinter {
			t.Errorf("printer sysDescr %q → %+v, want >=70 printer", descr, m)
		}
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

func TestVendorFromSysDescr(t *testing.T) {
	cases := map[string]string{
		"HP ETHERNET MULTI-ENVIRONMENT":              "HP",
		"Canon iR-ADV 4045 /P":                       "Canon",
		"KYOCERA Document Solutions Printing System": "Kyocera",
		"RICOH Aficio MP C3003":                      "Ricoh",
		"Brother HL-L2350DW series":                  "Brother",
		"some unknown device":                        "",
	}
	for descr, want := range cases {
		if got := VendorFromSysDescr(descr); got != want {
			t.Errorf("VendorFromSysDescr(%q) = %q, want %q", descr, got, want)
		}
	}
}

func TestModelFromSysDescr(t *testing.T) {
	if got := ModelFromSysDescr("Canon iR-ADV 4045 /P"); got != "iR-ADV 4045" {
		t.Errorf("Canon model = %q, want \"iR-ADV 4045\"", got)
	}
	// Generic HP JetDirect descr carries no model — must NOT guess.
	if got := ModelFromSysDescr("HP ETHERNET MULTI-ENVIRONMENT"); got != "" {
		t.Errorf("HP generic model = %q, want \"\"", got)
	}
}

// A printer that exposes prtGeneralPrinterName + prtGeneralSerialNumber must
// surface them as Model + Serial so the inventory row is no longer blank.
func TestCollect_ModelAndSerial(t *testing.T) {
	pdus := map[string][]snmp.PDU{
		mibs.PrtGeneralPrinterNameEntry:  {{OID: mibs.PrtGeneralPrinterNameEntry + ".1", Type: snmp.TypeOctetString, Value: "LaserJet Pro MFP M127fn"}},
		mibs.PrtGeneralSerialNumberEntry: {{OID: mibs.PrtGeneralSerialNumberEntry + ".1", Type: snmp.TypeOctetString, Value: "CNB7G1K9XY"}},
	}
	f, err := New().Collect(&Session{Client: fakeSNMP{pdus: pdus}, Ctx: context.Background()}, driver.Probe{})
	if err != nil {
		t.Fatal(err)
	}
	if f.Model != "LaserJet Pro MFP M127fn" {
		t.Errorf("Model = %q, want from prtGeneralPrinterName", f.Model)
	}
	if f.Serial != "CNB7G1K9XY" {
		t.Errorf("Serial = %q, want from prtGeneralSerialNumber", f.Serial)
	}
}
