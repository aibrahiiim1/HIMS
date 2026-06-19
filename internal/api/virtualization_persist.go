package api

import (
	"context"
	"net/netip"
	"strconv"
	"strings"
	"time"

	"github.com/coralsearesorts/hims/internal/osinv"
	"github.com/coralsearesorts/hims/internal/storage/postgres/db"
	"github.com/coralsearesorts/hims/internal/vsphere"
	"github.com/google/uuid"
)

// Stage 2: persist rich virtualization detail (datastores, host networks/NICs, per-VM
// disks/NICs) into durable tables for both ESXi (govmomi) and Hyper-V (Msvm). Existing
// VM↔host and VM↔device links are preserved; no duplicate devices are created.

func vstr(s string) *string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	return &s
} //nolint:gofmt
func v64(n int64) *int64 {
	if n == 0 {
		return nil
	}
	return &n
} //nolint:gofmt
func v32(n int32) *int32 {
	if n == 0 {
		return nil
	}
	return &n
} //nolint:gofmt

// putFact writes a non-empty string device fact (best-effort), used for host hardware summary.
func (s *Server) putFact(ctx context.Context, id uuid.UUID, key, val string) {
	if strings.TrimSpace(val) == "" {
		return
	}
	v := val
	_ = s.queries.UpsertDeviceFact(ctx, db.UpsertDeviceFactParams{DeviceID: id, Key: key, Value: &v, Driver: "vsphere"})
}

// persistVSphereInventory writes the full ESXi/vCenter inventory to the durable tables.
func (s *Server) persistVSphereInventory(ctx context.Context, hostID uuid.UUID, inv vsphere.Inventory) {
	start := time.Now().UTC()
	for _, ds := range inv.Datastores {
		_ = s.queries.UpsertDatastore(ctx, db.UpsertDatastoreParams{
			HostDeviceID: hostID, Name: ds.Name, Type: vstr(ds.Type),
			CapacityBytes: v64(ds.CapacityBytes), FreeBytes: v64(ds.FreeBytes),
		})
	}
	if len(inv.Hosts) > 0 {
		h := inv.Hosts[0]
		for _, p := range h.Pnics {
			_ = s.queries.UpsertHostNic(ctx, db.UpsertHostNicParams{
				HostDeviceID: hostID, Name: p.Name, Mac: vstr(p.MAC), LinkSpeedMbps: v32(p.SpeedMb),
			})
		}
		for _, sw := range h.VSwitches {
			_ = s.queries.UpsertVHNetwork(ctx, db.UpsertVHNetworkParams{
				HostDeviceID: hostID, Kind: "vswitch", Name: sw.Name, Uplinks: vstr(strings.Join(sw.Uplinks, ",")),
			})
		}
		for _, pg := range h.Portgroups {
			_ = s.queries.UpsertVHNetwork(ctx, db.UpsertVHNetworkParams{
				HostDeviceID: hostID, Kind: "portgroup", Name: pg.Name, Vlan: v32(pg.VLAN), SwitchName: vstr(pg.VSwitch),
			})
		}
		// Host hardware summary as facts (no dedicated columns) so the overview shows
		// CPU/memory/uptime for an ESXi host.
		s.putFact(ctx, hostID, "hardware.cpu_model", h.CPUModel)
		s.putFact(ctx, hostID, "hardware.cpu_cores", strconv.FormatInt(int64(h.CPUCores), 10))
		s.putFact(ctx, hostID, "memory.total_bytes", strconv.FormatInt(h.MemoryBytes, 10))
		if h.MemoryUsedBytes > 0 {
			s.putFact(ctx, hostID, "memory.used_bytes", strconv.FormatInt(h.MemoryUsedBytes, 10))
		}
		if h.UptimeSeconds > 0 {
			s.putFact(ctx, hostID, "hardware.uptime_seconds", strconv.FormatInt(h.UptimeSeconds, 10))
		}
	}
	for _, vm := range inv.VMs {
		var macs []string
		for _, n := range vm.Nics {
			if n.MAC != "" {
				macs = append(macs, n.MAC)
			}
		}
		macJoined := strings.Join(macs, ",")
		ipCandidate := vm.IP
		if ipCandidate == "" && len(vm.IPs) > 0 {
			ipCandidate = vm.IPs[0]
		}
		var vmIP *netip.Addr
		if a, err := netip.ParseAddr(ipCandidate); err == nil {
			vmIP = &a
		}
		row, err := s.queries.UpsertVM(ctx, db.UpsertVMParams{
			HostDeviceID: hostID, Name: vm.Name, PowerState: vm.PowerState,
			Vcpu: v32(vm.NumCPU), MemMb: v32(vm.MemoryMB), GuestOs: vstr(vm.GuestOS), PrimaryIp: vmIP,
			VmID: vstr(vm.UUID), Mac: vstr(macJoined),
			VmDeviceID: s.resolveVMDevice(ctx, vmIP, macJoined),
		})
		if err != nil {
			continue
		}
		_ = s.queries.SetVMExtra(ctx, db.SetVMExtraParams{ID: row.ID, ToolsState: vm.ToolsState, Datastore: vm.Datastore})
		for i, dk := range vm.Disks {
			label := dk.Label
			if label == "" {
				label = "disk" + strconv.Itoa(i)
			}
			_ = s.queries.UpsertVMDisk(ctx, db.UpsertVMDiskParams{
				VmID: row.ID, Label: label, Path: vstr(dk.Path), Datastore: vstr(dk.Datastore), CapacityBytes: v64(dk.CapacityBytes),
			})
		}
		for _, nc := range vm.Nics {
			if nc.MAC == "" {
				continue
			}
			conn := nc.Connected
			_ = s.queries.UpsertVMNic(ctx, db.UpsertVMNicParams{
				VmID: row.ID, Mac: nc.MAC, Network: vstr(nc.Network), IpAddresses: vstr(strings.Join(vm.IPs, ",")), Connected: &conn,
			})
		}
		_ = s.queries.DeleteStaleVMDisks(ctx, db.DeleteStaleVMDisksParams{VmID: row.ID, LastSeenAt: start})
		_ = s.queries.DeleteStaleVMNics(ctx, db.DeleteStaleVMNicsParams{VmID: row.ID, LastSeenAt: start})
	}
	_ = s.queries.DeleteStaleDatastores(ctx, db.DeleteStaleDatastoresParams{HostDeviceID: hostID, LastSeenAt: start})
	_ = s.queries.DeleteStaleNetworks(ctx, db.DeleteStaleNetworksParams{HostDeviceID: hostID, LastSeenAt: start})
	_ = s.queries.DeleteStaleHostNics(ctx, db.DeleteStaleHostNicsParams{HostDeviceID: hostID, LastSeenAt: start})
	vc := int32(len(inv.VMs))
	_ = s.queries.UpsertCollectionHealth(ctx, db.UpsertCollectionHealthParams{
		DeviceID: hostID, Collector: "vsphere", Status: "ok", VmCount: &vc,
	})
}

