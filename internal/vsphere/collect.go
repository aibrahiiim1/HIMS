// Package vsphere collects VM inventory + datastores from a VMware endpoint
// (a single ESXi host's API or a vCenter) over govmomi. It is the deep-
// collection counterpart to the SNMP esxi driver: SNMP gives host resources,
// this gives the host→VM map (with per-VM disks/NICs) the operator wants.
//
// Collect takes a *vim25.Client so it is testable against govmomi's in-memory
// simulator (vcsim) with no real vCenter — see collect_test.go. The connect-
// from-URL path lives in the collector's -vsphere mode.
package vsphere

import (
	"context"
	"strings"

	"github.com/vmware/govmomi/view"
	"github.com/vmware/govmomi/vim25"
	"github.com/vmware/govmomi/vim25/mo"
	"github.com/vmware/govmomi/vim25/types"
)

// VMDisk is one virtual disk attached to a VM.
type VMDisk struct {
	Label         string
	Path          string // vmdk path ("[datastore] vm/vm.vmdk")
	Datastore     string
	CapacityBytes int64
}

// VMNic is one virtual NIC attached to a VM.
type VMNic struct {
	MAC       string
	Network   string // port group / network name
	Connected bool
}

// VM is one virtual machine's normalized inventory.
type VM struct {
	Name       string
	UUID       string
	PowerState string // on | off | suspended | unknown
	NumCPU     int32
	MemoryMB   int32
	GuestOS    string
	IP         string   // primary guest IP
	IPs        []string // all guest IPs
	ToolsState string   // toolsOk | toolsOld | toolsNotRunning | toolsNotInstalled | ""
	Datastore  string   // primary datastore (from first disk)
	Disks      []VMDisk
	Nics       []VMNic
}

// Datastore is one datastore's capacity summary.
type Datastore struct {
	Name          string
	Type          string
	CapacityBytes int64
	FreeBytes     int64
}

// Pnic is one physical uplink NIC (vmnic) on the host.
type Pnic struct {
	Name    string
	MAC     string
	SpeedMb int32
}

// VSwitch is one standard virtual switch on the host.
type VSwitch struct {
	Name    string
	Uplinks []string
}

// Portgroup is one port group on the host.
type Portgroup struct {
	Name    string
	VSwitch string
	VLAN    int32
}

// Host is one ESXi host's hardware/OS summary (the host the operator manages).
type Host struct {
	Name            string
	Version         string // ESXi version, e.g. "7.0.3"
	Build           string
	FullName        string // e.g. "VMware ESXi 7.0.3 build-19193900"
	Vendor          string // hardware vendor, e.g. "Dell Inc."
	Model           string // hardware model, e.g. "PowerEdge R740"
	Serial          string // chassis serial / ServiceTag (matches the managing iLO/iDRAC)
	CPUModel        string
	CPUPackages     int32
	CPUCores        int32
	MemoryBytes     int64
	MemoryUsedBytes int64
	UptimeSeconds   int64
	Pnics           []Pnic
	VSwitches       []VSwitch
	Portgroups      []Portgroup
}

// hostSerial extracts the physical chassis serial from an ESXi host's identification
// info, preferring the vendor service tag (Dell "ServiceTag", HPE "SerialNumberTag" /
// "EnclosureSerialNumberTag") over an asset tag. This is the SAME serial the box's iLO/
// iDRAC reports, so it lets HIMS link a BMC to the physical server by real evidence.
func hostSerial(info []types.HostSystemIdentificationInfo) string {
	byKey := map[string]string{}
	for _, i := range info {
		if i.IdentifierType == nil {
			continue
		}
		k := i.IdentifierType.GetElementDescription().Key
		v := strings.TrimSpace(i.IdentifierValue)
		if v != "" && v != "Unknown" {
			byKey[k] = v
		}
	}
	for _, k := range []string{"ServiceTag", "SerialNumberTag", "EnclosureSerialNumberTag", "AssetTag"} {
		if v := byKey[k]; v != "" {
			return v
		}
	}
	return ""
}

// Inventory is what one Collect run gathered.
type Inventory struct {
	Hosts      []Host
	VMs        []VM
	Datastores []Datastore
}

