// Package vsphere is the HIMS driver for VMware hosts collected over the
// vSphere API (govmomi) — the deep-collection counterpart to the SNMP esxi
// driver. SNMP gives host CPU/RAM/datastore; this gives the host→VM map.
//
// Its Session carries a *vim25.Client (built from a vendor_api/http_basic
// credential by the collector's -vsphere mode), not an SNMP client.
package vsphere

import (
	"context"
	"fmt"
	"strings"

	"github.com/vmware/govmomi/vim25"

	"github.com/coralsearesorts/hims/internal/domain"
	"github.com/coralsearesorts/hims/internal/driver"
	vs "github.com/coralsearesorts/hims/internal/vsphere"
)

// Driver identifies + collects VMware hosts via the vSphere API.
type Driver struct{}

// New returns the driver.
func New() *Driver { return &Driver{} }

// Name implements driver.Driver.
func (*Driver) Name() string { return "vmware_vsphere" }

// Template implements driver.Driver.
func (*Driver) Template() string { return "virtual_host" }

// Fingerprint matches an ESXi/vCenter HTTPS banner. Confidence 71 (below an
// authoritative SNMP switch match); the SNMP esxi driver already classifies
// hosts by OID — this path is for API-credentialed deep collection.
func (*Driver) Fingerprint(p driver.Probe) driver.Match {
	banner := strings.ToLower(p.HTTPServer + " " + hint(p, "http_title"))
	if strings.Contains(banner, "vmware") || strings.Contains(banner, "esxi") || strings.Contains(banner, "vsphere") {
		return driver.Match{Confidence: 71, Category: domain.CatVirtualHost}
	}
	return driver.NoMatch
}

// Session carries the govmomi client.
type Session struct {
	driver.SessionBase
	Client *vim25.Client
	Ctx    context.Context //nolint:containedctx
}

// Collect gathers the host's VMs + datastores and maps them into driver.Facts
// (VMs + Storage). Host CPU/RAM come from the SNMP esxi path.
func (d *Driver) Collect(sess driver.Session, _ driver.Probe) (driver.Facts, error) {
	vsess, ok := sess.(*Session)
	if !ok {
		return driver.Facts{}, fmt.Errorf("vmware_vsphere: expected *Session, got %T", sess)
	}
	inv, err := vs.Collect(vsess.Ctx, vsess.Client)
	if err != nil {
		return driver.Facts{}, err
	}
	f := driver.Facts{KV: map[string]string{"hypervisor.type": "esxi"}, Raw: map[string]any{}, Vendor: "VMware"}
	for _, vm := range inv.VMs {
		f.VMs = append(f.VMs, driver.VMSnap{
			Name: vm.Name, PowerState: vm.PowerState, VCPU: vm.NumCPU,
			MemMB: vm.MemoryMB, GuestOS: vm.GuestOS, IP: vm.IP,
		})
	}
	for i, ds := range inv.Datastores {
		f.Storage = append(f.Storage, driver.StorageSnap{
			Index: int32(i + 1), Descr: ds.Name, Type: "datastore",
			TotalBytes: ds.CapacityBytes, UsedBytes: ds.CapacityBytes - ds.FreeBytes,
		})
	}
	return f, nil
}

func hint(p driver.Probe, k string) string {
	if p.Hints == nil {
		return ""
	}
	return p.Hints[k]
}
