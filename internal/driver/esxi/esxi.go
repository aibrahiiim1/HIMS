// Package esxi is the HIMS driver for VMware ESXi hypervisor hosts reachable
// via SNMP. ESXi's SNMP agent exposes HOST-RESOURCES (CPU/RAM/datastore) +
// interfaces, which this driver collects with the shared swsnmp collectors —
// identical to a server, but classified as a virtual_host so its detail page
// shows the VM-inventory section.
//
// Deep VM enumeration (per-VM power state, vCPU, guest OS, host→VM mapping)
// requires the vSphere API (govmomi) — a new transport — and is deferred to a
// follow-up (see PROGRESS 3b/3c carry-forward). This SNMP driver is the
// host-level baseline, mirroring how host_snmp is the server baseline.
package esxi

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

// Driver identifies and collects ESXi hosts via SNMP.
type Driver struct{}

// New returns the driver.
func New() *Driver { return &Driver{} }

// Name implements driver.Driver.
func (*Driver) Name() string { return "vmware_esxi" }

// Template implements driver.Driver.
func (*Driver) Template() string { return "virtual_host" }

// Fingerprint matches the VMware enterprise OID (authoritative, 90 — an ESXi
// host is unambiguously a hypervisor) or a sysDescr mentioning VMware/ESXi
// with SNMP open (70 heuristic).
func (*Driver) Fingerprint(p driver.Probe) driver.Match {
	oid := strings.TrimPrefix(strings.TrimSpace(p.SNMPSysObjectID), ".")
	if strings.HasPrefix(oid, strings.TrimPrefix(mibs.VMwareEnterprise, ".")) {
		return driver.Match{Confidence: 90, Category: domain.CatVirtualHost}
	}
	if p.HasTCPPort(161) || len(p.OpenUDPPorts) > 0 {
		d := strings.ToLower(p.SNMPSysDescr)
		if strings.Contains(d, "vmware") || strings.Contains(d, "esxi") {
			return driver.Match{Confidence: 70, Category: domain.CatVirtualHost}
		}
	}
	return driver.NoMatch
}

// Session is the ESXi collection session.
type Session struct {
	driver.SessionBase
	Client snmp.Client
	Ctx    context.Context //nolint:containedctx
}

// Collect gathers host-level resources (CPU/RAM/datastore) + interfaces via
// SNMP. VM inventory is left empty here — it arrives with the vSphere-API
// transport in a follow-up.
func (d *Driver) Collect(sess driver.Session, _ driver.Probe) (driver.Facts, error) {
	hs, ok := sess.(*Session)
	if !ok {
		return driver.Facts{}, fmt.Errorf("vmware_esxi: expected *Session, got %T", sess)
	}
	c, ctx := hs.Client, hs.Ctx

	f := driver.Facts{KV: map[string]string{}, Raw: map[string]any{}}
	si := swsnmp.CollectSysInfo(ctx, c)
	f.Hostname = si.Hostname
	f.Vendor = "VMware"
	f.OSVersion = esxiVersion(si.SysDescr)
	f.Raw["sysDescr"] = si.SysDescr
	f.Raw["sysObjectID"] = si.SysObjectID

	hr := swsnmp.CollectHostResources(ctx, c)
	if hr.UptimeCS > 0 {
		f.KV["hardware.uptime_centisec"] = fmt.Sprintf("%d", hr.UptimeCS)
	}
	f.KV["cpu.load_pct"] = fmt.Sprintf("%d", hr.CPULoadPct)
	f.KV["hypervisor.type"] = "esxi"

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

// esxiVersion pulls a version token out of the sysDescr if present.
func esxiVersion(descr string) string {
	d := strings.ToLower(descr)
	i := strings.Index(d, "esxi")
	if i < 0 {
		return ""
	}
	return strings.TrimSpace(descr[i:])
}
