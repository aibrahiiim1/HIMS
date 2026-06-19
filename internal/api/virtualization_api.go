package api

import (
	"context"
	"net/http"
	"net/netip"
	"strconv"

	"github.com/coralsearesorts/hims/internal/domain"
	"github.com/coralsearesorts/hims/internal/storage/postgres/db"
	"github.com/google/uuid"
)

func addrStr(a *netip.Addr) string {
	if a == nil || !a.IsValid() {
		return ""
	}
	return a.String()
}
func deref64(p *int64) int64 {
	if p == nil {
		return 0
	}
	return *p
}
func deref32(p *int32) int32 {
	if p == nil {
		return 0
	}
	return *p
}

// Stage 3 read APIs: surface the persisted virtualization detail so the UI never queries the
// DB directly. One list endpoint for the Virtual Hosts page + one consolidated detail endpoint
// (overview / VMs / storage / networks / host NICs / health) for the host detail tabs.

type vhostSummary struct {
	ID                string `json:"id"`
	Name              string `json:"name"`
	IP                string `json:"ip"`
	HypervisorType    string `json:"hypervisor_type"` // esxi | hyperv
	Management        string `json:"management"`
	VMCount           int    `json:"vm_count"`
	Running           int    `json:"running"`
	Stopped           int    `json:"stopped"`
	CPUModel          string `json:"cpu_model,omitempty"`
	CPUCores          int    `json:"cpu_cores,omitempty"`
	MemTotalBytes     int64  `json:"mem_total_bytes,omitempty"`
	MemUsedBytes      int64  `json:"mem_used_bytes,omitempty"`
	Version           string `json:"version,omitempty"`
	Vendor            string `json:"vendor,omitempty"`
	Model             string `json:"model,omitempty"`
	DatastoreCount    int    `json:"datastore_count"`
	DatastoreCapacity int64  `json:"datastore_capacity,omitempty"`
	DatastoreFree     int64  `json:"datastore_free,omitempty"`
	NetworkCount      int    `json:"network_count"`
	Health            string `json:"health"` // ok | partial | failed | none
	LastCollected     string `json:"last_collected,omitempty"`
}

func hypervisorTypeOf(d db.Device, hv map[uuid.UUID]string) string {
	if t := hv[d.ID]; t != "" {
		return t
	}
	if d.DeviceClass != nil && *d.DeviceClass == "hyperv" {
		return "hyperv"
	}
	return "esxi"
}

// virtualizationHosts (GET /virtualization/hosts) — the Virtual Hosts page list.
func (s *Server) virtualizationHosts(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	rows, err := s.queries.ListAllDevices(ctx)
	if err != nil {
		writeErr(w, err)
		return
	}
	rows = s.scopeDevices(ctx, rows)
	maps, err := s.buildStatusMaps(ctx)
	if err != nil {
		writeErr(w, err)
		return
	}
	counts := map[uuid.UUID][3]int{}
	if cs, e := s.queries.VMCountsByHost(ctx); e == nil {
		for _, c := range cs {
			counts[c.HostDeviceID] = [3]int{int(c.Total), int(c.Running), int(c.Stopped)}
		}
	}
	dsSum := map[uuid.UUID][3]int64{}
	if ds, e := s.queries.DatastoreSummaryByHost(ctx); e == nil {
		for _, d := range ds {
			dsSum[d.HostDeviceID] = [3]int64{int64(d.N), d.Capacity, d.Free}
		}
	}
	netCount := map[uuid.UUID]int{}
	if ns, e := s.queries.NetworkCountByHost(ctx); e == nil {
		for _, n := range ns {
			netCount[n.HostDeviceID] = int(n.N)
		}
	}
	out := []vhostSummary{}
	for _, d := range rows {
		if d.Category != string(domain.CatVirtualHost) {
			continue
		}
		st := maps.statusFor(d)
		vc := counts[d.ID]
		ds := dsSum[d.ID]
		sum := vhostSummary{
			ID: d.ID.String(), Name: d.Name, IP: addrStr(d.PrimaryIp),
			HypervisorType: hypervisorTypeOf(d, maps.hvType), Management: st.Management,
			VMCount: vc[0], Running: vc[1], Stopped: vc[2],
			DatastoreCount: int(ds[0]), DatastoreCapacity: ds[1], DatastoreFree: ds[2],
			NetworkCount: netCount[d.ID], Health: "none",
		}
		if d.OsVersion != nil {
			sum.Version = *d.OsVersion
		}
		if d.Vendor != nil {
			sum.Vendor = *d.Vendor
		}
		if d.Model != nil {
			sum.Model = *d.Model
		}
		for _, f := range factsOf(ctx, s, d.ID) {
			applyHostFact(&sum, f.Key, f.Value)
		}
		if hr, e := s.queries.GetCollectionHealth(ctx, d.ID); e == nil && len(hr) > 0 {
			sum.Health = hr[0].Status
			sum.LastCollected = hr[0].CollectedAt.Format("2006-01-02T15:04:05Z07:00")
		}
		out = append(out, sum)
	}
	writeJSON(w, http.StatusOK, out)
}

