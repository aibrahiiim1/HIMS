package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/xuri/excelize/v2"

	"github.com/coralsearesorts/hims/internal/storage/postgres/db"
)

// Virtual devices: operator-entered placeholders for gear HIMS can't integrate
// with (no reachable SNMP/SSH/API, or intentionally air-gapped) so the network
// picture stays complete. The device + its full config (ports/VLANs/neighbors/
// learned MACs) is stored in the SAME tables a real collection would fill, tagged
// collection_source='manual', so every existing detail page, the topology and the
// global search render it for free. The is_virtual flag keeps it counted yet
// distinguishable ("N devices, M virtual") and out of the probe loop.

const sourceManual = "manual"

var validDeviceStatus = map[string]bool{"up": true, "down": true, "warning": true, "unknown": true}

// --- Category-aware payload ---------------------------------------------------
// One superset request; createVirtualDevice/updateVirtualDevice persist only the
// blocks relevant to the device's category into the SAME tables a real collection
// fills (source=manual), so each category's existing detail page renders it.

type vdPort struct {
	IfIndex    int    `json:"if_index"`
	Name       string `json:"name"`
	Alias      string `json:"alias"`
	Up         bool   `json:"up"`         // operational status (true=up → IF-MIB oper 1)
	AdminDown  bool   `json:"admin_down"` // operator shut the port (admin 2)
	SpeedMbps  int    `json:"speed_mbps"`
	VLAN       int    `json:"vlan"`        // access/native VLAN (untagged / PVID)
	TrunkVLANs []int  `json:"trunk_vlans"` // tagged VLAN members (trunk)
	Role       string `json:"role"`        // access|trunk|uplink|unknown
	MAC        string `json:"mac"`
}

type vdVlan struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

type vdNeighbor struct {
	LocalPort    string `json:"local_port"`
	LocalIfIndex int    `json:"local_if_index"`
	RemoteName   string `json:"remote_name"`
	RemotePort   string `json:"remote_port"`
	RemoteMgmtIP string `json:"remote_mgmt_ip"`
	Protocol     string `json:"protocol"` // lldp|cdp|manual
}

type vdMAC struct {
	MAC     string `json:"mac"`
	VLAN    int    `json:"vlan"`
	IfIndex int    `json:"if_index"`
}

type vdNIC struct {
	Name      string `json:"name"`
	MAC       string `json:"mac"`
	IP        string `json:"ip"`
	Gateway   string `json:"gateway"`
	DNS       string `json:"dns"`
	Zone      string `json:"zone"` // firewall: WAN/LAN/DMZ zone label
	SpeedMbps int    `json:"speed_mbps"`
}

type vdDisk struct {
	Name       string `json:"name"`
	Model      string `json:"model"`
	Filesystem string `json:"filesystem"`
	TotalBytes int64  `json:"total_bytes"`
	UsedBytes  int64  `json:"used_bytes"`
	FreeBytes  int64  `json:"free_bytes"`
}

type vdSoftware struct {
	Name      string `json:"name"`
	Version   string `json:"version"`
	Publisher string `json:"publisher"`
}

type vdVpn struct {
	Name     string `json:"name"`
	P1Name   string `json:"p1_name"`
	RemoteGw string `json:"remote_gw"`
	Status   string `json:"status"`
}

type vdHA struct {
	Serial     string `json:"serial"`
	Hostname   string `json:"hostname"`
	SyncStatus string `json:"sync_status"`
}

type vdLicense struct {
	Contract string `json:"contract"`
	Expiry   string `json:"expiry"`
}

type vdFirewall struct {
	HaMode       string `json:"ha_mode"`
	HaGroupName  string `json:"ha_group_name"`
	SessionCount int64  `json:"session_count"`
}

type vdWlan struct {
	Vendor         string `json:"vendor"`
	Version        string `json:"version"`
	ControllerName string `json:"controller_name"`
	Model          string `json:"model"`
	Serial         string `json:"serial"`
}

type vdAP struct {
	Name   string `json:"name"`
	MAC    string `json:"mac"`
	Model  string `json:"model"`
	IP     string `json:"ip"`
	Status string `json:"status"`
	Serial string `json:"serial"`
	Band   string `json:"band"`
	Site   string `json:"site"`
}

type vdSSID struct {
	Name     string `json:"name"`
	Security string `json:"security"`
	Band     string `json:"band"`
	Vlan     string `json:"vlan"`
	Status   string `json:"status"`
}

type vdClient struct {
	MAC      string `json:"mac"`
	IP       string `json:"ip"`
	Hostname string `json:"hostname"`
	ApName   string `json:"ap_name"`
	Ssid     string `json:"ssid"`
	Band     string `json:"band"`
}

type vdUPS struct {
	Manufacturer  string `json:"manufacturer"`
	Model         string `json:"model"`
	BatteryStatus string `json:"battery_status"`
	ChargePct     int    `json:"charge_pct"`
	RuntimeMin    int    `json:"runtime_min"`
	LoadPct       int    `json:"load_pct"`
}

