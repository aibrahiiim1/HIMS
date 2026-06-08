// Package cisco is the HIMS driver for Cisco IOS switches. The standard
// MIBs come from swsnmp; Cisco's differentiator is CDP (in addition to, or
// instead of, LLDP) for neighbor discovery.
package cisco

import (
	"fmt"
	"strings"

	"github.com/coralsearesorts/hims/internal/domain"
	"github.com/coralsearesorts/hims/internal/driver"
	"github.com/coralsearesorts/hims/internal/driver/swsnmp"
	"github.com/coralsearesorts/hims/internal/mibs"
)

// Driver identifies and collects Cisco IOS switches.
type Driver struct{}

// New returns the driver.
func New() *Driver { return &Driver{} }

// Name implements driver.Driver.
func (*Driver) Name() string { return "cisco_ios" }

// Template implements driver.Driver.
func (*Driver) Template() string { return "switch" }

// Fingerprint implements driver.Driver:
//
//	90 — sysObjectID under the Cisco enterprise OID (1.3.6.1.4.1.9)
//	70 — sysDescr mentions "Cisco IOS" with SNMP open
func (*Driver) Fingerprint(p driver.Probe) driver.Match {
	oid := strings.TrimPrefix(strings.TrimSpace(p.SNMPSysObjectID), ".")
	if strings.HasPrefix(oid, strings.TrimPrefix(mibs.CiscoEnterprise, ".")) {
		return driver.Match{Confidence: 90, Category: domain.CatSwitch}
	}
	if p.HasTCPPort(161) || len(p.OpenUDPPorts) > 0 {
		if strings.Contains(strings.ToLower(p.SNMPSysDescr), "cisco ios") {
			return driver.Match{Confidence: 70, Category: domain.CatSwitch}
		}
	}
	return driver.NoMatch
}

// Session aliases the shared SNMP session (swsnmp.Session).
type Session = swsnmp.Session

// Collect implements driver.Collector. Standard MIBs via swsnmp + CDP, with
// LLDP merged in when present (mixed-vendor segments).
func (d *Driver) Collect(sess driver.Session, _ driver.Probe) (driver.Facts, error) {
	cs, ok := sess.(*Session)
	if !ok {
		return driver.Facts{}, fmt.Errorf("cisco_ios: expected *Session, got %T", sess)
	}
	c, ctx := cs.Client, cs.Ctx

	f := driver.Facts{KV: map[string]string{}, Raw: map[string]any{}}
	si := swsnmp.CollectSysInfo(ctx, c)
	f.Hostname = si.Hostname
	f.Vendor = "Cisco"
	f.OSVersion = swsnmp.FirmwareFromDescr(si.SysDescr)
	f.Raw["sysDescr"] = si.SysDescr
	f.Raw["sysObjectID"] = si.SysObjectID

	f.Interfaces = swsnmp.CollectInterfaces(ctx, c)
	f.VLANs = swsnmp.CollectVLANs(ctx, c)
	f.PortVLANs = swsnmp.CollectPortVLANs(ctx, c)
	f.MACs = swsnmp.CollectFDB(ctx, c)

	// Neighbors: CDP (Cisco-native) + LLDP, merged. Cisco often runs both.
	neighbors := swsnmp.CollectCDP(ctx, c)
	neighbors = append(neighbors, swsnmp.CollectLLDP(ctx, c)...)
	f.Neighbors = neighbors

	f.Interfaces = swsnmp.DerivePortRoles(f.Interfaces, f.Neighbors, f.MACs)
	return f, nil
}
