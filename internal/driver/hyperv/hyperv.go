// Package hyperv is the HIMS driver boundary for Windows Hyper-V hosts. It is
// collection-only: WinRM + a Windows banner is NOT specific to Hyper-V (every
// Windows server speaks WinRM), so Fingerprint deliberately returns NoMatch to
// avoid misclassifying plain Windows boxes as hypervisors. The operator runs
// the collector's -hyperv path against a known Hyper-V host; finding VMs is
// what confirms the virtual_host role.
package hyperv

import (
	"context"
	"fmt"

	"github.com/coralsearesorts/hims/internal/driver"
	hv "github.com/coralsearesorts/hims/internal/hyperv"
)

// Driver collects Hyper-V VM inventory over WinRM.
type Driver struct{}

// New returns the driver.
func New() *Driver { return &Driver{} }

// Name implements driver.Driver.
func (*Driver) Name() string { return "hyperv" }

// Template implements driver.Driver.
func (*Driver) Template() string { return "virtual_host" }

// Fingerprint is intentionally NoMatch — see the package doc. Hyper-V is
// confirmed by collection, not by probe banner.
func (*Driver) Fingerprint(driver.Probe) driver.Match { return driver.NoMatch }

// Session carries the WinRM command runner.
type Session struct {
	driver.SessionBase
	Runner hv.Runner
	Ctx    context.Context //nolint:containedctx
}

// Collect runs Get-VM over WinRM and maps the result into driver.Facts.
func (d *Driver) Collect(sess driver.Session, _ driver.Probe) (driver.Facts, error) {
	hs, ok := sess.(*Session)
	if !ok {
		return driver.Facts{}, fmt.Errorf("hyperv: expected *Session, got %T", sess)
	}
	vms, err := hv.CollectVMs(hs.Ctx, hs.Runner)
	if err != nil {
		return driver.Facts{}, err
	}
	f := driver.Facts{
		Vendor: "Microsoft",
		KV:     map[string]string{"hypervisor.type": "hyperv"},
		Raw:    map[string]any{},
	}
	for _, vm := range vms {
		f.VMs = append(f.VMs, driver.VMSnap{
			Name: vm.Name, PowerState: vm.PowerState, VCPU: vm.VCPU,
			MemMB: vm.MemoryMB, GuestOS: vm.GuestOS,
		})
	}
	return f, nil
}
