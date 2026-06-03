package vsphere

import (
	"context"
	"testing"

	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25"

	"github.com/coralsearesorts/hims/internal/domain"
	"github.com/coralsearesorts/hims/internal/driver"
)

func TestFingerprint(t *testing.T) {
	d := New()
	m := d.Fingerprint(driver.Probe{HTTPServer: "VMware ESXi", OpenTCPPorts: []int{443}})
	if m.Confidence != 71 || m.Category != domain.CatVirtualHost {
		t.Fatalf("esxi banner = %+v; want 71 virtual_host", m)
	}
	if d.Fingerprint(driver.Probe{HTTPServer: "nginx"}).Confidence != 0 {
		t.Fatal("plain web server should not match vsphere")
	}
}

func TestCollect_MapsVMsAndDatastores(t *testing.T) {
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		d := New()
		f, err := d.Collect(&Session{Client: c, Ctx: ctx}, driver.Probe{})
		if err != nil {
			t.Fatal(err)
		}
		if len(f.VMs) == 0 {
			t.Fatal("expected VMs mapped into Facts")
		}
		if len(f.Storage) == 0 || f.Storage[0].Type != "datastore" {
			t.Fatalf("expected datastores as storage snaps: %+v", f.Storage)
		}
		if f.KV["hypervisor.type"] != "esxi" {
			t.Fatal("hypervisor.type fact missing")
		}
	})
}

func TestCollect_WrongSession(t *testing.T) {
	d := New()
	if _, err := d.Collect(&driver.SessionBase{}, driver.Probe{}); err == nil {
		t.Fatal("expected error for non-vsphere session")
	}
}