// virtualDeviceReq is the create/update payload — identity + status + per-category
// blocks. The handler reads only the blocks relevant to the category.
type virtualDeviceReq struct {
	manualDeviceReq
	Status      string `json:"status"` // operator-set; virtual devices aren't probed
	Site        string `json:"site"`
	Notes       string `json:"notes"`
	Criticality string `json:"criticality"`
	// Switch / generic L2
	Ports     []vdPort     `json:"ports"`
	VLANs     []vdVlan     `json:"vlans"`
	Neighbors []vdNeighbor `json:"neighbors"`
	MACs      []vdMAC      `json:"macs"`
	// Server / workstation
	NICs     []vdNIC      `json:"nics"`
	Disks    []vdDisk     `json:"disks"`
	Roles    []string     `json:"roles"`
	Software []vdSoftware `json:"software"`
	// Firewall
	Firewall   *vdFirewall `json:"firewall"`
	VpnTunnels []vdVpn     `json:"vpn_tunnels"`
	HAMembers  []vdHA      `json:"ha_members"`
	Licenses   []vdLicense `json:"licenses"`
	// Wireless controller
	Wlan    *vdWlan    `json:"wlan"`
	APs     []vdAP     `json:"aps"`
	SSIDs   []vdSSID   `json:"ssids"`
	Clients []vdClient `json:"clients"`
	// UPS
	UPS *vdUPS `json:"ups"`
	// Scalar specs / notes that have no dedicated column (CPU, RAM, capacity, …).
	Facts map[string]string `json:"facts"`
}

// createVirtualDevice handles POST /devices/virtual — create the device + persist
// its category-specific manual config in one call.
func (s *Server) createVirtualDevice(w http.ResponseWriter, r *http.Request) {
	var req virtualDeviceReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}
	params, err := manualDeviceParams(req.manualDeviceReq)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	status := normVirtualStatus(req.Status, "up")
	if status == "" {
		http.Error(w, "invalid status; use up, down, warning or unknown", http.StatusBadRequest)
		return
	}
	params.Status = status
	params.Metadata = []byte(`{"source":"virtual"}`)

	dev, err := s.queries.CreateDevice(r.Context(), params)
	if err != nil {
		writeErr(w, err)
		return
	}
	if err := s.queries.MarkDeviceVirtual(r.Context(), db.MarkDeviceVirtualParams{ID: dev.ID, IsVirtual: true}); err != nil {
		writeErr(w, err)
		return
	}
	s.setVirtualIdentityExtras(r.Context(), dev, &req) // notes/criticality
	s.writeVirtualConfig(r.Context(), dev, &req)
	s.audit(r, "inventory", "device.create_virtual", "device", dev.ID.String(),
		"Created virtual "+dev.Category+" "+dev.Name, map[string]any{"category": dev.Category})
	out, _ := s.queries.GetDevice(r.Context(), dev.ID)
	writeJSON(w, http.StatusCreated, out)
}