// Collect retrieves host facts (incl. network + quickStats), VMs (incl. disks +
// NICs + tools state) + datastores via ContainerViews over the root folder.
func Collect(ctx context.Context, c *vim25.Client) (Inventory, error) {
	var inv Inventory
	m := view.NewManager(c)

	// Host systems: hardware/OS facts + quickStats (mem used/uptime) + network
	// (pNICs/vSwitches/portgroups).
	hostView, herr := m.CreateContainerView(ctx, c.ServiceContent.RootFolder, []string{"HostSystem"}, true)
	if herr == nil {
		defer func() { _ = hostView.Destroy(ctx) }()
		var hosts []mo.HostSystem
		if err := hostView.Retrieve(ctx, []string{"HostSystem"}, []string{"summary", "config.network", "hardware.systemInfo"}, &hosts); err == nil {
			for _, h := range hosts {
				host := Host{Name: h.Summary.Config.Name}
				if p := h.Summary.Config.Product; p != nil {
					host.Version, host.Build, host.FullName = p.Version, p.Build, p.FullName
				}
				if hw := h.Summary.Hardware; hw != nil {
					host.Vendor, host.Model = hw.Vendor, hw.Model
					host.CPUModel = hw.CpuModel
					host.CPUPackages = int32(hw.NumCpuPkgs)
					host.CPUCores = int32(hw.NumCpuCores)
					host.MemoryBytes = hw.MemorySize
					host.Serial = hostSerial(hw.OtherIdentifyingInfo) // summary path (often empty)
				}
				// The chassis serial lives under hardware.systemInfo — SerialNumber (6.7+) or
				// the vendor tag in OtherIdentifyingInfo. This is what matches the box's iLO/iDRAC.
				if host.Serial == "" && h.Hardware != nil {
					si := h.Hardware.SystemInfo
					if s := strings.TrimSpace(si.SerialNumber); s != "" {
						host.Serial = s
					} else {
						host.Serial = hostSerial(si.OtherIdentifyingInfo)
					}
					if host.Vendor == "" {
						host.Vendor = si.Vendor
					}
					if host.Model == "" {
						host.Model = si.Model
					}
				}
				if qs := h.Summary.QuickStats; qs.OverallMemoryUsage != 0 || qs.Uptime != 0 {
					host.MemoryUsedBytes = int64(qs.OverallMemoryUsage) * 1024 * 1024
					host.UptimeSeconds = int64(qs.Uptime)
				}
				if net := h.Config; net != nil && net.Network != nil {
					for _, pn := range net.Network.Pnic {
						p := Pnic{Name: pn.Device, MAC: pn.Mac}
						if pn.LinkSpeed != nil {
							p.SpeedMb = pn.LinkSpeed.SpeedMb
						}
						host.Pnics = append(host.Pnics, p)
					}
					for _, vs := range net.Network.Vswitch {
						sw := VSwitch{Name: vs.Name}
						for _, pk := range vs.Pnic {
							// pnic key looks like "key-vim.host.PhysicalNic-vmnic0"
							if i := strings.LastIndex(pk, "-"); i >= 0 {
								sw.Uplinks = append(sw.Uplinks, pk[i+1:])
							}
						}
						host.VSwitches = append(host.VSwitches, sw)
					}
					for _, pg := range net.Network.Portgroup {
						host.Portgroups = append(host.Portgroups, Portgroup{
							Name: pg.Spec.Name, VSwitch: pg.Spec.VswitchName, VLAN: pg.Spec.VlanId,
						})
					}
				}
				inv.Hosts = append(inv.Hosts, host)
			}
		}
	}

	vmView, err := m.CreateContainerView(ctx, c.ServiceContent.RootFolder, []string{"VirtualMachine"}, true)
	if err != nil {
		return inv, err
	}
	defer func() { _ = vmView.Destroy(ctx) }()
	var vms []mo.VirtualMachine
	if err := vmView.Retrieve(ctx, []string{"VirtualMachine"},
		[]string{"summary", "guest", "config.hardware.device", "config.uuid"}, &vms); err != nil {
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
			ToolsState: string(vm.Summary.Guest.ToolsStatus),
		}
		if vm.Config != nil {
			v.UUID = vm.Config.Uuid
		}
		if vm.Guest != nil {
			if vm.Guest.IpAddress != "" {
				v.IP = vm.Guest.IpAddress
			}
			for _, n := range vm.Guest.Net {
				for _, ip := range n.IpAddress {
					if strings.Contains(ip, ".") {
						v.IPs = append(v.IPs, ip)
					}
				}
			}
		}
		// Walk the virtual hardware for disks + NICs.
		if vm.Config != nil {
			for _, dev := range vm.Config.Hardware.Device {
				switch d := dev.(type) {
				case *types.VirtualDisk:
					disk := VMDisk{CapacityBytes: d.CapacityInKB * 1024}
					if d.DeviceInfo != nil {
						disk.Label = d.DeviceInfo.GetDescription().Label
					}
					if b, ok := d.Backing.(*types.VirtualDiskFlatVer2BackingInfo); ok {
						disk.Path = b.FileName
						disk.Datastore = datastoreFromPath(b.FileName)
					}
					if disk.Label == "" {
						disk.Label = disk.Path
					}
					if v.Datastore == "" {
						v.Datastore = disk.Datastore
					}
					v.Disks = append(v.Disks, disk)
				default:
					if nic, ok := asEthernet(dev); ok {
						v.Nics = append(v.Nics, nic)
					}
				}
			}
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
			Type:          ds.Summary.Type,
			CapacityBytes: ds.Summary.Capacity,
			FreeBytes:     ds.Summary.FreeSpace,
		})
	}
	return inv, nil
}

// asEthernet extracts MAC/network/connected from any VirtualEthernetCard subtype.
func asEthernet(dev types.BaseVirtualDevice) (VMNic, bool) {
	card, ok := dev.(types.BaseVirtualEthernetCard)
	if !ok {
		return VMNic{}, false
	}
	e := card.GetVirtualEthernetCard()
	nic := VMNic{MAC: e.MacAddress}
	if e.Connectable != nil {
		nic.Connected = e.Connectable.Connected
	}
	switch b := e.Backing.(type) {
	case *types.VirtualEthernetCardNetworkBackingInfo:
		nic.Network = b.DeviceName
	case *types.VirtualEthernetCardDistributedVirtualPortBackingInfo:
		nic.Network = b.Port.PortgroupKey
	}
	return nic, true
}

// datastoreFromPath parses the datastore name from a vmdk path "[datastore] dir/x.vmdk".
func datastoreFromPath(p string) string {
	if i := strings.Index(p, "["); i == 0 {
		if j := strings.Index(p, "]"); j > 1 {
			return p[1:j]
		}
	}
	return ""
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
