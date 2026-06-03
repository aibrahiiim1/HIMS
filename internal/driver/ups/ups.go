// Package ups is the HIMS driver for UPS units via SNMP (UPS-MIB, RFC 1628).
// It fingerprints a UPS by banner/sysDescr and collects battery status, charge
// %, estimated runtime, and output load — the operator's headline questions
// being "is it on battery" and "how long do I have".
package ups

import (
	"fmt"
	"strings"

	"github.com/coralsearesorts/hims/internal/domain"
	"github.com/coralsearesorts/hims/internal/driver"
	"github.com/coralsearesorts/hims/internal/driver/swsnmp"
	"github.com/coralsearesorts/hims/internal/mibs"
	"github.com/coralsearesorts/hims/internal/snmp"
)

// Driver identifies + collects SNMP UPS units.
type Driver struct{}

// New returns the driver.
func New() *Driver { return &Driver{} }

// Name implements driver.Driver.
func (*Driver) Name() string { return "ups_snmp" }

// Template implements driver.Driver.
func (*Driver) Template() string { return "ups" }

var upsKeywords = []string{"smart-ups", "ups ", "apc", "eaton", "liebert", "powerware", "cyberpower", "riello", "tripp lite"}

// Fingerprint scores UPS evidence by sysDescr keyword (68, below an
// authoritative switch OID). UPS-MIB presence confirms it at collection.
func (*Driver) Fingerprint(p driver.Probe) driver.Match {
	d := strings.ToLower(p.SNMPSysDescr + " " + p.HTTPServer)
	if p.HasTCPPort(161) || len(p.OpenUDPPorts) > 0 || d != "" {
		for _, kw := range upsKeywords {
			if strings.Contains(d, kw) {
				return driver.Match{Confidence: 68, Category: domain.CatUPS}
			}
		}
	}
	return driver.NoMatch
}

// Session aliases the shared SNMP session (swsnmp.Session) so the pipeline's
// single session type drives this driver too.
type Session = swsnmp.Session

// Collect reads the UPS-MIB scalars + output-load walk.
func (d *Driver) Collect(sess driver.Session, _ driver.Probe) (driver.Facts, error) {
	us, ok := sess.(*Session)
	if !ok {
		return driver.Facts{}, fmt.Errorf("ups_snmp: expected *Session, got %T", sess)
	}
	c, ctx := us.Client, us.Ctx
	f := driver.Facts{KV: map[string]string{}, Raw: map[string]any{}}

	snap := &driver.UPSSnap{BatteryStatus: "unknown"}
	pdus, err := c.Get(ctx,
		mibs.UpsIdentManufacturer, mibs.UpsIdentModel, mibs.UpsBatteryStatus,
		mibs.UpsEstChargeRemaining, mibs.UpsEstMinutesRemaining)
	if err == nil {
		for _, p := range pdus {
			switch p.OID {
			case mibs.UpsIdentManufacturer:
				snap.Manufacturer = snmp.PDUString(p)
			case mibs.UpsIdentModel:
				snap.Model = snmp.PDUString(p)
			case mibs.UpsBatteryStatus:
				if v, ok := snmp.PDUInt64(p); ok {
					snap.BatteryStatus = batteryStatus(v)
				}
			case mibs.UpsEstChargeRemaining:
				snap.ChargePct = i32(p)
			case mibs.UpsEstMinutesRemaining:
				snap.RuntimeMin = i32(p)
			}
		}
	}
	// Output load is a per-line table; take the highest line.
	var maxLoad int64 = -1
	_ = c.BulkWalk(ctx, mibs.UpsOutputPercentLoadCol, func(p snmp.PDU) error {
		if v, ok := snmp.PDUInt64(p); ok && v > maxLoad {
			maxLoad = v
		}
		return nil
	})
	if maxLoad >= 0 {
		l := int32(maxLoad)
		snap.LoadPct = &l
	}

	f.Vendor = snap.Manufacturer
	f.Model = snap.Model
	f.UPS = snap
	return f, nil
}

func batteryStatus(v int64) string {
	switch v {
	case 2:
		return "normal"
	case 3:
		return "low"
	case 4:
		return "depleted"
	default:
		return "unknown"
	}
}

func i32(p snmp.PDU) *int32 {
	if v, ok := snmp.PDUInt64(p); ok {
		x := int32(v)
		return &x
	}
	return nil
}