// updateVirtualDevice handles PUT /devices/virtual/{id} — replace identity + the
// category-specific config. Only virtual devices may be edited this way.
func (s *Server) updateVirtualDevice(w http.ResponseWriter, r *http.Request) {
	ctx, id, ok := pathDevice(w, r)
	if !ok {
		return
	}
	cur, err := s.queries.GetDevice(ctx, id)
	if err != nil {
		writeErr(w, err)
		return
	}
	if !cur.IsVirtual {
		http.Error(w, "not a virtual device", http.StatusBadRequest)
		return
	}
	var req virtualDeviceReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = cur.Name
	}
	cat := strings.TrimSpace(req.Category)
	if cat == "" {
		cat = cur.Category
	}
	if !validCategory(cat) {
		http.Error(w, "invalid category "+strconv.Quote(cat), http.StatusBadRequest)
		return
	}
	status := normVirtualStatus(req.Status, cur.Status)
	if status == "" {
		http.Error(w, "invalid status", http.StatusBadRequest)
		return
	}
	crit := cur.Criticality
	if strings.TrimSpace(req.Criticality) != "" && validCriticality[strings.TrimSpace(req.Criticality)] {
		crit = strings.TrimSpace(req.Criticality)
	}
	notes := cur.Notes
	if req.Notes != "" {
		notes = strings.TrimSpace(req.Notes)
	}
	dev, err := s.queries.UpdateDevice(ctx, db.UpdateDeviceParams{
		ID: id, Name: name, Category: cat,
		Vendor: strPtr(req.Vendor), Model: strPtr(req.Model), Serial: strPtr(req.Serial),
		OsVersion: strPtr(req.OSVersion), Hostname: strPtr(req.Hostname),
		Vlan: strPtr(req.VLAN), DeviceClass: strPtr(req.Class), Location: strPtr(req.Location),
		LocationID: parseUUIDPtr(req.LocationID), Subtype: cur.Subtype, Notes: notes,
		Criticality: crit, MonitoringEnabled: cur.MonitoringEnabled,
		ClassificationLocked: cur.ClassificationLocked, ManualClassificationReason: cur.ManualClassificationReason,
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	_ = s.queries.UpdateDeviceMonitoringStatus(ctx, db.UpdateDeviceMonitoringStatusParams{ID: id, Status: status})
	dev.IsVirtual = true
	s.writeVirtualConfig(ctx, dev, &req)
	s.audit(r, "inventory", "device.update_virtual", "device", id.String(), "Updated virtual "+cat+" "+name, nil)
	out, _ := s.queries.GetDevice(ctx, id)
	writeJSON(w, http.StatusOK, out)
}

// normVirtualStatus lowercases + validates an operator status; returns "" if
// invalid, or the fallback when the input is blank.
func normVirtualStatus(in, fallback string) string {
	s := strings.TrimSpace(strings.ToLower(in))
	if s == "" {
		s = strings.TrimSpace(strings.ToLower(fallback))
	}
	if !validDeviceStatus[s] {
		return ""
	}
	return s
}

// setVirtualIdentityExtras applies notes/criticality on create (CreateDevice has
// no columns for them) via a follow-up UpdateDevice.
func (s *Server) setVirtualIdentityExtras(ctx context.Context, dev db.Device, req *virtualDeviceReq) {
	notes := strings.TrimSpace(req.Notes)
	crit := strings.TrimSpace(req.Criticality)
	if notes == "" && crit == "" {
		return
	}
	if crit != "" && !validCriticality[crit] {
		crit = ""
	}
	_, _ = s.queries.UpdateDevice(ctx, db.UpdateDeviceParams{
		ID: dev.ID, Name: dev.Name, Category: dev.Category,
		Vendor: dev.Vendor, Model: dev.Model, Serial: dev.Serial, OsVersion: dev.OsVersion,
		Hostname: dev.Hostname, Vlan: dev.Vlan, DeviceClass: dev.DeviceClass, Location: dev.Location,
		LocationID: dev.LocationID, Subtype: dev.Subtype, Notes: notes, Criticality: crit,
		MonitoringEnabled: dev.MonitoringEnabled, ClassificationLocked: dev.ClassificationLocked,
		ManualClassificationReason: dev.ManualClassificationReason,
	})
}

// writeVirtualConfig dispatches by category to the per-category persist function.
// Each writes only its relevant tables (source=manual) and prunes stale manual
// rows so an edit fully replaces the prior manual config. Real-collected rows are
// never touched (DeleteStale* is scoped to source=manual).
func (s *Server) writeVirtualConfig(ctx context.Context, dev db.Device, req *virtualDeviceReq) {
	poll := time.Now().UTC()
	s.writeVirtualFacts(ctx, dev.ID, req) // common: scalar specs + site
	switch dev.Category {
	case "switch", "router", "isp_router":
		s.writeVirtualSwitch(ctx, dev.ID, req, poll)
	case "firewall":
		s.writeVirtualFirewall(ctx, dev.ID, req, poll)
	case "server", "virtual_host":
		s.writeVirtualServer(ctx, dev.ID, req, poll)
	case "endpoint":
		s.writeVirtualWorkstation(ctx, dev.ID, req, poll)
	case "wireless_controller":
		s.writeVirtualWireless(ctx, dev.ID, req, poll)
	case "ups":
		s.writeVirtualUPS(ctx, dev.ID, req)
		s.writeVirtualNeighbors(ctx, dev.ID, req, poll)
	default: // access_point, printer, camera, nvr, other, …
		s.writeVirtualGeneric(ctx, dev.ID, req, poll)
	}
}

// writeVirtualPorts upserts the port list as interfaces + their access/trunk VLAN
// membership, then prunes stale manual rows.
func (s *Server) writeVirtualPorts(ctx context.Context, devID uuid.UUID, ports []vdPort, poll time.Time) {
	for _, p := range ports {
		if p.IfIndex <= 0 {
			continue
		}
		oper := int16(1)
		if !p.Up {
			oper = 2
		}
		admin := int16(1)
		if p.AdminDown {
			admin = 2
		}
		role := strings.TrimSpace(p.Role)
		if role == "" {
			role = "unknown"
		}
		_, _ = s.queries.UpsertInterface(ctx, db.UpsertInterfaceParams{
			DeviceID: devID, IfIndex: int32(p.IfIndex), IfName: strPtr(p.Name), IfAlias: strPtr(p.Alias),
			Mac: strPtr(p.MAC), SpeedMbps: vdI32(p.SpeedMbps), AdminStatus: &admin, OperStatus: &oper,
			PortRole: role, CollectionSource: sourceManual, LastSeenAt: poll,
		})
		if p.VLAN > 0 { // access / native (untagged)
			_ = s.queries.UpsertPortVlan(ctx, db.UpsertPortVlanParams{
				DeviceID: devID, IfIndex: int32(p.IfIndex), VlanID: int32(p.VLAN),
				Tagged: false, CollectionSource: sourceManual, LastSeenAt: poll,
			})
		}
		for _, tv := range p.TrunkVLANs { // tagged (trunk) members
			if tv > 0 && tv != p.VLAN {
				_ = s.queries.UpsertPortVlan(ctx, db.UpsertPortVlanParams{
					DeviceID: devID, IfIndex: int32(p.IfIndex), VlanID: int32(tv),
					Tagged: true, CollectionSource: sourceManual, LastSeenAt: poll,
				})
			}
		}
	}
	_ = s.queries.DeleteStaleInterfaces(ctx, db.DeleteStaleInterfacesParams{DeviceID: devID, LastSeenAt: poll, CollectionSource: sourceManual})
	_ = s.queries.DeleteStalePortVlans(ctx, db.DeleteStalePortVlansParams{DeviceID: devID, LastSeenAt: poll, CollectionSource: sourceManual})
}

func (s *Server) writeVirtualVlans(ctx context.Context, devID uuid.UUID, vlans []vdVlan, poll time.Time) {
	for _, v := range vlans {
		if v.ID <= 0 {
			continue
		}
		_, _ = s.queries.UpsertVlan(ctx, db.UpsertVlanParams{
			DeviceID: devID, VlanID: int32(v.ID), Name: strPtr(v.Name), CollectionSource: sourceManual, LastSeenAt: poll,
		})
	}
	_ = s.queries.DeleteStaleVlans(ctx, db.DeleteStaleVlansParams{DeviceID: devID, LastSeenAt: poll, CollectionSource: sourceManual})
}

func (s *Server) writeVirtualNeighbors(ctx context.Context, devID uuid.UUID, req *virtualDeviceReq, poll time.Time) {
	for _, n := range req.Neighbors {
		if strings.TrimSpace(n.RemoteName) == "" && strings.TrimSpace(n.RemotePort) == "" {
			continue
		}
		proto := strings.TrimSpace(strings.ToLower(n.Protocol))
		if proto != "lldp" && proto != "cdp" {
			proto = sourceManual
		}
		_, _ = s.queries.UpsertNeighbor(ctx, db.UpsertNeighborParams{
			DeviceID: devID, LocalIfIndex: vdI32(n.LocalIfIndex), LocalIfName: strPtr(n.LocalPort),
			RemSysName: strPtr(n.RemoteName), RemPortID: strPtr(n.RemotePort), RemMgmtIp: parseIPPtr(n.RemoteMgmtIP),
			Protocol: proto, CollectionSource: sourceManual, LastSeenAt: poll,
		})
	}
	_ = s.queries.DeleteStaleNeighbors(ctx, db.DeleteStaleNeighborsParams{DeviceID: devID, LastSeenAt: poll, CollectionSource: sourceManual})
}

func (s *Server) writeVirtualMACs(ctx context.Context, devID uuid.UUID, macs []vdMAC, poll time.Time) {
	for _, m := range macs {
		mac := strings.TrimSpace(m.MAC)
		if mac == "" {
			continue
		}
		_ = s.queries.UpsertMAC(ctx, db.UpsertMACParams{
			DeviceID: devID, Mac: normMAC(mac), VlanID: int32(m.VLAN), IfIndex: vdI32(m.IfIndex),
			FdbStatus: 3, CollectionSource: sourceManual, LastSeenAt: poll,
		})
	}
	_ = s.queries.DeleteStaleMACEntries(ctx, db.DeleteStaleMACEntriesParams{DeviceID: devID, LastSeenAt: poll, CollectionSource: sourceManual})
}

// writeVirtualFacts stores scalar specs (CPU/RAM/capacity/…) + site as device
// facts (driver=manual) so they render in every detail page's facts panel.
func (s *Server) writeVirtualFacts(ctx context.Context, devID uuid.UUID, req *virtualDeviceReq) {
	put := func(k, v string) {
		v = strings.TrimSpace(v)
		if v == "" {
			return
		}
		val := v
		_ = s.queries.UpsertDeviceFact(ctx, db.UpsertDeviceFactParams{DeviceID: devID, Key: k, Value: &val, Driver: sourceManual, ValueJson: nil})
	}
	put("site", req.Site)
	for k, v := range req.Facts {
		k = strings.TrimSpace(k)
		if k != "" {
			put(k, v)
		}
	}
}

func (s *Server) writeVirtualSwitch(ctx context.Context, devID uuid.UUID, req *virtualDeviceReq, poll time.Time) {
	s.writeVirtualPorts(ctx, devID, req.Ports, poll)
	s.writeVirtualVlans(ctx, devID, req.VLANs, poll)
	s.writeVirtualNeighbors(ctx, devID, req, poll)
	s.writeVirtualMACs(ctx, devID, req.MACs, poll)
}

func (s *Server) writeVirtualFirewall(ctx context.Context, devID uuid.UUID, req *virtualDeviceReq, poll time.Time) {
	// WAN/LAN/DMZ interfaces → interfaces rows (alias=zone); IP/zone also as facts.
	for i, n := range req.NICs {
		name := strings.TrimSpace(n.Name)
		if name == "" {
			continue
		}
		oper := int16(1)
		_, _ = s.queries.UpsertInterface(ctx, db.UpsertInterfaceParams{
			DeviceID: devID, IfIndex: int32(i + 1), IfName: &name, IfAlias: strPtr(n.Zone),
			Mac: strPtr(n.MAC), SpeedMbps: vdI32(n.SpeedMbps), OperStatus: &oper,
			PortRole: "unknown", CollectionSource: sourceManual, LastSeenAt: poll,
		})
		if n.IP != "" {
			v := n.IP
			_ = s.queries.UpsertDeviceFact(ctx, db.UpsertDeviceFactParams{DeviceID: devID, Key: "interface." + name + ".ip", Value: &v, Driver: sourceManual})
		}
	}
	_ = s.queries.DeleteStaleInterfaces(ctx, db.DeleteStaleInterfacesParams{DeviceID: devID, LastSeenAt: poll, CollectionSource: sourceManual})

	if f := req.Firewall; f != nil {
		mode := strings.TrimSpace(f.HaMode)
		if mode == "" {
			mode = "standalone"
		}
		_ = s.queries.UpsertFirewallStatus(ctx, db.UpsertFirewallStatusParams{
			DeviceID: devID, HaMode: mode, HaGroupName: strPtr(f.HaGroupName),
			HaMemberCount: int32(len(req.HAMembers)), SessionCount: vdI64(f.SessionCount),
			CollectionSource: sourceManual, LastSeenAt: poll,
		})
	}
	for _, t := range req.VpnTunnels {
		if strings.TrimSpace(t.Name) == "" {
			continue
		}
		st := strings.TrimSpace(t.Status)
		if st == "" {
			st = "unknown"
		}
		_ = s.queries.UpsertVpnTunnel(ctx, db.UpsertVpnTunnelParams{
			DeviceID: devID, TunnelName: t.Name, P1Name: strPtr(t.P1Name), RemoteGw: parseIPPtr(t.RemoteGw),
			Status: st, CollectionSource: sourceManual, LastSeenAt: poll,
		})
	}
	_ = s.queries.DeleteStaleVpnTunnels(ctx, db.DeleteStaleVpnTunnelsParams{DeviceID: devID, LastSeenAt: poll, CollectionSource: sourceManual})

	for _, h := range req.HAMembers {
		if strings.TrimSpace(h.Serial) == "" {
			continue
		}
		sync := strings.TrimSpace(h.SyncStatus)
		if sync == "" {
			sync = "unknown"
		}
		_ = s.queries.UpsertHAMember(ctx, db.UpsertHAMemberParams{
			DeviceID: devID, Serial: h.Serial, Hostname: strPtr(h.Hostname), SyncStatus: sync,
			CollectionSource: sourceManual, LastSeenAt: poll,
		})
	}
	_ = s.queries.DeleteStaleHAMembers(ctx, db.DeleteStaleHAMembersParams{DeviceID: devID, LastSeenAt: poll, CollectionSource: sourceManual})

	for _, l := range req.Licenses {
		if strings.TrimSpace(l.Contract) == "" {
			continue
		}
		_ = s.queries.UpsertLicense(ctx, db.UpsertLicenseParams{
			DeviceID: devID, Contract: l.Contract, Expiry: strPtr(l.Expiry),
			CollectionSource: sourceManual, LastSeenAt: poll,
		})
	}
	_ = s.queries.DeleteStaleLicenses(ctx, db.DeleteStaleLicensesParams{DeviceID: devID, LastSeenAt: poll, CollectionSource: sourceManual})

	s.writeVirtualNeighbors(ctx, devID, req, poll)
}

func (s *Server) writeVirtualServer(ctx context.Context, devID uuid.UUID, req *virtualDeviceReq, poll time.Time) {
	// NICs → interfaces (IP as a fact, since interfaces has no IP column).
	for i, n := range req.NICs {
		name := strings.TrimSpace(n.Name)
		if name == "" {
			continue
		}
		oper := int16(1)
		_, _ = s.queries.UpsertInterface(ctx, db.UpsertInterfaceParams{
			DeviceID: devID, IfIndex: int32(i + 1), IfName: &name, Mac: strPtr(n.MAC),
			SpeedMbps: vdI32(n.SpeedMbps), OperStatus: &oper, PortRole: "unknown",
			CollectionSource: sourceManual, LastSeenAt: poll,
		})
		if n.IP != "" {
			v := n.IP
			_ = s.queries.UpsertDeviceFact(ctx, db.UpsertDeviceFactParams{DeviceID: devID, Key: "nic." + name + ".ip", Value: &v, Driver: sourceManual})
		}
	}
	_ = s.queries.DeleteStaleInterfaces(ctx, db.DeleteStaleInterfacesParams{DeviceID: devID, LastSeenAt: poll, CollectionSource: sourceManual})

	for i, d := range req.Disks {
		if strings.TrimSpace(d.Name) == "" && d.TotalBytes == 0 {
			continue
		}
		_ = s.queries.UpsertServerStorage(ctx, db.UpsertServerStorageParams{
			DeviceID: devID, HrIndex: int32(i + 1), Descr: strPtr(orName(d.Name, d.Model)),
			StorageType: "disk", TotalBytes: vdI64(d.TotalBytes), UsedBytes: vdI64(d.UsedBytes),
			CollectionSource: sourceManual, LastSeenAt: poll,
		})
	}
	_ = s.queries.DeleteStaleServerStorage(ctx, db.DeleteStaleServerStorageParams{DeviceID: devID, LastSeenAt: poll, CollectionSource: sourceManual})

	// Roles → device_roles (replace manual set).
	_ = s.queries.DeleteManualDeviceRoles(ctx, devID)
	for _, role := range req.Roles {
		role = strings.TrimSpace(role)
		if role == "" {
			continue
		}
		_ = s.queries.AddDeviceRole(ctx, db.AddDeviceRoleParams{DeviceID: devID, Role: role, Source: sourceManual})
	}
}

func (s *Server) writeVirtualWorkstation(ctx context.Context, devID uuid.UUID, req *virtualDeviceReq, poll time.Time) {
	host := strings.TrimSpace(req.Hostname)
	if host == "" {
		host = strings.TrimSpace(req.Name)
	}
	_, _ = s.queries.UpsertOSInventory(ctx, db.UpsertOSInventoryParams{
		DeviceID: devID, CollectionMethod: sourceManual, Hostname: strPtr(host),
		OsCaption: strPtr(req.OSVersion),
	})
	for _, n := range req.NICs {
		name := strings.TrimSpace(n.Name)
		if name == "" {
			name = "nic"
		}
		_ = s.queries.UpsertOSNic(ctx, db.UpsertOSNicParams{
			DeviceID: devID, Name: name, Mac: strPtr(n.MAC), IpAddresses: strPtr(n.IP),
			Gateway: strPtr(n.Gateway), DnsServers: strPtr(n.DNS),
			CollectionSource: sourceManual, LastSeenAt: poll,
		})
	}
	_ = s.queries.DeleteStaleOSNics(ctx, db.DeleteStaleOSNicsParams{DeviceID: devID, CollectionSource: sourceManual, LastSeenAt: poll})

	for _, d := range req.Disks {
		name := strings.TrimSpace(d.Name)
		if name == "" {
			continue
		}
		_ = s.queries.UpsertOSDisk(ctx, db.UpsertOSDiskParams{
			DeviceID: devID, Name: name, Model: strPtr(d.Model), Filesystem: strPtr(d.Filesystem),
			TotalBytes: vdI64(d.TotalBytes), FreeBytes: vdI64(d.FreeBytes),
			CollectionSource: sourceManual, LastSeenAt: poll,
		})
	}
	_ = s.queries.DeleteStaleOSDisks(ctx, db.DeleteStaleOSDisksParams{DeviceID: devID, CollectionSource: sourceManual, LastSeenAt: poll})

	for _, sw := range req.Software {
		if strings.TrimSpace(sw.Name) == "" {
			continue
		}
		_ = s.queries.UpsertOSSoftware(ctx, db.UpsertOSSoftwareParams{
			DeviceID: devID, Name: sw.Name, Version: sw.Version, Publisher: strPtr(sw.Publisher),
			CollectionSource: sourceManual, LastSeenAt: poll,
		})
	}
	_ = s.queries.DeleteStaleOSSoftware(ctx, db.DeleteStaleOSSoftwareParams{DeviceID: devID, CollectionSource: sourceManual, LastSeenAt: poll})

	for _, role := range req.Roles {
		role = strings.TrimSpace(role)
		if role == "" {
			continue
		}
		_ = s.queries.UpsertOSRole(ctx, db.UpsertOSRoleParams{DeviceID: devID, Role: role, CollectionSource: sourceManual, LastSeenAt: poll})
	}
	_ = s.queries.DeleteStaleOSRoles(ctx, db.DeleteStaleOSRolesParams{DeviceID: devID, CollectionSource: sourceManual, LastSeenAt: poll})

	s.writeVirtualNeighbors(ctx, devID, req, poll) // connected switch/port
}

func (s *Server) writeVirtualWireless(ctx context.Context, devID uuid.UUID, req *virtualDeviceReq, poll time.Time) {
	w := req.Wlan
	if w == nil {
		w = &vdWlan{}
	}
	_, _ = s.queries.UpsertWLANControllerInfo(ctx, db.UpsertWLANControllerInfoParams{
		DeviceID: devID, Vendor: strPtr(w.Vendor), Version: strPtr(w.Version),
		ApCount: int32(len(req.APs)), ClientCount: int32(len(req.Clients)), Source: sourceManual,
		ControllerName: w.ControllerName, Model: w.Model, Serial: w.Serial, SsidCount: int32(len(req.SSIDs)),
	})
	for _, ap := range req.APs {
		if strings.TrimSpace(ap.Name) == "" && strings.TrimSpace(ap.MAC) == "" {
			continue
		}
		st := strings.TrimSpace(ap.Status)
		if st == "" {
			st = "unknown"
		}
		_, _ = s.queries.UpsertAccessPoint(ctx, db.UpsertAccessPointParams{
			ControllerDeviceID: devID, Name: orName(ap.Name, ap.MAC), Mac: strPtr(ap.MAC), Model: strPtr(ap.Model),
			Ip: parseIPPtr(ap.IP), Status: st, Serial: ap.Serial, Band: ap.Band, Site: ap.Site, Source: sourceManual,
		})
	}
	_ = s.queries.DeleteStaleAccessPoints(ctx, db.DeleteStaleAccessPointsParams{ControllerDeviceID: devID, Source: sourceManual, CollectedAt: poll})

	for _, sid := range req.SSIDs {
		if strings.TrimSpace(sid.Name) == "" {
			continue
		}
		st := strings.TrimSpace(sid.Status)
		if st == "" {
			st = "unknown"
		}
		_, _ = s.queries.UpsertWirelessSSID(ctx, db.UpsertWirelessSSIDParams{
			ControllerDeviceID: devID, Name: sid.Name, Status: st, Security: sid.Security,
			Band: sid.Band, Vlan: sid.Vlan, Source: sourceManual,
		})
	}
	_ = s.queries.DeleteStaleWirelessSSIDs(ctx, db.DeleteStaleWirelessSSIDsParams{ControllerDeviceID: devID, Source: sourceManual, CollectedAt: poll})

	for _, c := range req.Clients {
		if strings.TrimSpace(c.MAC) == "" {
			continue
		}
		_, _ = s.queries.UpsertWirelessClient(ctx, db.UpsertWirelessClientParams{
			ControllerDeviceID: devID, Mac: normMAC(c.MAC), Ip: c.IP, Hostname: c.Hostname,
			ApName: c.ApName, Ssid: c.Ssid, Band: c.Band, Source: sourceManual,
		})
	}
	_ = s.queries.DeleteStaleWirelessClients(ctx, db.DeleteStaleWirelessClientsParams{ControllerDeviceID: devID, Source: sourceManual, CollectedAt: poll})
}

func (s *Server) writeVirtualUPS(ctx context.Context, devID uuid.UUID, req *virtualDeviceReq) {
	u := req.UPS
	if u == nil {
		return
	}
	bs := strings.TrimSpace(u.BatteryStatus)
	if bs == "" {
		bs = "unknown"
	}
	_ = s.queries.UpsertUPSStatus(ctx, db.UpsertUPSStatusParams{
		DeviceID: devID, Manufacturer: strPtr(u.Manufacturer), Model: strPtr(u.Model),
		BatteryStatus: bs, ChargePct: vdI32(u.ChargePct), RuntimeMin: vdI32(u.RuntimeMin), LoadPct: vdI32(u.LoadPct),
		LastSeenAt: time.Now().UTC(),
	})
}

// writeVirtualGeneric handles AP / printer / camera / NVR / other: a connected
// switch/port neighbor + any L2 detail the operator entered (so "Other" can still
// model ports/VLANs/MACs if needed).
func (s *Server) writeVirtualGeneric(ctx context.Context, devID uuid.UUID, req *virtualDeviceReq, poll time.Time) {
	if len(req.Ports) > 0 {
		s.writeVirtualPorts(ctx, devID, req.Ports, poll)
	}
	if len(req.VLANs) > 0 {
		s.writeVirtualVlans(ctx, devID, req.VLANs, poll)
	}
	if len(req.MACs) > 0 {
		s.writeVirtualMACs(ctx, devID, req.MACs, poll)
	}
	s.writeVirtualNeighbors(ctx, devID, req, poll)
}

// --- helpers ----------------------------------------------------------------

func vdI64(v int64) *int64 {
	if v == 0 {
		return nil
	}
	return &v
}

func orName(a, b string) string {
	if strings.TrimSpace(a) != "" {
		return a
	}
	return b
}

func vdI32(v int) *int32 {
	if v == 0 {
		return nil
	}
	x := int32(v)
	return &x
}

func parseIPPtr(s string) *netip.Addr {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	ip, err := netip.ParseAddr(s)
	if err != nil {
		return nil
	}
	return &ip
}

// --- Excel template + import ------------------------------------------------

var (
	vdHdrDevice    = []any{"name", "category", "vendor", "model", "serial", "os_version", "primary_ip", "site", "status", "notes"}
	vdHdrPorts     = []any{"if_index", "name", "alias", "up (yes/no)", "admin_down (yes/no)", "speed_mbps", "vlan", "role", "mac"}
	vdHdrVLANs     = []any{"vlan_id", "name"}
	vdHdrNeighbors = []any{"local_port", "local_if_index", "remote_name", "remote_port", "remote_mgmt_ip", "protocol"}
	vdHdrMACs      = []any{"mac", "vlan", "if_index"}
)

// virtualTemplateXLSX handles GET /devices/virtual/template.xlsx — a fillable
// workbook (one device per file) with a sheet per inventory section + examples.
func (s *Server) virtualTemplateXLSX(w http.ResponseWriter, r *http.Request) {
	xl := excelize.NewFile()
	defer xl.Close()

	put := func(sheet string, header []any, examples [][]any) {
		idx, _ := xl.NewSheet(sheet)
		_ = idx
		_ = xl.SetSheetRow(sheet, "A1", &header)
		for i, ex := range examples {
			_ = xl.SetSheetRow(sheet, "A"+strconv.Itoa(i+2), &ex)
		}
	}
	put("Device", vdHdrDevice, [][]any{{"Core-SW-EXAMPLE", "switch", "Cisco", "C9300", "FOC123", "IOS-XE 17", "172.21.96.9", "Main Hotel", "up", "manually entered"}})
	put("Ports", vdHdrPorts, [][]any{
		{1, "GigabitEthernet1/0/1", "uplink-to-core", "yes", "no", 1000, 10, "uplink", ""},
		{2, "GigabitEthernet1/0/2", "AP-Lobby", "yes", "no", 1000, 20, "access", ""},
		{3, "GigabitEthernet1/0/3", "spare", "no", "yes", 1000, 1, "access", ""},
	})
	put("VLANs", vdHdrVLANs, [][]any{{10, "Mgmt"}, {20, "Staff"}, {90, "Guest"}})
	put("Neighbors", vdHdrNeighbors, [][]any{{"GigabitEthernet1/0/1", 1, "CHV-CORE", "1/1/24", "172.21.96.2", "lldp"}})
	put("MACs", vdHdrMACs, [][]any{{"aa:bb:cc:dd:ee:ff", 20, 2}})

	xl.DeleteSheet("Sheet1")
	xl.SetActiveSheet(0)

	w.Header().Set("Content-Type", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet")
	w.Header().Set("Content-Disposition", `attachment; filename="virtual-device-template.xlsx"`)
	if err := xl.Write(w); err != nil {
		writeErr(w, err)
	}
}

// importVirtualXLSX handles POST /devices/virtual/import (multipart "file") — one
// virtual device per workbook, read from the Device/Ports/VLANs/Neighbors/MACs
// sheets, then created the same way as the JSON create path.
func (s *Server) importVirtualXLSX(w http.ResponseWriter, r *http.Request) {
	f, _, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "no file uploaded (field 'file'): "+err.Error(), http.StatusBadRequest)
		return
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, 16<<20))
	if err != nil {
		http.Error(w, "read failed: "+err.Error(), http.StatusBadRequest)
		return
	}
	xl, err := excelize.OpenReader(bytes.NewReader(data))
	if err != nil {
		http.Error(w, "not a valid .xlsx: "+err.Error(), http.StatusBadRequest)
		return
	}
	defer xl.Close()

	dev := sheetRows(xl, "Device")
	if len(dev) < 2 {
		http.Error(w, "the 'Device' sheet needs a header row + one device row", http.StatusBadRequest)
		return
	}
	dcol := headerIndex(dev[0])
	row := dev[1]
	cell := func(key string) string {
		if i, ok := dcol[key]; ok && i < len(row) {
			return strings.TrimSpace(row[i])
		}
		return ""
	}
	req := virtualDeviceReq{
		manualDeviceReq: manualDeviceReq{
			Name: cell("name"), Category: cell("category"), Vendor: cell("vendor"), Model: cell("model"),
			Serial: cell("serial"), OSVersion: cell("os_version"), PrimaryIP: cell("primary_ip"),
		},
		Status: cell("status"), Site: cell("site"),
	}
	if req.Name == "" {
		http.Error(w, "Device sheet: 'name' is required", http.StatusBadRequest)
		return
	}

	req.Ports = parsePorts(sheetRows(xl, "Ports"))
	req.VLANs = parseVLANs(sheetRows(xl, "VLANs"))
	req.Neighbors = parseNeighbors(sheetRows(xl, "Neighbors"))
	req.MACs = parseMACs(sheetRows(xl, "MACs"))

	params, perr := manualDeviceParams(req.manualDeviceReq)
	if perr != nil {
		http.Error(w, perr.Error(), http.StatusBadRequest)
		return
	}
	status := strings.ToLower(strings.TrimSpace(req.Status))
	if !validDeviceStatus[status] {
		status = "up"
	}
	params.Status = status
	params.Metadata = []byte(`{"source":"virtual"}`)

	device, err := s.queries.CreateDevice(r.Context(), params)
	if err != nil {
		writeErr(w, err)
		return
	}
	_ = s.queries.MarkDeviceVirtual(r.Context(), db.MarkDeviceVirtualParams{ID: device.ID, IsVirtual: true})
	device.IsVirtual = true
	s.writeVirtualConfig(r.Context(), device, &req)
	s.audit(r, "inventory", "device.import_virtual", "device", device.ID.String(),
		"Imported virtual device "+device.Name, map[string]any{"ports": len(req.Ports), "vlans": len(req.VLANs), "neighbors": len(req.Neighbors), "macs": len(req.MACs)})

	out, _ := s.queries.GetDevice(r.Context(), device.ID)
	writeJSON(w, http.StatusCreated, map[string]any{
		"device": out,
		"counts": map[string]int{"ports": len(req.Ports), "vlans": len(req.VLANs), "neighbors": len(req.Neighbors), "macs": len(req.MACs)},
	})
}