func factsOf(ctx context.Context, s *Server, id uuid.UUID) []db.DeviceFact {
	rows, _ := s.queries.ListDeviceFacts(ctx, id)
	return rows
}

func applyHostFact(sum *vhostSummary, key string, val *string) {
	if val == nil {
		return
	}
	switch key {
	case "hardware.cpu_model":
		sum.CPUModel = *val
	case "hardware.cpu_cores":
		sum.CPUCores = vAtoi(*val)
	case "memory.total_bytes":
		sum.MemTotalBytes = vAtoi64(*val)
	case "memory.used_bytes":
		sum.MemUsedBytes = vAtoi64(*val)
	}
}

func vAtoi(s string) int     { n, _ := strconv.Atoi(s); return n }
func vAtoi64(s string) int64 { n, _ := strconv.ParseInt(s, 10, 64); return n }

// --- consolidated host detail ------------------------------------------------

type vmDetail struct {
	ID             string   `json:"id"`
	Name           string   `json:"name"`
	PowerState     string   `json:"power_state"`
	GuestOS        string   `json:"guest_os,omitempty"`
	VCPU           int      `json:"vcpu,omitempty"`
	MemMB          int      `json:"mem_mb,omitempty"`
	IP             string   `json:"ip,omitempty"`
	MAC            string   `json:"mac,omitempty"`
	VMID           string   `json:"vm_id,omitempty"`
	Generation     string   `json:"generation,omitempty"`
	ToolsState     string   `json:"tools_state,omitempty"`
	Datastore      string   `json:"datastore,omitempty"`
	DiskCount      int      `json:"disk_count"`
	DiskTotalBytes int64    `json:"disk_total_bytes"`
	NICCount       int      `json:"nic_count"`
	Disks          []vmDisk `json:"disks"`
	NICs           []vmNic  `json:"nics"`
	LinkedDeviceID string   `json:"linked_device_id,omitempty"`
	LinkedName     string   `json:"linked_name,omitempty"`
	LinkedIP       string   `json:"linked_ip,omitempty"`
}
type vmDisk struct {
	Label         string `json:"label"`
	Path          string `json:"path,omitempty"`
	Datastore     string `json:"datastore,omitempty"`
	CapacityBytes int64  `json:"capacity_bytes,omitempty"`
	UsedBytes     int64  `json:"used_bytes,omitempty"`
}
type vmNic struct {
	MAC       string `json:"mac"`
	Network   string `json:"network,omitempty"`
	IPs       string `json:"ip_addresses,omitempty"`
	Connected *bool  `json:"connected,omitempty"`
}

