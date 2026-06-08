package mibpack

import (
	"context"
	"errors"
	"testing"

	"github.com/coralsearesorts/hims/internal/snmp"
)

// fakeSNMP is a deterministic snmp.Client for engine tests — it replays a fixed
// set of PDUs (or returns a fixed error) so WalkTable can be exercised without a
// live agent.
type fakeSNMP struct {
	pdus []snmp.PDU
	err  error
}

func (f *fakeSNMP) Connect(context.Context) error                      { return nil }
func (f *fakeSNMP) Get(context.Context, ...string) ([]snmp.PDU, error) { return nil, nil }
func (f *fakeSNMP) Close() error                                       { return nil }
func (f *fakeSNMP) Walk(ctx context.Context, root string, fn snmp.WalkFunc) error {
	return f.BulkWalk(ctx, root, fn)
}
func (f *fakeSNMP) BulkWalk(_ context.Context, _ string, fn snmp.WalkFunc) error {
	if f.err != nil {
		return f.err
	}
	for _, p := range f.pdus {
		if e := fn(p); e != nil {
			return e
		}
	}
	return nil
}

func pdu(oid, val string) snmp.PDU { return snmp.PDU{OID: oid, Value: val} }

func TestParse(t *testing.T) {
	const mib = `
HIPATH-WIRELESS-HWC-MIB DEFINITIONS ::= BEGIN

IMPORTS
    OBJECT-TYPE FROM SNMPv2-SMI
    DisplayString FROM SNMPv2-TC;

hwcApTable OBJECT-TYPE
    SYNTAX SEQUENCE OF HwcApEntry
    ::= { hiPathWirelessMgmt 5 }

hwcApName OBJECT-TYPE
    SYNTAX DisplayString
    ::= { hwcApEntry 2 }

hwcWlanTable OBJECT-TYPE
    SYNTAX SEQUENCE OF HwcWlanEntry
    ::= { hiPathWirelessMgmt 3 }

END
`
	p := Parse(mib)
	if p.Module != "HIPATH-WIRELESS-HWC-MIB" {
		t.Fatalf("module = %q, want HIPATH-WIRELESS-HWC-MIB", p.Module)
	}
	if p.TableCount != 2 {
		t.Fatalf("table count = %d, want 2 (%v)", p.TableCount, p.Tables)
	}
	if p.ObjectCount < 3 {
		t.Fatalf("object count = %d, want >=3", p.ObjectCount)
	}
	if len(p.Imports) == 0 {
		t.Fatalf("expected imports, got none")
	}
}

func TestParseWarnsOnGarbage(t *testing.T) {
	p := Parse("this is not a MIB file at all")
	if len(p.Warnings) == 0 {
		t.Fatalf("expected a warning for missing DEFINITIONS header")
	}
}

func TestWalkTableSupported(t *testing.T) {
	const root = "1.3.6.1.4.1.5624.1.2.5.1.2"
	// entry = root.1 ; columns root.1.<col>.<index>
	f := &fakeSNMP{pdus: []snmp.PDU{
		pdu(root+".1.2.1", "AP-One"),
		pdu(root+".1.4.1", "SER-1"),
		pdu(root+".1.2.2", "AP-Two"),
		pdu(root+".1.4.2", "SER-2"),
	}}
	res := WalkTable(context.Background(), f, root, 0)
	if res.Status != StatusSupported {
		t.Fatalf("status = %q, want supported (%s)", res.Status, res.Detail)
	}
	if res.Count != 2 {
		t.Fatalf("rows = %d, want 2", res.Count)
	}
	// First row: index "1", col 2 = name, col 4 = serial.
	r := res.Rows[0]
	if r.Index != "1" || r.Cols[2] != "AP-One" || r.Cols[4] != "SER-1" {
		t.Fatalf("row[0] = %+v, want index=1 col2=AP-One col4=SER-1", r)
	}
}

func TestWalkTableEmpty(t *testing.T) {
	res := WalkTable(context.Background(), &fakeSNMP{}, "1.3.6.1.4.1.5624.1.2.3.4.4", 0)
	if res.Status != StatusEmpty {
		t.Fatalf("status = %q, want empty", res.Status)
	}
	if res.Count != 0 {
		t.Fatalf("rows = %d, want 0", res.Count)
	}
}

func TestWalkTableTimeout(t *testing.T) {
	f := &fakeSNMP{err: errors.New("request timeout (after 2 retries)")}
	res := WalkTable(context.Background(), f, "1.3.6.1.4.1.5624.1.2.5.1.2", 0)
	if res.Status != StatusTimeout {
		t.Fatalf("status = %q, want timeout", res.Status)
	}
}

func TestWalkTableMaxRows(t *testing.T) {
	const root = "1.3.6.1.4.1.5624.1.2.6.2"
	f := &fakeSNMP{pdus: []snmp.PDU{
		pdu(root+".1.0.1", "a"),
		pdu(root+".1.0.2", "b"),
		pdu(root+".1.0.3", "c"),
	}}
	res := WalkTable(context.Background(), f, root, 2)
	if res.Count != 2 {
		t.Fatalf("rows = %d, want 2 (capped)", res.Count)
	}
}
