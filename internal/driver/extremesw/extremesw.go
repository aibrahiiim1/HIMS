// Package extremesw is the HIMS driver for Extreme Networks switches — both the
// legacy ERS line (ex-Nortel/Avaya, enterprise OID .1.3.6.1.4.1.45, e.g. ERS
// 3600 series) and Extreme EXOS (.1.3.6.1.4.1.1916). Standard MIBs via swsnmp +
// LLDP neighbors. (Distinct from internal/driver/extreme, which is the XIQ
// wireless-controller REST driver.)
package extremesw

import (
	"fmt"
	"strings"

	"github.com/coralsearesorts/hims/internal/domain"
	"github.com/coralsearesorts/hims/internal/driver"
	"github.com/coralsearesorts/hims/internal/driver/swsnmp"
	"github.com/coralsearesorts/hims/internal/mibs"
)

// Driver identifies and collects Extreme switches.
type Driver struct{}

// New returns the driver.
func New() *Driver { return &Driver{} }

// Name implements driver.Driver.
func (*Driver) Name() string { return "extreme_switch" }

// Template implements driver.Driver.
func (*Driver) Template() string { return "switch" }

// Fingerprint implements driver.Driver:
//
//	90 — sysObjectID under an Extreme enterprise OID (.45 ERS / .1916 EXOS)
//	70 — sysDescr mentions an Extreme switch signature with SNMP open
func (*Driver) Fingerprint(p driver.Probe) driver.Match {
	oid := strings.TrimPrefix(strings.TrimSpace(p.SNMPSysObjectID), ".")
	if strings.HasPrefix(oid, strings.TrimPrefix(mibs.ExtremeERSEnterprise, ".")) ||
		strings.HasPrefix(oid, strings.TrimPrefix(mibs.ExtremeEXOSEnterprise, ".")) {
		return driver.Match{Confidence: 90, Category: domain.CatSwitch}
	}
	if p.HasTCPPort(161) || len(p.OpenUDPPorts) > 0 {
		d := strings.ToLower(p.SNMPSysDescr)
		for _, kw := range []string{"extreme networks", "extremexos", "exos", "ethernet routing switch"} {
			if strings.Contains(d, kw) {
				return driver.Match{Confidence: 70, Category: domain.CatSwitch}
			}
		}
	}
	return driver.NoMatch
}

// Session aliases the shared SNMP session (swsnmp.Session).
type Session = swsnmp.Session

// Collect implements driver.Collector. Standard MIBs via swsnmp + LLDP.
func (d *Driver) Collect(sess driver.Session, _ driver.Probe) (driver.Facts, error) {
	cs, ok := sess.(*Session)
	if !ok {
		return driver.Facts{}, fmt.Errorf("extreme_switch: expected *Session, got %T", sess)
	}
	c, ctx := cs.Client, cs.Ctx

	f := driver.Facts{KV: map[string]string{}, Raw: map[string]any{}}
	si := swsnmp.CollectSysInfo(ctx, c)
	f.Hostname = si.Hostname
	f.Vendor = "Extreme"
	f.OSVersion = swsnmp.FirmwareFromDescr(si.SysDescr)
	f.Raw["sysDescr"] = si.SysDescr
	f.Raw["sysObjectID"] = si.SysObjectID

	f.Interfaces = swsnmp.CollectInterfaces(ctx, c)
	f.VLANs = swsnmp.CollectVLANs(ctx, c)
	f.PortVLANs = swsnmp.CollectPortVLANs(ctx, c)
	f.MACs = swsnmp.CollectFDB(ctx, c)
	f.Neighbors = swsnmp.CollectLLDP(ctx, c)

	f.Interfaces = swsnmp.DerivePortRoles(f.Interfaces, f.Neighbors, f.MACs)
	return f, nil
}