func sheetRows(xl *excelize.File, sheet string) [][]string {
	if i, _ := xl.GetSheetIndex(sheet); i < 0 {
		return nil
	}
	rows, _ := xl.GetRows(sheet)
	return rows
}

func headerIndex(header []string) map[string]int {
	m := map[string]int{}
	for i, h := range header {
		key := strings.ToLower(strings.TrimSpace(h))
		// normalize "up (yes/no)" → "up", "admin_down (yes/no)" → "admin_down"
		if sp := strings.IndexByte(key, ' '); sp >= 0 {
			key = key[:sp]
		}
		m[key] = i
	}
	return m
}

func cellAt(row []string, col map[string]int, key string) string {
	if i, ok := col[key]; ok && i < len(row) {
		return strings.TrimSpace(row[i])
	}
	return ""
}

func yesish(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "yes", "y", "true", "1", "up":
		return true
	}
	return false
}

func vdAtoi(s string) int {
	n, _ := strconv.Atoi(strings.TrimSpace(s))
	return n
}

func parsePorts(rows [][]string) []vdPort {
	if len(rows) < 2 {
		return nil
	}
	col := headerIndex(rows[0])
	out := []vdPort{}
	for _, row := range rows[1:] {
		if strings.TrimSpace(strings.Join(row, "")) == "" {
			continue
		}
		idx := vdAtoi(cellAt(row, col, "if_index"))
		if idx <= 0 {
			continue
		}
		out = append(out, vdPort{
			IfIndex: idx, Name: cellAt(row, col, "name"), Alias: cellAt(row, col, "alias"),
			Up: yesish(cellAt(row, col, "up")), AdminDown: yesish(cellAt(row, col, "admin_down")),
			SpeedMbps: vdAtoi(cellAt(row, col, "speed_mbps")), VLAN: vdAtoi(cellAt(row, col, "vlan")),
			Role: cellAt(row, col, "role"), MAC: cellAt(row, col, "mac"),
		})
	}
	return out
}