// persistHyperVDetail writes per-VM disks/NICs + host vSwitches for a Hyper-V host from the
// in-band Msvm enumeration. The VM rows themselves are upserted by markHyperVHost; this adds
// the child detail keyed on each VM's id (looked up by host+name).
func (s *Server) persistHyperVDetail(ctx context.Context, hostID uuid.UUID, rep osinv.Report) {
	start := time.Now().UTC()
	for _, sw := range rep.VSwitches {
		_ = s.queries.UpsertVHNetwork(ctx, db.UpsertVHNetworkParams{
			HostDeviceID: hostID, Kind: "vswitch", Name: sw.Name, SwitchName: vstr(sw.SwitchType),
		})
	}
	vmsByName := map[string]uuid.UUID{}
	if rows, err := s.queries.ListVMsByHost(ctx, hostID); err == nil {
		for _, r := range rows {
			vmsByName[r.Name] = r.ID
		}
	}
	for _, vm := range rep.VMs {
		id, ok := vmsByName[vm.Name]
		if !ok {
			continue
		}
		if vm.Generation != "" || vm.IntegrationServices != "" {
			_ = s.queries.SetVMExtra(ctx, db.SetVMExtraParams{ID: id, Generation: vm.Generation, IntegrationServices: vm.IntegrationServices})
		}
		for i, dk := range vm.Disks {
			label := dk.Path
			if label == "" {
				label = "disk" + strconv.Itoa(i)
			}
			_ = s.queries.UpsertVMDisk(ctx, db.UpsertVMDiskParams{
				VmID: id, Label: label, Path: vstr(dk.Path), CapacityBytes: v64(dk.CapacityBytes), UsedBytes: v64(dk.UsedBytes),
			})
		}
		for _, mc := range strings.Split(vm.MAC, ",") {
			if mc = strings.TrimSpace(mc); mc == "" {
				continue
			}
			_ = s.queries.UpsertVMNic(ctx, db.UpsertVMNicParams{
				VmID: id, Mac: mc, IpAddresses: vstr(vm.IP),
			})
		}
		_ = s.queries.DeleteStaleVMDisks(ctx, db.DeleteStaleVMDisksParams{VmID: id, LastSeenAt: start})
		_ = s.queries.DeleteStaleVMNics(ctx, db.DeleteStaleVMNicsParams{VmID: id, LastSeenAt: start})
	}
	_ = s.queries.DeleteStaleNetworks(ctx, db.DeleteStaleNetworksParams{HostDeviceID: hostID, LastSeenAt: start})
	vc := int32(len(rep.VMs))
	_ = s.queries.UpsertCollectionHealth(ctx, db.UpsertCollectionHealthParams{
		DeviceID: hostID, Collector: "hyperv", Status: "ok", VmCount: &vc,
	})
}
