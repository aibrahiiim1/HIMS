// Package printer is the HIMS driver for network printers via SNMP
// (Printer-MIB, RFC 3805). It fingerprints a printer by banner/port and
// collects marker-supply levels (toner/ink/drum) + the lifetime page count —
// the operator's headline question being "which printer needs toner".
package printer

import (
	"context"
	"fmt"
	"strings"

	"github.com/coralsearesorts/hims/internal/domain"
	"github.com/coralsearesorts/hims/internal/driver"
	"github.com/coralsearesorts/hims/internal/driver/swsnmp"
	"github.com/coralsearesorts/hims/internal/mibs"
	"github.com/coralsearesorts/hims/internal/snmp"
)

// Driver identifies + collects SNMP network printers.
type Driver struct{}

// New returns the driver.
func New() *Driver { return &Driver{} }

// Name implements driver.Driver.
func (*Driver) Name() string { return "printer_snmp" }

// Template implements driver.Driver.
func (*Driver) Template() string { return "printer" }

var printerKeywords = []string{"jetdirect", "laserjet", "officejet", "printer", "kyocera", "ricoh", "lexmark", "brother", "canon imagerunner", "xerox", "konica"}

// Fingerprint scores printer evidence: a printer-ish sysDescr/banner (70), or
// the raw-print port 9100 open (62). Below an authoritative switch OID.
func (*Driver) Fingerprint(p driver.Probe) driver.Match {
	d := strings.ToLower(p.SNMPSysDescr + " " + p.HTTPServer)
	for _, kw := range printerKeywords {
		if strings.Contains(d, kw) {
			return driver.Match{Confidence: 70, Category: domain.CatPrinter}
		}
	}
	if p.HasTCPPort(9100) {
		return driver.Match{Confidence: 62, Category: domain.CatPrinter}
	}
	return driver.NoMatch
}

// Session aliases the shared SNMP session (swsnmp.Session).
type Session = swsnmp.Session

// Collect gathers sysinfo + marker supplies + page count.
func (d *Driver) Collect(sess driver.Session, _ driver.Probe) (driver.Facts, error) {
	ps, ok := sess.(*Session)
	if !ok {
		return driver.Facts{}, fmt.Errorf("printer_snmp: expected *Session, got %T", sess)
	}
	c, ctx := ps.Client, ps.Ctx
	f := driver.Facts{KV: map[string]string{}, Raw: map[string]any{}}

	si := swsnmp.CollectSysInfo(ctx, c)
	f.Hostname = si.Hostname
	f.Raw["sysDescr"] = si.SysDescr

	f.PrinterSupplies = CollectSupplies(ctx, c)

	// Lifetime page count: take the max across markers (usually one).
	var maxPages int64
	_ = c.BulkWalk(ctx, mibs.PrtMarkerLifeCountEntry, func(p snmp.PDU) error {
		if v, ok := snmp.PDUInt64(p); ok && v > maxPages {
			maxPages = v
		}
		return nil
	})
	if maxPages > 0 {
		f.KV["printer.page_count"] = fmt.Sprintf("%d", maxPages)
	}
	return f, nil
}

// CollectSupplies walks prtMarkerSuppliesTable into supply snapshots, computing
// a percentage where the device reports a real level + capacity.
func CollectSupplies(ctx context.Context, c snmp.Client) []driver.PrinterSupplySnap {
	type acc struct {
		descr      string
		level, max int64
		hasLevel   bool
	}
	rows := map[int32]*acc{}
	get := func(i int32) *acc {
		a := rows[i]
		if a == nil {
			a = &acc{}
			rows[i] = a
		}
		return a
	}
	_ = c.BulkWalk(ctx, mibs.PrtMarkerSuppliesEntry, func(p snmp.PDU) error {
		col, idx, ok := snmp.ColumnAndIndex(p.OID, mibs.PrtMarkerSuppliesEntry)
		if !ok || len(idx) == 0 {
			return nil
		}
		key := int32(idx[len(idx)-1]) // last index element = supply index
		a := get(key)
		switch int(col) {
		case mibs.PrtSuppliesColDescription:
			a.descr = snmp.PDUString(p)
		case mibs.PrtSuppliesColMaxCapacity:
			if v, ok := snmp.PDUInt64(p); ok {
				a.max = v
			}
		case mibs.PrtSuppliesColLevel:
			if v, ok := snmp.PDUInt64(p); ok {
				a.level, a.hasLevel = v, true
			}
		}
		return nil
	})

	out := make([]driver.PrinterSupplySnap, 0, len(rows))
	for idx, a := range rows {
		s := driver.PrinterSupplySnap{Index: idx, Description: a.descr, Level: a.level, MaxCapacity: a.max}
		// Printer-MIB: level -2 = unknown, -3 = some-remaining; only a real
		// level + positive capacity yields a percentage.
		if a.hasLevel && a.level >= 0 && a.max > 0 {
			pct := int32(a.level * 100 / a.max)
			s.Pct = &pct
		}
		out = append(out, s)
	}
	return out
}