func parseVLANs(rows [][]string) []vdVlan {
	if len(rows) < 2 {
		return nil
	}
	col := headerIndex(rows[0])
	out := []vdVlan{}
	for _, row := range rows[1:] {
		id := vdAtoi(cellAt(row, col, "vlan_id"))
		if id <= 0 {
			continue
		}
		out = append(out, vdVlan{ID: id, Name: cellAt(row, col, "name")})
	}
	return out
}

func parseNeighbors(rows [][]string) []vdNeighbor {
	if len(rows) < 2 {
		return nil
	}
	col := headerIndex(rows[0])
	out := []vdNeighbor{}
	for _, row := range rows[1:] {
		name := cellAt(row, col, "remote_name")
		port := cellAt(row, col, "remote_port")
		if name == "" && port == "" {
			continue
		}
		out = append(out, vdNeighbor{
			LocalPort: cellAt(row, col, "local_port"), LocalIfIndex: vdAtoi(cellAt(row, col, "local_if_index")),
			RemoteName: name, RemotePort: port, RemoteMgmtIP: cellAt(row, col, "remote_mgmt_ip"),
			Protocol: cellAt(row, col, "protocol"),
		})
	}
	return out
}

func parseMACs(rows [][]string) []vdMAC {
	if len(rows) < 2 {
		return nil
	}
	col := headerIndex(rows[0])
	out := []vdMAC{}
	for _, row := range rows[1:] {
		mac := cellAt(row, col, "mac")
		if mac == "" {
			continue
		}
		out = append(out, vdMAC{MAC: mac, VLAN: vdAtoi(cellAt(row, col, "vlan")), IfIndex: vdAtoi(cellAt(row, col, "if_index"))})
	}
	return out
}
