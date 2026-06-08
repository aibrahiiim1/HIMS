// Package huawei is the HIMS driver for Huawei VRP switches. Huawei exposes
// the standard IF-MIB / Q-BRIDGE / LLDP MIBs, so collection is the shared
// swsnmp path; the enterprise OID (1.3.6.1.4.1.2011) drives fingerprinting.
package huawei

import (
	"fmt"
	"strings"

	"github.com/coralsearesorts/hims/internal/domain"
	"github.com/coralsearesorts/hims/internal/driver"
	"github.com/coralsearesorts/hims/internal/driver/swsnmp"
	"github.com/coralsearesorts/hims/internal/mibs"
)

// Driver identifies and collects Huawei VRP switches.
type Driver struct{}

// New returns the driver.
func New() *Driver { return &Driver{} }

// Name implements driver.Driver.
func (*Driver) Name() string { return "huawei_vrp" }

// Template implements driver.Driver.
func (*Driver) Template() string { return "switch" }

// Fingerprint implements driver.Driver:
//
//	90 — sysObjectID under the Huawei enterprise OID (1.3.6.1.4.1.2011)
//	70 — sysDescr mentions Huawei/VRP/Quidway with SNMP open
func (*Driver) Fingerprint(p driver.Probe) driver.Match {
	oid := strings.TrimPrefix(strings.TrimSpace(p.SNMPSysObjectID), ".")
	if strings.HasPrefix(oid, strings.TrimPrefix(mibs.HuaweiEnterprise, ".")) {
		return driver.Match{Confidence: 90, Category: domain.CatSwitch}
	}
	if p.HasTCPPort(161) || len(p.OpenUDPPorts) > 0 {
		d := strings.ToLower(p.SNMPSysDescr)
		for _, kw := range []string{"huawei", "vrp", "quidway"} {
			if strings.Contains(d, kw) {
				return driver.Match{Confidence: 70, Category: domain.CatSwitch}
			}
		}
	}
	return driver.NoMatch
}

// Session is the Huawei collection session.
// Session aliases the shared SNMP session (swsnmp.Session).
type Session = swsnmp.Session

// Collect implements driver.Collector via the shared swsnmp collectors.
func (d *Driver) Collect(sess driver.Session, _ driver.Probe) (driver.Facts, error) {
	hs, ok := sess.(*Session)
	if !ok {
		return driver.Facts{}, fmt.Errorf("huawei_vrp: expected *Session, got %T", sess)
	}
	c, ctx := hs.Client, hs.Ctx

	f := driver.Facts{KV: map[string]string{}, Raw: map[string]any{}}
	si := swsnmp.CollectSysInfo(ctx, c)
	f.Hostname = si.Hostname
	f.Vendor = "Huawei"
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
