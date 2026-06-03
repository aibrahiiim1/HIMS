// Package vsphere collects VM inventory + datastores from a VMware endpoint
// (a single ESXi host's API or a vCenter) over govmomi. It is the deep-
// collection counterpart to the SNMP esxi driver: SNMP gives host resources,
// this gives the host→VM map the operator actually wants.
//
// Collect takes a *vim25.Client so it is testable against govmomi's in-memory
// simulator (vcsim) with no real vCenter — see collect_test.go. The connect-
// from-URL path lives in the collector's -vsphere mode.
package vsphere

import (
	"context"

	"github.com/vmware/govmomi/view"
	"github.com/vmware/govmomi/vim25"
	"github.com/vmware/govmomi/vim25/mo"
	"github.com/vmware/govmomi/vim25/types"
)

// VM is one virtual machine's normalized inventory.
type VM struct {
	Name       string
	PowerState string // on | off | suspended | unknown
	NumCPU     int32
	MemoryMB   int32
	GuestOS    string
	IP         string
}

// Datastore is one datastore's capacity summary.
type Datastore struct {
	Name          string
	CapacityBytes int64
	FreeBytes     int64
}

// Inventory is what one Collect run gathered.
type Inventory struct {
	VMs        []VM
	Datastores []Datastore
}

// Collect retrieves VMs + datastores via a ContainerView over the root folder.
func Collect(ctx context.Context, c *vim25.Client) (Inventory, error) {
	var inv Inventory
	m := view.NewManager(c)

	vmView, err := m.CreateContainerView(ctx, c.ServiceContent.RootFolder, []string{"VirtualMachine"}, true)
	if err != nil {
		return inv, err
	}
	defer func() { _ = vmView.Destroy(ctx) }()
	var vms []mo.VirtualMachine
	if err := vmView.Retrieve(ctx, []string{"VirtualMachine"}, []string{"summary", "guest"}, &vms); err != nil {
		return inv, err
	}
	for _, vm := range vms {
		cfg := vm.Summary.Config
		v := VM{
			Name:       cfg.Name,
			PowerState: mapPower(vm.Summary.Runtime.PowerState),
			NumCPU:     cfg.NumCpu,
			MemoryMB:   cfg.MemorySizeMB,
			GuestOS:    cfg.GuestFullName,
		}
		if vm.Guest != nil && vm.Guest.IpAddress != "" {
			v.IP = vm.Guest.IpAddress
		}
		inv.VMs = append(inv.VMs, v)
	}

	dsView, err := m.CreateContainerView(ctx, c.ServiceContent.RootFolder, []string{"Datastore"}, true)
	if err != nil {
		return inv, err
	}
	defer func() { _ = dsView.Destroy(ctx) }()
	var dss []mo.Datastore
	if err := dsView.Retrieve(ctx, []string{"Datastore"}, []string{"summary"}, &dss); err != nil {
		return inv, err
	}
	for _, ds := range dss {
		inv.Datastores = append(inv.Datastores, Datastore{
			Name:          ds.Summary.Name,
			CapacityBytes: ds.Summary.Capacity,
			FreeBytes:     ds.Summary.FreeSpace,
		})
	}
	return inv, nil
}

// mapPower normalizes a vSphere power state to our schema's vocabulary.
func mapPower(p types.VirtualMachinePowerState) string {
	switch p {
	case types.VirtualMachinePowerStatePoweredOn:
		return "on"
	case types.VirtualMachinePowerStatePoweredOff:
		return "off"
	case types.VirtualMachinePowerStateSuspended:
		return "suspended"
	default:
		return "unknown"
	}
}
