package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
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

// virtualConfigFactKey stores the last-submitted virtual-device payload as a
// device fact so the edit form reloads it losslessly. It is hidden from the facts
// panel (see deviceFacts).
const virtualConfigFactKey = "virtual.config"

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
	if _, err := manualDeviceParams(req.manualDeviceReq); err != nil { // clean 400 on bad name/category/ip
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if normVirtualStatus(req.Status, "up") == "" {
		http.Error(w, "invalid status; use up, down, warning or unknown", http.StatusBadRequest)
		return
	}
	dev, _, err := s.persistVirtual(r.Context(), &req, nil)
	if err != nil {
		writeErr(w, err)
		return
	}
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
	if strings.TrimSpace(req.Name) == "" {
		req.Name = cur.Name
	}
	if c := strings.TrimSpace(req.Category); c != "" && !validCategory(c) {
		http.Error(w, "invalid category "+strconv.Quote(c), http.StatusBadRequest)
		return
	}
	if normVirtualStatus(req.Status, cur.Status) == "" {
		http.Error(w, "invalid status", http.StatusBadRequest)
		return
	}
	dev, _, err := s.persistVirtual(ctx, &req, &cur)
	if err != nil {
		writeErr(w, err)
		return
	}
	s.audit(r, "inventory", "device.update_virtual", "device", id.String(), "Updated virtual "+dev.Category+" "+dev.Name, nil)
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

// saveVirtualConfigBlob persists the submitted payload as a hidden fact for a
// lossless edit reload (GET /devices/virtual/{id}/config).
func (s *Server) saveVirtualConfigBlob(ctx context.Context, devID uuid.UUID, req *virtualDeviceReq) {
	b, err := json.Marshal(req)
	if err != nil {
		return
	}
	v := string(b)
	_ = s.queries.UpsertDeviceFact(ctx, db.UpsertDeviceFactParams{DeviceID: devID, Key: virtualConfigFactKey, Value: &v, Driver: "virtual"})
}

// getVirtualConfig handles GET /devices/virtual/{id}/config — returns the stored
// payload for the edit form (or an empty object if none).
func (s *Server) getVirtualConfig(w http.ResponseWriter, r *http.Request) {
	ctx, id, ok := pathDevice(w, r)
	if !ok {
		return
	}
	facts, err := s.queries.ListDeviceFacts(ctx, id)
	if err != nil {
		writeErr(w, err)
		return
	}
	for _, f := range facts {
		if f.Key == virtualConfigFactKey && f.Value != nil {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(*f.Value))
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{})
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
	// Don't swallow this: a CHECK/constraint failure here means the workstation's
	// OS-inventory row never lands and the detail page shows "No OS inventory yet"
	// despite the operator entering data (the 000064 root-cause). Surface it.
	if _, err := s.queries.UpsertOSInventory(ctx, db.UpsertOSInventoryParams{
		DeviceID: devID, CollectionMethod: sourceManual, Hostname: strPtr(host),
		OsCaption: strPtr(req.OSVersion),
	}); err != nil {
		slog.Warn("virtual workstation: os_inventory upsert failed", "device", devID, "error", err)
	}
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

// --- Category-aware Excel template + multi-device import ---------------------
// Every sheet is keyed by device_key (first column) so a single workbook can hold
// many devices; child rows attach to a device row in the Devices sheet by key. A
// type-scoped template includes only that category's relevant sheets; a no-type
// template includes the full set.

var vdSheetHdr = map[string][]any{
	"Devices":     {"device_key", "name", "category", "vendor", "model", "serial", "os_version", "primary_ip", "site", "status", "vlan", "class", "criticality", "notes"},
	"Ports":       {"device_key", "if_index", "name", "alias", "up (yes/no)", "admin_down (yes/no)", "speed_mbps", "vlan", "trunk_vlans", "role", "mac"},
	"VLANs":       {"device_key", "vlan_id", "name"},
	"Neighbors":   {"device_key", "local_port", "local_if_index", "remote_name", "remote_port", "remote_mgmt_ip", "protocol"},
	"LearnedMACs": {"device_key", "mac", "vlan", "if_index"},
	"Interfaces":  {"device_key", "name", "zone", "ip", "mac", "speed_mbps"},
	"Disks":       {"device_key", "name", "model", "filesystem", "total_bytes", "used_bytes", "free_bytes"},
	"Roles":       {"device_key", "role"},
	"Software":    {"device_key", "name", "version", "publisher"},
	"VpnTunnels":  {"device_key", "name", "p1_name", "remote_gw", "status"},
	"HAMembers":   {"device_key", "serial", "hostname", "sync_status"},
	"Licenses":    {"device_key", "contract", "expiry"},
	"APs":         {"device_key", "name", "mac", "model", "ip", "status", "serial", "band", "site"},
	"SSIDs":       {"device_key", "name", "security", "band", "vlan", "status"},
	"Clients":     {"device_key", "mac", "ip", "hostname", "ap_name", "ssid", "band"},
	"UPS":         {"device_key", "manufacturer", "model", "battery_status", "charge_pct", "runtime_min", "load_pct"},
	"Facts":       {"device_key", "key", "value"},
}

var vdSheetExample = map[string][]any{
	"Ports":       {"sw1", 1, "Gi1/0/1", "uplink-to-core", "yes", "no", 1000, 10, "20,30", "uplink", ""},
	"VLANs":       {"sw1", 10, "Mgmt"},
	"Neighbors":   {"sw1", "Gi1/0/1", 1, "CHV-CORE", "1/1/24", "172.21.96.2", "lldp"},
	"LearnedMACs": {"sw1", "aa:bb:cc:dd:ee:ff", 20, 2},
	"Interfaces":  {"fw1", "port1", "WAN", "203.0.113.2", "", 1000},
	"Disks":       {"srv1", "C:", "Samsung", "NTFS", 512000000000, 200000000000, 312000000000},
	"Roles":       {"srv1", "Application Server"},
	"Software":    {"ws1", "Microsoft Office", "2021", "Microsoft"},
	"VpnTunnels":  {"fw1", "Site-to-HQ", "ph1-hq", "198.51.100.1", "up"},
	"HAMembers":   {"fw1", "FGT123", "fw-a", "synchronized"},
	"Licenses":    {"fw1", "FortiCare", "2027-01-01"},
	"APs":         {"wlc1", "AP-Lobby", "f0:b0:52:00:00:01", "AP305", "10.98.0.10", "up", "SN123", "5GHz", "Main Hotel"},
	"SSIDs":       {"wlc1", "Guest-WiFi", "WPA2", "dual", "90", "up"},
	"Clients":     {"wlc1", "0a:11:22:33:44:55", "172.21.4.20", "iphone", "AP-Lobby", "Guest-WiFi", "5GHz"},
	"UPS":         {"ups1", "APC", "SMT1500", "normal", 100, 35, 28},
	"Facts":       {"srv1", "cpu", "2 x Xeon Gold 6230"},
}

// vdCategorySheets maps a category to the child sheets relevant to it.
var vdCategorySheets = map[string][]string{
	"switch":              {"Ports", "VLANs", "Neighbors", "LearnedMACs", "Facts"},
	"router":              {"Ports", "VLANs", "Neighbors", "LearnedMACs", "Facts"},
	"isp_router":          {"Ports", "VLANs", "Neighbors", "LearnedMACs", "Facts"},
	"firewall":            {"Interfaces", "VpnTunnels", "HAMembers", "Licenses", "Neighbors", "Facts"},
	"server":              {"Interfaces", "Disks", "Roles", "Neighbors", "Facts"},
	"virtual_host":        {"Interfaces", "Disks", "Roles", "Neighbors", "Facts"},
	"endpoint":            {"Interfaces", "Disks", "Software", "Roles", "Neighbors", "Facts"},
	"wireless_controller": {"APs", "SSIDs", "Clients", "Facts"},
	"ups":                 {"UPS", "Facts"},
	"access_point":        {"Neighbors", "Facts"},
	"printer":             {"Neighbors", "Facts"},
	"camera":              {"Neighbors", "Facts"},
	"nvr":                 {"Neighbors", "Facts"},
}

// vdAllChildSheets is the full set used for an All-Inventory (no type) template.
var vdAllChildSheets = []string{"Ports", "VLANs", "Neighbors", "LearnedMACs", "Interfaces", "Disks", "Roles", "Software", "VpnTunnels", "HAMembers", "Licenses", "APs", "SSIDs", "Clients", "UPS", "Facts"}

// virtualTemplateXLSX handles GET /devices/virtual/template.xlsx?type=<category>.
// Multi-device, category-aware: only the relevant sheets for the type (or the full
// set when no/unknown type), each keyed by device_key.
func (s *Server) virtualTemplateXLSX(w http.ResponseWriter, r *http.Request) {
	typ := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("type")))
	child, scoped := vdCategorySheets[typ]
	if !scoped {
		child = vdAllChildSheets
	}
	xl := excelize.NewFile()
	defer xl.Close()
	put := func(sheet string, examples [][]any) {
		_, _ = xl.NewSheet(sheet)
		if hdr := vdSheetHdr[sheet]; hdr != nil {
			_ = xl.SetSheetRow(sheet, "A1", &hdr)
		}
		for i, ex := range examples {
			_ = xl.SetSheetRow(sheet, "A"+strconv.Itoa(i+2), &ex)
		}
	}
	exCat := typ
	if exCat == "" {
		exCat = "switch"
	}
	// One coherent example device_key used on the Devices row AND every child row,
	// so the unmodified template imports as a single complete device. (Previously
	// the Devices example used "device1" while child examples used "sw1"/"srv1"/… —
	// mismatched keys meant the example child rows linked to no device.)
	const exKey = "dev1"
	put("Devices", [][]any{{exKey, "EXAMPLE-" + strings.ToUpper(exCat), exCat, "Vendor", "Model", "SN123", "", "172.21.96.9", "Main Hotel", "up", "10", "", "normal", "manually entered"}})
	for _, sh := range child {
		var ex [][]any
		if e := vdSheetExample[sh]; e != nil {
			row := append([]any{exKey}, e[1:]...) // force device_key to match the Devices row
			ex = [][]any{row}
		}
		put(sh, ex)
	}
	xl.DeleteSheet("Sheet1")
	xl.SetActiveSheet(0)

	fname := "virtual-devices-template.xlsx"
	if scoped {
		fname = "virtual-" + typ + "-template.xlsx"
	}
	w.Header().Set("Content-Type", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet")
	w.Header().Set("Content-Disposition", `attachment; filename="`+fname+`"`)
	if err := xl.Write(w); err != nil {
		writeErr(w, err)
	}
}

// vdImportError is one cell/row problem, located precisely for the operator.
type vdImportError struct {
	Sheet   string `json:"sheet"`
	Row     int    `json:"row"`
	Field   string `json:"field,omitempty"`
	Message string `json:"message"`
}

type vdImportReport struct {
	Created int             `json:"created"`
	Updated int             `json:"updated"`
	Skipped int             `json:"skipped"`
	Failed  int             `json:"failed"`
	Devices []string        `json:"devices,omitempty"`
	Errors  []vdImportError `json:"errors,omitempty"`
}

// importVirtualXLSX handles POST /devices/virtual/import (multipart "file") — a
// multi-device, category-aware workbook. Child sheet rows link to a Devices row by
// device_key. Returns a structured per-row report (created/updated/skipped/failed).
func (s *Server) importVirtualXLSX(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
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

	reqs, rows, parseErrs, fatal := parseVirtualWorkbook(xl)
	if fatal != "" {
		http.Error(w, fatal, http.StatusBadRequest)
		return
	}
	// Existing virtual devices by lowercased name → create-or-update.
	existing := map[string]db.Device{}
	if all, lerr := s.queries.ListAllDevices(ctx); lerr == nil {
		for _, d := range all {
			if d.IsVirtual {
				existing[strings.ToLower(d.Name)] = d
			}
		}
	}
	rep := vdImportReport{Errors: parseErrs, Failed: len(parseErrs)}
	for i := range reqs {
		req := reqs[i]
		cur, isUpdate := existing[strings.ToLower(req.Name)]
		var curPtr *db.Device
		if isUpdate {
			curPtr = &cur
		}
		dev, action, perr := s.persistVirtual(ctx, &req, curPtr)
		if perr != nil {
			rep.Failed++
			rep.Errors = append(rep.Errors, vdImportError{Sheet: "Devices", Row: rows[i], Message: perr.Error()})
			continue
		}
		if action == "updated" {
			rep.Updated++
		} else {
			rep.Created++
		}
		rep.Devices = append(rep.Devices, dev.Name)
	}
	s.audit(r, "inventory", "device.import_virtual", "device", "",
		"Imported virtual devices from workbook", map[string]any{"created": rep.Created, "updated": rep.Updated, "failed": rep.Failed})
	writeJSON(w, http.StatusOK, rep)
}

// parseVirtualWorkbook reads a multi-device workbook into per-device requests
// (child sheet rows attached by device_key), plus per-row parse errors. A non-empty
// fatal means the workbook is unusable (no Devices sheet).
func parseVirtualWorkbook(xl *excelize.File) (reqs []virtualDeviceReq, rows []int, errs []vdImportError, fatal string) {
	devRows := sheetRows(xl, "Devices")
	if len(devRows) < 2 {
		return nil, nil, nil, "the 'Devices' sheet needs a header row + at least one device row"
	}

	// Single-device fallback key: child rows may omit device_key when there's one device.
	singleKey := ""
	if n := countDataRows(devRows); n == 1 {
		singleKey = firstDeviceKey(devRows)
	}
	keyOf := func(get func(string) string) string {
		k := strings.ToLower(strings.TrimSpace(get("device_key")))
		if k == "" {
			return singleKey
		}
		return k
	}

	// The set of device_keys that actually have a Devices row — used to warn on
	// child rows that reference a non-existent device (instead of dropping silently).
	deviceKeys := map[string]bool{}
	forEachRow(devRows, func(_ int, g func(string) string) {
		if strings.TrimSpace(g("name")) == "" {
			return
		}
		k := strings.ToLower(strings.TrimSpace(g("device_key")))
		if k == "" {
			k = strings.ToLower(strings.TrimSpace(g("name")))
		}
		deviceKeys[k] = true
	})

	// Group every child sheet by device_key.
	ports := map[string][]vdPort{}
	forEachRow(sheetRows(xl, "Ports"), func(_ int, g func(string) string) {
		if vdAtoi(g("if_index")) <= 0 {
			return
		}
		ports[keyOf(g)] = append(ports[keyOf(g)], vdPort{
			IfIndex: vdAtoi(g("if_index")), Name: g("name"), Alias: g("alias"),
			Up: yesish(g("up")), AdminDown: yesish(g("admin_down")), SpeedMbps: vdAtoi(g("speed_mbps")),
			VLAN: vdAtoi(g("vlan")), TrunkVLANs: vdAtoiList(g("trunk_vlans")), Role: g("role"), MAC: g("mac"),
		})
	})
	vlans := map[string][]vdVlan{}
	forEachRow(sheetRows(xl, "VLANs"), func(_ int, g func(string) string) {
		if vdAtoi(g("vlan_id")) <= 0 {
			return
		}
		vlans[keyOf(g)] = append(vlans[keyOf(g)], vdVlan{ID: vdAtoi(g("vlan_id")), Name: g("name")})
	})
	neighbors := map[string][]vdNeighbor{}
	forEachRow(sheetRows(xl, "Neighbors"), func(_ int, g func(string) string) {
		if g("remote_name") == "" && g("remote_port") == "" {
			return
		}
		neighbors[keyOf(g)] = append(neighbors[keyOf(g)], vdNeighbor{
			LocalPort: g("local_port"), LocalIfIndex: vdAtoi(g("local_if_index")), RemoteName: g("remote_name"),
			RemotePort: g("remote_port"), RemoteMgmtIP: g("remote_mgmt_ip"), Protocol: g("protocol"),
		})
	})
	macs := map[string][]vdMAC{}
	forEachRow(sheetRows(xl, "LearnedMACs"), func(_ int, g func(string) string) {
		if g("mac") == "" {
			return
		}
		macs[keyOf(g)] = append(macs[keyOf(g)], vdMAC{MAC: g("mac"), VLAN: vdAtoi(g("vlan")), IfIndex: vdAtoi(g("if_index"))})
	})
	nics := map[string][]vdNIC{}
	forEachRow(sheetRows(xl, "Interfaces"), func(_ int, g func(string) string) {
		if g("name") == "" {
			return
		}
		nics[keyOf(g)] = append(nics[keyOf(g)], vdNIC{Name: g("name"), Zone: g("zone"), IP: g("ip"), MAC: g("mac"), SpeedMbps: vdAtoi(g("speed_mbps"))})
	})
	disks := map[string][]vdDisk{}
	forEachRow(sheetRows(xl, "Disks"), func(_ int, g func(string) string) {
		if g("name") == "" {
			return
		}
		disks[keyOf(g)] = append(disks[keyOf(g)], vdDisk{Name: g("name"), Model: g("model"), Filesystem: g("filesystem"), TotalBytes: vdAtoi64(g("total_bytes")), UsedBytes: vdAtoi64(g("used_bytes")), FreeBytes: vdAtoi64(g("free_bytes"))})
	})
	roles := map[string][]string{}
	forEachRow(sheetRows(xl, "Roles"), func(_ int, g func(string) string) {
		if g("role") == "" {
			return
		}
		roles[keyOf(g)] = append(roles[keyOf(g)], g("role"))
	})
	software := map[string][]vdSoftware{}
	forEachRow(sheetRows(xl, "Software"), func(_ int, g func(string) string) {
		if g("name") == "" {
			return
		}
		software[keyOf(g)] = append(software[keyOf(g)], vdSoftware{Name: g("name"), Version: g("version"), Publisher: g("publisher")})
	})
	vpns := map[string][]vdVpn{}
	forEachRow(sheetRows(xl, "VpnTunnels"), func(_ int, g func(string) string) {
		if g("name") == "" {
			return
		}
		vpns[keyOf(g)] = append(vpns[keyOf(g)], vdVpn{Name: g("name"), P1Name: g("p1_name"), RemoteGw: g("remote_gw"), Status: g("status")})
	})
	has := map[string][]vdHA{}
	forEachRow(sheetRows(xl, "HAMembers"), func(_ int, g func(string) string) {
		if g("serial") == "" {
			return
		}
		has[keyOf(g)] = append(has[keyOf(g)], vdHA{Serial: g("serial"), Hostname: g("hostname"), SyncStatus: g("sync_status")})
	})
	lics := map[string][]vdLicense{}
	forEachRow(sheetRows(xl, "Licenses"), func(_ int, g func(string) string) {
		if g("contract") == "" {
			return
		}
		lics[keyOf(g)] = append(lics[keyOf(g)], vdLicense{Contract: g("contract"), Expiry: g("expiry")})
	})
	aps := map[string][]vdAP{}
	forEachRow(sheetRows(xl, "APs"), func(_ int, g func(string) string) {
		if g("name") == "" && g("mac") == "" {
			return
		}
		aps[keyOf(g)] = append(aps[keyOf(g)], vdAP{Name: g("name"), MAC: g("mac"), Model: g("model"), IP: g("ip"), Status: g("status"), Serial: g("serial"), Band: g("band"), Site: g("site")})
	})
	ssids := map[string][]vdSSID{}
	forEachRow(sheetRows(xl, "SSIDs"), func(_ int, g func(string) string) {
		if g("name") == "" {
			return
		}
		ssids[keyOf(g)] = append(ssids[keyOf(g)], vdSSID{Name: g("name"), Security: g("security"), Band: g("band"), Vlan: g("vlan"), Status: g("status")})
	})
	clients := map[string][]vdClient{}
	forEachRow(sheetRows(xl, "Clients"), func(_ int, g func(string) string) {
		if g("mac") == "" {
			return
		}
		clients[keyOf(g)] = append(clients[keyOf(g)], vdClient{MAC: g("mac"), IP: g("ip"), Hostname: g("hostname"), ApName: g("ap_name"), Ssid: g("ssid"), Band: g("band")})
	})
	upsByKey := map[string]*vdUPS{}
	forEachRow(sheetRows(xl, "UPS"), func(_ int, g func(string) string) {
		upsByKey[keyOf(g)] = &vdUPS{Manufacturer: g("manufacturer"), Model: g("model"), BatteryStatus: g("battery_status"), ChargePct: vdAtoi(g("charge_pct")), RuntimeMin: vdAtoi(g("runtime_min")), LoadPct: vdAtoi(g("load_pct"))}
	})
	facts := map[string]map[string]string{}
	forEachRow(sheetRows(xl, "Facts"), func(_ int, g func(string) string) {
		if g("key") == "" {
			return
		}
		k := keyOf(g)
		if facts[k] == nil {
			facts[k] = map[string]string{}
		}
		facts[k][g("key")] = g("value")
	})

	// Surface child rows that reference a device_key with no matching Devices row
	// (the most common import mistake), instead of dropping them silently. Skipped
	// for a single-device workbook, where every child row attaches to the one device.
	if singleKey == "" {
		for _, sh := range vdAllChildSheets {
			forEachRow(sheetRows(xl, sh), func(rn int, g func(string) string) {
				raw := strings.TrimSpace(g("device_key"))
				k := strings.ToLower(raw)
				if k == "" {
					errs = append(errs, vdImportError{Sheet: sh, Row: rn, Field: "device_key",
						Message: "device_key is required (the Devices sheet has more than one device) — row skipped"})
				} else if !deviceKeys[k] {
					errs = append(errs, vdImportError{Sheet: sh, Row: rn, Field: "device_key",
						Message: "no device with device_key " + strconv.Quote(raw) + " in the Devices sheet — row skipped"})
				}
			})
		}
	}

	forEachRow(devRows, func(rowNum int, g func(string) string) {
		name := strings.TrimSpace(g("name"))
		if name == "" {
			errs = append(errs, vdImportError{Sheet: "Devices", Row: rowNum, Field: "name", Message: "name is required"})
			return
		}
		key := strings.ToLower(strings.TrimSpace(g("device_key")))
		if key == "" {
			key = strings.ToLower(name)
		}
		req := virtualDeviceReq{
			manualDeviceReq: manualDeviceReq{
				Name: name, Category: g("category"), Vendor: g("vendor"), Model: g("model"), Serial: g("serial"),
				OSVersion: g("os_version"), PrimaryIP: g("primary_ip"), VLAN: g("vlan"), Class: g("class"),
			},
			Status: g("status"), Site: g("site"), Notes: g("notes"), Criticality: g("criticality"),
			Ports: ports[key], VLANs: vlans[key], Neighbors: neighbors[key], MACs: macs[key],
			NICs: nics[key], Disks: disks[key], Roles: roles[key], Software: software[key],
			VpnTunnels: vpns[key], HAMembers: has[key], Licenses: lics[key],
			APs: aps[key], SSIDs: ssids[key], Clients: clients[key], UPS: upsByKey[key], Facts: facts[key],
		}
		if req.Category == "" {
			req.Category = "unknown"
		}
		if !validCategory(req.Category) {
			errs = append(errs, vdImportError{Sheet: "Devices", Row: rowNum, Field: "category", Message: "invalid category " + strconv.Quote(req.Category)})
			return
		}
		reqs = append(reqs, req)
		rows = append(rows, rowNum)
	})
	return reqs, rows, errs, ""
}

// persistVirtual creates (cur==nil) or updates a virtual device's identity +
// status, then writes its category config. Shared by the JSON create/update
// handlers' logic intent and the Excel import. Returns the device + "created"/"updated".
func (s *Server) persistVirtual(ctx context.Context, req *virtualDeviceReq, cur *db.Device) (db.Device, string, error) {
	fallback := "up"
	if cur != nil {
		fallback = cur.Status
	}
	status := normVirtualStatus(req.Status, fallback)
	if status == "" {
		status = "up"
	}
	crit := strings.TrimSpace(req.Criticality)
	if crit != "" && !validCriticality[crit] {
		crit = ""
	}
	if cur != nil { // update
		cat := strings.TrimSpace(req.Category)
		if cat == "" || !validCategory(cat) {
			cat = cur.Category
		}
		notes := cur.Notes
		if strings.TrimSpace(req.Notes) != "" {
			notes = strings.TrimSpace(req.Notes)
		}
		if crit == "" {
			crit = cur.Criticality
		}
		dev, err := s.queries.UpdateDevice(ctx, db.UpdateDeviceParams{
			ID: cur.ID, Name: strings.TrimSpace(req.Name), Category: cat,
			Vendor: strPtr(req.Vendor), Model: strPtr(req.Model), Serial: strPtr(req.Serial),
			OsVersion: strPtr(req.OSVersion), Hostname: strPtr(req.Hostname), Vlan: strPtr(req.VLAN),
			DeviceClass: strPtr(req.Class), Location: strPtr(req.Location), LocationID: parseUUIDPtr(req.LocationID),
			Subtype: cur.Subtype, Notes: notes, Criticality: crit, MonitoringEnabled: cur.MonitoringEnabled,
			ClassificationLocked: cur.ClassificationLocked, ManualClassificationReason: cur.ManualClassificationReason,
		})
		if err != nil {
			return db.Device{}, "", err
		}
		_ = s.queries.UpdateDeviceMonitoringStatus(ctx, db.UpdateDeviceMonitoringStatusParams{ID: dev.ID, Status: status})
		dev.IsVirtual = true
		s.writeVirtualConfig(ctx, dev, req)
		s.saveVirtualConfigBlob(ctx, dev.ID, req)
		return dev, "updated", nil
	}
	// create
	params, err := manualDeviceParams(req.manualDeviceReq)
	if err != nil {
		return db.Device{}, "", err
	}
	params.Status = status
	params.Metadata = []byte(`{"source":"virtual"}`)
	dev, err := s.queries.CreateDevice(ctx, params)
	if err != nil {
		return db.Device{}, "", err
	}
	_ = s.queries.MarkDeviceVirtual(ctx, db.MarkDeviceVirtualParams{ID: dev.ID, IsVirtual: true})
	dev.IsVirtual = true
	s.setVirtualIdentityExtras(ctx, dev, req)
	s.writeVirtualConfig(ctx, dev, req)
	s.saveVirtualConfigBlob(ctx, dev.ID, req)
	return dev, "created", nil
}

// --- workbook helpers --------------------------------------------------------

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
		if sp := strings.IndexByte(key, ' '); sp >= 0 { // "up (yes/no)" → "up"
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

// forEachRow iterates non-blank data rows, calling fn with the 1-based sheet row
// number and a column getter.
func forEachRow(rows [][]string, fn func(rowNum int, get func(string) string)) {
	if len(rows) < 2 {
		return
	}
	col := headerIndex(rows[0])
	for i, row := range rows[1:] {
		if strings.TrimSpace(strings.Join(row, "")) == "" {
			continue
		}
		r := row
		fn(i+2, func(k string) string { return cellAt(r, col, k) })
	}
}

func countDataRows(rows [][]string) int {
	n := 0
	forEachRow(rows, func(int, func(string) string) { n++ })
	return n
}

func firstDeviceKey(rows [][]string) string {
	out := ""
	forEachRow(rows, func(_ int, g func(string) string) {
		if out != "" {
			return
		}
		if k := strings.ToLower(strings.TrimSpace(g("device_key"))); k != "" {
			out = k
		} else {
			out = strings.ToLower(strings.TrimSpace(g("name")))
		}
	})
	return out
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

func vdAtoi64(s string) int64 {
	n, _ := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	return n
}

// vdAtoiList parses "20,30,40" (or space/semicolon separated) into ints.
func vdAtoiList(s string) []int {
	out := []int{}
	for _, p := range strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ';' || r == ' ' }) {
		if n := vdAtoi(p); n > 0 {
			out = append(out, n)
		}
	}
	return out
}
