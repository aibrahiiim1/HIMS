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

// vdPort is one manually-entered switch/host port.
type vdPort struct {
	IfIndex   int    `json:"if_index"`
	Name      string `json:"name"`
	Alias     string `json:"alias"`
	Up        bool   `json:"up"`        // operational status (true=up → IF-MIB oper 1)
	AdminDown bool   `json:"admin_down"` // operator shut the port (admin 2)
	SpeedMbps int    `json:"speed_mbps"`
	VLAN      int    `json:"vlan"`  // access/native VLAN (pvid)
	Role      string `json:"role"`  // access|trunk|uplink|unknown
	MAC       string `json:"mac"`
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

// virtualDeviceReq is the create/update payload: device identity (reusing the
// manual-add fields) + status + the full inventory config.
type virtualDeviceReq struct {
	manualDeviceReq
	Status    string       `json:"status"` // operator-set; virtual devices aren't probed
	Site      string       `json:"site"`
	Ports     []vdPort     `json:"ports"`
	VLANs     []vdVlan     `json:"vlans"`
	Neighbors []vdNeighbor `json:"neighbors"`
	MACs      []vdMAC      `json:"macs"`
}

// createVirtualDevice handles POST /devices/virtual — create the device + persist
// its full manual config in one call.
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
	status := strings.TrimSpace(strings.ToLower(req.Status))
	if status == "" {
		status = "up"
	}
	if !validDeviceStatus[status] {
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
	s.writeVirtualConfig(r.Context(), dev.ID, &req)
	s.audit(r, "inventory", "device.create_virtual", "device", dev.ID.String(),
		"Created virtual device "+dev.Name, map[string]any{
			"category": dev.Category, "ports": len(req.Ports), "vlans": len(req.VLANs),
			"neighbors": len(req.Neighbors), "macs": len(req.MACs),
		})
	out, _ := s.queries.GetDevice(r.Context(), dev.ID)
	writeJSON(w, http.StatusCreated, out)
}

// updateVirtualDevice handles PUT /devices/virtual/{id} — replace the device's
// identity + full config. Only virtual devices may be edited this way.
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
	status := strings.TrimSpace(strings.ToLower(req.Status))
	if status == "" {
		status = cur.Status
	}
	if !validDeviceStatus[status] {
		http.Error(w, "invalid status", http.StatusBadRequest)
		return
	}
	if _, err := s.queries.UpdateDevice(ctx, db.UpdateDeviceParams{
		ID: id, Name: name, Category: cat,
		Vendor: strPtr(req.Vendor), Model: strPtr(req.Model), Serial: strPtr(req.Serial),
		OsVersion: strPtr(req.OSVersion), Hostname: strPtr(req.Hostname),
		Vlan: strPtr(req.VLAN), DeviceClass: strPtr(req.Class), Location: strPtr(req.Location),
		LocationID: parseUUIDPtr(req.LocationID), Subtype: cur.Subtype, Notes: cur.Notes,
		Criticality: cur.Criticality, MonitoringEnabled: cur.MonitoringEnabled,
		ClassificationLocked: cur.ClassificationLocked, ManualClassificationReason: cur.ManualClassificationReason,
	}); err != nil {
		writeErr(w, err)
		return
	}
	_ = s.queries.UpdateDeviceMonitoringStatus(ctx, db.UpdateDeviceMonitoringStatusParams{ID: id, Status: status})
	s.writeVirtualConfig(ctx, id, &req)
	s.audit(r, "inventory", "device.update_virtual", "device", id.String(), "Updated virtual device "+name, nil)
	out, _ := s.queries.GetDevice(ctx, id)
	writeJSON(w, http.StatusOK, out)
}

// writeVirtualConfig upserts the device's manual inventory then prunes any manual
// rows not in this payload (so an update is a full replace of the manual config).
// Real-collected rows (source != 'manual') are never touched.
func (s *Server) writeVirtualConfig(ctx context.Context, devID uuid.UUID, req *virtualDeviceReq) {
	poll := time.Now().UTC()

	for _, p := range req.Ports {
		if p.IfIndex <= 0 {
			continue
		}
		oper := int16(1) // up
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
		// Access-VLAN membership (untagged) so the port shows its VLAN.
		if p.VLAN > 0 {
			_ = s.queries.UpsertPortVlan(ctx, db.UpsertPortVlanParams{
				DeviceID: devID, IfIndex: int32(p.IfIndex), VlanID: int32(p.VLAN),
				Tagged: false, CollectionSource: sourceManual, LastSeenAt: poll,
			})
		}
	}
	_ = s.queries.DeleteStaleInterfaces(ctx, db.DeleteStaleInterfacesParams{DeviceID: devID, LastSeenAt: poll, CollectionSource: sourceManual})
	_ = s.queries.DeleteStalePortVlans(ctx, db.DeleteStalePortVlansParams{DeviceID: devID, LastSeenAt: poll, CollectionSource: sourceManual})

	for _, v := range req.VLANs {
		if v.ID <= 0 {
			continue
		}
		_, _ = s.queries.UpsertVlan(ctx, db.UpsertVlanParams{
			DeviceID: devID, VlanID: int32(v.ID), Name: strPtr(v.Name), CollectionSource: sourceManual, LastSeenAt: poll,
		})
	}
	_ = s.queries.DeleteStaleVlans(ctx, db.DeleteStaleVlansParams{DeviceID: devID, LastSeenAt: poll, CollectionSource: sourceManual})

	for _, n := range req.Neighbors {
		if strings.TrimSpace(n.RemoteName) == "" && strings.TrimSpace(n.RemotePort) == "" {
			continue
		}
		proto := strings.TrimSpace(strings.ToLower(n.Protocol))
		if proto == "" {
			proto = sourceManual
		}
		_, _ = s.queries.UpsertNeighbor(ctx, db.UpsertNeighborParams{
			DeviceID: devID, LocalIfIndex: vdI32(n.LocalIfIndex), LocalIfName: strPtr(n.LocalPort),
			RemSysName: strPtr(n.RemoteName), RemPortID: strPtr(n.RemotePort), RemMgmtIp: parseIPPtr(n.RemoteMgmtIP),
			Protocol: proto, CollectionSource: sourceManual, LastSeenAt: poll,
		})
	}
	_ = s.queries.DeleteStaleNeighbors(ctx, db.DeleteStaleNeighborsParams{DeviceID: devID, LastSeenAt: poll, CollectionSource: sourceManual})

	for _, m := range req.MACs {
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

// --- helpers ----------------------------------------------------------------

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
	s.writeVirtualConfig(r.Context(), device.ID, &req)
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
