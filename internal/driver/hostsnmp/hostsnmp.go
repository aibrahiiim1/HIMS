// Package hostsnmp is the HIMS driver for general-purpose servers reachable
// via SNMP (Linux net-snmp, Windows SNMP service). It collects
// HOST-RESOURCES-MIB (CPU/RAM/disk) + interfaces. Deeper OS inventory
// (WinRM/WMI for Windows, SSH for Linux) is a later phase; this driver is
// the SNMP-only baseline that works on any SNMP-enabled host.
package hostsnmp

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

// Driver identifies and collects SNMP-reachable servers.
type Driver struct{}

// New returns the driver.
func New() *Driver { return &Driver{} }

// Name implements driver.Driver.
func (*Driver) Name() string { return "host_snmp" }

// Template implements driver.Driver.
func (*Driver) Template() string { return "server" }

// Fingerprint implements driver.Driver. Server confidence is deliberately
// BELOW a switch driver's authoritative enterprise-OID match (90), so a
// Linux-based switch still classifies as a switch:
//
//	80 — sysObjectID under net-snmp (8072) or Microsoft (311)
//	55 — sysDescr mentions Linux/Windows/Unix with SNMP open (weak — a
//	     network OS may also say "Linux", so a switch driver outranks this)
func (*Driver) Fingerprint(p driver.Probe) driver.Match {
	oid := strings.TrimPrefix(strings.TrimSpace(p.SNMPSysObjectID), ".")
	if strings.HasPrefix(oid, strings.TrimPrefix(mibs.NetSnmpEnterprise, ".")) ||
		strings.HasPrefix(oid, strings.TrimPrefix(mibs.MicrosoftEnterprise, ".")) {
		return driver.Match{Confidence: 80, Category: domain.CatServer}
	}
	if p.HasTCPPort(161) || len(p.OpenUDPPorts) > 0 {
		d := strings.ToLower(p.SNMPSysDescr)
		for _, kw := range []string{"linux", "windows", "unix", "freebsd", "darwin"} {
			if strings.Contains(d, kw) {
				return driver.Match{Confidence: 55, Category: domain.CatServer}
			}
		}
	}
	return driver.NoMatch
}

// Session is the host_snmp collection session.
type Session struct {
	driver.SessionBase
	Client snmp.Client
	Ctx    context.Context //nolint:containedctx
}

// Collect implements driver.Collector: HOST-RESOURCES (CPU/RAM/disk) +
// interfaces. RAM total/used land as facts; disk volumes flow as storage
// snapshots; interfaces reuse the shared collector.
func (d *Driver) Collect(sess driver.Session, _ driver.Probe) (driver.Facts, error) {
	hs, ok := sess.(*Session)
	if !ok {
		return driver.Facts{}, fmt.Errorf("host_snmp: expected *Session, got %T", sess)
	}
	c, ctx := hs.Client, hs.Ctx

	f := driver.Facts{KV: map[string]string{}, Raw: map[string]any{}}
	si := swsnmp.CollectSysInfo(ctx, c)
	f.Hostname = si.Hostname
	f.OSVersion = osFromDescr(si.SysDescr)
	f.Vendor = vendorFromDescr(si.SysDescr)
	f.Raw["sysDescr"] = si.SysDescr
	f.Raw["sysObjectID"] = si.SysObjectID

	hr := swsnmp.CollectHostResources(ctx, c)
	if hr.UptimeCS > 0 {
		f.KV["hardware.uptime_centisec"] = fmt.Sprintf("%d", hr.UptimeCS)
	}
	f.KV["cpu.load_pct"] = fmt.Sprintf("%d", hr.CPULoadPct)

	// Sum RAM rows into a memory fact; disks become Storage snapshots.
	var ramTotal, ramUsed int64
	for _, s := range hr.Storage {
		if s.Type == "ram" {
			ramTotal += s.TotalBytes
			ramUsed += s.UsedBytes
		}
	}
	if ramTotal > 0 {
		f.KV["memory.total_bytes"] = fmt.Sprintf("%d", ramTotal)
		f.KV["memory.used_bytes"] = fmt.Sprintf("%d", ramUsed)
	}
	f.Storage = hr.Storage

	f.Interfaces = swsnmp.CollectInterfaces(ctx, c)
	return f, nil
}

func osFromDescr(descr string) string {
	d := strings.ToLower(descr)
	switch {
	case strings.Contains(d, "windows"):
		return "Windows"
	case strings.Contains(d, "linux"):
		return "Linux"
	case strings.Contains(d, "freebsd"):
		return "FreeBSD"
	}
	return ""
}

func vendorFromDescr(descr string) string {
	d := strings.ToLower(descr)
	switch {
	case strings.Contains(d, "windows"), strings.Contains(d, "microsoft"):
		return "Microsoft"
	case strings.Contains(d, "linux"):
		return "Linux"
	}
	return ""
}
