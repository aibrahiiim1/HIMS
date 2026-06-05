package vsphere

import (
	"context"
	"testing"

	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25"
)

// TestCollect_AgainstSimulator runs against govmomi's in-memory vCenter
// (vcsim) default model — no real hardware. The default model ships a handful
// of VMs + datastores, so we assert we retrieved them and normalized fields.
func TestCollect_AgainstSimulator(t *testing.T) {
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		inv, err := Collect(ctx, c)
		if err != nil {
			t.Fatal(err)
		}
		if len(inv.VMs) == 0 {
			t.Fatal("expected the vcsim default model to have VMs")
		}
		if len(inv.Datastores) == 0 {
			t.Fatal("expected the vcsim default model to have datastores")
		}
		// Stage B: host facts (name + product version) must be collected.
		if len(inv.Hosts) == 0 {
			t.Fatal("expected the vcsim default model to have host systems")
		}
		for _, h := range inv.Hosts {
			if h.Name == "" {
				t.Errorf("host with empty name: %+v", h)
			}
			if h.Version == "" && h.FullName == "" {
				t.Errorf("host %s has no product version/full name", h.Name)
			}
		}
		for _, vm := range inv.VMs {
			if vm.Name == "" {
				t.Errorf("VM with empty name: %+v", vm)
			}
			switch vm.PowerState {
			case "on", "off", "suspended", "unknown":
			default:
				t.Errorf("VM %s has unmapped power state %q", vm.Name, vm.PowerState)
			}
		}
		for _, ds := range inv.Datastores {
			if ds.Name == "" || ds.CapacityBytes <= 0 {
				t.Errorf("datastore not populated: %+v", ds)
			}
		}
	})
}

func TestMapPower(t *testing.T) {
	if mapPower("poweredOn") != "on" || mapPower("poweredOff") != "off" ||
		mapPower("suspended") != "suspended" || mapPower("bogus") != "unknown" {
		t.Fatal("power-state mapping wrong")
	}
}