// virtualizationDetail (GET /devices/{id}/virtualization) — powers all host-detail tabs.
func (s *Server) virtualizationDetail(w http.ResponseWriter, r *http.Request) {
	ctx, id, ok := pathDevice(w, r)
	if !ok {
		return
	}
	d, err := s.queries.GetDevice(ctx, id)
	if err != nil {
		writeErr(w, err)
		return
	}
	maps, _ := s.buildStatusMaps(ctx)
	hvType := "esxi"
	if maps != nil {
		hvType = hypervisorTypeOf(d, maps.hvType)
	}

	// VMs + grouped disks/NICs.
	disksByVM := map[uuid.UUID][]vmDisk{}
	if rows, e := s.queries.ListVMDisksByHost(ctx, id); e == nil {
		for _, dk := range rows {
			disksByVM[dk.VmID] = append(disksByVM[dk.VmID], vmDisk{
				Label: dk.Label, Path: derefStr(dk.Path), Datastore: derefStr(dk.Datastore),
				CapacityBytes: deref64(dk.CapacityBytes), UsedBytes: deref64(dk.UsedBytes),
			})
		}
	}
	nicsByVM := map[uuid.UUID][]vmNic{}
	if rows, e := s.queries.ListVMNicsByHost(ctx, id); e == nil {
		for _, nc := range rows {
			nicsByVM[nc.VmID] = append(nicsByVM[nc.VmID], vmNic{
				MAC: nc.Mac, Network: derefStr(nc.Network), IPs: derefStr(nc.IpAddresses), Connected: nc.Connected,
			})
		}
	}
	vms := []vmDetail{}
	if rows, e := s.queries.ListVMsByHostDetail(ctx, id); e == nil {
		for _, v := range rows {
			vd := vmDetail{
				ID: v.ID.String(), Name: v.Name, PowerState: v.PowerState,
				GuestOS: derefStr(v.GuestOs), VCPU: int(deref32(v.Vcpu)), MemMB: int(deref32(v.MemMb)),
				IP: addrStr(v.PrimaryIp), MAC: derefStr(v.Mac), VMID: derefStr(v.VmID),
				Generation: derefStr(v.Generation), ToolsState: derefStr(v.ToolsState), Datastore: derefStr(v.Datastore),
				Disks: disksByVM[v.ID], NICs: nicsByVM[v.ID],
			}
			if vd.Disks == nil {
				vd.Disks = []vmDisk{}
			}
			if vd.NICs == nil {
				vd.NICs = []vmNic{}
			}
			vd.DiskCount = len(vd.Disks)
			for _, dk := range vd.Disks {
				vd.DiskTotalBytes += dk.CapacityBytes
			}
			vd.NICCount = len(vd.NICs)
			if v.VmDeviceID != nil {
				vd.LinkedDeviceID = v.VmDeviceID.String()
				vd.LinkedName = derefStr(v.LinkedName)
				vd.LinkedIP = v.LinkedIp
			}
			vms = append(vms, vd)
		}
	}

	datastores, _ := s.queries.ListDatastoresByHost(ctx, id)
	networks, _ := s.queries.ListNetworksByHost(ctx, id)
	hostNics, _ := s.queries.ListHostNicsByHost(ctx, id)
	// Hyper-V host NICs come from the host's own OS inventory (os_nics) — surface them too.
	osNics, _ := s.queries.ListOSNics(ctx, id)
	health, _ := s.queries.GetCollectionHealth(ctx, id)

	running, stopped := 0, 0
	for _, v := range vms {
		switch v.PowerState {
		case "on":
			running++
		case "off":
			stopped++
		}
	}
	overview := map[string]any{
		"id": d.ID.String(), "name": d.Name, "ip": addrStr(d.PrimaryIp),
		"hypervisor_type": hvType, "version": derefStr(d.OsVersion),
		"vendor": derefStr(d.Vendor), "model": derefStr(d.Model),
		"vm_count": len(vms), "running": running, "stopped": stopped,
		"datastore_count": len(datastores), "network_count": len(networks),
	}
	if maps != nil {
		overview["management"] = maps.statusFor(d).Management
	}
	for _, f := range factsOf(ctx, s, id) {
		if f.Value == nil {
			continue
		}
		switch f.Key {
		case "hardware.cpu_model", "hardware.cpu_cores", "memory.total_bytes", "memory.used_bytes", "hardware.uptime_seconds":
			overview[f.Key] = *f.Value
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"overview":   overview,
		"vms":        vms,
		"datastores": datastores,
		"networks":   networks,
		"host_nics":  hostNics,
		"os_nics":    osNics,
		"health":     health,
	})
}
